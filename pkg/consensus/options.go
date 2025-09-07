package consensus

import "time"

// RaftOptions carries Raft configuration knobs used by bootstrap.
type RaftOptions struct {
	SnapshotInterval  time.Duration
	SnapshotThreshold uint64
	Bootstrap         bool
	AdvertiseAddr     string
}
