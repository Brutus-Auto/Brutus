// internal/mqttreceiver/mqtt/client.go
package mqtt

import (
	"fmt"
	"strings"

	"brutus/internal/mqttreceiver/logger"
	"brutus/internal/mqttreceiver/metrics"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Client struct {
	mqtt.Client
	topics       []string
	subscribeQoS byte
	publishQoS   byte
	onMessage    func(device, parameter, value string)
}

func NewClient(
	brokerURL string,
	clientID string,
	topics []string,
	subscribeQoS byte,
	publishQoS byte,
	username string,
	password string,
	onMessage func(string, string, string),
) (*Client, error) {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(brokerURL)
	opts.SetClientID(clientID)
	opts.AutoReconnect = true // Настроенный реконнект, keep-alive=30 по умолчанию и явно не указываю
	// Сохранение сессии для целостности отправленных QOS 1,2 сообщений и без повторной подписки
	opts.CleanSession = false
	// Логирование потери соединения
	opts.OnConnectionLost = func(c mqtt.Client, err error) {
		logger.Log.Warn().
			Str("component", "mqtt").
			Err(err).
			Msg("MQTT connection lost")
	}
	// Логирование установленного соединения
	opts.OnConnect = func(c mqtt.Client) {
		logger.Log.Info().
			Str("component", "mqtt").
			Msg("MQTT connection established")
	}

	if username != "" {
		opts.SetUsername(username)
		logger.Log.Info().Str("component", "mqtt").Msg("Using MQTT username authentication")
	} else {
		logger.Log.Info().Str("component", "mqtt").Msg("No MQTT username set, connecting anonymously")
	}

	if password != "" {
		opts.SetPassword(password)
	}

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		return nil, token.Error()
	}

	m := &Client{
		Client:       client,
		topics:       topics,
		subscribeQoS: subscribeQoS,
		publishQoS:   publishQoS,
		onMessage:    onMessage,
	}

	// Подписываемся на все топики с единым QoS
	for _, topic := range topics {
		token := client.Subscribe(topic, subscribeQoS, m.createMessageHandler())
		if token.Wait() && token.Error() != nil {
			logger.Log.Error().
				Str("component", "mqtt").
				Str("topic", topic).
				Uint8("qos", subscribeQoS).
				Err(token.Error()).
				Msg("Subscription failed")
		} else {
			logger.Log.Info().
				Str("component", "mqtt").
				Str("topic", topic).
				Uint8("qos", subscribeQoS).
				Msg("Subscribed to topic")
		}
	}

	return m, nil
}

func (m *Client) createMessageHandler() mqtt.MessageHandler {
	return func(c mqtt.Client, msg mqtt.Message) {
		t := msg.Topic()
		if len(t) > 0 && t[0] == '/' {
			t = t[1:]
		}
		parts := splitTopic(t)
		if len(parts) == 4 && parts[0] == "devices" && parts[2] == "controls" {
			device := parts[1]
			parameter := parts[3]
			value := string(msg.Payload())

			logger.Log.Debug().
				Str("component", "mqtt").
				Str("device", device).
				Str("parameter", parameter).
				Str("value", value).
				Msg("Message received")

			metrics.MsgReceived.Inc()
			m.onMessage(device, parameter, value)
		} else {
			logger.Log.Warn().
				Str("component", "mqtt").
				Str("topic", msg.Topic()).
				Msg("Received message on unexpected topic")
		}
	}
}

func (m *Client) Publish(device, parameter, value string) {
	topic := fmt.Sprintf("/devices/%s/controls/%s", device, parameter)
	token := m.Client.Publish(topic, m.publishQoS, false, value)
	if token.Wait() && token.Error() != nil {
		logger.Log.Error().
			Str("component", "mqtt").
			Err(token.Error()).
			Msg("Failed to publish command")
		metrics.MsgErrors.Inc()
	} else {
		logger.Log.Info().
			Str("component", "mqtt").
			Str("topic", topic).
			Uint8("qos", m.publishQoS).
			Str("value", value).
			Msg("Published command")
	}
}

func splitTopic(t string) []string {
	var parts []string
	for _, part := range strings.Split(t, "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}
