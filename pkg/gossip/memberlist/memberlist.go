package memberlist

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	hmemberlist "github.com/hashicorp/memberlist"

	"github.com/raj/fluid/pkg/cache"
	"github.com/raj/fluid/pkg/gossip"
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
		mlCfg.BindAddr, mlCfg.BindPort = parseAddr(cfg.BindAddr)
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
	m.mu.RLock()
	eps := m.entries[serviceName]
	m.mu.RUnlock()
	if len(eps) == 0 {
		return nil, nil
	}
	cp := make([]types.ServiceEndpoint, len(eps))
	copy(cp, eps)
	return cp, nil
}

func (m *memberlistGossip) Upsert(ctx context.Context, serviceName string, endpoints []types.ServiceEndpoint) error {
	if serviceName == "" {
		return errors.New("serviceName required")
	}
	cp := make([]types.ServiceEndpoint, len(endpoints))
	copy(cp, endpoints)
	m.mu.Lock()
	m.entries[serviceName] = cp
	m.mu.Unlock()
	m.cache.Put(serviceName, cp)
	msg := wireMsg{Type: msgUpsert, Name: serviceName, Payload: cp}
	m.broadcast(msg)
	return nil
}

func (m *memberlistGossip) Remove(ctx context.Context, serviceName string) error {
	if serviceName == "" {
		return errors.New("serviceName required")
	}
	m.mu.Lock()
	delete(m.entries, serviceName)
	m.mu.Unlock()
	m.cache.Remove(serviceName)
	msg := wireMsg{Type: msgRemove, Name: serviceName}
	m.broadcast(msg)
	return nil
}

func (m *memberlistGossip) apply(msg wireMsg) {
	switch msg.Type {
	case msgUpsert:
		m.mu.Lock()
		m.entries[msg.Name] = msg.Payload
		m.mu.Unlock()
		m.cache.Put(msg.Name, msg.Payload)
	case msgRemove:
		m.mu.Lock()
		delete(m.entries, msg.Name)
		m.mu.Unlock()
		m.cache.Remove(msg.Name)
	}
}

func (m *memberlistGossip) broadcast(msg wireMsg) {
	b, _ := json.Marshal(msg)
	m.queue.QueueBroadcast(simpleBroadcast(b))
}

// delegateImpl wires memberlist callbacks to our implementation.
type delegateImpl struct{ g *memberlistGossip }

// Delegate
func (d *delegateImpl) NodeMeta(limit int) []byte { return nil }
func (d *delegateImpl) NotifyMsg(b []byte) {
	var m wireMsg
	if json.Unmarshal(b, &m) == nil {
		d.g.apply(m)
	}
}
func (d *delegateImpl) GetBroadcasts(overhead, limit int) [][]byte {
	return d.g.queue.GetBroadcasts(overhead, limit)
}
func (d *delegateImpl) LocalState(join bool) []byte {
	// On join, send a snapshot of known entries
	d.g.mu.RLock()
	snap := make(map[string][]types.ServiceEndpoint, len(d.g.entries))
	for k, v := range d.g.entries {
		cp := make([]types.ServiceEndpoint, len(v))
		copy(cp, v)
		snap[k] = cp
	}
	d.g.mu.RUnlock()
	b, _ := json.Marshal(snap)
	return b
}
func (d *delegateImpl) MergeRemoteState(buf []byte, join bool) {
	if !join || len(buf) == 0 {
		return
	}
	var snap map[string][]types.ServiceEndpoint
	if err := json.Unmarshal(buf, &snap); err != nil {
		return
	}
	for name, eps := range snap {
		// merge: last-write wins per current design; we just upsert snapshot
		d.g.mu.Lock()
		d.g.entries[name] = eps
		d.g.mu.Unlock()
		d.g.cache.Put(name, eps)
	}
}

// EventDelegate
func (d *delegateImpl) NotifyJoin(n *hmemberlist.Node)   {}
func (d *delegateImpl) NotifyLeave(n *hmemberlist.Node)  {}
func (d *delegateImpl) NotifyUpdate(n *hmemberlist.Node) {}

// simple broadcast wrapper
type simpleBroadcast []byte

func (s simpleBroadcast) Invalidates(other hmemberlist.Broadcast) bool { return false }
func (s simpleBroadcast) Message() []byte                              { return []byte(s) }
func (s simpleBroadcast) Finished()                                    {}

// parseAddr splits host:port into host and port int
func parseAddr(addr string) (string, int) {
	var host string
	var port int
	fmt.Sscanf(addr, "%[^:]:%d", &host, &port)
	return host, port
}
