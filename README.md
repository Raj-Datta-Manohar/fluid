# Fluid

Fluid is a three-tier service discovery system:
- Tier 1: In-process cache for sub-millisecond lookups
- Tier 2: Gossip mesh for eventually consistent updates
- Tier 3: Raft for strongly consistent, critical events

Status: scaffolding + local single-node Raft + gossip (in-memory or memberlist) are implemented.

Quick start

Agent (in-memory gossip):

```
go run ./cmd/agent
```

Agent (memberlist gossip):

```
GOSSIP_IMPL=memberlist \
GOSSIP_BIND=127.0.0.1:7946 \
GOSSIP_NODE=node-1 \
# optional shared key for message auth
# GOSSIP_KEY_HEX=00112233445566778899aabbccddeeff \
go run ./cmd/agent
```

Create an app via HTTP API:

```
curl -s -X POST http://127.0.0.1:18080/v1/apps \
  -H 'Content-Type: application/json' \
  -d '{"appId":"checkout","ip":"10.0.0.9"}'
```

Or use the CLI:

```
go run ./cmd/fluidctl -app checkout -ip 10.0.0.9
```

Health and readiness:

```
curl http://127.0.0.1:18080/healthz
curl http://127.0.0.1:18080/readyz
```

Examples

```
go run ./examples/basic
```

Design notes

- Tier 1 cache is TTL-evicted and concurrency safe
- Tier 2 supports in-memory dev gossip and a memberlist-backed mesh
- Tier 3 uses `hashicorp/raft` (single node in dev) as the source of truth
- Critical writes go to Raft; updates propagate down to gossip and cache

Next steps

- Metrics and tracing
- Multi-node Raft wiring and redirection
- Memberlist hardening (compression, batching, tuning)