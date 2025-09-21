package mqtt

import (
	"brutus/internal/mqttreceiver/logger"
	"brutus/internal/mqttreceiver/metrics"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Client оборачивает paho mqtt.Client и сохраняет параметры подключения
type Client struct {
	mqtt.Client
	topics       []string
	subscribeQoS byte
	publishQoS   byte
	onMessage    func(topic, payload string)
}

// NewClient создаёт и подключает MQTT клиента.
// onMessage вызывается при получении сообщения.
func NewClient(
	brokerURL string,
	clientID string,
	topics []string,
	subscribeQoS byte,
	publishQoS byte,
	username string,
	password string,
	onMessage func(string, string),
) (*Client, error) {

	opts := mqtt.NewClientOptions()
	opts.AddBroker(brokerURL)
	opts.SetClientID(clientID)
	opts.AutoReconnect = true
	opts.CleanSession = false // сохраняем подписки на брокере
	// keep-alive и другие параметры можно выставить через opts

	// Логирование при потере соединения
	opts.OnConnectionLost = func(c mqtt.Client, err error) {
		logger.Log.Warn().Str("component", "mqtt").Err(err).Msg("MQTT connection lost")
		metrics.BrokerConnected.Set(0)
	}

	// При (re)connect — логируем и подписываемся на топики заново (на случай, если брокер "забыл")
	opts.OnConnect = func(c mqtt.Client) {
		logger.Log.Info().Str("component", "mqtt").Msg("MQTT connection established (OnConnect)")
		// помечаем что брокер доступен
		metrics.BrokerConnected.Set(1)

		// Подписываемся на топики (включая повторные подключения)
		for _, topic := range topics {
			token := c.Subscribe(topic, subscribeQoS, func(cl mqtt.Client, msg mqtt.Message) {
				metrics.MsgReceived.Inc()
				if onMessage != nil {
					onMessage(msg.Topic(), string(msg.Payload()))
				}
			})
			if token.Wait() && token.Error() != nil {
				logger.Log.Error().
					Str("component", "mqtt").
					Str("topic", topic).
					Uint8("qos", subscribeQoS).
					Err(token.Error()).
					Msg("Subscription failed on connect")
			} else {
				logger.Log.Debug().
					Str("component", "mqtt").
					Str("topic", topic).
					Uint8("qos", subscribeQoS).
					Msg("Subscribed to topic (OnConnect)")
			}
		}
	}

	// Авторизация
	if username != "" {
		opts.SetUsername(username)
		logger.Log.Info().Str("component", "mqtt").Msg("Using MQTT username authentication")
	} else {
		logger.Log.Info().Str("component", "mqtt").Msg("No MQTT username set, connecting anonymously")
	}
	if password != "" {
		opts.SetPassword(password)
	}

	// Создаём клиента и подключаемся
	pclient := mqtt.NewClient(opts)
	if token := pclient.Connect(); token.Wait() && token.Error() != nil {
		logger.Log.Error().Err(token.Error()).Str("component", "mqtt").Msg("Failed to connect to MQTT broker")
		return nil, token.Error()
	}

	// Клиент успешно подключён — создаём wrapper
	m := &Client{
		Client:       pclient,
		topics:       topics,
		subscribeQoS: subscribeQoS,
		publishQoS:   publishQoS,
		onMessage:    onMessage,
	}

	// (Подписки уже выполняются в OnConnect, поэтому здесь ничего дополнительно не нужно.)
	logger.Log.Info().Str("component", "mqtt").Msg("MQTT client created and connected")
	return m, nil
}

// Publish публикует сообщение в топик
func (m *Client) Publish(topic, value string) {
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

// Disconnect корректно отключает клиента (quiesce — миллисекунды ожидания)
func (m *Client) Disconnect(quiesce uint) {
	// paho Client.Disconnect принимает миллисекунды ожидания
	m.Client.Disconnect(quiesce)
	metrics.BrokerConnected.Set(0)
	logger.Log.Info().Str("component", "mqtt").Msg("MQTT client disconnected")
}
