// internal/mqttreceiver/storage/schema.go
package storage

import (
	"brutus/internal/mqttreceiver/logger"
	"database/sql"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Модели (точные имена колонок и таблиц)

// systems
type System struct {
	SystemID   uint   `gorm:"primaryKey;column:system_id;autoIncrement"`
	SystemName string `gorm:"column:system_name;type:TEXT"`
}

func (System) TableName() string { return "systems" }

// devices
type Device struct {
	DeviceID   uint   `gorm:"primaryKey;column:device_id;autoIncrement"`
	SystemID   uint   `gorm:"column:system_id;not null;index:idx_devices_system_id"` // индекс для ускорения поиска устройств по системе
	DeviceName string `gorm:"column:device_name;type:TEXT"`
	Category   string `gorm:"column:category;type:TEXT"`
	Room       string `gorm:"column:room;type:TEXT"`
}

func (Device) TableName() string { return "devices" }

// parameters
type Parameter struct {
	ParameterID   uint    `gorm:"primaryKey;column:parameter_id;autoIncrement"`
	DeviceID      uint    `gorm:"column:device_id;not null;index:idx_parameters_device_id"` // индекс для ускорения поиска параметров по устройству
	ParameterName string  `gorm:"column:parameter_name;type:TEXT"`
	CommandObject *string `gorm:"column:command_object;type:TEXT"` // уникальность для непустых значений создадим через raw SQL
	StatusObject  *string `gorm:"column:status_object;type:TEXT"`  // уникальность для непустых значений создадим через raw SQL
	Unit          string  `gorm:"column:unit;type:TEXT"`
	Transforms    string  `gorm:"column:transforms;type:TEXT"`
	PanelVisible  int     `gorm:"column:panel_visible;type:TEXT"`
	ParamType     string  `gorm:"column:param_type;type:TEXT"`
	Mapping       string  `gorm:"column:mapping;type:TEXT"`
}

func (Parameter) TableName() string { return "parameters" }

// parameter_current
type ParameterCurrent struct {
	ParameterID uint      `gorm:"primaryKey;column:parameter_id"` // PK и FK на parameters.parameter_id
	Value       string    `gorm:"column:value;type:TEXT"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime"` // будем записывать время обновления
}

func (ParameterCurrent) TableName() string { return "parameter_current" }

// parameter_history
type ParameterHistory struct {
	ParameterID uint      `gorm:"column:parameter_id;not null;index:idx_history_param"` // индекс покрывающий parameter_id
	Timestamp   time.Time `gorm:"column:timestamp;not null;index:idx_history_ts"`       // отдельный индекс/для читабельности; составной индекс создадим raw SQL
	Value       string    `gorm:"column:value;type:TEXT"`
}

func (ParameterHistory) TableName() string { return "parameter_history" }

// DB wrapper
type DB struct {
	Conn *gorm.DB
}

// Init и миграция
func Init(dbFile string) (*DB, error) {
	// Подключаемся к БД через GORM+SQLite
	db, err := gorm.Open(sqlite.Open(dbFile), &gorm.Config{
		Logger: logger.NewGormLogger(),
	})
	if err != nil {
		return nil, err
	}

	// Получаем *sql.DB для выполнения PRAGMA и сырых SQL-запросов
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// PRAGMA настройки (WAL, foreign keys и др.)
	// Используем sqlDB.Exec, чтобы PRAGMA выполнился на уровне соединения
	_, err = sqlDB.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
		PRAGMA busy_timeout = 5000;
		PRAGMA journal_size_limit = 1000000;
		PRAGMA cache_size = -10000;  -- ~10MB
		PRAGMA foreign_keys = ON;
	`)
	if err != nil {
		return nil, err
	}

	// Настройка пула соединений
	// Для SQLite рекомендуется малое количество открытых соединений
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(time.Hour)

	// Создаем таблицы по структурам (если не существуют)
	// В GORM AutoMigrate создаст таблицы и колонки, но не создаст partial indexes.
	if err := db.AutoMigrate(
		&System{},
		&Device{},
		&Parameter{},
		&ParameterCurrent{},
		&ParameterHistory{},
	); err != nil {
		return nil, err
	}

	// После миграции создаём индексы и частичные уникальные индексы, которых GORM не умеет создать через теги:
	if err := ensureIndexesAndConstraints(sqlDB); err != nil {
		return nil, err
	}

	logger.Log.Info().
		Str("component", "storage").
		Str("db_file", dbFile).
		Msg("Database initialized with WAL mode and schema migrated")

	return &DB{Conn: db}, nil
}

// ensureIndexesAndConstraints создаёт дополнительные индексы и частичные уникальные индексы
func ensureIndexesAndConstraints(sqlDB *sql.DB) error {
	// Используем IF NOT EXISTS, чтобы вызов был идемпотентным

	stmts := []string{
		// Индекс на devices.system_id (на всякий случай, GORM уже создал индекс по тегу, но оставим IF NOT EXISTS)
		`CREATE INDEX IF NOT EXISTS idx_devices_system_id ON devices(system_id);`,

		// Индекс на parameters.device_id
		`CREATE INDEX IF NOT EXISTS idx_parameters_device_id ON parameters(device_id);`,

		// Составной индекс для ускоренной выборки истории по параметру и времени
		`CREATE INDEX IF NOT EXISTS idx_history_param_ts ON parameter_history(parameter_id, timestamp);`,

		// Частичные уникальные индексы: уникальность только для непустых (NOT NULL) значений.
		// Это поведение соответствует требованию: "значения могут быть пустыми/NULL, но непустые — уникальны".
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_parameters_status_obj ON parameters(status_object) WHERE status_object IS NOT NULL;`,

		`CREATE UNIQUE INDEX IF NOT EXISTS idx_parameters_command_obj ON parameters(command_object) WHERE command_object IS NOT NULL;`,
	}

	for _, s := range stmts {
		if _, err := sqlDB.Exec(s); err != nil {
			return err
		}
	}

	return nil
}

// Закрываем БД
func (db *DB) Close() error {
	sqlDB, err := db.Conn.DB()
	if err != nil {
		return err
	}

	// Переносим данные с WAL-журнала в основную БД, игнорируя ошибку переноса
	_, _ = sqlDB.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
	return sqlDB.Close()
}

// func (db *DB) Close() error {
//     sqlDB, err := db.Conn.DB()
//     if err != nil {
//         return err
//     }

//     // Попытки выполнить checkpoint, если он не прошёл — повторим несколько раз
//     const maxAttempts = 5
//     for i := 1; i <= maxAttempts; i++ {
//         if _, err := sqlDB.Exec("PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
//             // Если это последняя попытка — логируем и продолжаем к Close (чтобы не блокировать завершение)
//             logger.Log.Warn().
//                 Err(err).
//                 Int("attempt", i).
//                 Msg("wal_checkpoint(TRUNCATE) failed")
//             time.Sleep(time.Duration(i) * 200 * time.Millisecond) // экспоненциальный бэкофф
//             continue
//         }
//         logger.Log.Info().Int("attempt", i).Msg("wal_checkpoint(TRUNCATE) succeeded")
//         break
//     }

//     // Закрываем DB — возвращаем ошибку, если есть
//     if err := sqlDB.Close(); err != nil {
//         logger.Log.Error().Err(err).Msg("sqlDB.Close failed")
//         return err
//     }
//     return nil
// }
