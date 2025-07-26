// cmd/mqttreceiver/main.go
package main

import (
	"fmt"
	"net/http"
	"time"

	"brutus/internal/mqttreceiver/config"
	"brutus/internal/mqttreceiver/grpc"
	"brutus/internal/mqttreceiver/logger"
	"brutus/internal/mqttreceiver/metrics"
	"brutus/internal/mqttreceiver/mqtt"
	"brutus/internal/mqttreceiver/storage"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Структура сообщений в приниающем канале с брокера
type IngestMessage struct {
	Device    string
	Parameter string
	Value     string
}

func main() {

	// Загрузка конфигурации
	cfg, err := config.LoadConfig()
	if err != nil {
		panic(fmt.Errorf("config error: %v", err))
	}
	// Инициализация логгера
	logger.Init()

	// Инициализация БД
	db, err := storage.Initialized(cfg.DBFile)
	if err != nil {
		logger.Log.Fatal().
			Str("component", "main").
			Err(err).
			Msg("Database init failed")
	}

	// Запукаем горутину для очистки старых записей в истории значений
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			logger.Log.Info().Str("component", "main").Msg("Starting history cleanup")
			if err := db.CleanOldHistory(cfg.HistoryRetentionDays); err != nil {
				logger.Log.Error().Str("component", "main").Err(err).Msg("Failed to clean old history")
			} else {
				logger.Log.Info().Str("component", "main").Msg("Old history cleaned successfully")
			}
		}
	}()

	var mqttClient *mqtt.Client
	grpcSrv := grpc.NewServer(db, mqttClient)

	// Объявляем канал с настраиваемым размером
	ingestQueue := make(chan IngestMessage, cfg.MQTTIngestQueueSize)

	// Условие для Воркеров, чтобы обрабатывать сообщения
	for i := 0; i < cfg.WorkerCount; i++ {
		// Отдельная горутина для каждого Воркера
		go func() {
			// Обрабатываем сообщения сообщение
			for msg := range ingestQueue {
				metrics.IngestQueueLength.Set(float64(len(ingestQueue)))
				start := time.Now()

				if err := db.SaveValue(msg.Device, msg.Parameter, msg.Value); err != nil {
					logger.Log.Error().
						Str("component", "ingestWorker").
						Err(err).
						Msg("Failed to save value")
					metrics.MsgErrors.Inc()
					continue
				}

				grpcSrv.BroadcastValue(msg.Device, msg.Parameter, msg.Value, time.Now().Unix())
				metrics.ProcessingTime.Observe(time.Since(start).Seconds())
			}
		}()
	}
	// Если буфер полон, то дропаем сообщение, иначе запись в буфер
	mqttHandler := func(device, parameter, value string) {
		select {
		case ingestQueue <- IngestMessage{device, parameter, value}:
			metrics.MsgReceived.Inc()
			metrics.IngestQueueLength.Set(float64(len(ingestQueue)))
		default:
			metrics.DroppedMessages.Inc()
			logger.Log.Warn().
				Str("component", "mqttHandler").
				Str("device", device).
				Str("parameter", parameter).
				Msg("Dropped incoming message — ingestQueue full")
		}
	}
	// Подключение к брокеру
	mqttClient, err = mqtt.NewClient(
		cfg.MQTTHost,
		cfg.MQTTClientID,
		cfg.MQTTTopics,
		cfg.MQTTSubscribeQoS,
		cfg.MQTTPublishQoS,
		cfg.MQTTUsername,
		cfg.MQTTPassword,
		mqttHandler,
	)
	if err != nil {
		logger.Log.Fatal().Str("component", "main").Err(err).Msg("MQTT client init failed")
	}
	grpcSrv.SetMQTTClient(mqttClient)

	http.Handle("/metrics", promhttp.Handler())
	go func() {
		addr := fmt.Sprintf(":%d", cfg.MetricsPort)
		logger.Log.Info().
			Str("component", "main").
			Str("metrics_addr", addr).
			Msg("Metrics endpoint listening")
		if err := http.ListenAndServe(addr, nil); err != nil {
			logger.Log.Fatal().Str("component", "main").Err(err).Msg("Metrics server failed")
		}
	}()

	if err := grpcSrv.Start(cfg.GRPCPort); err != nil {
		logger.Log.Fatal().Str("component", "main").Err(err).Msg("gRPC server failed")
	}
}
