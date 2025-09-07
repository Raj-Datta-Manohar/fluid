package consensus

import (
	"context"
)

// ConsensusClient abstracts a Raft-like strongly consistent log.
// Implementations should block in ProposeEvent until commit or error.
type ConsensusClient interface {
	ProposeEvent(ctx context.Context, event any) error
	RegisterEventHandler(eventType string, handler func(event any)) error
}
