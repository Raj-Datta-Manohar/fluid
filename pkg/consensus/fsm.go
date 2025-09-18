package consensus

import (
	"context"
	"encoding/json"
	"io"
	"sync"
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

// FSM implements raft.FSM and decodes log entries to typed events then dispatches.
type FSM struct {
	adapter *RaftAdapter

	mu    sync.RWMutex
	state map[string][]types.ServiceEndpoint
}

func (f *FSM) Apply(log *raft.Log) any {
	start := time.Now()
	defer func() {
		metrics.ObserveRaftOperationDuration(metrics.RaftOpApply, time.Since(start).Seconds())
	}()

	tracer := otel.Tracer(tracing.TracerRaft)
	_, span := tracer.Start(context.Background(), tracing.SpanRaftApply, trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	var entry raftLogEntry
	if err := json.Unmarshal(log.Data, &entry); err != nil {
		metrics.RecordRaftOperation(metrics.RaftOpApply, metrics.RaftResultUnmarshalError)
		span.SetStatus(codes.Error, "unmarshal log entry failed")
		return err
	}

	span.SetAttributes(
		attribute.String("raft.event_type", entry.EventType),
		attribute.Int("raft.log_index", int(log.Index)),
	)

	metrics.RecordRaftOperation(metrics.RaftOpApply, "start")
	// Record log size
	metrics.SetRaftLogSize(float64(len(log.Data)))

	// Apply to in-memory state
	switch entry.EventType {
	case types.EventTypeCreateApp:
		var ev types.CreateAppEvent
		if err := json.Unmarshal(entry.Payload, &ev); err == nil {
			f.mu.Lock()
			f.ensure()
			f.state[ev.AppID] = []types.ServiceEndpoint{{
				IP:                    ev.IP,
				Port:                  80,
				Status:                types.StatusHealthy,
				LastHeartbeatUnixNano: types.NowUnixNano(),
			}}
			f.mu.Unlock()
			metrics.RecordRaftOperation(metrics.RaftOpCreateApp, metrics.RaftResultSuccess)
			span.SetAttributes(
				attribute.String("app.id", ev.AppID),
				attribute.String("app.ip", ev.IP),
			)
		} else {
			metrics.RecordRaftOperation(metrics.RaftOpCreateApp, metrics.RaftResultUnmarshalError)
			span.SetStatus(codes.Error, "unmarshal create app event failed")
		}

	case types.EventTypeDeleteApp:
		var ev types.DeleteAppEvent
		if err := json.Unmarshal(entry.Payload, &ev); err == nil {
			f.mu.Lock()
			f.ensure()
			_, existed := f.state[ev.AppID]
			delete(f.state, ev.AppID)
			f.mu.Unlock()
			if existed {
				metrics.RecordRaftOperation(metrics.RaftOpDeleteApp, metrics.RaftResultSuccess)
			} else {
				metrics.RecordRaftOperation(metrics.RaftOpDeleteApp, metrics.RaftResultNotFound)
			}
		} else {
			metrics.RecordRaftOperation(metrics.RaftOpDeleteApp, metrics.RaftResultUnmarshalError)
		}

	case types.EventTypeUpdateGlobalConfig:
		// no-op in memory for now
		metrics.RecordRaftOperation(metrics.RaftOpUpdateConfig, metrics.RaftResultNoop)
	}
	// Notify adapters for further handling (gossip/cache)
	f.adapter.dispatch(entry.EventType, entry.Payload)
	return nil
}

func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveRaftOperationDuration(metrics.RaftOpSnapshot, time.Since(start).Seconds())
	}()

	f.mu.RLock()
	defer f.mu.RUnlock()
	f.ensure()
	// Deep copy
	cp := make(map[string][]types.ServiceEndpoint, len(f.state))
	for k, v := range f.state {
		vv := make([]types.ServiceEndpoint, len(v))
		copy(vv, v)
		cp[k] = vv
	}
	data, err := json.Marshal(cp)
	if err != nil {
		metrics.RecordRaftSnapshot("create", metrics.RaftSnapshotError)
		return nil, err
	}
	metrics.RecordRaftSnapshot("create", metrics.RaftSnapshotSuccess)
	metrics.SetRaftLogSize(float64(len(data)))
	return &snapshot{data: data}, nil
}

func (f *FSM) Restore(rc io.ReadCloser) error {
	start := time.Now()
	defer func() {
		metrics.ObserveRaftOperationDuration(metrics.RaftOpRestore, time.Since(start).Seconds())
	}()
	defer rc.Close()
	var cp map[string][]types.ServiceEndpoint
	dec := json.NewDecoder(rc)
	if err := dec.Decode(&cp); err != nil {
		metrics.RecordRaftSnapshot("restore", metrics.RaftSnapshotError)
		return err
	}
	f.mu.Lock()
	f.state = cp
	f.mu.Unlock()
	metrics.RecordRaftSnapshot("restore", metrics.RaftSnapshotSuccess)
	return nil
}

func (f *FSM) ensure() {
	if f.state == nil {
		f.state = make(map[string][]types.ServiceEndpoint)
	}
}

func (f *FSM) copyState() map[string][]types.ServiceEndpoint {
	f.mu.RLock()
	defer f.mu.RUnlock()
	f.ensure()
	cp := make(map[string][]types.ServiceEndpoint, len(f.state))
	for k, v := range f.state {
		vv := make([]types.ServiceEndpoint, len(v))
		copy(vv, v)
		cp[k] = vv
	}
	return cp
}

type snapshot struct{ data []byte }

func (s *snapshot) Persist(sink raft.SnapshotSink) error {
	if _, err := sink.Write(s.data); err != nil {
		metrics.RecordRaftSnapshot("persist", metrics.RaftSnapshotError)
		sink.Cancel()
		return err
	}
	if err := sink.Close(); err != nil {
		metrics.RecordRaftSnapshot("persist", metrics.RaftSnapshotError)
		return err
	}
	metrics.RecordRaftSnapshot("persist", metrics.RaftSnapshotSuccess)
	return nil
}
func (s *snapshot) Release() {}
