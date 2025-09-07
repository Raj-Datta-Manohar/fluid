package consensus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	raft "github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"

	"github.com/raj/fluid/pkg/types"
)

// RaftAdapter is a thin wrapper around hashicorp/raft to satisfy ConsensusClient.
// This scaffolding shows event propose and commit dispatch via the FSM.
type RaftAdapter struct {
	logger *slog.Logger
	raft   *raft.Raft

	handlers map[string][]func(any)
}

// raftLogEntry is a generic wrapper we serialize to the log.
type raftLogEntry struct {
	EventType string          `json:"eventType"`
	Payload   json.RawMessage `json:"payload"`
}

func NewRaftAdapter(logger *slog.Logger, r *raft.Raft) *RaftAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &RaftAdapter{logger: logger.With("component", "raft_adapter"), raft: r, handlers: make(map[string][]func(any))}
}

// LeaderAddress returns the current leader address if known.
func (r *RaftAdapter) LeaderAddress() string {
	if r.raft == nil {
		return ""
	}
	return string(r.raft.Leader())
}

// IsLeader reports whether this node is leader.
func (r *RaftAdapter) IsLeader() bool {
	if r.raft == nil {
		return false
	}
	return r.raft.State() == raft.Leader
}

// WaitForLeader blocks until a leader is known or ctx is done.
func (r *RaftAdapter) WaitForLeader(ctx context.Context) error {
	if r.raft == nil {
		return errors.New("raft instance not set")
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if addr := string(r.raft.Leader()); addr != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *RaftAdapter) ProposeEvent(ctx context.Context, event any) error {
	if r.raft == nil {
		return errors.New("raft instance not set")
	}
	// Ensure we know the leader; if not this node, return NotLeaderError with hint.
	if addr := string(r.raft.Leader()); addr == "" {
		// Try to wait briefly for leader election before rejecting.
		wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = r.WaitForLeader(wctx)
	}
	if addr := string(r.raft.Leader()); addr != "" && r.raft.State() != raft.Leader {
		return &NotLeaderError{LeaderAddr: addr}
	}

	eventType := eventTypeConstant(event)
	if eventType == "" {
		return fmt.Errorf("unknown event type: %T", event)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	entryBytes, err := json.Marshal(&raftLogEntry{EventType: eventType, Payload: payload})
	if err != nil {
		return err
	}
	f := r.raft.Apply(entryBytes, time.Second*5)
	done := make(chan error, 1)
	go func() { done <- f.Error() }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (r *RaftAdapter) RegisterEventHandler(eventType string, handler func(event any)) error {
	if eventType == "" || handler == nil {
		return errors.New("eventType and handler are required")
	}
	r.handlers[eventType] = append(r.handlers[eventType], handler)
	return nil
}

// FSM implements raft.FSM and decodes log entries to typed events then dispatches.
type FSM struct {
	adapter *RaftAdapter
}

func (f *FSM) Apply(log *raft.Log) any {
	var entry raftLogEntry
	if err := json.Unmarshal(log.Data, &entry); err != nil {
		return err
	}
	f.adapter.dispatch(entry.EventType, entry.Payload)
	return nil
}

func (f *FSM) Snapshot() (raft.FSMSnapshot, error) { return &noopSnapshot{}, nil }
func (f *FSM) Restore(rc io.ReadCloser) error      { return nil }

type noopSnapshot struct{}

func (n *noopSnapshot) Persist(sink raft.SnapshotSink) error { return sink.Close() }
func (n *noopSnapshot) Release()                             {}

// dispatch decodes payload by eventType and invokes handlers.
func (r *RaftAdapter) dispatch(eventType string, payload json.RawMessage) {
	switch eventType {
	case types.EventTypeCreateApp:
		var ev types.CreateAppEvent
		if err := json.Unmarshal(payload, &ev); err == nil {
			for _, h := range r.handlers[eventType] {
				func(fn func(any), e any) { defer func() { recover() }(); fn(e) }(h, &ev)
			}
		}
	case types.EventTypeDeleteApp:
		var ev types.DeleteAppEvent
		if err := json.Unmarshal(payload, &ev); err == nil {
			for _, h := range r.handlers[eventType] {
				func(fn func(any), e any) { defer func() { recover() }(); fn(e) }(h, &ev)
			}
		}
	case types.EventTypeUpdateGlobalConfig:
		var ev types.UpdateGlobalConfigEvent
		if err := json.Unmarshal(payload, &ev); err == nil {
			for _, h := range r.handlers[eventType] {
				func(fn func(any), e any) { defer func() { recover() }(); fn(e) }(h, &ev)
			}
		}
	default:
		// Unknown event; ignore
	}
}

func eventTypeConstant(event any) string {
	switch event.(type) {
	case *types.CreateAppEvent:
		return types.EventTypeCreateApp
	case *types.DeleteAppEvent:
		return types.EventTypeDeleteApp
	case *types.UpdateGlobalConfigEvent:
		return types.EventTypeUpdateGlobalConfig
	default:
		return ""
	}
}

// NewSingleNodeRaft initializes a single-node Raft for local testing.
// dataDir must exist. bindAddr like "127.0.0.1:12000".
func NewSingleNodeRaft(logger *slog.Logger, dataDir string, bindAddr string, serverID string) (*RaftAdapter, func(), error) {
	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(serverID)

	logStore, err := raftboltdb.NewBoltStore(dataDir + "/raft-log.bolt")
	if err != nil {
		return nil, nil, err
	}
	stableStore, err := raftboltdb.NewBoltStore(dataDir + "/raft-stable.bolt")
	if err != nil {
		return nil, nil, err
	}
	snapshots, err := raft.NewFileSnapshotStore(dataDir, 3, os.Stderr)
	if err != nil {
		return nil, nil, err
	}
	transport, err := raft.NewTCPTransport(bindAddr, nil, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, nil, err
	}

	adapter := NewRaftAdapter(logger, nil)
	fsm := &FSM{adapter: adapter}
	r, err := raft.NewRaft(cfg, fsm, logStore, stableStore, snapshots, transport)
	if err != nil {
		return nil, nil, err
	}
	adapter.raft = r

	// Bootstrap if fresh
	future := r.GetConfiguration()
	if err := future.Error(); err != nil {
		return nil, nil, err
	}
	if len(future.Configuration().Servers) == 0 {
		cfg := raft.Configuration{Servers: []raft.Server{{ID: cfg.LocalID, Address: transport.LocalAddr()}}}
		if err := r.BootstrapCluster(cfg).Error(); err != nil {
			return nil, nil, err
		}
	}

	shutdown := func() {
		r.Shutdown()
		transport.Close()
	}
	return adapter, shutdown, nil
}
