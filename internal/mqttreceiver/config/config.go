// internal/mqttreceiver/config/config.go
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	MQTTHost             string
	MQTTClientID         string
	MQTTUsername         string
	MQTTPassword         string
	MQTTSubscribeQoS     byte
	MQTTPublishQoS       byte
	MQTTTopics           []string
	TopicPattern         string
	DBFile               string
	GRPCPort             int
	MetricsPort          int
	LogLevel             string
	HistoryRetentionDays int
	MQTTIngestQueueSize  int
	WorkerCount          int
}

func LoadConfig() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		MQTTHost:     os.Getenv("MQTT_BROKER"),
		MQTTClientID: os.Getenv("MQTT_CLIENT_ID"),
		MQTTUsername: os.Getenv("MQTT_USERNAME"),
		MQTTPassword: os.Getenv("MQTT_PASSWORD"),
		DBFile:       os.Getenv("DB_FILE"),
		LogLevel:     os.Getenv("LOG_LEVEL"),
	}

	if cfg.MQTTHost == "" {
		cfg.MQTTHost = "tcp://localhost:1883"
	}
	if cfg.MQTTClientID == "" {
		cfg.MQTTClientID = "mqttreceiver"
	}
	if cfg.DBFile == "" {
		cfg.DBFile = "brutus.db"
	}
	if cfg.TopicPattern == "" {
		cfg.TopicPattern = "{device_id}/{control_id}"
	}

	if qosStr := os.Getenv("MQTT_SUBSCRIBE_QOS"); qosStr != "" {
		qos, err := strconv.Atoi(qosStr)
		if err != nil || qos < 0 || qos > 2 {
			return nil, fmt.Errorf("invalid MQTT_SUBSCRIBE_QOS: must be 0, 1 or 2")
		}
		cfg.MQTTSubscribeQoS = byte(qos)
	} else {
		cfg.MQTTSubscribeQoS = 1
	}

	if qosStr := os.Getenv("MQTT_PUBLISH_QOS"); qosStr != "" {
		qos, err := strconv.Atoi(qosStr)
		if err != nil || qos < 0 || qos > 2 {
			return nil, fmt.Errorf("invalid MQTT_PUBLISH_QOS: must be 0, 1 or 2")
		}
		cfg.MQTTPublishQoS = byte(qos)
	} else {
		cfg.MQTTPublishQoS = 1
	}

	if topicsEnv := os.Getenv("MQTT_TOPICS"); topicsEnv != "" {
		cfg.MQTTTopics = strings.Split(topicsEnv, ",")
		for i, topic := range cfg.MQTTTopics {
			cfg.MQTTTopics[i] = strings.TrimSpace(topic)
		}
	} else {
		cfg.MQTTTopics = []string{"/devices/+/controls/+"}
	}

	if grpcPort := os.Getenv("GRPC_PORT"); grpcPort != "" {
		if p, err := strconv.Atoi(grpcPort); err == nil {
			cfg.GRPCPort = p
		} else {
			return nil, fmt.Errorf("invalid GRPC_PORT: %v", err)
		}
	} else {
		cfg.GRPCPort = 50051
	}

	if metricsPort := os.Getenv("METRICS_PORT"); metricsPort != "" {
		if p, err := strconv.Atoi(metricsPort); err == nil {
			cfg.MetricsPort = p
		} else {
			return nil, fmt.Errorf("invalid METRICS_PORT: %v", err)
		}
	} else {
		cfg.MetricsPort = 9090
	}

	if retentionStr := os.Getenv("HISTORY_RETENTION_DAYS"); retentionStr != "" {
		if r, err := strconv.Atoi(retentionStr); err == nil && r >= 1 {
			cfg.HistoryRetentionDays = r
		} else {
			return nil, fmt.Errorf("invalid HISTORY_RETENTION_DAYS")
		}
	} else {
		cfg.HistoryRetentionDays = 7
	}

	if sizeStr := os.Getenv("MQTT_INGEST_QUEUE_SIZE"); sizeStr != "" {
		if size, err := strconv.Atoi(sizeStr); err == nil && size > 0 {
			cfg.MQTTIngestQueueSize = size
		} else {
			return nil, fmt.Errorf("invalid MQTT_INGEST_QUEUE_SIZE")
		}
	} else {
		cfg.MQTTIngestQueueSize = 10000
	}

	if wcStr := os.Getenv("INGEST_WORKERS"); wcStr != "" {
		if n, err := strconv.Atoi(wcStr); err == nil && n > 0 {
			cfg.WorkerCount = n
		} else {
			return nil, fmt.Errorf("invalid INGEST_WORKERS")
		}
	} else {
		cfg.WorkerCount = 4
	}

	return cfg, nil
}
