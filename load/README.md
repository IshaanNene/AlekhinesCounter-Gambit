# Load tests

Two tools, two load profiles — because the platform has two very different hot
paths.

- **autocannon** (`autocannon/graphql.js`) — raw HTTP throughput on the GraphQL
  read path (leaderboard, opening explorer). Measures the gateway →
  game-service → Postgres/Redis chain and the rate limiter under a flood.
- **k6** (`k6/game_flow.js`, `k6/spectate.js`) — realistic end-to-end scenarios:
  playing full games (the write path, through Stockfish and the session
  manager) and holding open hundreds of WebSocket spectators (the fanout path).

## Running

```bash
# from the repo root, with `make up` running

# GraphQL read throughput (autocannon)
cd load && npm install
DURATION=10 CONNECTIONS=60 node autocannon/graphql.js

# Full authenticated game flow (k6)
k6 run -e VUS=15 -e DURATION=20s load/k6/game_flow.js

# WebSocket spectator fanout — gateway graphql-ws path (k6)
k6 run -e VUS=150 -e DURATION=20s load/k6/spectate.js

# WebSocket spectator fanout — dedicated fanout tier, one hot game (k6)
k6 run -e GAME=<id> -e VUS=2000 -e DURATION=1m load/k6/fanout.js
```

`make load` runs all three.

`spectate.js` drives the gateway's graphql-ws subscription; `fanout.js` drives
the purpose-built `services/fanout` tier (`/spectate`), the "one game, a huge
crowd" shape where one Redis reader serves every viewer. GAME must already exist
on the event stream (play a game first). See
[chaos/RESULTS.md](chaos/RESULTS.md) for measured numbers and the
`session-handoff` chaos experiment (kill a session-manager node; games survive).

## Chaos & scalability

`load/chaos/` goes further, on the Kubernetes (kind) deployment: it scales and
kills components under load to measure how the platform fails and recovers —
horizontal scaling, pod loss, zero-downtime deploys, Redis/Postgres outages, and
HPA autoscaling. `make chaos` runs the suite; see [chaos/RESULTS.md](chaos/RESULTS.md)
for the documented findings — including two dependency-timeout bugs this testing
caught and fixed (a Redis fail-open hang and an unbounded Postgres dial).

## What the numbers mean

While a test runs, watch **Grafana** (http://localhost:3001) and **Jaeger**
(http://localhost:16686) light up — the load is the point where the observability
work pays off.

### Baselines (local, Docker Compose, M-series laptop)

Indicative, not a benchmark — one machine runs the load generator *and* the
whole stack, so these are a floor, not a ceiling.

| Test | Result |
|------|--------|
| GraphQL reads, limiter **off** | **~18,800 req/s**, all 2xx, 8 ms p99, 0 errors |
| GraphQL reads, limiter **on** (flood) | ~30k req/s processed; ~20/s per IP served, rest 429'd, backend untouched |
| Full game flow, 15 VUs | 170 games completed, **move p95 15 ms**, 0% errors |
| WebSocket spectators, 150 VUs | 590 snapshots, first-snapshot **p95 13 ms**, 0 connection failures |

Two results worth understanding:

- **Moves complete in ~15 ms even though each triggers an engine reply.** Every
  virtual user plays the same opening, so after the first game every position is
  in the Redis eval cache — the cache turning a second of Stockfish search into a
  millisecond lookup is exactly what makes the platform scale.
- **The flood test's 429s are a pass, not a failure.** From one host every
  connection shares an IP and one token bucket, so a 30k req/s flood is
  *supposed* to be mostly rejected. Zero 5xx means the limiter shielded the
  backend rather than letting it fall over. Set `ACG_RATE_LIMIT_RPS=0` on the
  gateway to lift the cap and measure raw throughput.
