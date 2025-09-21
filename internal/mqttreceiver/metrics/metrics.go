package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

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

var allCollectors = []prometheus.Collector{
	MsgReceived,
	MsgErrors,
	ProcessingTime,
	BrokerConnected,
	DroppedMessages,
	IngestQueueLength,
	BroadcastDropped,
}

// Init registers all metrics with Prometheus registry.
// It is safe to call multiple times: already-registered collectors are ignored.
func Init() {
	for _, c := range allCollectors {
		if err := prometheus.Register(c); err != nil {
			if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
				// if already registered — use the existing one (no-op)
				_ = are
				continue
			}
			// other errors are unexpected — panic to fail fast
			panic(err)
		}
	}
}

// Helper wrappers (convenience functions)

// IncMsgReceived increments the received messages counter.
func IncMsgReceived() {
	MsgReceived.Inc()
}

// IncMsgErrors increments the message error counter.
func IncMsgErrors() {
	MsgErrors.Inc()
}

// ObserveProcessingTime records processing time in seconds.
func ObserveProcessingTime(d time.Duration) {
	ProcessingTime.Observe(d.Seconds())
}

// SetBrokerConnected sets the broker connected gauge: 1 = connected, 0 = disconnected.
func SetBrokerConnected(connected bool) {
	if connected {
		BrokerConnected.Set(1)
	} else {
		BrokerConnected.Set(0)
	}
}

// IncDroppedMessages increments the dropped messages counter.
func IncDroppedMessages() {
	DroppedMessages.Inc()
}

// SetIngestQueueLength sets the current ingest queue length gauge.
func SetIngestQueueLength(n int) {
	IngestQueueLength.Set(float64(n))
}

// IncBroadcastDropped increments the broadcast dropped counter.
func IncBroadcastDropped() {
	BroadcastDropped.Inc()
}
