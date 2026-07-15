# ADR-0002: What Redis is — and is not — used for

- **Status:** Accepted
- **Date:** 2026-07-15

## Context

Redis offers a large surface: strings, hashes, sets, sorted sets, streams,
pub/sub, Lua, HyperLogLog, and the Stack modules (RedisJSON, RediSearch,
RedisBloom, Top-K, Count-Min Sketch, TimeSeries). Nearly every feature in this
platform *could* be routed through one of them.

The project's guiding rule is that every technology earns a real job
([ROADMAP.md](../../ROADMAP.md)). Redis is a cache and a message bus. Postgres is
the source of truth, Kafka is the event backbone (Q3), and Prometheus is the
metrics store (Q4). Wherever Redis would duplicate one of those, it loses.

## Decision

**Adopted, because each solves a problem the alternatives solve worse:**

| Job | Structure | Why not the alternative |
|-----|-----------|-------------------------|
| Cross-replica fanout | Pub/Sub | In-process fanout only reaches sockets on one replica |
| Position → evaluation cache | Strings + TTL | A fixed-depth search is deterministic, so it is memoisable; 355ms → 46ms |
| Rate limiting | Lua token bucket | A fixed window allows a 2× burst across the boundary; Lua makes refill-and-spend atomic across replicas |
| Matchmaking queue | Sorted set + Lua | A list pops the longest waiter, pairing a 900 with a 2400; a ZSET pairs by rating |
| Player rank | Sorted set | `ZREVRANK` is O(log N); SQL must count everyone above you |
| Presence | Sorted set by timestamp | Answers "is X online?" *and* "who is online?" without a keyspace SCAN |
| Spectators | Set | Viewers must be distinct; O(1) membership |
| Daily actives | HyperLogLog | ~12KB at ~0.81% error vs. memory linear in users. Unions are lossless, so weekly actives = merge of 7 dailies |
| Token revocation | Strings + TTL | A stateless JWT cannot be un-signed; the only way to end a session early is to remember what we refuse. Entries expire with the token |

**Rejected, with reasons:**

- **Move validation in Lua.** The rules live in `pkg/chess`, verified by perft.
  A second implementation would be a second source of truth, and the two would
  eventually disagree about who won a game.
- **Rating updates via MULTI/EXEC.** Ratings are durable data. Postgres already
  applies them transactionally and idempotently. Redis is not durable; a restart
  would lose them.
- **Game state in RedisJSON as storage.** Postgres owns game state. Redis may
  cache it; calling it storage is how games get lost.
- **Streams for analysis jobs / notifications.** Kafka is the Q3 backbone. Two
  message buses is worse than either alone.
- **RedisTimeSeries for metrics.** Prometheus does this in Q4.
- **RedisBloom for username/email existence checks.** The Postgres unique index
  is already an index hit; a Bloom filter has false positives, so it must consult
  Postgres anyway. It also re-opens the account-enumeration oracle we closed.
- **Redlock.** We run a single Redis, so its multi-master algorithm buys nothing
  over `SET NX EX`; and it is not safe for correctness-critical locking in any
  case. Advisory de-duplication only.
- **Bitmaps for feature flags / achievements.** No such feature exists.
- **Sentinel / Cluster.** Q4 infrastructure concerns.

**Deferred to Q3, where the data exists to justify them:** RedisBloom for
duplicate-PGN detection, Top-K / Count-Min Sketch for trending openings, and
RediSearch for player/game search. These need Redis Stack rather than
`redis:7-alpine`.

## Consequences

- Redis stays a cache and a bus: **the platform plays chess without it**, just on
  one replica and without cached evaluations. Every helper is nil-safe and
  degrades rather than failing.
- The rate limiter and revocation list **fail open**: an unavailable cache must
  not become an unavailable platform.
- Derived structures (leaderboard, presence) can be rebuilt from Postgres, so a
  Redis flush costs a rebuild, never data.
