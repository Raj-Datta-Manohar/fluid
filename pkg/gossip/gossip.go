package gossip

import (
	"context"

	"github.com/raj/fluid/pkg/types"
)

// GossipMemberlist defines the Tier 2 interaction surface used by the service layer.
type GossipMemberlist interface {
	Lookup(ctx context.Context, serviceName string) ([]types.ServiceEndpoint, error)
	Upsert(ctx context.Context, serviceName string, endpoints []types.ServiceEndpoint) error
	Remove(ctx context.Context, serviceName string) error
}
