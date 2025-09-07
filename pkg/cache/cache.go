package cache

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/raj/fluid/pkg/types"
)

// LocalCache defines the Tier 1 interface.
type LocalCache interface {
	Get(serviceName string) ([]types.ServiceEndpoint, error)
	Put(serviceName string, endpoints []types.ServiceEndpoint)
	Remove(serviceName string)
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
func (c *InMemoryCache) Get(serviceName string) ([]types.ServiceEndpoint, error) {
	c.mu.RLock()
	entry, ok := c.entries[serviceName]
	c.mu.RUnlock()
	if !ok {
		// metrics.cacheMisses.Inc()
		return nil, nil
	}
	if time.Since(entry.updatedAt) > c.entryTTL {
		// Treat as miss if expired; eviction loop will clean it up.
		// metrics.cacheExpired.Inc()
		return nil, nil
	}
	// metrics.cacheHits.Inc()
	// Defensive copy to avoid external mutation.
	cp := make([]types.ServiceEndpoint, len(entry.endpoints))
	copy(cp, entry.endpoints)
	return cp, nil
}

// Put upserts the endpoints and refreshes updatedAt.
func (c *InMemoryCache) Put(serviceName string, endpoints []types.ServiceEndpoint) {
	if serviceName == "" {
		return
	}
	cp := make([]types.ServiceEndpoint, len(endpoints))
	copy(cp, endpoints)
	c.mu.Lock()
	c.entries[serviceName] = &cacheEntry{endpoints: cp, updatedAt: time.Now()}
	c.mu.Unlock()
}

// Remove deletes the service entry if present.
func (c *InMemoryCache) Remove(serviceName string) {
	c.mu.Lock()
	delete(c.entries, serviceName)
	c.mu.Unlock()
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
	now := time.Now()
	var removed int
	c.mu.Lock()
	for key, entry := range c.entries {
		if now.Sub(entry.updatedAt) > c.entryTTL {
			delete(c.entries, key)
			removed++
		}
	}
	c.mu.Unlock()
	if removed > 0 {
		c.logger.Debug("evicted expired cache entries", "count", removed)
		// metrics.cacheEvictions.Add(float64(removed))
	}
}
