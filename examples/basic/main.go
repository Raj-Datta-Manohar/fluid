package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/raj/fluid/pkg/cache"
	"github.com/raj/fluid/pkg/consensus"
	"github.com/raj/fluid/pkg/gossip"
	"github.com/raj/fluid/pkg/service"
	"github.com/raj/fluid/pkg/types"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	// Tier 1
	lc := cache.NewInMemoryCache(logger, 10*time.Second, 1*time.Second)
	lc.Start(ctx)

	// Tier 2
	g := gossip.NewInMemoryGossip(logger, lc)

	// Tier 3 single-node
	dataDir := filepath.Join(os.TempDir(), "fluid-raft-example")
	_ = os.MkdirAll(dataDir, 0o755)
	ra, shutdown, err := consensus.NewSingleNodeRaft(logger, dataDir, "127.0.0.1:21000", "node-ex")
	if err != nil {
		logger.Error("failed to init raft", "error", err)
		return
	}
	defer shutdown()

	// Service
	fs := service.NewFluidService(logger, lc, g, ra)

	// Subscribe: on CreateApp update Tier 2 which backfills Tier 1
	_ = ra.RegisterEventHandler(types.EventTypeCreateApp, func(event any) {
		if ev, ok := event.(*types.CreateAppEvent); ok {
			_ = g.Upsert(ctx, ev.AppID, []types.ServiceEndpoint{{
				IP:                    ev.IP,
				Port:                  80,
				Status:                types.StatusHealthy,
				LastHeartbeatUnixNano: types.NowUnixNano(),
			}})
		}
	})

	// Propose and then lookup
	if err := fs.CreateApp(ctx, "checkout", "10.1.2.3"); err != nil {
		logger.Error("CreateApp failed", "error", err)
		return
	}
	time.Sleep(200 * time.Millisecond)
	endpoints, _ := fs.LookupService(ctx, "checkout")
	fmt.Printf("Lookup checkout -> %v\n", endpoints)
}
