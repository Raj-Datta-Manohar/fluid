package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/raj/fluid/pkg/cache"
	"github.com/raj/fluid/pkg/consensus"
	"github.com/raj/fluid/pkg/gossip"
	"github.com/raj/fluid/pkg/types"
)

// FluidService orchestrates across tiers.
type FluidService struct {
	logger          *slog.Logger
	cache           cache.LocalCache
	gossip          gossip.GossipMemberlist
	consensusClient consensus.ConsensusClient
}

func NewFluidService(logger *slog.Logger, lc cache.LocalCache, g gossip.GossipMemberlist, cc consensus.ConsensusClient) *FluidService {
	if logger == nil {
		logger = slog.Default()
	}
	return &FluidService{
		logger:          logger.With("component", "fluid_service"),
		cache:           lc,
		gossip:          g,
		consensusClient: cc,
	}
}

// LookupService resolves service endpoints, preferring the local Tier 1 cache.
func (s *FluidService) LookupService(ctx context.Context, name string) ([]types.ServiceEndpoint, error) {
	if name == "" {
		return nil, errors.New("service name is required")
	}
	start := time.Now()
	endpoints, _ := s.cache.Get(name)
	if len(endpoints) > 0 {
		// metrics.cacheHits.Inc()
		return endpoints, nil
	}
	// metrics.cacheMisses.Inc()
	if s.gossip != nil {
		// Best-effort query Tier 2 on exceptional miss.
		gCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		fromGossip, err := s.gossip.Lookup(gCtx, name)
		if err == nil && len(fromGossip) > 0 {
			// Opportunistically warm the cache.
			s.cache.Put(name, fromGossip)
			// metrics.cacheBackfill.Inc()
			return fromGossip, nil
		}
	}
	// metrics.lookupErrors.Inc()
	_ = start // placeholder to potentially emit histograms: metrics.lookupLatency.Observe(time.Since(start).Seconds())
	return nil, nil
}

// CreateApp issues a critical event to the Raft cluster via the ConsensusClient.
func (s *FluidService) CreateApp(ctx context.Context, appID, ip string) error {
	if appID == "" || ip == "" {
		return errors.New("appID and ip are required")
	}
	// Optional leader readiness wait via adapter, if available.
	if w, ok := s.consensusClient.(interface{ WaitForLeader(context.Context) error }); ok {
		_ = w.WaitForLeader(ctx)
	}
	ev := &types.CreateAppEvent{AppID: appID, IP: ip}
	var lastErr error
	backoff := 50 * time.Millisecond
	for attempt := 0; attempt < 5; attempt++ {
		if err := s.consensusClient.ProposeEvent(ctx, ev); err != nil {
			if ne, notLeader := consensus.IsNotLeader(err); notLeader {
				// metrics.consensusRedirects.Inc()
				if ne.LeaderAddr != "" {
					s.logger.Info("redirecting to leader", "leader", ne.LeaderAddr)
				}
				time.Sleep(backoff)
				if backoff < time.Second {
					backoff *= 2
				}
				lastErr = err
				continue
			}
			return err
		}
		return nil
	}
	return lastErr
}
