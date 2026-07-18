# ADR-0005: A dedicated spectator fanout tier

- **Status:** Accepted
- **Date:** 2026-07-18

## Context

Two clients watch a game very differently. A **player** has an interactive,
authenticated, bidirectional session (the gateway's GraphQL-over-WebSocket
subscription). A **spectator** is read-only, unauthenticated, and — for a popular
game — arrives by the thousand. Serving both down the same path couples a
write-side concern (auth, mutations) to a pure broadcast problem, and the
original fanout (the gateway publishing *full game snapshots* over Redis pub/sub)
is the wrong shape at scale: every spectator is a separate subscription, and every
update ships the whole board.

The force is the "one hot game, a huge crowd" scenario — the thing that makes a
real-time platform interesting, and the second half of the scale story.

## Decision

Build a **separate `services/fanout` tier**, fed by the durable per-move event
stream (`pkg/eventlog`), with three deliberate choices.

- **One Redis reader per game, not per viewer.** A single goroutine (the game's
  "hub") tails the game's stream and broadcasts from an in-memory history to every
  local spectator. A crowd of N costs one Redis reader + N in-memory sends, not N
  Redis subscriptions. A late joiner is handed the backlog **and** registered for
  live updates atomically under one lock, so it can never miss or double a move.
- **Deltas, not snapshots.** Each spectator receives per-move deltas (the move +
  resulting FEN + stream id), then a `synced` marker, then live moves — a fraction
  of the bytes of a full-board snapshot per update.
- **Backpressure by reconnection.** A spectator whose send buffer overflows is
  dropped, not allowed to stall the broadcast; it reconnects with
  `from=<last stream id>` and replays exactly the gap from the same stream. Bounded
  memory, self-healing, no per-client unbounded queues.

The tier holds no game state and depends only on Redis, so it scales on its own
(HPA), and the interactive play path is left untouched.

## Consequences

- **Easier:** spectator scale is now a first-class, independently-scalable concern
  with its own metrics (spectators, hubs, deltas/s, drops) on the Grafana wall. A
  local load test shows 500 spectators on one game caught up in ~2 ms with zero
  drops; at cluster scale each fanout replica keeps its own reader, so N replicas
  is an N-way tree over Redis.
- **Harder / accepted:** history lives in each hub's memory (bounded — a chess game
  is a few hundred plies), and a very hot single game whose spectator count exceeds
  one replica's connection ceiling would want an explicit relay tree. We defer that:
  horizontal replicas already spread a hot game N ways, which covers the realistic
  range; the relay tree is a known next step, not a day-one need.
- **Cost:** a second WebSocket surface and its NGINX route; a browser watch page
  (`web/watch.html`) that speaks the delta protocol. Delivery is at-least-once with
  client-side idempotency (ignore ids ≤ last seen), the same contract as the stream.
