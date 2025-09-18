package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Cache metrics label constants
const (
	// Operations
	CacheOpGet    = "get"
	CacheOpPut    = "put"
	CacheOpRemove = "remove"

	// Results
	CacheResultHit      = "hit"
	CacheResultMiss     = "miss"
	CacheResultExpired  = "expired"
	CacheResultInvalid  = "invalid"
	CacheResultSuccess  = "success"
	CacheResultNotFound = "not_found"
)

var (
	cacheOpsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluid_cache_operations_total",
			Help: "Total number of cache operations by type and result",
		},
		[]string{"operation", "result"},
	)

	cacheSizeBytes = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "fluid_cache_size_bytes",
			Help: "Current size of the cache in bytes",
		},
	)

	cacheEvictionsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "fluid_cache_evictions_total",
			Help: "Total number of cache evictions",
		},
	)

	cacheOpDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "fluid_cache_operation_duration_seconds",
			Help:    "Duration of cache operations",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation"},
	)
)

func init() {
	MustRegister(
		cacheOpsTotal,
		cacheSizeBytes,
		cacheEvictionsTotal,
		cacheOpDurationSeconds,
	)
}

// RecordCacheOperation records a cache operation with its result
func RecordCacheOperation(op string, result string) {
	cacheOpsTotal.WithLabelValues(op, result).Inc()
}

// SetCacheSize sets the current cache size in bytes
func SetCacheSize(bytes float64) {
	cacheSizeBytes.Set(bytes)
}

// IncrementCacheEvictions increments the eviction counter
func IncrementCacheEvictions() {
	cacheEvictionsTotal.Inc()
}

// ObserveCacheOperationDuration records the duration of a cache operation
func ObserveCacheOperationDuration(op string, duration float64) {
	cacheOpDurationSeconds.WithLabelValues(op).Observe(duration)
}
