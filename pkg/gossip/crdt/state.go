package crdt

import (
	"sync"
	"time"

	"github.com/raj/fluid/pkg/metrics"
	"github.com/raj/fluid/pkg/types"
)

// State manages CRDT state for gossip.
type State struct {
	nodeID string
	mu     sync.RWMutex
	state  map[string]*LWWRegister
	clock  VectorClock
}

// NewState creates a new CRDT state manager.
func NewState(nodeID string) *State {
	return &State{
		nodeID: nodeID,
		state:  make(map[string]*LWWRegister),
		clock:  make(VectorClock),
	}
}

// Upsert updates or creates a service with CRDT semantics.
func (s *State) Upsert(serviceName string, endpoints []types.ServiceEndpoint) {
	start := time.Now()
	defer func() {
		metrics.ObserveGossipOperationDuration("crdt_state_upsert", time.Since(start).Seconds())
	}()

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.clock[s.nodeID]++

	reg := &LWWRegister{
		Value:     endpoints,
		Timestamp: now,
		Clock:     make(VectorClock),
		NodeID:    s.nodeID,
	}
	// Copy clock state
	for k, v := range s.clock {
		reg.Clock[k] = v
	}

	_, exists := s.state[serviceName]
	s.state[serviceName] = reg

	if exists {
		metrics.RecordGossipMessage("crdt_state", "update")
	} else {
		metrics.RecordGossipMessage("crdt_state", "create")
	}

	// Track CRDT state size
	stateSize := s.calculateStateSize()
	metrics.ObserveGossipMessageSize("crdt_state", float64(stateSize))
}

// calculateStateSize returns the approximate size of the CRDT state in bytes
func (s *State) calculateStateSize() int {
	size := 0
	for name, reg := range s.state {
		size += len(name)
		size += len(reg.NodeID)
		for _, ep := range reg.Value {
			size += len(ep.IP) + 8 // 8 bytes for port
		}
		for k := range reg.Clock {
			size += len(k) + 8 // 8 bytes for counter
		}
	}
	return size
}

// Remove deletes a service.
func (s *State) Remove(serviceName string) bool {
	start := time.Now()
	defer func() {
		metrics.ObserveGossipOperationDuration("crdt_state_remove", time.Since(start).Seconds())
	}()

	s.mu.Lock()
	defer s.mu.Unlock()

	_, exists := s.state[serviceName]
	delete(s.state, serviceName)
	s.clock[s.nodeID]++

	if exists {
		metrics.RecordGossipMessage("crdt_state", "remove")
		// Track CRDT state size after removal
		stateSize := s.calculateStateSize()
		metrics.ObserveGossipMessageSize("crdt_state", float64(stateSize))
	} else {
		metrics.RecordGossipMessage("crdt_state", "remove_not_found")
	}

	return exists
}

// Get retrieves a service if present and not deleted.
func (s *State) Get(serviceName string) []types.ServiceEndpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()

	reg, ok := s.state[serviceName]
	if !ok || reg.Value == nil {
		return nil
	}

	// Defensive copy
	cp := make([]types.ServiceEndpoint, len(reg.Value))
	copy(cp, reg.Value)
	return cp
}

// MergeRemoteState merges a remote state snapshot.
func (s *State) MergeRemoteState(snapshot *StateSnapshot) {
	start := time.Now()
	defer func() {
		metrics.ObserveGossipOperationDuration("crdt_state_merge", time.Since(start).Seconds())
	}()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Merge clocks
	for nodeID, remoteClock := range snapshot.Clock {
		if s.clock[nodeID] < remoteClock {
			s.clock[nodeID] = remoteClock
		}
	}

	// Merge service states
	for serviceName, remoteReg := range snapshot.Services {
		localReg, exists := s.state[serviceName]
		if !exists {
			// New service from remote
			s.state[serviceName] = remoteReg
		} else {
			// Merge with local state
			merged := localReg.Merge(remoteReg)
			s.state[serviceName] = merged
		}
	}
}

// Snapshot creates a state snapshot for gossip sync.
func (s *State) Snapshot() *StateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	services := make(map[string]*LWWRegister, len(s.state))
	for k, v := range s.state {
		services[k] = v
	}

	clock := make(VectorClock, len(s.clock))
	for k, v := range s.clock {
		clock[k] = v
	}

	return &StateSnapshot{
		Services: services,
		NodeID:   s.nodeID,
		Clock:    clock,
	}
}

// AllServices returns all non-deleted services.
func (s *State) AllServices() map[string][]types.ServiceEndpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string][]types.ServiceEndpoint)
	for name, reg := range s.state {
		if reg.Value != nil {
			cp := make([]types.ServiceEndpoint, len(reg.Value))
			copy(cp, reg.Value)
			result[name] = cp
		}
	}
	return result
}
