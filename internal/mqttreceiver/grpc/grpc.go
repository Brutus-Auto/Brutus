// internal/mqttreceiver/grpc/server.go

package grpc

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"

	"brutus/internal/mqttreceiver/logger"
	"brutus/internal/mqttreceiver/metrics"
	"brutus/internal/mqttreceiver/mqtt"
	"brutus/internal/mqttreceiver/storage"
	pb "brutus/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
)

type Server struct {
	pb.UnimplementedMQTTReceiverServer
	mqttClient  *mqtt.Client
	db          *storage.DB
	mu          sync.Mutex
	subscribers map[chan *pb.Value]struct{}
}

// SetMQTTClient устанавливает MQTT клиента после инициализации
func (s *Server) SetMQTTClient(client *mqtt.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mqttClient = client
}

// NewServer создает новый экземпляр gRPC сервера
func NewServer(db *storage.DB, mqttClient *mqtt.Client) *Server {
	return &Server{
		mqttClient:  mqttClient,
		db:          db,
		subscribers: make(map[chan *pb.Value]struct{}),
	}
}

// DataExchange обрабатывает двунаправленный поток команд и значений
func (s *Server) DataExchange(stream pb.MQTTReceiver_DataExchangeServer) error {
	// Получение информации о клиенте
	peerInfo, ok := peer.FromContext(stream.Context())
	if ok {
		logger.Log.Info().
			Str("component", "grpc").
			Str("client_addr", peerInfo.Addr.String()).
			Msg("Client connected to DataExchange")
	} else {
		logger.Log.Info().
			Str("component", "grpc").
			Msg("Client connected to DataExchange (IP unknown)")
	}

	ch := make(chan *pb.Value, 100)

	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.subscribers, ch)
		close(ch)
		s.mu.Unlock()

		logger.Log.Info().
			Str("component", "grpc").
			Msg("Client disconnected from DataExchange")
	}()

	// Поток отправки данных клиенту
	go func() {
		for val := range ch {
			if err := stream.Send(val); err != nil {
				logger.Log.Error().
					Str("component", "grpc").
					Err(err).
					Msg("Failed to send value to client")
				return
			}
		}
	}()

	// Поток получения команд от клиента
	for {
		cmd, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			logger.Log.Error().
				Str("component", "grpc").
				Err(err).
				Msg("Error receiving from client")
			return err
		}

		logger.Log.Info().
			Str("component", "grpc").
			Str("device", cmd.Device).
			Str("param", cmd.Parameter).
			Str("value", cmd.Value).
			Msg("Command received from gRPC client")

		s.mqttClient.Publish(cmd.Device, cmd.Parameter, cmd.Value)
	}
}

// BroadcastValue отправляет значение всем подписчикам
func (s *Server) BroadcastValue(device, parameter, value string, timestamp int64) {
	msg := &pb.Value{
		Device:    device,
		Parameter: parameter,
		Value:     value,
		Timestamp: timestamp,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dropped := 0
	for ch := range s.subscribers {
		select {
		case ch <- msg:
			// отправлено успешно
		default:
			dropped++
		}
	}

	if dropped > 0 {
		logger.Log.Warn().
			Str("component", "grpc").
			Int("dropped", dropped).
			Msg("BroadcastValue: some subscriber channels full, dropped messages")
		metrics.BroadcastDropped.Add(float64(dropped))
	}
}

// GetHistory реализует получение истории значений по параметру и периоду
func (s *Server) GetHistory(ctx context.Context, req *pb.HistoryRequest) (*pb.HistoryResponse, error) {
	// Вызываем метод storage.GetHistory с Unix миллисекундами
	history, err := s.db.GetHistory(req.Device, req.Parameter, req.StartTimestamp, req.EndTimestamp)
	if err != nil {
		logger.Log.Error().
			Str("component", "grpc").
			Err(err).
			Msg("Failed to get history")
		return nil, err
	}

	values := make([]*pb.Value, 0, len(history))
	for _, h := range history {
		values = append(values, &pb.Value{
			Device:    h.Device,
			Parameter: h.Parameter,
			Value:     h.Value,
			Timestamp: h.Timestamp.UnixMilli(), // преобразуем time.Time в Unix миллисекунды
		})
	}

	return &pb.HistoryResponse{Values: values}, nil
}

// Start запускает gRPC сервер
func (s *Server) Start(port int) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer()
	pb.RegisterMQTTReceiverServer(grpcServer, s)
	reflection.Register(grpcServer)

	logger.Log.Info().
		Str("component", "grpc").
		Int("port", port).
		Msg("gRPC server listening")

	return grpcServer.Serve(lis)
}
