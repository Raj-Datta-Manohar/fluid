package cache

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/raj/fluid/pkg/metrics"
	"github.com/raj/fluid/pkg/tracing"
	"github.com/raj/fluid/pkg/types"
)

// LocalCache defines the Tier 1 interface.
type LocalCache interface {
	Get(ctx context.Context, serviceName string) ([]types.ServiceEndpoint, error)
	Put(ctx context.Context, serviceName string, endpoints []types.ServiceEndpoint)
	Remove(ctx context.Context, serviceName string)
	Start(ctx context.Context)
}

// InMemoryCache is a concurrency-safe cache with TTL-based eviction.
type InMemoryCache struct {
	logger        *slog.Logger
	entryTTL      time.Duration
	sweepInterval time.Duration
	mu            sync.RWMutex
	entries       map[string]*cacheEntry
	startedOnce   sync.Once
}

type cacheEntry struct {
	endpoints []types.ServiceEndpoint
	updatedAt time.Time
}

// NewInMemoryCache constructs a new cache with TTL and sweep interval.
func NewInMemoryCache(logger *slog.Logger, entryTTL, sweepInterval time.Duration) *InMemoryCache {
	if logger == nil {
		logger = slog.Default()
	}
	return &InMemoryCache{
		logger:        logger.With("component", "cache"),
		entryTTL:      entryTTL,
		sweepInterval: sweepInterval,
		entries:       make(map[string]*cacheEntry),
	}
}

// Get returns endpoints if present and not expired.
func (c *InMemoryCache) Get(ctx context.Context, serviceName string) ([]types.ServiceEndpoint, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveCacheOperationDuration(metrics.CacheOpGet, time.Since(start).Seconds())
	}()

	tracer := otel.Tracer(tracing.TracerCache)
	_, span := tracer.Start(ctx, tracing.SpanCacheGet, trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()

	span.SetAttributes(attribute.String("cache.key", serviceName))

	c.mu.RLock()
	entry, ok := c.entries[serviceName]
	c.mu.RUnlock()

	if !ok {
		metrics.RecordCacheOperation(metrics.CacheOpGet, metrics.CacheResultMiss)
		span.SetAttributes(attribute.String("cache.result", "miss"))
		return nil, nil
	}

	if time.Since(entry.updatedAt) > c.entryTTL {
		// Treat as miss if expired; eviction loop will clean it up
		metrics.RecordCacheOperation(metrics.CacheOpGet, metrics.CacheResultExpired)
		span.SetAttributes(attribute.String("cache.result", "expired"))
		return nil, nil
	}

	metrics.RecordCacheOperation(metrics.CacheOpGet, metrics.CacheResultHit)
	span.SetAttributes(
		attribute.String("cache.result", "hit"),
		attribute.Int("cache.endpoints_count", len(entry.endpoints)),
	)
	// Defensive copy to avoid external mutation
	cp := make([]types.ServiceEndpoint, len(entry.endpoints))
	copy(cp, entry.endpoints)
	return cp, nil
}

// Put upserts the endpoints and refreshes updatedAt.
func (c *InMemoryCache) Put(ctx context.Context, serviceName string, endpoints []types.ServiceEndpoint) {
	start := time.Now()
	defer func() {
		metrics.ObserveCacheOperationDuration(metrics.CacheOpPut, time.Since(start).Seconds())
	}()

	tracer := otel.Tracer(tracing.TracerCache)
	_, span := tracer.Start(ctx, tracing.SpanCachePut, trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()

	span.SetAttributes(
		attribute.String("cache.key", serviceName),
		attribute.Int("cache.endpoints_count", len(endpoints)),
	)

	if serviceName == "" {
		metrics.RecordCacheOperation(metrics.CacheOpPut, metrics.CacheResultInvalid)
		span.SetAttributes(attribute.String("cache.result", "invalid"))
		return
	}

	cp := make([]types.ServiceEndpoint, len(endpoints))
	copy(cp, endpoints)

	c.mu.Lock()
	c.entries[serviceName] = &cacheEntry{endpoints: cp, updatedAt: time.Now()}
	c.mu.Unlock()

	metrics.RecordCacheOperation(metrics.CacheOpPut, metrics.CacheResultSuccess)
	span.SetAttributes(attribute.String("cache.result", "success"))

	// Update cache size metric (approximate size in bytes)
	size := float64(len(serviceName))
	for _, ep := range endpoints {
		size += float64(len(ep.IP) + 8) // 8 bytes for the port number
	}
	metrics.SetCacheSize(size)
}

// Remove deletes the service entry if present.
func (c *InMemoryCache) Remove(ctx context.Context, serviceName string) {
	start := time.Now()
	defer func() {
		metrics.ObserveCacheOperationDuration(metrics.CacheOpRemove, time.Since(start).Seconds())
	}()

	tracer := otel.Tracer(tracing.TracerCache)
	_, span := tracer.Start(ctx, tracing.SpanCacheRemove, trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()

	span.SetAttributes(attribute.String("cache.key", serviceName))

	c.mu.Lock()
	_, exists := c.entries[serviceName]
	delete(c.entries, serviceName)
	c.mu.Unlock()

	if exists {
		metrics.RecordCacheOperation(metrics.CacheOpRemove, metrics.CacheResultSuccess)
		span.SetAttributes(attribute.String("cache.result", "success"))
	} else {
		metrics.RecordCacheOperation(metrics.CacheOpRemove, metrics.CacheResultNotFound)
		span.SetAttributes(attribute.String("cache.result", "not_found"))
	}
}

// Start launches the eviction loop once.
func (c *InMemoryCache) Start(ctx context.Context) {
	c.startedOnce.Do(func() {
		if c.sweepInterval <= 0 {
			c.sweepInterval = time.Second
		}
		go c.evictLoop(ctx)
	})
}

func (c *InMemoryCache) evictLoop(ctx context.Context) {
	ticker := time.NewTicker(c.sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("cache eviction loop exiting", "reason", ctx.Err())
			return
		case <-ticker.C:
			c.evictExpired()
		}
	}
}

func (c *InMemoryCache) evictExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	var removed int
	for serviceName, entry := range c.entries {
		if now.Sub(entry.updatedAt) > c.entryTTL {
			delete(c.entries, serviceName)
			removed++
			metrics.IncrementCacheEvictions()
		}
	}

	if removed > 0 {
		c.logger.Debug("evicted expired cache entries", "count", removed)
	}

	// Update total cache size after eviction
	var totalSize float64
	for svc, entry := range c.entries {
		size := float64(len(svc))
		for _, ep := range entry.endpoints {
			size += float64(len(ep.IP) + 8) // 8 bytes for port
		}
		totalSize += size
	}
	metrics.SetCacheSize(totalSize)
}
