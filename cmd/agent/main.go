package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/raj/fluid/pkg/cache"
	"github.com/raj/fluid/pkg/consensus"
	"github.com/raj/fluid/pkg/gossip"
	"github.com/raj/fluid/pkg/httpserver"
	"github.com/raj/fluid/pkg/service"
	"github.com/raj/fluid/pkg/types"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Tier 1
	lc := cache.NewInMemoryCache(logger, 10*time.Second, 1*time.Second)
	lc.Start(ctx)

	// Tier 2 (in-memory placeholder)
	g := gossip.NewInMemoryGossip(logger, lc)

	// Tier 3: real single-node Raft adapter
	dataDir := filepath.Join(os.TempDir(), "fluid-raft")
	_ = os.MkdirAll(dataDir, 0o755)
	ra, shutdown, err := consensus.NewSingleNodeRaft(logger, dataDir, "127.0.0.1:11000", "node-1")
	if err != nil {
		logger.Error("failed to init raft", "error", err)
		return
	}
	defer shutdown()
	var cc consensus.ConsensusClient = ra

	// Service orchestrator
	fs := service.NewFluidService(logger, lc, g, cc)

	// HTTP health/readiness + API
	stopHTTP := httpserver.Start(ctx, logger, cc, "127.0.0.1:18080", fs.CreateApp)
	defer func() { _ = stopHTTP(context.Background()) }()

	// Register event handlers to update gossip (which backfills cache)
	_ = cc.RegisterEventHandler(types.EventTypeCreateApp, func(event any) {
		if ev, ok := event.(*types.CreateAppEvent); ok {
			_ = g.Upsert(ctx, ev.AppID, []types.ServiceEndpoint{{
				IP:                    ev.IP,
				Port:                  80,
				Status:                types.StatusHealthy,
				LastHeartbeatUnixNano: types.NowUnixNano(),
			}})
		}
	})

	<-ctx.Done()
	logger.Info("agent exiting", "reason", ctx.Err())
}
