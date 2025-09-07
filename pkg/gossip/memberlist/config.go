package memberlist

import (
	"time"
)

// Config holds settings for the memberlist-based gossip layer.
type Config struct {
	NodeName       string
	BindAddr       string // host:port
	Seeds          []string
	RetransmitMult int
	GossipInterval time.Duration
	ProbeInterval  time.Duration
	KeyHex         string // optional hex-encoded secret for keyring
	Compression    bool   // enable message compression
	BatchSize      int    // batch multiple messages
	MaxMessageSize int    // max message size before compression
}
