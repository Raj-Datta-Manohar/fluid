package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluid_http_requests_total",
			Help: "Total number of HTTP requests by method, endpoint and status",
		},
		[]string{"method", "endpoint", "status"},
	)

	httpRequestDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "fluid_http_request_duration_seconds",
			Help:    "Duration of HTTP requests",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"method", "endpoint"},
	)

	httpActiveRequests = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "fluid_http_active_requests",
			Help: "Number of currently active HTTP requests",
		},
	)
)

func init() {
	MustRegister(
		httpRequestsTotal,
		httpRequestDurationSeconds,
		httpActiveRequests,
	)
}

// RecordHTTPRequest records an HTTP request with its result
func RecordHTTPRequest(method, endpoint, status string) {
	httpRequestsTotal.WithLabelValues(method, endpoint, status).Inc()
}

// ObserveHTTPRequestDuration records the duration of an HTTP request
func ObserveHTTPRequestDuration(method, endpoint string, duration float64) {
	httpRequestDurationSeconds.WithLabelValues(method, endpoint).Observe(duration)
}

// IncrementActiveRequests increments the active request counter
func IncrementActiveRequests() {
	httpActiveRequests.Inc()
}

// DecrementActiveRequests decrements the active request counter
func DecrementActiveRequests() {
	httpActiveRequests.Dec()
}
