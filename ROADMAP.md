# Alekhine's Counter-Gambit — Roadmap

A distributed chess engine + platform. Built in four quarterly releases (Q1–Q4).
Each quarter ships something demoable; by the end of **Q4 the full architecture is
running in production** (Kubernetes, GitOps, observability, load-tested).

**Guiding principle:** every technology earns a real job. No checkbox-driven adds.

---

## Architecture (target state — live by end of Q4)

```
                         ┌─── NGINX Ingress ───┐
   Client (web) ─────────┤  HTTP/GraphQL + WS  ├──────────┐
                         └─────────────────────┘          │
                                    │                      │
                          ┌─────────▼──────────┐   ┌───────▼─────────┐
                          │  API Gateway       │   │  WS Fanout      │
                          │  GraphQL (Go)      │   │  (Redis pub/sub)│
                          └─────────┬──────────┘   └───────┬─────────┘
                                    │ gRPC + protobuf      │
              ┌─────────────────────┼──────────────────────┘
              │                     │
   ┌──────────▼─────────┐  ┌────────▼──────────┐   ┌──────────────────┐
   │  Game Service      │  │  Session Manager  │   │  Engine Workers  │
   │  moves, validation │  │  ★ ERLANG/OTP     │   │  Stockfish/UCI   │
   │  (Go)              │  │  actor-per-game   │   │  gRPC, scaled    │
   └──────────┬─────────┘  └────────┬──────────┘   └────────┬─────────┘
              │                     │                       │
              └─────────────────────┼───────────────────────┘
                           ┌────────▼──────────────┐
                           │  Kafka (event stream) │
                           └───────────────────────┘
        ┌──────────┬──────────────┬──────────────┬─────────────┐
   ┌────▼───┐ ┌────▼─────┐  ┌─────▼─────┐   ┌─────▼─────┐  ┌────▼────┐
   │Postgres│ │  Redis   │  │  MinIO    │   │Prometheus │  │ Jaeger  │
   │users,  │ │eval cache│  │ PGN,books │   │+ Grafana  │  │  +OTel  │
   │games   │ │pub/sub   │  │ analyses  │   │ metrics   │  │ traces  │
   └────────┘ └──────────┘  └───────────┘   └───────────┘  └─────────┘
```

**Tech → job map**

| Tech | Job | Lands in |
|------|-----|----------|
| Protocol Buffers | Shared schema for all internal RPC | Q1 |
| Stockfish / UCI | Engine workers wrap UCI, stateless | Q1 |
| PostgreSQL | Users, games, PGN, ratings | Q1 |
| Docker | Local dev + image builds | Q1 |
| gRPC | Typed internal RPC between services | Q1–Q2 |
| GraphQL | Public API + subscriptions for the web client | Q2 |
| Erlang/OTP | Session manager: one supervised actor per live game | Q2 |
| WebSockets | Live board updates / spectating | Q2 |
| Redis | Position→eval cache, WS pub/sub fanout, rate limiting | Q2 |
| Kafka | Decouple move events from analysis pipeline | Q3 |
| MinIO | Opening books, stored analyses, PGN archives (S3-compatible) | Q3 |
| Kubernetes | Orchestration for every service | Q4 |
| Helm | Package + template the k8s manifests | Q4 |
| Terraform | Provision cluster + managed deps | Q4 |
| NGINX | Ingress / TLS termination / routing | Q4 |
| ArgoCD | GitOps continuous deployment | Q4 |
| GitHub Actions | CI: build, test, lint, push images | Q4 (basic CI in Q1) |
| Prometheus + Grafana | Metrics + dashboards | Q4 |
| Jaeger + OpenTelemetry | Distributed tracing across the request path | Q4 |
| autocannon + k6 | Load test GraphQL (autocannon) + WS scenarios (k6) | Q4 |

---

## Q1 — Foundation & Vertical Slice

**Theme:** One game, end to end. Get a move flowing from client → gateway →
game service → engine worker → back, all in Docker Compose.

**Demo at end of Q1:** Start a game against Stockfish over a simple API, play
moves, get engine replies, game persists to Postgres and can be reloaded.

### Epic 1.1 — Repo & tooling
- [x] Initialize monorepo layout (`/services`, `/proto`, `/infra`, `/docs`, `/load`)
- [x] Set up Go workspace + module structure for services
- [x] Add `Makefile` / task runner (build, test, lint, proto-gen, up/down)
- [x] Write ADR-0001 (architecture decision record) documenting the design
- [x] Add root `README.md` with quickstart

### Epic 1.2 — Protobuf contracts
- [x] Define `game.proto` (game state, move, result)
- [x] Define `engine.proto` (analyze request/response, bestmove, eval)
- [x] Set up `buf` (or protoc) codegen pipeline → Go stubs
- [x] Commit generated code + CI check that it's up to date

