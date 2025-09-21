// internal/mqttreceiver/config/config.go

package config

import (
	"encoding/json"
	"os"
)

type Conf struct {
	Systems []System `json:"Systems"`
}

type System struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Devices []Device `json:"devices"`
}

type Device struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Category   string      `json:"category"`
	Room       string      `json:"room"`
	Parameters []Parameter `json:"parameters"`
}

type Parameter struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	PanelVisible  string     `json:"panel_visible"`
	ParamType     string     `json:"param_type"`
	Mapping       string     `json:"mapping,omitempty"`
	Unit          string     `json:"unit,omitempty"`
	StatusObject  string     `json:"status_object"`
	CommandObject string     `json:"command_object,omitempty"`
	Transforms    *Transform `json:"transforms,omitempty"`
}

type Transform struct {
	Type string `json:"type"`
	K    string `json:"k"`
	B    string `json:"b"`
}

// Загружаем конфиг
func LoadConfig(filename string) (*Conf, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var config Conf
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}
