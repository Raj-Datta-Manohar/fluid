package crdt

import (
	"time"

	"github.com/raj/fluid/pkg/types"
)

// VectorClock tracks logical time per node for conflict resolution.
type VectorClock map[string]uint64

// LWWRegister is a Last-Write-Wins register with vector clock for ordering.
type LWWRegister struct {
	Value     []types.ServiceEndpoint `json:"value"`
	Timestamp time.Time               `json:"timestamp"`
	Clock     VectorClock             `json:"clock"`
	NodeID    string                  `json:"nodeId"`
}

// Compare returns -1 if this < other, 0 if equal, 1 if this > other.
func (l *LWWRegister) Compare(other *LWWRegister) int {
	if l.Timestamp.Before(other.Timestamp) {
		return -1
	}
	if l.Timestamp.After(other.Timestamp) {
		return 1
	}
	// Same timestamp: use node ID for deterministic tie-breaking
	if l.NodeID < other.NodeID {
		return -1
	}
	if l.NodeID > other.NodeID {
		return 1
	}
	return 0
}

// Merge combines two LWW registers, keeping the winner.
func (l *LWWRegister) Merge(other *LWWRegister) *LWWRegister {
	if l.Compare(other) >= 0 {
		return l
	}
	return other
}

// IncrementClock bumps the logical clock for this node.
func (l *LWWRegister) IncrementClock(nodeID string) {
	if l.Clock == nil {
		l.Clock = make(VectorClock)
	}
	l.Clock[nodeID]++
}

// ServiceState holds CRDT state for a service name.
type ServiceState struct {
	Name     string       `json:"name"`
	Register *LWWRegister `json:"register"`
}

// StateSnapshot is a full state snapshot for gossip sync.
type StateSnapshot struct {
	Services map[string]*LWWRegister `json:"services"`
	NodeID   string                  `json:"nodeId"`
	Clock    VectorClock             `json:"clock"`
}
