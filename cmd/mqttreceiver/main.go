package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"brutus/internal/mqttreceiver/config"
	grpcserver "brutus/internal/mqttreceiver/grpc"
	"brutus/internal/mqttreceiver/logger"
	"brutus/internal/mqttreceiver/metrics"
	"brutus/internal/mqttreceiver/mqtt"
	"brutus/internal/mqttreceiver/storage"
	pb "brutus/proto"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	// 1. Load environment / config
	cfg, err := config.LoadEnvironment()
	if err != nil {
		panic(fmt.Sprintf("Failed to load .env: %v", err))
	}

	// 2. Init logger
	logger.Init(cfg.LogLevel)

	// 3. Init metrics and expose /metrics
	metrics.Init()

	metricsAddr := fmt.Sprintf(":%d", cfg.MetricsPort) // <-- используем порт из конфигурации
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		logger.Log.Info().Str("addr", metricsAddr).Msg("Starting metrics HTTP server")
		if err := http.ListenAndServe(metricsAddr, nil); err != nil && err != http.ErrServerClosed {
			logger.Log.Error().Err(err).Msg("Metrics HTTP server failed")
		}
	}()

	// 4. Init DB
	db, err := storage.Init(cfg.DBFile)
	if err != nil {
		logger.Log.Fatal().Err(err).Msg("Database init failed")
	}
	defer func() {
		_ = db.Close()
	}()

	// 5. Periodic cleanup of old history
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		// run once at start
		if err := db.CleanOldHistory(cfg.HistoryRetentionDays); err != nil {
			logger.Log.Error().Err(err).Msg("Initial history cleanup failed")
		}
		for {
			select {
			case <-ticker.C:
				logger.Log.Info().Msg("Starting scheduled history cleanup")
				if err := db.CleanOldHistory(cfg.HistoryRetentionDays); err != nil {
					logger.Log.Error().Err(err).Msg("Failed to clean old history")
				}
			case <-ctx.Done():
				logger.Log.Info().Msg("History cleanup goroutine stopping")
				return
			}
		}
	}()

	// // 6. Import config into DB (idempotent)
	// conf, err := config.LoadConfig(cfg.ConfigPath)
	// if err != nil {
	// 	logger.Log.Fatal().Err(err).Msg("Failed to load config")
	// }
	// if err := db.ImportConfig(conf); err != nil {
	// 	logger.Log.Fatal().Err(err).Msg("Failed to import config into DB")
	// }

	// 7. Build topic -> parameter_id map (and list of topics)
	paramsWithStatus, err := db.ParametersWithStatus()
	if err != nil {
		logger.Log.Fatal().Err(err).Msg("Failed to fetch parameters with status_object")
	}

	statusToParam := make(map[string]uint, len(paramsWithStatus))
	topics := make([]string, 0, len(paramsWithStatus))
	for _, p := range paramsWithStatus {
		if p.StatusObject != nil && *p.StatusObject != "" {
			topics = append(topics, *p.StatusObject)
			statusToParam[*p.StatusObject] = p.ParameterID
		}
	}
	// Protect map for safe concurrent access in case of dynamic reloads later
	var statusMu sync.RWMutex

	// 8. Channel for DB ingest + DB writer goroutine
	dbWriteChan := make(chan storage.IngestMessage, 4000) // configurable
	// Monitor queue length (set periodically)
	go func() {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		for range t.C {
			metrics.IngestQueueLength.Set(float64(len(dbWriteChan)))
		}
	}()

	// Create gRPC server instance (pass DB and mqtt client later)
	// We'll create mqtt client first, then create grpc server with mqtt client reference so SetParameter can publish.

	// 9. MQTT client (callback pushes to dbWriteChan)
	mqttClient, err := mqtt.NewClient(
		cfg.MQTTHost,
		cfg.MQTTClientID,
		topics,
		cfg.MQTTSubscribeQoS,
		cfg.MQTTPublishQoS,
		cfg.MQTTUsername,
		cfg.MQTTPassword,
		func(topic, payload string) {
			// quick lookup: topic -> parameterID
			statusMu.RLock()
			paramID, ok := statusToParam[topic]
			statusMu.RUnlock()
			if !ok {
				logger.Log.Warn().Str("topic", topic).Msg("Unknown MQTT topic (no mapping to parameter_id)")
				metrics.MsgErrors.Inc()
				return
			}
			select {
			case dbWriteChan <- storage.IngestMessage{ParameterID: paramID, Value: payload}:
				// queued
			default:
				// queue full — drop message
				metrics.DroppedMessages.Inc()
				logger.Log.Warn().Str("topic", topic).Msg("Ingest queue full, dropping message")
			}
		},
	)
	if err != nil {
		logger.Log.Fatal().Err(err).Msg("Failed to create MQTT client")
	}

	// 10. gRPC server
	grpcSrv := grpcserver.NewServer(db, mqttClient)

	// Start gRPC server in goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := grpcSrv.Start(cfg.GRPCPort); err != nil {
			// Serve returns non-nil on fatal errors; log them.
			logger.Log.Fatal().Err(err).Msg("gRPC server exited with error")
		}
	}()

	// Start gRPC-Web HTTP server in goroutine (for browser clients)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := grpcSrv.StartWeb(cfg.GRPCWebPort, nil); err != nil {
			logger.Log.Fatal().Err(err).Msg("gRPC-Web server exited with error")
		}
	}()

	// 11. DB writer goroutine: consumes dbWriteChan, writes to DB, broadcasts via gRPC
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range dbWriteChan {
			start := time.Now()
			if err := db.SaveValue(msg.ParameterID, msg.Value); err != nil {
				logger.Log.Error().Uint("parameter_id", msg.ParameterID).Err(err).Msg("Failed to save parameter value")
				metrics.MsgErrors.Inc()
			} else {
				// on success, broadcast via gRPC
				update := &pb.ParameterUpdate{
					ParameterId: int32(msg.ParameterID),
					Value:       msg.Value,
					Timestamp:   time.Now().UnixMilli(),
				}
				grpcSrv.BroadcastValue(update)
			}
			metrics.ProcessingTime.Observe(time.Since(start).Seconds())
		}
		logger.Log.Info().Msg("DB writer goroutine stopped (dbWriteChan closed)")
	}()

	// 12. Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	<-stop // wait for signal
	logger.Log.Info().Msg("Shutdown signal received, stopping...")

	// 12.1 stop accepting new messages: stop MQTT subscription and disconnect
	// Paho client provides Disconnect — our wrapper embeds mqtt.Client so it has Disconnect method
	if mqttClient != nil {
		// give 250ms to finish pending work on network
		mqttClient.Disconnect(250)
		logger.Log.Info().Msg("MQTT client disconnected")
	}

	// 12.2 stop DB writer: close channel and wait for goroutines to finish
	close(dbWriteChan)
	// cancel cleanup goroutine
	cancel()

	// wait for goroutines (db writer, grpc server goroutine, cleanup goroutine) to finish
	waitCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitCh)
	}()
	// give some time for graceful shutdown
	select {
	case <-waitCh:
		logger.Log.Info().Msg("All goroutines exited")
	case <-time.After(5 * time.Second):
		logger.Log.Warn().Msg("Timeout waiting for goroutines to stop")
	}

	logger.Log.Info().Msg("Shutdown complete")
}
