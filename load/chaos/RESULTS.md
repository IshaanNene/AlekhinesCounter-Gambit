# Chaos & scalability results

Fault-injection and scaling experiments run against the Kubernetes (kind)
deployment with `load/chaos/chaos.sh`. Each drives read load through the
gateway's NodePort (`localhost:8088`) with `probe.mjs` while `kubectl` scales or
kills something underneath it.

These are **shape** results, not a benchmark: one laptop runs the 3-node cluster,
all its infra, *and* the load generator, so absolute throughput is a floor. What
matters is the behaviour — does throughput scale, do failures stay contained,
does the platform heal itself.

Run one experiment: `load/chaos/chaos.sh <name>` · everything: `make chaos`.

## Scalability — read throughput vs. gateway replicas

Rate limiter disabled (`ACG_RATE_LIMIT_RPS=0`) so a single load-gen IP isn't
throttled to 20/s.

| gateway replicas | req/s | p50 | p99 | 5xx/err |
|---|---|---|---|---|
| ×1 | 17,649 | 2 ms | 39 ms | 0 |
| ×2 | 21,551 | 1 ms | 28 ms | 0 |
| ×4 | 21,481 | 1 ms | 30 ms | 0 |

Throughput rises 1→2 and p99 tightens (39→28 ms), then plateaus at ×4: past two
replicas the ceiling is downstream — the single `game-service` replica and a
single-node NodePort ingress on one host — not the gateway. Honest read:
horizontal scaling works until you hit the next bottleneck, and here that
bottleneck is the laptop.

## Fault tolerance — pod loss under load

| scenario | result |
|---|---|
| Kill a gateway pod (of 2) mid-load | **526,976 served, 0 errors**, p99 33 ms |
| Kill the sole game-service pod (of 1) | reads error, **self-heals in ~16 s** (reschedule + gRPC reconnect) |

The lesson is redundancy, stated as a number: a replicated tier absorbs an
instance loss with zero dropped requests; a singleton has a real recovery window,
but Kubernetes restores it automatically with no human in the loop.

## Zero-downtime deploy — rolling restart under load

`kubectl rollout restart deploy/gateway` (2 replicas) during 30 s of load:

- **617,876 served, 0 dropped.**

Readiness gating + rolling update means a deploy never drops a request — the old
pod keeps serving until the new one is ready.

## Graceful degradation — Redis loss  ⚠️ found and fixed a bug

The rate limiter calls Redis on **every** request. The first run of this
experiment exposed a real defect: with Redis scaled to zero, *every* request —
including the gateway-local `health` — **hung ~10 s**. `Allow()` passed the
request context (no deadline) straight to Redis, so it blocked on the client's
dial timeout before finally failing open. The intent ("availability over strict
enforcement") was right; the implementation was too slow to deliver it.

**Fix** (`pkg/redisx/ratelimit.go`): bound the Redis call to `limiterTimeout`
(50 ms), so an unreachable Redis fails open in milliseconds instead of stalling
the request path.

After the fix, with Redis down:

| Redis | throttled (429) | served (2xx) | 5xx/err | `health` latency |
|---|---|---|---|---|
| up | 131,288 | 182 | 0 | — |
| **down** | 0 | 5,520 | **0** | **62 ms** (was ~10 s) |

Killing Redis now releases throttling and breaks nothing: reads are
Postgres-backed and the limiter fails open fast. Redis degrades to "no rate
limiting", never to "down". (Throughput dips while Redis is out, since each
request waits up to the 50 ms bound before failing open — a fine trade against a
10-second hang.)

This is the whole point of chaos testing: the bug was invisible until the
dependency actually went away under load.

## Blast radius — Postgres loss (the hard dependency)  ⚠️ found and fixed a second issue

Reads are Postgres-backed, so with the DB gone they genuinely can't be served.
The question is whether the damage is contained and self-healing.

The first run of this experiment exposed the same class of problem as the Redis
one: game-service dialed Postgres with no bound, so with the DB down every
DB-backed read **stalled on the connection dial** — only ~13 requests completed
in 8 s, each stuck connection held for the whole window.

**Fix** (`pkg/store/store.go`): build the pool with `pgxpool.NewWithConfig` and
set `ConnConfig.ConnectTimeout` (3 s) so a dead DB fails fast, plus a server-side
`statement_timeout` (5 s) for the other failure mode — a live but slow or
lock-blocked query. A read against a down Postgres now returns a structured error
in ~3 s instead of hanging, and `health` stays instant (22 ms).

| metric | before | after |
|---|---|---|
| reqs completed during 8 s outage | 13 | **42** |
| single read latency, DB down | ~10 s (hang) | **~3 s** (clean error) |
| gateway / game-service crashes | 0 / 0 | 0 / 0 |
| recovery after DB returns | contained | contained, reconnects fast |

The DB is a hard dependency, so reads still can't succeed while it is down — but
now they **fail fast and free the connection** instead of stalling it, and the
services never crash or crash-loop. (Here on an emptyDir the schema is re-migrated
by a game-service restart; a PVC-backed Postgres just reconnects.)

## Autoscaling — HPA on the gateway

metrics-server installed (`--kubelet-insecure-tls` for kind), HPA at 50% CPU,
min 1 / max 5, under sustained load:

| t | replicas | CPU (of 50% target) |
|---|---|---|
| +10 s | 1 | warming up |
| +30 s | 4 | 1984% |
| +50 s | 5 | 1994% |
| +60 s | 5 (max) | 1986% |

The HPA observes CPU far over target and scales to the ceiling within ~30 s —
autoscaling that actually reacts, not just a manifest that claims to.

---

### Summary

| Property | Verdict |
|---|---|
| Horizontal scaling | ✅ scales to the next bottleneck; 0 errors throughout |
| Instance loss (replicated) | ✅ zero dropped requests |
| Instance loss (singleton) | ✅ ~16 s automatic self-heal |
| Rolling deploy | ✅ zero-downtime |
| Redis outage | ✅ **after fix**: fails open in ~60 ms, 0 errors |
| Postgres outage | ✅ **after fix**: fails fast in ~3 s, 0 crashes, auto-recovers |
| Autoscaling | ✅ HPA scales 1→5 under load |

Two real resilience bugs found and fixed, both of the same shape — an unbounded
call to a dependency that turned that dependency's outage into stuck requests:

- **Redis** (`pkg/redisx/ratelimit.go`): the per-request rate-limiter call had no
  timeout, so a Redis outage hung every request ~10 s. Bounded to 50 ms.
- **Postgres** (`pkg/store/store.go`): the connection dial had no timeout, so a DB
  outage stalled DB-backed reads ~10 s. Bounded with a 3 s connect timeout and a
  5 s statement timeout.
