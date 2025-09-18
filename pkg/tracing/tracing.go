package tracing

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Tracer names
const (
	TracerHTTP    = "httpserver"
	TracerRaft    = "raft"
	TracerService = "service"
	TracerCache   = "cache"
	TracerGossip  = "gossip"
	TracerCLI     = "fluidctl"
)

// Span names
const (
	SpanHTTPRequest   = "http.request"
	SpanRaftPropose   = "raft.propose"
	SpanRaftApply     = "raft.apply"
	SpanServiceLookup = "service.lookup"
	SpanCacheGet      = "cache.get"
	SpanCachePut      = "cache.put"
	SpanCacheRemove   = "cache.remove"
	SpanGossipLookup  = "gossip.lookup"
	SpanGossipUpsert  = "gossip.upsert"
	SpanCLICreateApp  = "fluidctl.create_app"
	SpanCLIRaftJoin   = "fluidctl.raft_join"
	SpanCLILookup     = "fluidctl.lookup_service"
)

type ShutdownFunc func(context.Context) error

// Init configures a global tracer provider. Uses stdout exporter by default when OTEL_TRACING_STDOUT=1.
// Otherwise sets up a basic in-memory provider (no-op exporter) so spans can still be created.
func Init(ctx context.Context, logger *slog.Logger) (ShutdownFunc, error) {
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "fluid"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", serviceName),
			attribute.String("library", "github.com/raj/fluid"),
		),
	)
	if err != nil {
		if logger != nil {
			logger.Warn("tracing resource init failed", "error", err)
		}
	}

	var tp *sdktrace.TracerProvider

	if os.Getenv("OTEL_TRACING_STDOUT") == "1" {
		exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			if logger != nil {
				logger.Error("stdout trace exporter init failed", "error", err)
			}
		} else {
			tp = sdktrace.NewTracerProvider(
				sdktrace.WithBatcher(exp),
				sdktrace.WithResource(res),
				sdktrace.WithSampler(sdktrace.AlwaysSample()),
			)
		}
	}

	if tp == nil {
		// Fallback to a provider without exporter; spans won't be exported but APIs work
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
		)
	}

	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
