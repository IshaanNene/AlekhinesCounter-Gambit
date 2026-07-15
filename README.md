# Alekhine's Counter-Gambit

A distributed chess engine and platform: play chess against Stockfish or other
humans, with moves validated, persisted, streamed live, analyzed asynchronously,
and the whole system running on Kubernetes with full observability.

Built in four quarterly releases — see [ROADMAP.md](ROADMAP.md) for the plan and
[TASKS.md](TASKS.md) for execution-level tasks.

## Stack

Go · Erlang/OTP · gRPC + Protocol Buffers · GraphQL · WebSockets · PostgreSQL ·
Redis · Kafka · MinIO · Stockfish/UCI · Docker · Kubernetes · Helm · Terraform ·
NGINX · ArgoCD · GitHub Actions · Prometheus + Grafana · Jaeger + OpenTelemetry ·
autocannon + k6

## Quickstart

```bash
make tools     # one-time: install buf, protoc plugins, goose, grpcurl, linter
make up        # bring up postgres + engine-worker + game-service (Docker Compose)
make run-game  # play a full game vs Stockfish from the terminal
make down      # tear it all down
```

Run `make help` to see every target.

## Repository layout

```
proto/        protobuf contracts (source of truth) + generated Go stubs
pkg/chess/    shared chess logic (board, FEN, legal move generation)
services/     game-service, engine-worker, gateway (Q2), session-manager (Q2, Erlang)
cmd/          small binaries (play CLI)
migrations/   SQL migrations (goose)
infra/        Helm, Terraform, k8s, observability (Q4)
load/         autocannon + k6 load tests (Q4)
docs/adr/     architecture decision records
```

## Status

**Q1 (foundation & vertical slice) — complete.** You can play a full game vs
Stockfish end-to-end through real gRPC services, persisted to Postgres, via
`make up` + `make run-game`. Highlights:

- `pkg/chess` — legal move generation verified by perft (start position to
  depth 4 = 197,281 nodes, plus Kiwipete and endgame positions).
- `engine-worker` — Stockfish driven over UCI behind a gRPC `Analyze` API.
- `game-service` — move validation, Postgres persistence, engine orchestration,
  embedded migrations applied on startup.
- `docker-compose.yml` — one-command local stack; `cmd/play` CLI to play a game.
- CI (GitHub Actions) — build, test (with Postgres + Stockfish), lint, proto drift.

**Q2 (distributed core & real-time) — in progress.** The Erlang/OTP session
manager is live and bridged to Go over gRPC:

- `session-manager` (Erlang/OTP) — one supervised `gen_server` per live game
  owning Fischer clocks, turn, presence, and reconnect grace. Crash-isolated:
  killing one game never touches another. Served over gRPC via grpcbox.
- `game-service` ↔ `session-manager` — the Go service provisions a session per
  human-vs-human game, authorizes the side to move, reports each validated move
  (the session applies the clock + increment), and closes the session on
  checkmate/stalemate/draw so a decided game can never flag-fall.
- `gateway` (Go + gqlgen) — public GraphQL API on `:8080/graphql` with a
  playground at `/`. Holds no state: it translates GraphQL into internal gRPC.
  `Game.clock` is a lazy field resolver, so the session-manager is only queried
  when a client actually selects it (and is null for engine games).
- **Live subscriptions** — `subscription { gameUpdated(gameId:) }` over
  WebSocket (`graphql-transport-ws` at `/ws`). Subscribers get the current state
  on connect, then every move pushed with no polling. Fanout is in-process for
  now; T2.7 swaps in Redis so it survives multiple gateway replicas.
- **Web client** (`web/`) — a dependency-free neumorphic UI served by NGINX,
  which also proxies `/graphql` and `/ws` so the browser sees one origin.
  Play Stockfish or share a link and watch the board update live in both tabs.
- `make up` now runs all six services: Postgres, engine-worker,
  session-manager, game-service, gateway, web (**open http://localhost:3000**).

Play a whole game over GraphQL:

```graphql
mutation { createGame(input: { engineDepth: 10 }) { id fen } }
mutation { move(input: { gameId: "<id>", uci: "e2e4" }) { fen moves { ply uci } } }
query    { game(id: "<id>") { status endReason clock { whiteMs blackMs running } } }
```

Next in Q2: WebSocket subscriptions, Redis fanout/cache, matchmaking + auth.
See [TASKS.md](TASKS.md).
