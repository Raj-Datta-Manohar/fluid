# Fluid

Fluid is a three-tier service discovery system:
- **Tier 1**: In-process cache for sub-millisecond lookups
- **Tier 2**: Gossip mesh for eventually consistent updates
- **Tier 3**: Raft for strongly consistent, critical events

## Quick Start

### Agent (in-memory gossip)
```bash
go run ./cmd/agent
```

### Agent (memberlist gossip)
```bash
GOSSIP_IMPL=memberlist \
GOSSIP_BIND=127.0.0.1:7946 \
GOSSIP_NODE=node-1 \
go run ./cmd/agent
```

### Agent (CRDT gossip)
```bash
GOSSIP_IMPL=crdt \
GOSSIP_BIND=127.0.0.1:7946 \
GOSSIP_NODE=node-1 \
GOSSIP_COMPRESSION=true \
go run ./cmd/agent
```

### Multi-node Raft cluster
```bash
# Node 1 (bootstrap)
RAFT_BIND=127.0.0.1:11000 \
RAFT_ID=node1 \
RAFT_BOOTSTRAP=true \
go run ./cmd/agent

# Node 2 (join)
RAFT_BIND=127.0.0.1:11001 \
RAFT_ID=node2 \
RAFT_JOIN_URL=http://127.0.0.1:18080 \
go run ./cmd/agent
```

## API Usage

### Create an app via HTTP API
```bash
curl -s -X POST http://127.0.0.1:18080/v1/apps \
  -H 'Content-Type: application/json' \
  -d '{"appId":"checkout","ip":"10.0.0.9"}'
```

### Use the CLI
```bash
go run ./cmd/fluidctl -app checkout -ip 10.0.0.9
```

### Health and readiness
```bash
curl http://127.0.0.1:18080/healthz
curl http://127.0.0.1:18080/readyz
```

### Service lookup
```bash
curl http://127.0.0.1:18080/v1/services/checkout
```

## Examples
```bash
go run ./examples/basic
```

## Environment Variables

### Gossip Configuration
- `GOSSIP_IMPL`: Gossip implementation (`inmemory`, `memberlist`, `crdt`)
- `GOSSIP_BIND`: Bind address for gossip (default: `127.0.0.1:7946`)
- `GOSSIP_NODE`: Node name/ID
- `GOSSIP_SEEDS`: Comma-separated seed nodes
- `GOSSIP_KEY_HEX`: Optional hex-encoded secret for message authentication
- `GOSSIP_COMPRESSION`: Enable message compression (`true`/`false`)

### Raft Configuration
- `RAFT_BIND`: Raft bind address (default: `127.0.0.1:11000`)
- `RAFT_ID`: Raft node ID
- `RAFT_BOOTSTRAP`: Bootstrap single-node cluster (`true`/`false`)
- `RAFT_JOIN_URL`: URL to join existing cluster
- `RAFT_SNAPSHOT_INTERVAL`: Snapshot interval (default: `30s`)
- `RAFT_SNAPSHOT_THRESHOLD`: Snapshot threshold (default: `1000`)

## Design Notes

- **Tier 1**: TTL-evicted, concurrency-safe in-memory cache
- **Tier 2**: Supports in-memory dev gossip, memberlist mesh, and CRDT gossip with LWW + vector clocks
- **Tier 3**: Uses `hashicorp/raft` as the source of truth for critical events
- **CRDT**: Last-Write-Wins semantics with vector clocks for conflict resolution
- **Compression**: Optional gzip compression for gossip messages
- **Multi-node**: Raft cluster with automatic join and leader redirection

## Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Tier 1        │    │   Tier 2        │    │   Tier 3        │
│   Local Cache   │◄───┤   Gossip Mesh   │◄───┤   Raft Log      │
│   (TTL eviction)│    │   (CRDT/LWW)    │    │   (Consensus)   │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

## Next Steps

- [ ] Prometheus metrics and OpenTelemetry tracing
- [ ] Size-based cache eviction (LRU) and negative caching
- [ ] Non-critical write APIs (Tier 2 paths)
- [ ] CLI enhancements (delete/get/list/config)
- [ ] Unit and integration tests
- [ ] Docker and docker-compose setup