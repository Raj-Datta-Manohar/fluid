package memberlist

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	hmemberlist "github.com/hashicorp/memberlist"

	"github.com/raj/fluid/pkg/cache"
	"github.com/raj/fluid/pkg/gossip"
	"github.com/raj/fluid/pkg/metrics"
	"github.com/raj/fluid/pkg/types"
)

type messageType string

const (
	msgUpsert messageType = "upsert"
	msgRemove messageType = "remove"
)

type wireMsg struct {
	Type    messageType             `json:"type"`
	Name    string                  `json:"name"`
	Payload []types.ServiceEndpoint `json:"payload,omitempty"`
}

type memberlistGossip struct {
	logger *slog.Logger
	cache  cache.LocalCache

	mu      sync.RWMutex
	entries map[string][]types.ServiceEndpoint

	ml    *hmemberlist.Memberlist
	queue *hmemberlist.TransmitLimitedQueue
}

// New creates a memberlist-based implementation of GossipMemberlist.
func New(logger *slog.Logger, cfg Config, lc cache.LocalCache) (gossip.GossipMemberlist, func() error, error) {
	if logger == nil {
		logger = slog.Default()
	}
	impl := &memberlistGossip{
		logger:  logger.With("component", "gossip_memberlist"),
		cache:   lc,
		entries: make(map[string][]types.ServiceEndpoint),
	}

	mlCfg := hmemberlist.DefaultLANConfig()
	if cfg.NodeName != "" {
		mlCfg.Name = cfg.NodeName
	}
	if cfg.BindAddr != "" {
		mlCfg.BindAddr, mlCfg.BindPort = ParseAddr(cfg.BindAddr)
	}
	if cfg.RetransmitMult > 0 {
		mlCfg.RetransmitMult = cfg.RetransmitMult
	}
	if cfg.GossipInterval > 0 {
		mlCfg.GossipInterval = cfg.GossipInterval
	}
	if cfg.ProbeInterval > 0 {
		mlCfg.ProbeInterval = cfg.ProbeInterval
	}
	if cfg.KeyHex != "" {
		b, err := hex.DecodeString(cfg.KeyHex)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid KeyHex: %w", err)
		}
		kr, err := hmemberlist.NewKeyring([][]byte{b}, b)
		if err != nil {
			return nil, nil, err
		}
		mlCfg.Keyring = kr
	}

	delegate := &delegateImpl{g: impl}
	mlCfg.Delegate = delegate
	mlCfg.Events = delegate

	ml, err := hmemberlist.Create(mlCfg)
	if err != nil {
		return nil, nil, err
	}
	impl.ml = ml
	impl.queue = &hmemberlist.TransmitLimitedQueue{
		NumNodes:       func() int { return ml.NumMembers() },
		RetransmitMult: mlCfg.RetransmitMult,
	}
	if len(cfg.Seeds) > 0 {
		_, _ = ml.Join(cfg.Seeds)
	}
	shutdown := func() error { return ml.Shutdown() }
	return impl, shutdown, nil
}

func (m *memberlistGossip) Lookup(ctx context.Context, serviceName string) ([]types.ServiceEndpoint, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveGossipOperationDuration("lookup", time.Since(start).Seconds())
	}()

	m.mu.RLock()
	eps := m.entries[serviceName]
	m.mu.RUnlock()

	if len(eps) == 0 {
		metrics.RecordGossipMessage("lookup", "miss")
		return nil, nil
	}

	cp := make([]types.ServiceEndpoint, len(eps))
	copy(cp, eps)
	metrics.RecordGossipMessage("lookup", "hit")
	return cp, nil
}

func (m *memberlistGossip) Upsert(ctx context.Context, serviceName string, endpoints []types.ServiceEndpoint) error {
	start := time.Now()
	defer func() {
		metrics.ObserveGossipOperationDuration("upsert", time.Since(start).Seconds())
	}()

	if serviceName == "" {
		metrics.RecordGossipMessage("upsert", "invalid")
		return errors.New("serviceName required")
	}

	cp := make([]types.ServiceEndpoint, len(endpoints))
	copy(cp, endpoints)

	m.mu.Lock()
	m.entries[serviceName] = cp
	m.mu.Unlock()

	m.cache.Put(context.Background(), serviceName, cp)
	msg := wireMsg{Type: msgUpsert, Name: serviceName, Payload: cp}

	// Record message size before broadcast
	msgBytes, _ := json.Marshal(msg)
	metrics.ObserveGossipMessageSize("upsert", float64(len(msgBytes)))

	m.broadcast(msg)
	metrics.RecordGossipMessage("upsert", "success")
	return nil
}

func (m *memberlistGossip) Remove(ctx context.Context, serviceName string) error {
	start := time.Now()
	defer func() {
		metrics.ObserveGossipOperationDuration("remove", time.Since(start).Seconds())
	}()

	if serviceName == "" {
		metrics.RecordGossipMessage("remove", "invalid")
		return errors.New("serviceName required")
	}

	m.mu.Lock()
	_, exists := m.entries[serviceName]
	delete(m.entries, serviceName)
	m.mu.Unlock()

	m.cache.Remove(context.Background(), serviceName)
	msg := wireMsg{Type: msgRemove, Name: serviceName}

	// Record message size before broadcast
	msgBytes, _ := json.Marshal(msg)
	metrics.ObserveGossipMessageSize("remove", float64(len(msgBytes)))

	m.broadcast(msg)

	if exists {
		metrics.RecordGossipMessage("remove", "success")
	} else {
		metrics.RecordGossipMessage("remove", "not_found")
	}
	return nil
}