### Epic 1.3 — Core game logic (Go)
- [x] FEN/PGN parsing + board representation
- [x] Legal move generation + validation
- [x] Game state machine (check, checkmate, stalemate, draw rules)
- [x] Unit tests against known positions (perft tests)

### Epic 1.4 — Engine worker (Stockfish/UCI)
- [x] Wrap Stockfish binary, drive it over the UCI protocol
- [x] Expose gRPC `Analyze` (position → bestmove + eval + depth)
- [x] Stateless design (any request routable to any worker)
- [x] Dockerfile bundling Stockfish

### Epic 1.5 — Game service (Go)
- [x] gRPC service: create game, submit move, get game
- [x] Call engine worker over gRPC for opponent moves
- [x] Persist games + moves to Postgres (with migrations)
- [x] Postgres schema: `users`, `games`, `moves`

### Epic 1.6 — Local orchestration
- [x] `docker-compose.yml`: game service, engine worker, Postgres
- [x] Seed/migration on startup
- [x] Minimal CLI or HTTP shim to play a game manually
- [x] Basic GitHub Actions CI: build + unit tests on push

**Exit criteria:** `make up` → play a full game vs Stockfish → game survives a restart.

---

## Q2 — Distributed Core & Real-time

**Theme:** Make it multiplayer and live. Introduce the Erlang session manager,
the GraphQL public API, WebSockets, and Redis.

**Demo at end of Q2:** Two browsers join a game, moves appear live on both
boards in real time; a spectator can watch; disconnect/reconnect is handled.

### Epic 2.1 — Session manager (★ Erlang/OTP)
- [x] OTP application skeleton with supervision tree
- [x] `gen_server` actor **per live game** (clock, turn, players, spectators)
- [x] Supervisor restart strategy for crashed game processes
- [x] Player disconnect / reconnect handling with grace timers
- [x] Chess clock logic (increment, flag-fall)
- [x] gRPC interface between Go game service and Erlang session manager

### Epic 2.2 — GraphQL API gateway (Go)
- [x] GraphQL schema: `game` query, `createGame`/`move`/`resign` mutations
- [x] Translate GraphQL ↔ internal gRPC calls (game-service + session-manager)
- [x] `me` / `user` / `gameHistory` / `leaderboard` queries
- [x] GraphQL subscriptions for live game updates
- [x] AuthN (JWT sessions) + basic authorization

