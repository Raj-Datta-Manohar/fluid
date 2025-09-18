package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	registry = prometheus.NewRegistry()
)

// Registry returns the global metrics registry
func Registry() *prometheus.Registry {
	return registry
}

// MustRegister registers the provided collectors with the global registry
func MustRegister(cs ...prometheus.Collector) {
	registry.MustRegister(cs...)
}

// Unregister removes the provided collectors from the global registry
func Unregister(cs ...prometheus.Collector) bool {
	for _, c := range cs {
		if !registry.Unregister(c) {
			return false
		}
	}
	return true
}
