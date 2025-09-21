// internal/mqttreceiver/config/env.go

package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv" // Используем библиотеку для загрузки из файлов .env
)

type Config struct {
	MQTTHost             string
	MQTTClientID         string
	MQTTUsername         string
	MQTTPassword         string
	ConfigPath           string
	DBFile               string
	LogLevel             string
	JWTSecret            string
	AuthUsername         string
	AuthPassword         string
	MQTTSubscribeQoS     byte
	MQTTPublishQoS       byte
	GRPCPort             int
	GRPCWebPort          int
	MetricsPort          int
	HistoryRetentionDays int
	MQTTIngestQueueSize  int
	IngestWorkers        int
}

func LoadEnvironment() (*Config, error) {
	// Используем Load для поиска файла .env в текущей директории и загрузки пары значений KEY=VALUE
	if err := godotenv.Load(); err != nil {
		return nil, err
	}

	cfg := &Config{
		MQTTHost:     getenvDefault("MQTT_BROKER", "tcp://localhost:1883"),
		MQTTClientID: getenvDefault("MQTT_CLIENT_ID", "mqttreceiver"),
		MQTTUsername: os.Getenv("MQTT_USERNAME"),
		MQTTPassword: os.Getenv("MQTT_PASSWORD"),
		ConfigPath:   getenvDefault("CONFIG_PATH", "conf.json"),
		DBFile:       getenvDefault("DB_FILE", "brutus.db"),
		LogLevel:     getenvDefault("LOG_LEVEL", "info"),
		JWTSecret:    getenvDefault("JWT_SECRET", "some-very-secret-key"),
		AuthUsername: getenvDefault("AUTH_USERNAME", "admin"),
		AuthPassword: getenvDefault("AUTH_PASSWORD", "s3cr3t"),
	}

	// MQTT QoS (должен быть 0, 1 или 2, возвращаем значение по умолчанию если упущен)
	cfg.MQTTSubscribeQoS = byte(getenvIntInRange("MQTT_SUBSCRIBE_QOS", 0, 2, 0))
	cfg.MQTTPublishQoS = byte(getenvIntInRange("MQTT_PUBLISH_QOS", 0, 2, 0))

	// gRPC порт (значение по умолчанию 50052)
	cfg.GRPCPort = getenvIntDefault("GRPC_PORT", 50052)

	// gRPC-Web HTTP порт (значение по умолчанию 8080)
	cfg.GRPCWebPort = getenvIntDefault("GRPC_WEB_PORT", 8080)

	// Порт для метрик (значение по умолчанию 9090)
	cfg.MetricsPort = getenvIntDefault("METRICS_PORT", 9090)

	// History retention days (минимум 1, значение по умолчанию 7)
	cfg.HistoryRetentionDays = getenvIntMin("HISTORY_RETENTION_DAYS", 1, 7)

	// Размер канала входящих топиков (минимум 1, значение по умолчанию 10000)
	cfg.MQTTIngestQueueSize = getenvIntMin("MQTT_INGEST_QUEUE_SIZE", 1, 10000)

	// Количество воркеров (минимум 1, значение по умолчанию 4)
	cfg.IngestWorkers = getenvIntMin("INGEST_WORKERS", 1, 4)

	return cfg, nil
}

// --- Функции обрабатывающие конфиг ---

func getenvDefault(key, def string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return def
}

func getenvIntDefault(key string, def int) int {
	val := os.Getenv(key)
	if val == "" {
		return def // переменной нет — используем дефолт
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		fmt.Printf("Warning: invalid %s value %q, using default %d\n", key, val, def)
		return def // некорректное значение — используем дефолт
	}
	return n
}

func getenvIntInRange(key string, min, max, def int) int {
	val := os.Getenv(key)
	if val == "" {
		return def // переменной нет — используем дефолт
	}

	n, err := strconv.Atoi(val)
	if err != nil || n < min || n > max {
		fmt.Printf("Warning: invalid %s value %q, using default %d\n", key, val, def)
		return def // некорректное значение — используем дефолт
	}

	return n // валидное значение
}

func getenvIntMin(key string, min, def int) int {
	val := os.Getenv(key)

	if val == "" {
		return def // переменной нет — используем дефолт
	}

	n, err := strconv.Atoi(val)
	if err != nil || n < min {
		fmt.Printf("invalid %s: must be at least %d", key, min)
		return def // некорректное значение — используем дефолт
	}

	return n // валидное значение
}
