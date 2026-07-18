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
- [x] Game service produces move/game events to Kafka
- [x] Workers consume `analysis-requested` (pull-based work queue)
- [x] Consumer groups for horizontal scaling of workers (`analysis-workers` group)
- [x] Protobuf serialization on the wire (proto.Marshal in `kafkax`; the proto
      schemas are the contract — a standalone registry is the enterprise variant)

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
- [x] Store full analysis artifacts (JSON) keyed by game id (analysis-worker
      archives the report as JSON to the `analysis` bucket, best-effort)
- [x] Opening book files + serve to engine workers (`pkg/openingbook`; the book
      lives in the `books` bucket, is loaded by every engine replica, and is
      consulted for play via the `use_book` flag — never for analysis)

### Epic 3.4 — Ratings & history
- [x] Elo rating updates on game completion (tiered K-factor)
- [x] Leaderboards + user game history (paginated GraphQL)
- [x] Opening explorer backed by stored games (transposition-collapsing, SAN, scored)

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
- [x] Production Dockerfiles for every service (all six multi-stage → slim runtimes)
- [x] Kubernetes manifests: Deployments, Services, Secrets (templated by the chart)
- [x] HorizontalPodAutoscalers (engine-worker 3→12 on CPU; verified scaling 1→5 live)
- [x] Liveness/readiness probes, resource requests/limits (app + bundled infra)
- [x] StatefulSets + PVCs for Postgres and MinIO; Kafka is a deliberately disposable
      Deployment (single-node KRaft can't recover its log on restart — see the k8s fix)

### Epic 4.2 — Helm
- [~] One umbrella chart templates every service from values (deliberately not a
      subchart-per-service — overkill at this scale)
- [~] Values files: base + values-kind (local). staging/prod overlays not built
      (no cloud target yet)
- [x] Templated config, secrets, image tags (secret.yaml, global.imageRegistry)

### Epic 4.3 — Terraform
- [x] Provision cluster — kind, via tehcyx/kind (with ingress + NodePort host maps).
      EKS/GKE is the documented swap point in main.tf; module not built (no cloud target)
- [~] In-cluster Postgres/Redis/MinIO — provisioned by the chart, not TF (TF owns the
      cluster; a cloud build would add RDS/ElastiCache/S3 modules)
- [ ] Networking, DNS, IAM, TLS certs — cloud-only, not applicable to local kind
- [ ] Remote state + workspaces per environment — local state (single environment)

### Epic 4.4 — Ingress & routing (NGINX)
- [x] ingress-nginx controller (pinned, pinned to the ingress-ready node on kind)
- [x] Route HTTP/GraphQL + WebSocket upgrade traffic (path-routed; long ws timeouts)
- [~] Rate limiting is enforced at the gateway (token bucket, Redis). Edge TLS not
      terminated locally (plain HTTP on localhost); a cloud build adds cert-manager

### Epic 4.5 — GitOps CD (ArgoCD)
- [x] ArgoCD installed, watching this repo (renders the Helm chart from main)
- [~] Single Application for the platform (the chart already fans out to every
      service; an app-of-apps buys nothing until there are multiple charts)
- [~] Automated sync + self-heal + prune (verified reverting drift in ~5s); Argo
      keeps rollback history. Multi-env promotion not built (single environment)

### Epic 4.6 — CI (GitHub Actions)
- [x] Build, test, lint, vet (Go via lint/test jobs; the Erlang session-manager
      is compiled in its image build)
- [x] Proto codegen + drift check (buf lint + regenerate-and-diff)
- [x] Build + push images to registry on merge (all six services → GHCR, SHA +
      latest tags, gated on green lint/proto/test)
- [~] Update image tags → ArgoCD picks them up — images are in GHCR and ArgoCD
      watches git; the last hop (Argo Image Updater or a CI tag write-back) is the
      cloud flow, skipped locally because kind runs host-loaded images (pull-never)

### Epic 4.7 — Metrics (Prometheus + Grafana)
- [x] Prometheus metrics on every Go service — gateway, game-service, engine-worker,
      analysis-worker (RED via a shared interceptor + chess-specific counters). The
      Erlang session-manager is the one exception (no Prometheus client wired)
- [x] RED + chess dashboard auto-provisioned into Grafana (request rate, error ratio,
      p95 latency; games active, eval-cache hit rate, matchmaking wait, moves/sec,
      engine analyses/sec, results)
- [x] Alerting rules (RPC error rate > 5%; analyses stalled while games finish)

### Epic 4.8 — Tracing (Jaeger + OpenTelemetry)
- [x] OTel SDK in every Go service; context propagated across gRPC via otelgrpc
      (client + server handlers). Kafka-header and WS propagation not wired
- [x] Export spans to Jaeger (OTLP)
- [x] Trace a full request across gateway → game-service → engine-worker (all now
      otelgrpc-instrumented; the Erlang session-manager is not in the span tree)

### Epic 4.9 — Load & chaos testing (autocannon + k6)
- [x] autocannon suite hammering the GraphQL API (~18.8k req/s reads; limiter shields a 30k flood)
- [x] k6 scenarios: full game flow (move p95 15ms) + WebSocket spectators (150 VUs, 0 failures)
- [x] Baselines documented in load/README.md
- [x] Chaos suite (load/chaos): scale-out, pod-kill recovery, zero-downtime rollout,
      Redis/Postgres degradation, HPA — found and fixed two real dependency-timeout
      bugs (see load/chaos/RESULTS.md)

### Epic 4.10 — Hardening & polish
- [x] Graceful shutdown across services (Go: signal context + gRPC GracefulStop /
      HTTP Shutdown; Erlang: OTP supervision tree)
- [~] Secrets via a Helm Secret + envFromSecret; network policies not added (kind's
      default CNI does not enforce them — a cloud CNI like Cilium would)
- [~] Architecture + decisions in docs/adr (0001 architecture, 0002 Redis, 0003
      fair-play) and the README; a single consolidated runbook not written
- [~] Screenshots captured throughout (light/dark UI, Grafana dashboard, chaos runs);
      no polished GIF reel yet

**Exit criteria:** the full architecture diagram is live in Kubernetes via
ArgoCD, observable in Grafana + Jaeger, and survives a k6 load test with
autoscaling — all reproducible from `terraform apply` + `git push`.

---

## Q5 — Horizontal Scale & No Single Point of Failure

Close the two remaining scale gaps the earlier quarters left honest about: the
single stateful session replica, and full-snapshot fanout. Both build on one new
primitive.

### Epic 5.1 — Durable per-move event log
- [x] Transactional outbox: `AppendMove` writes the move + an outbox row in one
      Postgres tx, so an event is durable exactly when its move is (no dual-write)
- [x] A relay drains the outbox to a Redis Stream per game (`pkg/eventlog`),
      at-least-once, ply-ordered; replay + live tail APIs
- [x] Verified against real Redis + Postgres (append→replay, outbox roundtrip)

### Epic 5.2 — No-SPOF distributed session tier (★ Erlang)
- [x] `syn`-backed cluster-wide game registry (location-transparent addressing)
- [x] Consistent **rendezvous** hashing for game ownership; owner-routed create
- [x] Redis checkpoint of clock/turn; a dead node's games **re-home onto a
      survivor**, deducting the outage from the side on the move
- [x] Proven by a two-node `peer` test (halt the owner, game survives, clocks intact)
- [x] Deploy: env-driven node identity, headless-Service DNS discovery, 3 replicas
      + HPA; kind runs 2 for the handoff demo
- [~] Cluster-level `chaos.sh session-handoff` scenario written; run on a real
      cluster to record survivor counts (mechanism proven by the 2-node test)

### Epic 5.3 — Spectator fanout tier
- [x] `services/fanout`: one Redis reader per game fans out to many WebSocket
      spectators; in-memory backlog handed over atomically (never miss/dup a move)
- [x] Delta protocol + reconnect-replay (`from=<id>`) + slow-consumer backpressure
- [x] Browser watch page (`web/watch.html`) + NGINX `/spectate` route; Prometheus
      metrics for the Grafana wall
- [x] k6 load test (`load/k6/fanout.js`): 500 spectators/game caught up in ~2 ms,
      0 drops (local floor; fans out further per replica)

**Exit criteria:** killing any session-manager node under live games loses zero
games (clocks intact), and a single hot game serves a large spectator crowd from
one reader per replica — both reproducible from the tests and `chaos.sh`.

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
- Eliminated the platform's last single point of failure by clustering the stateful
  Erlang session tier (syn + rendezvous-hash ownership); a killed node's live games
  re-home onto a survivor from a Redis checkpoint with clocks intact — verified by a
  two-node node-kill test.
- Built a dedicated WebSocket fanout tier (one Redis-Streams reader per game, delta
  protocol, reconnect-replay, backpressure) that serves a large spectator crowd on a
  single game; ~2 ms catch-up, zero drops under load.
