package consensus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	raft "github.com/hashicorp/raft"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/raj/fluid/pkg/metrics"
	"github.com/raj/fluid/pkg/tracing"
	"github.com/raj/fluid/pkg/types"
)

// RaftAdapter is a thin wrapper around hashicorp/raft to satisfy ConsensusClient.
// This scaffolding shows event propose and commit dispatch via the FSM.
type RaftAdapter struct {
	logger *slog.Logger
	raft   *raft.Raft
	fsm    *FSM

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
		metrics.RecordRaftOperation(metrics.RaftOpLeaderCheck, metrics.RaftResultNoRaft)
		return ""
	}
	leader := string(r.raft.Leader())
	if leader == "" {
		metrics.RecordRaftOperation(metrics.RaftOpLeaderCheck, metrics.RaftResultNoLeader)
	} else {
		metrics.RecordRaftOperation(metrics.RaftOpLeaderCheck, metrics.RaftResultFound)
	}
	return leader
}

// IsLeader reports whether this node is leader.
func (r *RaftAdapter) IsLeader() bool {
	if r.raft == nil {
		metrics.RecordRaftOperation(metrics.RaftOpIsLeader, metrics.RaftResultNoRaft)
		return false
	}
	return r.raft.State() == raft.Leader
}

// StateSnapshot returns a deep copy of FSM in-memory state for warming caches.
func (r *RaftAdapter) StateSnapshot() map[string][]types.ServiceEndpoint {
	if r.fsm == nil {
		return nil
	}
	return r.fsm.copyState()
}

// Raft returns the underlying *raft.Raft instance. Use for admin operations.
func (r *RaftAdapter) Raft() *raft.Raft { return r.raft }

// WaitForLeader blocks until a leader is known or ctx is done.
func (r *RaftAdapter) WaitForLeader(ctx context.Context) error {
	start := time.Now()
	defer func() {
		metrics.ObserveRaftOperationDuration(metrics.RaftOpWaitForLeader, time.Since(start).Seconds())
	}()
	if r.raft == nil {
		metrics.RecordRaftOperation(metrics.RaftOpWaitForLeader, metrics.RaftResultNoRaft)
		return errors.New("raft instance not set")
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if addr := string(r.raft.Leader()); addr != "" {
			metrics.RecordRaftOperation(metrics.RaftOpWaitForLeader, metrics.RaftResultSuccess)
			return nil
		}
		select {
		case <-ctx.Done():
			metrics.RecordRaftOperation(metrics.RaftOpWaitForLeader, metrics.RaftResultTimeout)
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// AddVoter adds a server to the cluster. Must be called on the leader.
func (r *RaftAdapter) AddVoter(ctx context.Context, id string, addr string) error {
	start := time.Now()
	defer func() {
		metrics.ObserveRaftOperationDuration(metrics.RaftOpAddVoter, time.Since(start).Seconds())
	}()
	if r.raft == nil {
		metrics.RecordRaftOperation(metrics.RaftOpAddVoter, metrics.RaftResultNoRaft)
		return errors.New("raft instance not set")
	}
	if r.raft.State() != raft.Leader {
		metrics.RecordRaftOperation(metrics.RaftOpAddVoter, metrics.RaftResultNotLeader)
		return &NotLeaderError{LeaderAddr: r.LeaderAddress()}
	}
	f := r.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(addr), 0, 5*time.Second)
	select {
	case <-ctx.Done():
		metrics.RecordRaftOperation(metrics.RaftOpAddVoter, metrics.RaftResultTimeout)
		return ctx.Err()
	case err := <-asyncErr(f):
		if err != nil {
			metrics.RecordRaftOperation(metrics.RaftOpAddVoter, "error")
		} else {
			metrics.RecordRaftOperation(metrics.RaftOpAddVoter, metrics.RaftResultSuccess)
		}
		return err
	}
}

func asyncErr(f raft.Future) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- f.Error() }()
	return ch
}

func (r *RaftAdapter) ProposeEvent(ctx context.Context, event any) error {
	start := time.Now()
	defer func() {
		metrics.ObserveRaftOperationDuration(metrics.RaftOpPropose, time.Since(start).Seconds())
	}()

	tracer := otel.Tracer(tracing.TracerRaft)
	_, span := tracer.Start(ctx, tracing.SpanRaftPropose, trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	if r.raft == nil {
		metrics.RecordRaftOperation(metrics.RaftOpPropose, metrics.RaftResultNoRaft)
		span.SetStatus(codes.Error, "raft instance not set")
		return errors.New("raft instance not set")
	}

	// Ensure we know the leader; if not this node, return NotLeaderError with hint.
	if addr := string(r.raft.Leader()); addr == "" {
		metrics.RecordRaftOperation(metrics.RaftOpPropose, metrics.RaftResultNoLeader)
		// Try to wait briefly for leader election before rejecting.
		wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = r.WaitForLeader(wctx)
	}

	if addr := string(r.raft.Leader()); addr != "" && r.raft.State() != raft.Leader {
		metrics.RecordRaftOperation(metrics.RaftOpPropose, metrics.RaftResultNotLeader)
		return &NotLeaderError{LeaderAddr: addr}
	}

	eventType := eventTypeConstant(event)
	if eventType == "" {
		metrics.RecordRaftOperation(metrics.RaftOpPropose, metrics.RaftResultUnknownEvent)
		span.SetStatus(codes.Error, "unknown event type")
		return fmt.Errorf("unknown event type: %T", event)
	}

	span.SetAttributes(
		attribute.String("raft.event_type", eventType),
		attribute.String("raft.leader", string(r.raft.Leader())),
	)

	payload, err := json.Marshal(event)
	if err != nil {
		metrics.RecordRaftOperation(metrics.RaftOpPropose, metrics.RaftResultMarshalPayloadError)
		span.SetStatus(codes.Error, "marshal payload failed")
		return err
	}

	entryBytes, err := json.Marshal(&raftLogEntry{EventType: eventType, Payload: payload})
	if err != nil {
		metrics.RecordRaftOperation(metrics.RaftOpPropose, metrics.RaftResultMarshalEntryError)
		return err
	}

	metrics.SetRaftLogSize(float64(len(entryBytes)))
	metrics.RecordRaftOperation(metrics.RaftOpPropose, metrics.RaftResultProposing)

	f := r.raft.Apply(entryBytes, time.Second*5)
	done := make(chan error, 1)
	go func() {
		err := f.Error()
		if err != nil {
			metrics.RecordRaftOperation(metrics.RaftOpPropose, metrics.RaftResultApplyError)
			span.SetStatus(codes.Error, "raft apply failed")
		} else {
			metrics.RecordRaftOperation(metrics.RaftOpPropose, metrics.RaftResultSuccess)
			span.SetStatus(codes.Ok, "")
		}
		done <- err
	}()

	select {
	case <-ctx.Done():
		metrics.RecordRaftOperation(metrics.RaftOpPropose, metrics.RaftResultTimeout)
		span.SetStatus(codes.Error, "timeout")
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
