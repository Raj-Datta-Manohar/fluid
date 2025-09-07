package consensus

import (
	"encoding/json"
	"io"
	"sync"

	raft "github.com/hashicorp/raft"

	"github.com/raj/fluid/pkg/types"
)

// FSM implements raft.FSM and decodes log entries to typed events then dispatches.
type FSM struct {
	adapter *RaftAdapter

	mu    sync.RWMutex
	state map[string][]types.ServiceEndpoint
}

func (f *FSM) Apply(log *raft.Log) any {
	var entry raftLogEntry
	if err := json.Unmarshal(log.Data, &entry); err != nil {
		return err
	}
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
		}
	case types.EventTypeDeleteApp:
		var ev types.DeleteAppEvent
		if err := json.Unmarshal(entry.Payload, &ev); err == nil {
			f.mu.Lock()
			f.ensure()
			delete(f.state, ev.AppID)
			f.mu.Unlock()
		}
	case types.EventTypeUpdateGlobalConfig:
		// no-op in memory for now
	}
	// Notify adapters for further handling (gossip/cache)
	f.adapter.dispatch(entry.EventType, entry.Payload)
	return nil
}

func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
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
	data, _ := json.Marshal(cp)
	return &snapshot{data: data}, nil
}

func (f *FSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	var cp map[string][]types.ServiceEndpoint
	dec := json.NewDecoder(rc)
	if err := dec.Decode(&cp); err != nil {
		return err
	}
	f.mu.Lock()
	f.state = cp
	f.mu.Unlock()
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
		sink.Cancel()
		return err
	}
	return sink.Close()
}
func (s *snapshot) Release() {}
