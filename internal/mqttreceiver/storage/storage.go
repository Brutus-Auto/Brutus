package storage

import (
	"brutus/internal/mqttreceiver/logger"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Структура текущих значений параметров устройств
type CurrentValue struct {
	ID        uint   `gorm:"primaryKey"`
	Device    string `gorm:"index"`
	Parameter string `gorm:"index"`
	Value     string
	UpdatedAt time.Time `gorm:"autoUpdateTime:false"`
}

// Структура истории значений параметров устройств
type History struct {
	ID        uint   `gorm:"primaryKey"`
	Device    string `gorm:"index"`
	Parameter string `gorm:"index"`
	Value     string
	Timestamp time.Time
}

// Структура надстройки над GORM
type DB struct {
	Conn *gorm.DB
}

// Инициализация БД с режимом WAL и запуском миграции
func Initialized(dbFile string) (*DB, error) {
	// Подключаемся к БД с помощью API GORM
	db, err := gorm.Open(sqlite.Open(dbFile), &gorm.Config{
		Logger: logger.NewGormLogger(),
	})
	if err != nil {
		return nil, err
	}

	// Открываем БД для ее последующего конфигурирования
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// Активируем режим WAL и другие настройки
	_, err = sqlDB.Exec(`
		PRAGMA journal_mode=WAL;
		PRAGMA synchronous=NORMAL;
		PRAGMA busy_timeout=5000;
		PRAGMA journal_size_limit=1000000;
		PRAGMA cache_size=-10000;  -- 10MB cache
		PRAGMA foreign_keys=ON;
	`)
	if err != nil {
		return nil, err
	}

	// Настройка подключений:
	// максимальное количество параллельных подключений(защита от SQL Busy),
	// максимальное количество застывших соединений
	// Время жизни одного соединения(освобождение ресурсов)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(time.Hour)

	// Запуск миграции на соответствие БД со структурами - создание таблиц если их нет, в моем случае
	err = db.AutoMigrate(&CurrentValue{}, &History{})
	if err != nil {
		return nil, err
	}

	logger.Log.Info().
		Str("component", "storage").
		Str("db_file", dbFile).
		Msg("Database initialized with WAL mode")

	return &DB{Conn: db}, nil
}

// Функция обновляет текущее значение и добавляет строку истории за одну транзакцию
func (db *DB) SaveValue(device, parameter, value string) error {
	now := time.Now().UTC().Truncate(time.Millisecond)

	return db.Conn.Transaction(func(tx *gorm.DB) error {
		// Upsert current value
		var curr CurrentValue
		result := tx.
			Where(CurrentValue{Device: device, Parameter: parameter}).
			Attrs(CurrentValue{Value: value, UpdatedAt: now}).
			FirstOrCreate(&curr)
		if result.Error != nil {
			return result.Error
		}

		// If record existed, update it
		if curr.ID != 0 && (curr.Value != value || curr.UpdatedAt.Before(now)) {
			curr.Value = value
			curr.UpdatedAt = now
			if err := tx.Save(&curr).Error; err != nil {
				return err
			}
		}

		// Добавление записи в историю
		return tx.Create(&History{
			Device:    device,
			Parameter: parameter,
			Value:     value,
			Timestamp: now,
		}).Error
	})
}

// CleanOldHistory deletes history entries older than retentionDays.
func (db *DB) CleanOldHistory(retentionDays int) error {
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays).Truncate(time.Millisecond)
	return db.Conn.Transaction(func(tx *gorm.DB) error {
		return tx.Where("timestamp < ?", cutoff).Delete(&History{}).Error
	})
}

// Функция возвращающая историю значений в хронологическом порядке
func (db *DB) GetHistory(device, parameter string, startMs, endMs int64) ([]History, error) {
	startTime := time.UnixMilli(startMs).UTC().Truncate(time.Millisecond)
	endTime := time.UnixMilli(endMs).UTC().Truncate(time.Millisecond)

	var history []History
	err := db.Conn.
		Where("device = ? AND parameter = ? AND timestamp BETWEEN ? AND ?",
			device, parameter, startTime, endTime).
		Order("timestamp ASC").
		Find(&history).Error

	return history, err
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