### Epic 2.3 — Real-time transport (WebSockets)
- [x] WebSocket endpoint for board/clock updates (graphql-transport-ws at `/ws`)
- [x] Web client: neumorphic board, live via subscription, NGINX-fronted
- [x] Redis pub/sub fanout so any gateway replica can push to any client
- [ ] Presence tracking (who's connected / spectating) — session-manager tracks
      it internally; not yet surfaced through GraphQL

### Epic 2.4 — Redis integration
- [x] Position→eval cache (keyed by FEN + depth) — 355ms → 46ms on a repeat
- [x] Rate limiting on API + moves (distributed token bucket, Lua)
- [x] Session store — not needed: JWTs are stateless and self-verifying, so
      there is nothing to store. Redis stays a pure cache/bus.

### Epic 2.5 — Matchmaking
- [x] Open seats: create a game, share the link, opponent joins as themselves
- [x] Elo ratings applied on completion (+ history and leaderboard)
- [x] Automatic queue pairing by rating band (Redis ZSET + Lua, band widens with wait)
- [ ] Bot fallback (play vs engine if no human found)

**Exit criteria:** live human-vs-human game across two browsers, spectators,
reconnect works, evals served from Redis cache on repeat positions.

---

## Q3 — Event-driven Data & Analysis

**Theme:** Decouple with events; add durable object storage and an analysis
pipeline. This is where the system becomes genuinely distributed and async.

**Demo at end of Q3:** Finished games are auto-analyzed in the background;
users get move-by-move eval graphs; PGNs and analyses are archived and
downloadable; opening book influences engine play.

### Epic 3.1 — Kafka event backbone
- [x] Topics: `game-finished`, `analysis-requested`, `analysis-completed`
- [ ] Game service produces move/game events to Kafka
- [ ] Engine workers consume `analysis-requests` (pull-based work queue)
- [ ] Consumer groups for horizontal scaling of workers
- [ ] Schema registry / protobuf serialization on the wire

### Epic 3.2 — Analysis pipeline
- [x] On game end, enqueue full-game analysis request
- [x] Workers analyze each position, emit eval + best line
- [x] Store per-move analysis; compute blunders/mistakes/accuracy
- [x] Eval-graph data exposed via GraphQL (+ rendered in the web client)
- [x] Theoretical novelty detection (RedisBloom)
- [x] Fair-play signals for human review (RedisTimeSeries) — see ADR-0003

### Epic 3.3 — MinIO object storage
- [x] Store PGN archives (S3-compatible, real SAN generated from the game)
- [x] Presigned download URLs via the gateway (split-horizon aware)
- [ ] Store full analysis artifacts (JSON) keyed by game id
- [ ] Opening book files + serve to engine workers

### Epic 3.4 — Ratings & history
- [ ] Elo/Glicko rating updates on game completion
- [ ] Leaderboards + user game history (paginated GraphQL)
- [ ] Opening explorer backed by stored games

**Exit criteria:** completed games flow through Kafka, get analyzed async by a
pool of workers, results land in Postgres + MinIO, and show up as an eval graph.

---

## Q4 — Production Platform (full architecture live)

**Theme:** Everything ships to a real cluster with GitOps, full observability,
and load testing. **By the end of Q4 every component in the architecture diagram
is deployed and running in production**, and all remaining stack items land here.

**Demo at end of Q4:** `git push` → CI builds/tests/pushes images → ArgoCD syncs
to Kubernetes → NGINX routes traffic → Grafana dashboards + Jaeger traces show a
live request crossing every service → k6 load test drives thousands of concurrent
games and the system autoscales.

### Epic 4.1 — Containerization & Kubernetes
- [ ] Production Dockerfiles for every service (multi-stage, slim)
- [ ] Kubernetes manifests: Deployments, Services, ConfigMaps, Secrets
- [ ] HorizontalPodAutoscalers (esp. engine workers)
- [ ] Liveness/readiness probes, resource requests/limits
- [ ] StatefulSets for Postgres/Kafka/MinIO (or managed equivalents)

### Epic 4.2 — Helm
- [ ] Chart per service + an umbrella chart for the platform
- [ ] Values files for local / staging / prod
- [ ] Templated config, secrets, image tags

### Epic 4.3 — Terraform
- [ ] Provision cluster (kind/k3d for local; EKS/GKE module for cloud)
- [ ] Managed Postgres, Redis, object storage (or in-cluster via TF)
- [ ] Networking, DNS, IAM, TLS certs
- [ ] Remote state + workspaces per environment

### Epic 4.4 — Ingress & routing (NGINX)
- [ ] NGINX ingress controller
- [ ] Route HTTP/GraphQL + WebSocket upgrade traffic
- [ ] TLS termination, rate limiting at the edge

### Epic 4.5 — GitOps CD (ArgoCD)
- [ ] ArgoCD installed, watching the infra repo
- [ ] App-of-apps pattern for all services
- [ ] Automated sync + rollback; environment promotion flow

### Epic 4.6 — CI (GitHub Actions)
- [ ] Build, test, lint, vet for every service (Go + Erlang)
- [ ] Proto codegen + drift check
- [ ] Build + push images to registry on merge
- [ ] Update image tags → ArgoCD picks them up

### Epic 4.7 — Metrics (Prometheus + Grafana)
- [ ] Instrument every service with Prometheus metrics
- [ ] RED/USE dashboards + chess-specific panels (games/sec, eval latency, queue depth)
- [ ] Alerting rules (worker saturation, Kafka lag, error rates)

### Epic 4.8 — Tracing (Jaeger + OpenTelemetry)
- [ ] OTel SDK in each service; propagate context across gRPC/Kafka/WS
- [ ] Export spans to Jaeger
- [ ] Trace a full request: gateway → game service → session mgr → engine worker

### Epic 4.9 — Load testing (autocannon + k6)
- [ ] autocannon suite hammering the GraphQL API
- [ ] k6 scenarios simulating concurrent live games over WebSockets
- [ ] Capture baselines; tune autoscaling; document results in README

### Epic 4.10 — Hardening & polish
- [ ] Graceful shutdown / draining across services
- [ ] Secrets management, network policies
- [ ] Runbook + architecture docs finalized
- [ ] Demo script + screenshots/GIFs for the resume/README

**Exit criteria:** the full architecture diagram is live in Kubernetes via
ArgoCD, observable in Grafana + Jaeger, and survives a k6 load test with
autoscaling — all reproducible from `terraform apply` + `git push`.

---

## Resume bullets (harvest as you go)
- Built a distributed chess platform (Go + Erlang/OTP) running Stockfish engine
  workers, orchestrated on Kubernetes with GitOps (ArgoCD) and Terraform-provisioned infra.
- Designed an actor-per-game session manager in Erlang/OTP with supervision-tree
  fault tolerance, handling live clocks and reconnects for concurrent games.
- Implemented an event-driven analysis pipeline over Kafka with horizontally
  autoscaling gRPC engine workers; results served via GraphQL + WebSockets.
- Instrumented end-to-end observability (Prometheus/Grafana metrics, Jaeger/OTel
  tracing) and load-tested to N concurrent games with autocannon + k6.
