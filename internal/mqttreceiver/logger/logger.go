// internal/mqttreceiver/logger/logger.go

package logger

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

var Log zerolog.Logger

func Init(log_level string) {
	output := zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: time.RFC3339,
		FormatLevel: func(i interface{}) string {
			return strings.ToUpper(fmt.Sprintf("- %-5s -", i))
		},
		FormatMessage: func(i interface{}) string {
			return fmt.Sprintf("%s", i)
		},
	}

	lvl, err := zerolog.ParseLevel(strings.ToLower(log_level))
	if err != nil {
		lvl = zerolog.InfoLevel
	}

	Log = zerolog.New(output).
		Level(lvl).
		With().
		Timestamp().
		Logger()

	Log.Info().Msg("Logger initialized")
}
