package mqtt

import "brutus/internal/mqttreceiver/storage"

// ParseMessage конвертирует topic + payload в IngestMessage
func ParseMessage(topic, payload string, statusToParam map[string]uint) storage.IngestMessage {
	paramID := statusToParam[topic]
	return storage.IngestMessage{
		ParameterID: paramID,
		Value:       payload,
	}
}
