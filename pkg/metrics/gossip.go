package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// Counter for all types of gossip messages
	gossipMessageCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluid_gossip_message_events_total",
			Help: "Total number of gossip message events by type and status",
		},
		[]string{"type", "status"},
	)

	// Total message counter for basic metrics
	gossipMsgsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluid_gossip_messages_total",
			Help: "Total number of gossip messages by type and direction",
		},
		[]string{"type", "direction"},
	)

	gossipMembersTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "fluid_gossip_members_total",
			Help: "Current number of members in the gossip cluster",
		},
	)

	gossipMsgSizeBytes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "fluid_gossip_message_size_bytes",
			Help:    "Size of gossip messages in bytes",
			Buckets: []float64{64, 256, 1024, 4096, 16384, 65536},
		},
		[]string{"type"},
	)

	gossipCompressionRatio = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "fluid_gossip_compression_ratio",
			Help: "Compression ratio achieved for gossip messages",
		},
		[]string{"type"},
	)

	gossipOperationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "fluid_gossip_operation_duration_seconds",
			Help:    "Duration of gossip operations in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation"},
	)
)

func init() {
	MustRegister(
		gossipMessageCounter,
		gossipMsgsTotal,
		gossipMembersTotal,
		gossipMsgSizeBytes,
		gossipCompressionRatio,
		gossipOperationDuration,
	)
}

// RecordGossipMessage records a gossip message event with its type and status
func RecordGossipMessage(msgType string, status string) {
	gossipMessageCounter.WithLabelValues(msgType, status).Inc()
}

// SetGossipMembers sets the current number of cluster members
func SetGossipMembers(count float64) {
	gossipMembersTotal.Set(count)
}

// ObserveGossipMessageSize records the size of a gossip message
func ObserveGossipMessageSize(msgType string, bytes float64) {
	gossipMsgSizeBytes.WithLabelValues(msgType).Observe(bytes)
}

// SetGossipCompressionRatio sets the compression ratio for a message type
func SetGossipCompressionRatio(msgType string, ratio float64) {
	gossipCompressionRatio.WithLabelValues(msgType).Set(ratio)
}

// ObserveGossipOperationDuration records the duration of a gossip operation
func ObserveGossipOperationDuration(operation string, seconds float64) {
	gossipOperationDuration.WithLabelValues(operation).Observe(seconds)
}
