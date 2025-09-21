package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"brutus/internal/mqttreceiver/logger"
	"brutus/internal/mqttreceiver/metrics"
	"brutus/internal/mqttreceiver/mqtt"
	"brutus/internal/mqttreceiver/storage"
	pb "brutus/proto"

	grpcweb "github.com/improbable-eng/grpc-web/go/grpcweb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"gorm.io/gorm"
)

// Server — реализация gRPC-сервиса MQTTReceiver
type Server struct {
	pb.UnimplementedMQTTReceiverServer
	db          *storage.DB
	mqttClient  *mqtt.Client
	mu          sync.RWMutex
	subscribers map[chan *pb.ParameterUpdate]struct{}
}

// NewServer создаёт сервер
func NewServer(db *storage.DB, mqttClient *mqtt.Client) *Server {
	return &Server{
		db:          db,
		mqttClient:  mqttClient,
		subscribers: make(map[chan *pb.ParameterUpdate]struct{}),
	}
}

// Start запускает gRPC сервер на указанном порту
func (s *Server) Start(port int) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(UnaryAuthInterceptor),
		grpc.StreamInterceptor(StreamAuthInterceptor),
	)

	pb.RegisterMQTTReceiverServer(grpcServer, s)
	log.Printf("gRPC server started on :%d", port)
	return grpcServer.Serve(lis)
}

// StartWeb запускает HTTP сервер с поддержкой gRPC-Web (для браузерного фронтенда).
// allowedOrigins — список origin'ов, которым разрешены CORS (например, ["http://localhost:5173"]).
// Если список пуст, будут разрешены все origin (только для разработки!).
func (s *Server) StartWeb(port int, allowedOrigins []string) error {
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(UnaryAuthInterceptor),
		grpc.StreamInterceptor(StreamAuthInterceptor),
	)

	pb.RegisterMQTTReceiverServer(grpcServer, s)

	wrapper := grpcweb.WrapServer(
		grpcServer,
		grpcweb.WithCorsForRegisteredEndpointsOnly(false),
		grpcweb.WithOriginFunc(func(origin string) bool {
			if len(allowedOrigins) == 0 {
				return true // dev: allow all
			}
			low := strings.ToLower(origin)
			for _, o := range allowedOrigins {
				if strings.TrimSpace(strings.ToLower(o)) == low {
					return true
				}
			}
			return false
		}),
		grpcweb.WithAllowedRequestHeaders([]string{"*"}),
	)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Простая обработка CORS preflight
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			// Разрешаем заголовки, запрошенные браузером, и добавляем стандартные для grpc-web
			requested := r.Header.Get("Access-Control-Request-Headers")
			allow := requested
			if allow == "" {
				allow = "authorization, content-type, x-grpc-web, grpc-timeout, x-user-agent"
			} else {
				allow = allow + ", authorization, content-type, x-grpc-web, grpc-timeout, x-user-agent"
			}
			w.Header().Set("Access-Control-Allow-Headers", allow)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if wrapper.IsGrpcWebRequest(r) || wrapper.IsAcceptableGrpcCorsRequest(r) || wrapper.IsGrpcWebSocketRequest(r) {
			w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			wrapper.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("gRPC-Web server started on %s", addr)
	return http.ListenAndServe(addr, handler)
}

// BroadcastValue рассылает обновление всем подписчикам (non-blocking)
func (s *Server) BroadcastValue(update *pb.ParameterUpdate) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dropped := 0
	for ch := range s.subscribers {
		select {
		case ch <- update:
			// sent
		default:
			// subscriber channel full => drop
			dropped++
		}
	}
	if dropped > 0 {
		metrics.BroadcastDropped.Add(float64(dropped))
		logger.Log.Warn().Int("dropped", dropped).Msg("BroadcastValue: dropped messages for slow subscribers")
	}
}

// -------------------------
// gRPC методы
// -------------------------

// Login — выдаёт JWT (CheckCredentials / GenerateJWT в auth.go)
func (s *Server) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	if !CheckCredentials(req.Username, req.Password) {
		return nil, status.Error(codes.Unauthenticated, "invalid username or password")
	}
	token, err := GenerateJWT(req.Username)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to generate token: %v", err)
	}
	return &pb.LoginResponse{Token: token}, nil
}

