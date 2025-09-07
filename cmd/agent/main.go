package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/raj/fluid/pkg/cache"
	"github.com/raj/fluid/pkg/consensus"
	"github.com/raj/fluid/pkg/gossip"
	gml "github.com/raj/fluid/pkg/gossip/memberlist"
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

	// Tier 2 (configurable)
	var g gossip.GossipMemberlist
	if os.Getenv("GOSSIP_IMPL") == "memberlist" {
		cfg := gml.Config{
			NodeName:       os.Getenv("GOSSIP_NODE"),
			BindAddr:       os.Getenv("GOSSIP_BIND"),
			Seeds:          splitNonEmpty(os.Getenv("GOSSIP_SEEDS")),
			RetransmitMult: 2,
			KeyHex:         os.Getenv("GOSSIP_KEY_HEX"),
			Compression:    os.Getenv("GOSSIP_COMPRESSION") == "1" || strings.ToLower(os.Getenv("GOSSIP_COMPRESSION")) == "true",
			BatchSize:      1024,
			MaxMessageSize: 512,
		}
		mg, shutdown, err := gml.New(logger, cfg, lc)
		if err != nil {
			logger.Error("memberlist init failed; falling back to in-memory gossip", "error", err)
			g = gossip.NewInMemoryGossip(logger, lc)
		} else {
			g = mg
			defer func() { _ = shutdown() }()
		}
	} else if os.Getenv("GOSSIP_IMPL") == "crdt" {
		cfg := gml.Config{
			NodeName:       os.Getenv("GOSSIP_NODE"),
			BindAddr:       os.Getenv("GOSSIP_BIND"),
			Seeds:          splitNonEmpty(os.Getenv("GOSSIP_SEEDS")),
			RetransmitMult: 2,
			KeyHex:         os.Getenv("GOSSIP_KEY_HEX"),
			Compression:    os.Getenv("GOSSIP_COMPRESSION") == "1" || strings.ToLower(os.Getenv("GOSSIP_COMPRESSION")) == "true",
			BatchSize:      1024,
			MaxMessageSize: 512,
		}
		mg, shutdown, err := gml.NewCRDT(logger, cfg, lc)
		if err != nil {
			logger.Error("crdt gossip init failed; falling back to in-memory gossip", "error", err)
			g = gossip.NewInMemoryGossip(logger, lc)
		} else {
			g = mg
			defer func() { _ = shutdown() }()
		}
	} else {
		g = gossip.NewInMemoryGossip(logger, lc)
	}

	// Tier 3: Raft
	raftBind := os.Getenv("RAFT_BIND")
	if raftBind == "" {
		raftBind = "127.0.0.1:11000"
	}
	raftID := os.Getenv("RAFT_ID")
	if raftID == "" {
		raftID = "node-1"
	}
	var ro consensus.RaftOptions
	if v := os.Getenv("RAFT_SNAPSHOT_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			ro.SnapshotInterval = d
		}
	}
	if v := os.Getenv("RAFT_SNAPSHOT_THRESHOLD"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			ro.SnapshotThreshold = n
		}
	}
	if v := os.Getenv("RAFT_BOOTSTRAP"); v == "1" || strings.ToLower(v) == "true" {
		ro.Bootstrap = true
	}
	if v := os.Getenv("RAFT_ADVERTISE"); v != "" {
		ro.AdvertiseAddr = v
	}
	dataDir := filepath.Join(os.TempDir(), "fluid-raft")
	_ = os.MkdirAll(dataDir, 0o755)
	ra, shutdown, err := consensus.NewSingleNodeRaft(logger, dataDir, raftBind, raftID, ro)
	if err != nil {
		logger.Error("failed to init raft", "error", err)
		return
	}
	defer shutdown()
	var cc consensus.ConsensusClient = ra

	// Auto-join if RAFT_JOIN_URL provided and not bootstrapping
	if ro.Bootstrap == false {
		if joinURL := os.Getenv("RAFT_JOIN_URL"); joinURL != "" {
			waitForLeader(joinURL, logger, 10*time.Second)
			_ = joinWithRetry(joinURL, raftID, raftBind, logger, 5)
		}
	}

	// Pre-warm Tier 1 from Raft FSM state snapshot
	if adapter, ok := cc.(*consensus.RaftAdapter); ok {
		state := adapter.StateSnapshot()
		for name, eps := range state {
			lc.Put(name, eps)
		}
	}

	// Service orchestrator
	fs := service.NewFluidService(logger, lc, g, cc)

	// HTTP health/readiness + API
	stopHTTP := httpserver.Start(ctx, logger, cc, "127.0.0.1:18080", fs.CreateApp, fs.LookupService)
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

func splitNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func waitForLeader(baseURL string, logger *slog.Logger, timeout time.Duration) {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/../readyz")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func joinWithRetry(joinURL, id, addr string, logger *slog.Logger, attempts int) error {
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	payload, _ := json.Marshal(map[string]string{"id": id, "addr": addr})
	backoff := 200 * time.Millisecond
	for i := 0; i < attempts; i++ {
		resp, err := client.Post(joinURL, "application/json", bytes.NewReader(payload))
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				return nil
			}
			if resp.StatusCode == http.StatusTemporaryRedirect {
				loc := resp.Header.Get("Location")
				resp.Body.Close()
				if loc != "" {
					joinURL = strings.TrimRight(loc, "/") + "/raft/join"
				}
				// retry immediately with new leader URL
				continue
			}
			resp.Body.Close()
		}
		time.Sleep(backoff)
		if backoff < 2*time.Second {
			backoff *= 2
		}
	}
	logger.Warn("raft join failed after retries", "url", joinURL, "id", id, "addr", addr)
	return nil
}
