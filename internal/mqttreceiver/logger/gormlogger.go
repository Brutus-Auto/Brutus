// internal/mqttreceiver/config/gormlogger.go

package logger

import (
	"context"
	"time"

	"gorm.io/gorm/logger"
)

type GormLogger struct{}

func NewGormLogger() logger.Interface {
	return &GormLogger{}
}

func (l *GormLogger) LogMode(level logger.LogLevel) logger.Interface {
	return l
}

func (l *GormLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	Log.Info().Msgf(msg, data...)
}

func (l *GormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	Log.Warn().Msgf(msg, data...)
}

func (l *GormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	Log.Error().Msgf(msg, data...)
}

func (l *GormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	elapsed := time.Since(begin)
	sql, rows := fc()

	event := Log.Debug().
		Dur("elapsed", elapsed).
		Str("sql", sql).
		Int64("rows", rows)

	if err != nil {
		event = Log.Error().
			Err(err).
			Dur("elapsed", elapsed).
			Str("sql", sql).
			Int64("rows", rows)
	}

	event.Msg("gorm query")
}
