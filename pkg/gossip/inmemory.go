package gossip

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/raj/fluid/pkg/cache"
	"github.com/raj/fluid/pkg/types"
)

// InMemoryGossip is a basic, single-process gossip placeholder that
// serves as Tier 2 for local dev/testing. It backfills Tier 1 cache on updates.
type InMemoryGossip struct {
	logger *slog.Logger
	cache  cache.LocalCache

	mu      sync.RWMutex
	entries map[string][]types.ServiceEndpoint
}

func NewInMemoryGossip(logger *slog.Logger, lc cache.LocalCache) *InMemoryGossip {
	if logger == nil {
		logger = slog.Default()
	}
	return &InMemoryGossip{
		logger:  logger.With("component", "gossip_inmemory"),
		cache:   lc,
		entries: make(map[string][]types.ServiceEndpoint),
	}
}

func (g *InMemoryGossip) Lookup(ctx context.Context, serviceName string) ([]types.ServiceEndpoint, error) {
	g.mu.RLock()
	eps := g.entries[serviceName]
	g.mu.RUnlock()
	if len(eps) == 0 {
		return nil, nil
	}
	cp := make([]types.ServiceEndpoint, len(eps))
	copy(cp, eps)
	return cp, nil
}

func (g *InMemoryGossip) Upsert(ctx context.Context, serviceName string, endpoints []types.ServiceEndpoint) error {
	if serviceName == "" {
		return errors.New("serviceName required")
	}
	cp := make([]types.ServiceEndpoint, len(endpoints))
	copy(cp, endpoints)
	g.mu.Lock()
	g.entries[serviceName] = cp
	g.mu.Unlock()
	// Backfill Tier 1 cache
	g.cache.Put(ctx, serviceName, cp)
	return nil
}

func (g *InMemoryGossip) Remove(ctx context.Context, serviceName string) error {
	if serviceName == "" {
		return errors.New("serviceName required")
	}
	g.mu.Lock()
	delete(g.entries, serviceName)
	g.mu.Unlock()
	g.cache.Remove(ctx, serviceName)
	return nil
}