// SubscribeParameters — серверный стриминг:
// клиент может указать список parameter_ids; если пустой — будет получать все обновления
func (s *Server) SubscribeParameters(req *pb.SubscribeRequest, stream pb.MQTTReceiver_SubscribeParametersServer) error {
	// prepare filter set for quick lookup
	filter := map[int32]struct{}{}
	for _, id := range req.ParameterIds {
		filter[id] = struct{}{}
	}

	ch := make(chan *pb.ParameterUpdate, 200) // per-subscriber buffer
	// register subscriber
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()

	// unregister on exit
	defer func() {
		s.mu.Lock()
		delete(s.subscribers, ch)
		s.mu.Unlock()
		close(ch)
	}()

	// send existing current values? (optional) — skipped here; could be added later.

	// stream loop: deliver updates that match filter (or all if filter empty)
	for update := range ch {
		if len(filter) > 0 {
			if _, ok := filter[update.ParameterId]; !ok {
				// not subscribed to this parameter
				continue
			}
		}
		if err := stream.Send(update); err != nil {
			// error while sending — client probably disconnected
			logger.Log.Info().Err(err).Msg("SubscribeParameters: send error, disconnecting subscriber")
			return err
		}
	}
	return nil
}

// ListSystems
func (s *Server) ListSystems(ctx context.Context, _ *emptypb.Empty) (*pb.ListSystemsResponse, error) {
	systems, err := s.db.ListSystems()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list systems: %v", err)
	}
	resp := &pb.ListSystemsResponse{}
	for _, sys := range systems {
		resp.Systems = append(resp.Systems, &pb.SystemInfo{
			SystemId: int32(sys.SystemID),
			Name:     sys.SystemName,
		})
	}
	return resp, nil
}

// ListDevices
func (s *Server) ListDevices(ctx context.Context, req *pb.ListDevicesRequest) (*pb.ListDevicesResponse, error) {
	devs, err := s.db.ListDevices(uint(req.SystemId))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list devices: %v", err)
	}
	resp := &pb.ListDevicesResponse{}
	for _, d := range devs {
		resp.Devices = append(resp.Devices, &pb.DeviceInfo{
			DeviceId: int32(d.DeviceID),
			SystemId: int32(d.SystemID),
			Name:     d.DeviceName,
			Category: d.Category,
			Room:     d.Room,
		})
	}
	return resp, nil
}

// ListParameters
func (s *Server) ListParameters(ctx context.Context, req *pb.ListParametersRequest) (*pb.ListParametersResponse, error) {
	params, err := s.db.ListParameters(uint(req.DeviceId), req.VisibleOnly)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list parameters: %v", err)
	}
	resp := &pb.ListParametersResponse{}
	for _, p := range params {
		isControl := p.CommandObject != nil && *p.CommandObject != ""
		resp.Parameters = append(resp.Parameters, &pb.ParameterInfo{
			ParameterId:  int32(p.ParameterID),
			Name:         p.ParameterName,
			Unit:         p.Unit,
			Type:         p.ParamType,
			PanelVisible: int32(p.PanelVisible),
			IsControl:    isControl,
		})
	}
	return resp, nil
}

// GetParameterHistory
func (s *Server) GetParameterHistory(ctx context.Context, req *pb.HistoryRequest) (*pb.HistoryResponse, error) {
	history, err := s.db.GetHistory(uint(req.ParameterId), req.StartTimestamp, req.EndTimestamp)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get history: %v", err)
	}
	resp := &pb.HistoryResponse{}
	for _, h := range history {
		resp.Values = append(resp.Values, &pb.ParameterUpdate{
			ParameterId: int32(h.ParameterID),
			Value:       h.Value,
			Timestamp:   h.Timestamp.UnixMilli(),
		})
	}
	return resp, nil
}

// SetParameter — публикует команду в MQTT через command_object (если есть)
func (s *Server) SetParameter(ctx context.Context, req *pb.SetParameterRequest) (*pb.SetParameterResponse, error) {
	// Найдём параметр в БД
	var param storage.Parameter
	if err := s.db.Conn.Where("parameter_id = ?", req.ParameterId).First(&param).Error; err != nil {
		if storageNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "parameter not found")
		}
		return nil, status.Errorf(codes.Internal, "db error: %v", err)
	}

	if param.CommandObject == nil || *param.CommandObject == "" {
		return nil, status.Error(codes.FailedPrecondition, "parameter has no command_object")
	}

	// Публикуем команду
	if s.mqttClient == nil {
		return nil, status.Error(codes.FailedPrecondition, "mqtt client is not initialized")
	}
	s.mqttClient.Publish(*param.CommandObject, req.Value)

	updated := &pb.ParameterUpdate{
		ParameterId: req.ParameterId,
		Value:       req.Value,
		Timestamp:   time.Now().UnixMilli(),
	}
	return &pb.SetParameterResponse{Success: true, Updated: updated}, nil
}

// helper: detect gorm record not found in a safe way (keep here to avoid direct dependency on gorm errors)
func storageNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	// preferred way: use errors.Is with gorm.ErrRecordNotFound
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true
	}
	// fallback: check substring (for older GORM or wrapped errors)
	if strings.Contains(strings.ToLower(err.Error()), "record not found") {
		return true
	}
	return false
}