func (m *memberlistGossip) apply(msg wireMsg) {
	start := time.Now()
	defer func() {
		metrics.ObserveGossipOperationDuration("apply", time.Since(start).Seconds())
	}()

	switch msg.Type {
	case msgUpsert:
		m.mu.Lock()
		m.entries[msg.Name] = msg.Payload
		m.mu.Unlock()
		m.cache.Put(context.Background(), msg.Name, msg.Payload)
		metrics.RecordGossipMessage("apply", "upsert")
	case msgRemove:
		m.mu.Lock()
		delete(m.entries, msg.Name)
		m.mu.Unlock()
		m.cache.Remove(context.Background(), msg.Name)
		metrics.RecordGossipMessage("apply", "remove")
	}
}

func (m *memberlistGossip) broadcast(msg wireMsg) {
	start := time.Now()
	defer func() {
		metrics.ObserveGossipOperationDuration("broadcast", time.Since(start).Seconds())
	}()

	b, err := json.Marshal(msg)
	if err != nil {
		metrics.RecordGossipMessage("broadcast", "marshal_error")
		return
	}

	metrics.ObserveGossipMessageSize("broadcast", float64(len(b)))
	m.queue.QueueBroadcast(simpleBroadcast(b))
	metrics.RecordGossipMessage("broadcast", "queued")
}

// delegateImpl wires memberlist callbacks to our implementation.
type delegateImpl struct{ g *memberlistGossip }

// Delegate
func (d *delegateImpl) NodeMeta(limit int) []byte { return nil }

func (d *delegateImpl) NotifyMsg(b []byte) {
	metrics.RecordGossipMessage("receive", "raw")
	metrics.ObserveGossipMessageSize("receive", float64(len(b)))

	var m wireMsg
	if err := json.Unmarshal(b, &m); err == nil {
		d.g.apply(m)
		metrics.RecordGossipMessage("receive", string(m.Type))
	} else {
		metrics.RecordGossipMessage("receive", "error")
	}
}

func (d *delegateImpl) GetBroadcasts(overhead, limit int) [][]byte {
	broadcasts := d.g.queue.GetBroadcasts(overhead, limit)
	for _, b := range broadcasts {
		metrics.RecordGossipMessage("broadcast", "sent")
		metrics.ObserveGossipMessageSize("broadcast", float64(len(b)))
	}
	return broadcasts
}
func (d *delegateImpl) LocalState(join bool) []byte {
	start := time.Now()
	defer func() {
		metrics.ObserveGossipOperationDuration("local_state", time.Since(start).Seconds())
	}()

	// On join, send a snapshot of known entries
	d.g.mu.RLock()
	snap := make(map[string][]types.ServiceEndpoint, len(d.g.entries))
	for k, v := range d.g.entries {
		cp := make([]types.ServiceEndpoint, len(v))
		copy(cp, v)
		snap[k] = cp
	}
	d.g.mu.RUnlock()

	b, err := json.Marshal(snap)
	if err != nil {
		metrics.RecordGossipMessage("local_state", "marshal_error")
		return nil
	}

	metrics.RecordGossipMessage("local_state", "success")
	metrics.ObserveGossipMessageSize("local_state", float64(len(b)))
	return b
}
func (d *delegateImpl) MergeRemoteState(buf []byte, join bool) {
	start := time.Now()
	defer func() {
		metrics.ObserveGossipOperationDuration("merge_state", time.Since(start).Seconds())
	}()

	if !join || len(buf) == 0 {
		metrics.RecordGossipMessage("merge_state", "skip")
		return
	}

	metrics.ObserveGossipMessageSize("merge_state", float64(len(buf)))

	var snap map[string][]types.ServiceEndpoint
	if err := json.Unmarshal(buf, &snap); err != nil {
		metrics.RecordGossipMessage("merge_state", "unmarshal_error")
		return
	}

	mergedServices := 0
	for name, eps := range snap {
		// merge: last-write wins per current design; we just upsert snapshot
		d.g.mu.Lock()
		d.g.entries[name] = eps
		d.g.mu.Unlock()
		d.g.cache.Put(context.Background(), name, eps)
		mergedServices++
	}

	metrics.RecordGossipMessage("merge_state", fmt.Sprintf("merged_%d", mergedServices))
}

// EventDelegate
func (d *delegateImpl) NotifyJoin(n *hmemberlist.Node) {
	metrics.RecordGossipMessage("member", "join")
	metrics.SetGossipMembers(float64(d.g.ml.NumMembers()))
}

func (d *delegateImpl) NotifyLeave(n *hmemberlist.Node) {
	metrics.RecordGossipMessage("member", "leave")
	metrics.SetGossipMembers(float64(d.g.ml.NumMembers()))
}

func (d *delegateImpl) NotifyUpdate(n *hmemberlist.Node) {
	metrics.RecordGossipMessage("member", "update")
	metrics.SetGossipMembers(float64(d.g.ml.NumMembers()))
}

// simple broadcast wrapper
type simpleBroadcast []byte

func (s simpleBroadcast) Invalidates(other hmemberlist.Broadcast) bool { return false }
func (s simpleBroadcast) Message() []byte                              { return []byte(s) }
func (s simpleBroadcast) Finished()                                    {}

// ParseAddr splits host:port into host and port int
func ParseAddr(addr string) (string, int) {
	var host string
	var port int
	fmt.Sscanf(addr, "%[^:]:%d", &host, &port)
	return host, port
}
