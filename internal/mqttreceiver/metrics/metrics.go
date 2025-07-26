// internal/mqttreceiver/metrics/metrics.go

package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	MsgReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqttreceiver_messages_received_total",
		Help: "Total number of MQTT messages received.",
	})
	MsgErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqttreceiver_message_errors_total",
		Help: "Total number of errors processing messages.",
	})
	ProcessingTime = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "mqttreceiver_message_processing_seconds",
		Help:    "Time taken to process messages.",
		Buckets: prometheus.DefBuckets,
	})
	BrokerConnected = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqttreceiver_broker_connected",
		Help: "MQTT broker connection status (1=connected, 0=disconnected).",
	})
	DroppedMessages = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqttreceiver_messages_dropped_total",
		Help: "Total number of dropped MQTT messages due to full queue.",
	})
	IngestQueueLength = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqttreceiver_ingest_queue_length",
		Help: "Current number of messages in the ingest queue.",
	})
	BroadcastDropped = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqttreceiver_broadcast_dropped_total",
		Help: "Total number of messages dropped during gRPC broadcast due to slow clients.",
	})
)

func init() {
	prometheus.MustRegister(
		MsgReceived, MsgErrors,
		ProcessingTime, BrokerConnected,
		DroppedMessages, IngestQueueLength,
		BroadcastDropped,
	)
}
