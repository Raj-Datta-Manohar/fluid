package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Raft metrics label constants
const (
	// Operations
	RaftOpLeaderCheck   = "leader_check"
	RaftOpIsLeader      = "is_leader"
	RaftOpPropose       = "propose"
	RaftOpApply         = "apply"
	RaftOpSnapshot      = "snapshot"
	RaftOpRestore       = "restore"
	RaftOpWaitForLeader = "wait_for_leader"
	RaftOpAddVoter      = "add_voter"
	RaftOpCreateApp     = "create_app"
	RaftOpDeleteApp     = "delete_app"
	RaftOpUpdateConfig  = "update_config"

	// Results
	RaftResultNoRaft              = "no_raft"
	RaftResultNoLeader            = "no_leader"
	RaftResultFound               = "found"
	RaftResultUnknownEvent        = "unknown_event"
	RaftResultMarshalPayloadError = "marshal_payload_error"
	RaftResultMarshalEntryError   = "marshal_entry_error"
	RaftResultProposing           = "proposing"
	RaftResultApplyError          = "apply_error"
	RaftResultSuccess             = "success"
	RaftResultTimeout             = "timeout"
	RaftResultNotLeader           = "not_leader"
	RaftResultUnmarshalError      = "unmarshal_error"
	RaftResultNoop                = "noop"
	RaftResultNotFound            = "not_found"
	RaftSnapshotSuccess           = "success"
	RaftSnapshotError             = "error"
)

var (
	raftOpsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluid_raft_operations_total",
			Help: "Total number of Raft operations by type and result",
		},
		[]string{"operation", "result"},
	)

	raftOpDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "fluid_raft_operation_duration_seconds",
			Help:    "Duration of Raft operations",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation"},
	)

	raftLeaderChanges = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "fluid_raft_leader_changes_total",
			Help: "Total number of Raft leader changes",
		},
	)

	raftLogSizeBytes = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "fluid_raft_log_size_bytes",
			Help: "Current size of the Raft log in bytes",
		},
	)

	raftSnapshotsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "fluid_raft_snapshots_total",
			Help: "Total number of Raft snapshot events by phase and result",
		},
		[]string{"phase", "result"},
	)
)

func init() {
	MustRegister(
		raftOpsTotal,
		raftOpDurationSeconds,
		raftLeaderChanges,
		raftLogSizeBytes,
		raftSnapshotsTotal,
	)
}

// RecordRaftOperation records a Raft operation with its result
func RecordRaftOperation(op string, result string) {
	raftOpsTotal.WithLabelValues(op, result).Inc()
}

// ObserveRaftOperationDuration records the duration of a Raft operation
func ObserveRaftOperationDuration(op string, duration float64) {
	raftOpDurationSeconds.WithLabelValues(op).Observe(duration)
}

// IncrementRaftLeaderChanges increments the leader change counter
func IncrementRaftLeaderChanges() {
	raftLeaderChanges.Inc()
}

// SetRaftLogSize sets the current Raft log size in bytes
func SetRaftLogSize(bytes float64) {
	raftLogSizeBytes.Set(bytes)
}

// RecordRaftSnapshot records a Raft snapshot operation
func RecordRaftSnapshot(phase string, result string) {
	raftSnapshotsTotal.WithLabelValues(phase, result).Inc()
}
