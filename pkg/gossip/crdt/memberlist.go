package crdt

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
	"github.com/raj/fluid/pkg/gossip/memberlist"
	"github.com/raj/fluid/pkg/metrics"
	"github.com/raj/fluid/pkg/types"
)

type crdtMessageType string

const (
	crdtMsgUpsert crdtMessageType = "upsert"
	crdtMsgRemove crdtMessageType = "remove"
	crdtMsgState  crdtMessageType = "state"
)

type crdtWireMsg struct {
	Type    crdtMessageType         `json:"type"`
	Name    string                  `json:"name"`
	Payload []types.ServiceEndpoint `json:"payload,omitempty"`
	State   *StateSnapshot          `json:"state,omitempty"`
}

type crdtGossip struct {
	logger *slog.Logger
	cache  cache.LocalCache
	state  *State

	mu    sync.RWMutex
	ml    *hmemberlist.Memberlist
	queue *hmemberlist.TransmitLimitedQueue
}

// NewCRDT creates a memberlist-based CRDT gossip implementation.
func NewCRDT(logger *slog.Logger, cfg memberlist.Config, lc cache.LocalCache) (gossip.GossipMemberlist, func() error, error) {
	if logger == nil {
		logger = slog.Default()
	}

	nodeID := cfg.NodeName
	if nodeID == "" {
		nodeID = "unknown"
	}

	impl := &crdtGossip{
		logger: logger.With("component", "gossip_crdt"),
		cache:  lc,
		state:  NewState(nodeID),
	}

	mlCfg := hmemberlist.DefaultLANConfig()
	if cfg.NodeName != "" {
		mlCfg.Name = cfg.NodeName
	}
	if cfg.BindAddr != "" {
		mlCfg.BindAddr, mlCfg.BindPort = memberlist.ParseAddr(cfg.BindAddr)
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

	delegate := &crdtDelegateImpl{g: impl}
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

func (g *crdtGossip) Lookup(ctx context.Context, serviceName string) ([]types.ServiceEndpoint, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveGossipOperationDuration("crdt_lookup", time.Since(start).Seconds())
	}()

	eps := g.state.Get(serviceName)
	if len(eps) == 0 {
		metrics.RecordGossipMessage("crdt_lookup", "miss")
	} else {
		metrics.RecordGossipMessage("crdt_lookup", "hit")
	}
	return eps, nil
}

func (g *crdtGossip) Upsert(ctx context.Context, serviceName string, endpoints []types.ServiceEndpoint) error {
	start := time.Now()
	defer func() {
		metrics.ObserveGossipOperationDuration("crdt_upsert", time.Since(start).Seconds())
	}()

	if serviceName == "" {
		metrics.RecordGossipMessage("crdt_upsert", "invalid")
		return errors.New("serviceName required")
	}

	// Update CRDT state
	g.state.Upsert(serviceName, endpoints)

	// Update cache
	g.cache.Put(context.Background(), serviceName, endpoints)

	// Broadcast update with size tracking
	msg := crdtWireMsg{Type: crdtMsgUpsert, Name: serviceName, Payload: endpoints}
	msgBytes, _ := json.Marshal(msg)
	metrics.ObserveGossipMessageSize("crdt_upsert", float64(len(msgBytes)))

	g.broadcast(msg)
	metrics.RecordGossipMessage("crdt_upsert", "success")
	return nil
}

func (g *crdtGossip) Remove(ctx context.Context, serviceName string) error {
	start := time.Now()
	defer func() {
		metrics.ObserveGossipOperationDuration("crdt_remove", time.Since(start).Seconds())
	}()

	if serviceName == "" {
		metrics.RecordGossipMessage("crdt_remove", "invalid")
		return errors.New("serviceName required")
	}

	// Update CRDT state
	existed := g.state.Remove(serviceName)

	// Update cache
	g.cache.Remove(context.Background(), serviceName)

	// Broadcast removal
	msg := crdtWireMsg{Type: crdtMsgRemove, Name: serviceName}
	msgBytes, _ := json.Marshal(msg)
	metrics.ObserveGossipMessageSize("crdt_remove", float64(len(msgBytes)))

	g.broadcast(msg)

	if existed {
		metrics.RecordGossipMessage("crdt_remove", "success")
	} else {
		metrics.RecordGossipMessage("crdt_remove", "not_found")
	}
	return nil
}

func (g *crdtGossip) apply(msg crdtWireMsg) {
	switch msg.Type {
	case crdtMsgUpsert:
		g.state.Upsert(msg.Name, msg.Payload)
		g.cache.Put(context.Background(), msg.Name, msg.Payload)
	case crdtMsgRemove:
		g.state.Remove(msg.Name)
		g.cache.Remove(context.Background(), msg.Name)
	case crdtMsgState:
		if msg.State != nil {
			g.state.MergeRemoteState(msg.State)
			// Update cache with merged state
			allServices := g.state.AllServices()
			for name, eps := range allServices {
				g.cache.Put(context.Background(), name, eps)
			}
		}
	}
}

func (g *crdtGossip) broadcast(msg crdtWireMsg) {
	b, _ := json.Marshal(msg)
	g.queue.QueueBroadcast(crdtSimpleBroadcast(b))
}

// crdtDelegateImpl wires memberlist callbacks to CRDT implementation.
type crdtDelegateImpl struct{ g *crdtGossip }

// Delegate
func (d *crdtDelegateImpl) NodeMeta(limit int) []byte { return nil }
func (d *crdtDelegateImpl) NotifyMsg(b []byte) {
	var m crdtWireMsg
	if json.Unmarshal(b, &m) == nil {
		d.g.apply(m)
	}
}
func (d *crdtDelegateImpl) GetBroadcasts(overhead, limit int) [][]byte {
	return d.g.queue.GetBroadcasts(overhead, limit)
}
func (d *crdtDelegateImpl) LocalState(join bool) []byte {
	// Send full state snapshot on join
	snapshot := d.g.state.Snapshot()
	b, _ := json.Marshal(&crdtWireMsg{Type: crdtMsgState, State: snapshot})
	return b
}
func (d *crdtDelegateImpl) MergeRemoteState(buf []byte, join bool) {
	var msg crdtWireMsg
	if json.Unmarshal(buf, &msg) == nil && msg.Type == crdtMsgState {
		d.g.apply(msg)
	}
}

// EventDelegate
func (d *crdtDelegateImpl) NotifyJoin(n *hmemberlist.Node)   {}
func (d *crdtDelegateImpl) NotifyLeave(n *hmemberlist.Node)  {}
func (d *crdtDelegateImpl) NotifyUpdate(n *hmemberlist.Node) {}

// crdt simple broadcast wrapper
type crdtSimpleBroadcast []byte

func (s crdtSimpleBroadcast) Invalidates(other hmemberlist.Broadcast) bool { return false }
func (s crdtSimpleBroadcast) Message() []byte                              { return []byte(s) }
func (s crdtSimpleBroadcast) Finished()                                    {}
