package consensus

import (
	"log/slog"
	"os"
	"time"

	raft "github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"
	"github.com/raj/fluid/pkg/metrics"
)

// NewSingleNodeRaft initializes a single-node Raft for local testing.
// dataDir must exist. bindAddr like "127.0.0.1:12000".
func NewSingleNodeRaft(logger *slog.Logger, dataDir string, bindAddr string, serverID string, opts ...RaftOptions) (*RaftAdapter, func(), error) {
	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(serverID)
	var o RaftOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.SnapshotInterval > 0 {
		cfg.SnapshotInterval = o.SnapshotInterval
	}
	if o.SnapshotThreshold > 0 {
		cfg.SnapshotThreshold = o.SnapshotThreshold
	}

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
	// Note: using bindAddr as the advertised address. If external advertise is needed,
	// pass it to join requests and server configs.

	adapter := NewRaftAdapter(logger, nil)
	fsm := &FSM{adapter: adapter}
	r, err := raft.NewRaft(cfg, fsm, logStore, stableStore, snapshots, transport)
	if err != nil {
		metrics.RecordRaftOperation("bootstrap", "raft_error")
		return nil, nil, err
	}
	adapter.raft = r
	adapter.fsm = fsm

	// Monitor leadership changes
	observer := raft.NewObserver(make(chan raft.Observation, 1), false,
		func(o *raft.Observation) bool {
			switch o.Data.(type) {
			case raft.LeaderObservation:
				metrics.IncrementRaftLeaderChanges()
				metrics.RecordRaftOperation("leadership", "change")
				return true
			}
			return false
		})
	r.RegisterObserver(observer)
	metrics.RecordRaftOperation("bootstrap", "success")

	// Bootstrap if requested and fresh
	future := r.GetConfiguration()
	if err := future.Error(); err != nil {
		return nil, nil, err
	}
	if o.Bootstrap && len(future.Configuration().Servers) == 0 {
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
