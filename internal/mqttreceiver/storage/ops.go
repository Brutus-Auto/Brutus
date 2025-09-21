package storage

import (
	"encoding/json"
	"strings"
	"time"

	"brutus/internal/mqttreceiver/config"
	"brutus/internal/mqttreceiver/logger"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// IngestMessage — сообщение для записи в DB Writer
type IngestMessage struct {
	ParameterID uint
	Value       string
}

// SaveValue сохраняет текущее значение и историю параметра
func (db *DB) SaveValue(parameterID uint, value string) error {
	now := time.Now().UTC().Truncate(time.Millisecond)

	return db.Conn.Transaction(func(tx *gorm.DB) error {
		curr := ParameterCurrent{
			ParameterID: parameterID,
			Value:       value,
			UpdatedAt:   now,
		}

		if err := tx.
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "parameter_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
			}).
			Create(&curr).Error; err != nil {
			logger.Log.Error().Err(err).Uint("parameter_id", parameterID).Msg("SaveValue: failed to upsert parameter_current")
			return err
		}

		h := ParameterHistory{
			ParameterID: parameterID,
			Value:       value,
			Timestamp:   now,
		}

		if err := tx.Create(&h).Error; err != nil {
			logger.Log.Error().Err(err).Uint("parameter_id", parameterID).Msg("SaveValue: failed to insert parameter_history")
			return err
		}
		return nil
	})
}

// CleanOldHistory удаляет историю старше retentionDays
func (db *DB) CleanOldHistory(retentionDays int) error {
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays).Truncate(time.Millisecond)
	return db.Conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("timestamp < ?", cutoff).Delete(&ParameterHistory{}).Error; err != nil {
			logger.Log.Error().Err(err).Time("cutoff", cutoff).Int("retention_days", retentionDays).Msg("CleanOldHistory failed")
			return err
		}
		logger.Log.Info().Time("cutoff", cutoff).Int("retention_days", retentionDays).Msg("Old history cleaned")
		return nil
	})
}

// GetHistory возвращает историю значений
func (db *DB) GetHistory(parameterID uint, startMs, endMs int64) ([]ParameterHistory, error) {
	startTime := time.UnixMilli(startMs).UTC().Truncate(time.Millisecond)
	endTime := time.UnixMilli(endMs).UTC().Truncate(time.Millisecond)

	var out []ParameterHistory
	err := db.Conn.
		Where("parameter_id = ? AND timestamp BETWEEN ? AND ?", parameterID, startTime, endTime).
		Order("timestamp ASC").
		Find(&out).Error
	if err != nil {
		logger.Log.Error().Err(err).Uint("parameter_id", parameterID).Time("start", startTime).Time("end", endTime).Msg("GetHistory failed")
		return nil, err
	}
	return out, nil
}

// ImportConfig загружает конфигурацию (config.Conf) в БД.
// Если необходимо, можно перед вставкой очищать старые данные — сейчас вставка идёт напрямую.
func (db *DB) ImportConfig(conf *config.Conf) error {
	return db.Conn.Transaction(func(tx *gorm.DB) error {
		for _, sys := range conf.Systems {
			sysRow := System{
				SystemName: sys.Name,
			}
			if err := tx.Create(&sysRow).Error; err != nil {
				logger.Log.Error().Err(err).Msg("ImportConfig: Failed to insert system")
				return err
			}

			for _, dev := range sys.Devices {
				devRow := Device{
					SystemID:   sysRow.SystemID,
					DeviceName: dev.Name,
					Category:   dev.Category,
					Room:       dev.Room,
				}
				if err := tx.Create(&devRow).Error; err != nil {
					logger.Log.Error().Err(err).Msg("ImportConfig: Failed to insert device")
					return err
				}

				for _, p := range dev.Parameters {
					// convert panel_visible string to int
					pv := 0
					if strings.ToLower(p.PanelVisible) == "true" {
						pv = 1
					}

					// serialize transforms if any
					trStr := ""
					if p.Transforms != nil {
						raw, _ := json.Marshal(p.Transforms)
						trStr = string(raw)
					}

					var cmdObj *string
					if p.CommandObject != "" {
						cmdObj = &p.CommandObject
					}
					var statObj *string
					if p.StatusObject != "" {
						statObj = &p.StatusObject
					}

					paramRow := Parameter{
						DeviceID:      devRow.DeviceID,
						ParameterName: p.Name,
						StatusObject:  statObj,
						CommandObject: cmdObj,
						Unit:          p.Unit,
						Transforms:    trStr,
						PanelVisible:  pv,
						ParamType:     p.ParamType,
						Mapping:       p.Mapping,
					}

					if err := tx.Create(&paramRow).Error; err != nil {
						logger.Log.Error().Err(err).Msg("ImportConfig: Failed to insert parameter")
						return err
					}
				}
			}
		}
		return nil
	})
}

// ----------------------
// Новые функции для gRPC и main
// ----------------------

// ListAllParameters возвращает все параметры (включая те у которых status_object == NULL)
func (db *DB) ListAllParameters() ([]Parameter, error) {
	var params []Parameter
	if err := db.Conn.Find(&params).Error; err != nil {
		return nil, err
	}
	return params, nil
}

// ParametersWithStatus возвращает все параметры, у которых status_object не NULL
func (db *DB) ParametersWithStatus() ([]Parameter, error) {
	var params []Parameter
	if err := db.Conn.Where("status_object IS NOT NULL").Find(&params).Error; err != nil {
		return nil, err
	}
	return params, nil
}

// ListSystems возвращает все системы
func (db *DB) ListSystems() ([]System, error) {
	var systems []System
	err := db.Conn.Find(&systems).Error
	return systems, err
}

// ListDevices возвращает устройства системы
func (db *DB) ListDevices(systemID uint) ([]Device, error) {
	var devices []Device
	err := db.Conn.Where("system_id = ?", systemID).Find(&devices).Error
	return devices, err
}

// ListParameters возвращает параметры устройства (visibleOnly фильтрует panel_visible)
func (db *DB) ListParameters(deviceID uint, visibleOnly bool) ([]Parameter, error) {
	var params []Parameter
	query := db.Conn.Where("device_id = ?", deviceID)
	if visibleOnly {
		query = query.Where("panel_visible = ?", 1)
	}
	err := query.Find(&params).Error
	return params, err
}

// GetStatusTopics возвращает список топиков MQTT по status_object
func (db *DB) GetStatusTopics() ([]string, error) {
	var params []Parameter
	if err := db.Conn.Where("status_object IS NOT NULL").Find(&params).Error; err != nil {
		return nil, err
	}
	topics := make([]string, 0, len(params))
	for _, p := range params {
		if p.StatusObject != nil {
			topics = append(topics, *p.StatusObject)
		}
	}
	return topics, nil
}

// ParameterIDByTopic возвращает parameter_id по топику MQTT
func (db *DB) ParameterIDByTopic(topic string) (uint, error) {
	var p Parameter
	if err := db.Conn.Where("status_object = ?", topic).First(&p).Error; err != nil {
		return 0, err
	}
	return p.ParameterID, nil
}
