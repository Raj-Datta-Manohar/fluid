package httpserver

import (
	"fmt"
	"net/http"
	"time"

	"github.com/raj/fluid/pkg/metrics"
	"github.com/raj/fluid/pkg/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// metricsMiddleware wraps an http.Handler and records metrics about the request
type metricsMiddleware struct {
	handler     http.Handler
	handlerName string
}

// ServeHTTP implements http.Handler
func (m *metricsMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	metrics.IncrementActiveRequests()
	defer metrics.DecrementActiveRequests()

	// Create a response wrapper to capture the status code
	wrapped := &responseWriter{ResponseWriter: w}

	// Start HTTP server span
	tracer := otel.Tracer(tracing.TracerHTTP)
	ctx, span := tracer.Start(r.Context(), tracing.SpanHTTPRequest, trace.WithSpanKind(trace.SpanKindServer))
	span.SetAttributes(
		attribute.String("http.method", r.Method),
		attribute.String("http.route", m.handlerName),
	)

	// Call the wrapped handler with context containing the span
	m.handler.ServeHTTP(wrapped, r.WithContext(ctx))

	// Record metrics after the handler returns
	duration := time.Since(start).Seconds()
	endpoint := fmt.Sprintf("%s %s", r.Method, m.handlerName)

	metrics.ObserveHTTPRequestDuration(r.Method, endpoint, duration)
	metrics.RecordHTTPRequest(r.Method, endpoint, fmt.Sprintf("%d", wrapped.statusCode))

	// Finish span with status
	span.SetAttributes(attribute.Int("http.status_code", wrapped.statusCode))
	if wrapped.statusCode >= 500 {
		span.SetStatus(codes.Error, "server error")
	}
	span.End()
}

// responseWriter wraps http.ResponseWriter to capture the status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}
	return rw.ResponseWriter.Write(b)
}

// wrapWithMetrics wraps an http.Handler with metrics collection
func wrapWithMetrics(handlerName string, handler http.Handler) http.Handler {
	return &metricsMiddleware{
		handler:     handler,
		handlerName: handlerName,
	}
}

// wrapHandlerFuncWithMetrics wraps an http.HandlerFunc with metrics collection
func wrapHandlerFuncWithMetrics(handlerName string, handler http.HandlerFunc) http.Handler {
	return wrapWithMetrics(handlerName, handler)
}
