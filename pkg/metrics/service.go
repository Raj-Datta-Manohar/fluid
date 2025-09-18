package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Service metrics labels (use constants to avoid typos and keep low cardinality)
const (
	// Operations
	ServiceOpLookup = "lookup"

	// Results
	ServiceResultCacheHit       = "cache_hit"
	ServiceResultCacheMiss      = "cache_miss"
	ServiceResultGossipBackfill = "gossip_backfill"
	ServiceResultNotFound       = "not_found"
)

var (
	serviceLookupCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluid_service_lookup_total",
			Help: "Total number of service lookups by result",
		},
		[]string{"operation", "result"},
	)

	serviceLookupDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "fluid_service_lookup_duration_seconds",
			Help:    "Duration of service lookups",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation"},
	)

	serviceConsensusRedirects = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "fluid_service_consensus_redirects_total",
			Help: "Total number of consensus redirects observed in service layer",
		},
	)
)

func init() {
	MustRegister(
		serviceLookupCounter,
		serviceLookupDurationSeconds,
		serviceConsensusRedirects,
	)
}

// RecordServiceLookup records the outcome of a service lookup.
func RecordServiceLookup(operation string, result string) {
	serviceLookupCounter.WithLabelValues(operation, result).Inc()
}

// ObserveServiceLookupDuration records the duration of a service lookup.
func ObserveServiceLookupDuration(operation string, seconds float64) {
	serviceLookupDurationSeconds.WithLabelValues(operation).Observe(seconds)
}

// IncrementServiceConsensusRedirects increments the consensus redirect counter.
func IncrementServiceConsensusRedirects() {
	serviceConsensusRedirects.Inc()
}
