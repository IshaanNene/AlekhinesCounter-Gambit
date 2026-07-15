# TASKS — Alekhine's Counter-Gambit

Execution-level task breakdown for the [ROADMAP.md](ROADMAP.md). Every task is
**self-contained**: it states its own context, dependencies, files, steps, and
acceptance criteria so any agent (human or model) can pick it up cold and finish
it without reading the rest of the conversation.

> **How to use this doc**
> 1. Read the **Global Context** section once — it defines conventions every task assumes.
> 2. Pick the lowest-numbered unblocked task (check its `Depends on`).
> 3. Do the **Steps**, satisfy the **Acceptance criteria**, run the **Verify** commands.
> 4. Tick the checkbox, commit with the message in the task, move on.
> 5. If a task's reality differs from its spec, update the task text in this file in the same commit.

---

## Global Context (read once, applies to every task)

**What we're building:** a distributed chess platform. Users play chess against
Stockfish or each other; games are validated, persisted, streamed live, analyzed
async, and the whole thing runs on Kubernetes with full observability.

**Repository:** monorepo with a **single Go module** at the repo root
(base path `github.com/IshaanNene/AlekhinesCounter-Gambit`). Services live under
`services/` as packages of that one module — no per-service `go.mod`, no `go.work`.
This keeps Docker builds trivial (copy root, `go build ./services/x`). The Erlang
`session-manager` is the one exception (its own rebar3 build). *(Decision made during
T1.1; supersedes the earlier per-service-module idea.)*

**Directory layout (create as needed):**
```
/
├── ROADMAP.md
├── TASKS.md
├── go.mod                      # single root module for all Go code
├── Makefile                    # single entrypoint for all dev commands
├── docker-compose.yml          # local orchestration (Q1)
├── proto/                      # protobuf source of truth
│   ├── game/v1/game.proto
│   ├── engine/v1/engine.proto
│   └── gen/go/                 # generated Go stubs (committed)
├── services/
│   ├── game-service/           # Go — moves, validation, persistence
│   ├── engine-worker/          # Go — wraps Stockfish over UCI, gRPC server
│   ├── gateway/                # Go — GraphQL + WebSockets (Q2)
│   └── session-manager/        # Erlang/OTP — actor-per-game (Q2)
├── pkg/
│   └── chess/                  # shared Go chess logic library
├── infra/
│   ├── helm/                   # Helm charts (Q4)
│   ├── terraform/              # Terraform modules (Q4)
│   ├── k8s/                    # raw manifests / ArgoCD apps (Q4)
│   └── observability/          # Prometheus, Grafana, Jaeger config (Q4)
├── web/                        # static neumorphic client + NGINX (Q2)
├── load/                       # autocannon + k6 scripts (Q4)
├── migrations/                 # SQL migrations (goose format)
├── docs/
│   └── adr/                    # architecture decision records
└── .github/workflows/          # GitHub Actions (CI)
```

**Toolchain & versions (pin these):**
- Go `1.23+`
- Erlang/OTP `27+`, `rebar3` for builds
- Protobuf via `buf` (preferred) — `buf.build`, plugins `protoc-gen-go` + `protoc-gen-go-grpc`
- Postgres `16`, Redis `7`, Kafka `3.7` (KRaft mode, no ZooKeeper), MinIO latest
- Stockfish `16+` (installed in the engine-worker image)
- Docker + Docker Compose v2
- Migrations: `goose` (`github.com/pressly/goose`)

**Fixed conventions (do not deviate without an ADR):**
- **gRPC ports:** game-service `50051`, engine-worker `50052`, session-manager `50053`
- **HTTP ports:** gateway `8080` (GraphQL at `/graphql`, WS at `/ws`, health at `/healthz`, metrics at `/metrics`)
- **Infra ports (local host):** Postgres `5433` (host) → `5432` (container; 5433 avoids a common local Postgres on 5432), Redis `6379`, Kafka `9092`, MinIO `9000`/console `9001`. In-container/compose network, Postgres is reached as `postgres:5432`.
- **Proto packages:** `alekhine.game.v1`, `alekhine.engine.v1`, `alekhine.session.v1`, `alekhine.auth.v1`. Go package option `github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/<pkg>`.
- **Config:** 12-factor. Read from env vars, prefix `ACG_` (e.g. `ACG_POSTGRES_DSN`). Provide sane localhost defaults.
  Known vars: `ACG_POSTGRES_DSN`, `ACG_GAME_ADDR` (game-service listen), `ACG_ENGINE_ADDR`,
  `ACG_STOCKFISH_PATH`, `ACG_RUN_MIGRATIONS`, `ACG_SESSION_ADDR` (client → session-manager;
  empty disables live sessions), `ACG_SESSION_PORT` (session-manager's own gRPC listen port),
  `ACG_GATEWAY_ADDR` (gateway HTTP listen), `ACG_GAME_ADDR_CLIENT` (gateway → game-service),
  `ACG_GRAPHQL_PLAYGROUND`, `ACG_SESSION_SECRET` (**required** by the gateway; 32+ bytes,
  signs session JWTs), `ACG_COOKIE_SECURE` (set true behind HTTPS), `ACG_MAIL_ENABLED`
  (false returns passwordless tokens in-band for local dev instead of emailing them),
  `ACG_REDIS_ADDR` (empty disables the eval cache, rate limiting, and cross-replica
  fanout — the platform still works, on one replica).
- **Logging:** structured JSON via `log/slog`. Every service logs a startup line with its version + listen addr.
- **Errors:** wrap with `fmt.Errorf("...: %w", err)`. gRPC handlers return proper `status.Error` codes.
- **Chess notation:** positions are FEN strings; moves are UCI long algebraic (e.g. `e2e4`, `e7e8q`).
- **IDs:** UUIDv7 (time-ordered) for games/users.

**Definition of Done (every task):**
- [ ] Code compiles / service builds.
- [ ] `make lint` passes (gofmt, `go vet`, golangci-lint where configured).
- [ ] New logic has unit tests; `make test` is green.
- [ ] Acceptance criteria in the task are all met and manually verified via the Verify commands.
- [ ] Public functions/types have doc comments; task's files match the layout above.
- [ ] Committed with the task's commit message; ROADMAP checkbox ticked if it completes an epic.

**Commit message convention:** `<type>(<scope>): <summary>` e.g. `feat(engine-worker): drive stockfish over UCI`.
Types: `feat`, `fix`, `chore`, `test`, `docs`, `refactor`, `ci`, `infra`.

**Anti-context-loss rules for agents:**
- Never invent a port, path, or package name — use the ones above.
- If you need a value not defined here (e.g. a new env var), add it to this Global Context section in the same commit.
- Prefer editing existing files over creating parallel ones; check the layout first.
- Keep each service independently buildable and runnable.

---

# Q1 — Foundation & Vertical Slice  (full detail)

Goal: play a full game vs Stockfish through real services, persisted to Postgres,
all via `make up`. Tasks T1.x are ordered; respect `Depends on`.

---

### [x] T1.1 — Initialize repo, single Go module, Makefile
**Depends on:** none
**Files:** `go.mod`, `Makefile`, `.gitignore`, `README.md`, `.editorconfig`
**Context:** Bootstrap the monorepo so every later task has a build entrypoint.
**Steps:**
1. `git init` if not already a repo. Add `.gitignore` (Go, Node, Erlang `_build/`, `.env`, `bin/`, `.DS_Store`).
2. Create a single root `go.mod` (`go 1.23`) at the repo base path.
3. Create `Makefile` with targets: `tools`, `proto`, `build`, `test`, `lint`, `migrate`, `up`, `down`, `run-game`, `clean` + a `help` default.
4. `README.md`: description, stack, quickstart, layout.
5. `.editorconfig`: tabs for Go, 2-space for yaml/proto.
**Acceptance criteria:**
- `go build ./...` succeeds (no packages yet is fine).
- `make` with no args prints help listing all targets.
**Verify:** `make help && go build ./...`
**Commit:** `chore(repo): scaffold monorepo, root go module, makefile`

---

### [x] T1.2 — ADR-0001: architecture & tech decisions
**Depends on:** T1.1
**Files:** `docs/adr/0001-architecture.md`, `docs/adr/0000-adr-template.md`
**Context:** Record *why* Go + Erlang, gRPC internal / GraphQL external, event-driven analysis. Future tasks reference this.
**Steps:**
1. Add a short ADR template (Status / Context / Decision / Consequences).
2. Write ADR-0001 summarizing the architecture from ROADMAP.md, the tech→job map, and the Go-for-services / Erlang-for-sessions decision.
**Acceptance criteria:** ADR explains each major tech choice in ≤2 sentences and links ROADMAP.md.
**Verify:** file renders as valid markdown.
**Commit:** `docs(adr): record architecture decisions (ADR-0001)`

---

### [x] T1.3 — Protobuf tooling + `game.proto` + `engine.proto`
**Depends on:** T1.1
**Files:** `buf.yaml`, `buf.gen.yaml`, `proto/game/v1/game.proto`, `proto/engine/v1/engine.proto`, updated `Makefile` (`proto` target)
**Context:** Protobuf is the single source of truth for all internal RPC. Generate Go stubs into `proto/gen/go` and commit them.
**Steps:**
1. Add `buf.yaml` (v2) with lint + breaking rules; `buf.gen.yaml` wiring `protoc-gen-go` and `protoc-gen-go-grpc` → `proto/gen/go`.
2. `engine/v1/engine.proto`, package `alekhine.engine.v1`:
   - `service EngineService { rpc Analyze(AnalyzeRequest) returns (AnalyzeResponse); }`
   - `AnalyzeRequest { string fen = 1; uint32 depth = 2; uint32 movetime_ms = 3; }`
   - `AnalyzeResponse { string bestmove = 1; int32 score_cp = 2; bool mate = 3; int32 mate_in = 4; uint32 depth = 5; repeated string pv = 6; }`
3. `game/v1/game.proto`, package `alekhine.game.v1`:
   - `service GameService { rpc CreateGame(...); rpc SubmitMove(...); rpc GetGame(...); }`
   - Messages: `Game { string id; string fen; repeated string moves; Status status; ... }`, `Status` enum (IN_PROGRESS, WHITE_WON, BLACK_WON, DRAW), plus request/response messages.
4. Implement `make proto` → `buf generate`. Commit generated code.
**Acceptance criteria:**
- `make proto` regenerates cleanly and `git diff` is empty afterward (generated code committed).
- `buf lint` passes.
**Verify:** `make proto && git diff --exit-code proto/gen`
**Commit:** `feat(proto): define game and engine contracts + codegen`

---

### [x] T1.4 — Shared chess library: board, FEN, move gen
**Depends on:** T1.1
**Files:** `pkg/chess/` (`board.go`, `fen.go`, `movegen.go`, `move.go`, `*_test.go`)
**Context:** Correct, well-tested chess rules power validation everywhere. This library has **no external deps** and no I/O.
**Steps:**
1. Board representation (8x8 or bitboards — your call; document it). Piece + color types.
2. `ParseFEN(string) (*Board, error)` and `(*Board).FEN() string` round-trip.
3. Legal move generation including castling, en passant, promotion, check evasion.
4. `(*Board).ApplyMove(Move) error` (UCI move parsing) returning new state.
5. Terminal detection: checkmate, stalemate, insufficient material, 50-move, threefold.
**Acceptance criteria:**
- **Perft tests pass** for the standard start position to depth 4 (known node counts: 20, 400, 8902, 197281) and for at least 2 tricky positions (Kiwipete etc.).
- FEN round-trips for 10+ positions.
**Verify:** `cd pkg/chess && go test ./... -run Perft -v`
**Commit:** `feat(chess): board, FEN, legal move generation with perft tests`

---

### [x] T1.5 — Postgres schema + migrations
**Depends on:** T1.1
**Files:** `migrations/0001_init.sql` (goose up/down), `Makefile` `migrate` target
**Context:** Durable store for users, games, moves. Migrations run via goose.
**Steps:**
1. `users` (id uuid pk, username unique, elo int default 1200, created_at).
2. `games` (id uuid pk, white_id, black_id nullable, status text, fen text, result text, started_at, ended_at nullable). `black_id` null ⇒ vs engine.
3. `moves` (id bigserial, game_id fk, ply int, uci text, fen_after text, created_at; unique(game_id, ply)).
4. Indexes on `moves(game_id)`, `games(status)`.
5. `make migrate` runs goose against `ACG_POSTGRES_DSN`.
**Acceptance criteria:** `make migrate` applies and rolls back cleanly against a local Postgres.
**Verify:** `docker compose up -d postgres && make migrate && goose ... status`
**Commit:** `feat(db): initial schema and migrations for users, games, moves`

---

### [x] T1.6 — Engine worker: Stockfish over UCI + gRPC
**Depends on:** T1.3
**Files:** `services/engine-worker/` (`main.go`, `internal/uci/uci.go`, `internal/server/server.go`, `Dockerfile`), tests
**Context:** Stateless worker. Spawns Stockfish, speaks UCI, exposes `EngineService.Analyze` over gRPC on `:50052`. Any request routable to any replica.
**Steps:**
1. `internal/uci`: manage a Stockfish subprocess. Implement handshake (`uci`→`uciok`, `isready`→`readyok`), `position fen <fen>`, `go depth N` / `go movetime M`, parse `info ... score cp/mate ... pv ...` and `bestmove`.
2. Guard the subprocess with a mutex (one command at a time per process) or a small pool.
3. `internal/server`: implement `EngineService` gRPC, translate `AnalyzeRequest`→UCI→`AnalyzeResponse` (populate score_cp/mate/pv/depth).
4. `main.go`: read `ACG_STOCKFISH_PATH` (default `stockfish`), listen on `:50052`, structured logging, graceful shutdown, gRPC health service.
5. `Dockerfile`: multi-stage; final image installs Stockfish binary.
**Acceptance criteria:**
- gRPC `Analyze` on the start position at depth 12 returns a legal bestmove within ~1s.
- Handles `movetime_ms` and `depth` independently.
**Verify:** run worker, `grpcurl -plaintext -d '{"fen":"<start-fen>","depth":12}' localhost:50052 alekhine.engine.v1.EngineService/Analyze`
**Commit:** `feat(engine-worker): drive stockfish over UCI behind gRPC`

---

### [x] T1.7 — Game service: gRPC, validation, persistence, engine calls
**Depends on:** T1.4, T1.5, T1.6
**Files:** `services/game-service/` (`main.go`, `internal/server/`, `internal/store/` (pgx), `internal/engine/` (gRPC client), `Dockerfile`), tests
**Context:** Core orchestration. Validates moves with `pkg/chess`, persists to Postgres, and (for vs-engine games) calls the engine worker for the reply move. Listens `:50051`.
**Steps:**
1. `internal/store`: pgx-based repo — `CreateGame`, `GetGame`, `AppendMove`, `UpdateGameStatus`. Use transactions for move+status.
2. `internal/engine`: gRPC client to engine-worker (`ACG_ENGINE_ADDR`, default `localhost:50052`).
3. `internal/server` implements `GameService`:
   - `CreateGame`: insert game (optionally vs engine), return id + start FEN.
   - `SubmitMove`: load game → validate with `pkg/chess` → apply → persist move + new FEN → detect terminal → if vs-engine and game not over, call engine `Analyze`, apply + persist its move → return updated game.
   - `GetGame`: return game + move list.
4. `main.go`: wire DSN, engine addr, listen `:50051`, health service, graceful shutdown.
**Acceptance criteria:**
- Illegal move ⇒ gRPC `InvalidArgument`, nothing persisted.
- Legal move vs engine ⇒ both player and engine moves persisted, FENs consistent, terminal states set correctly.
**Verify:** `grpcurl` CreateGame → SubmitMove `e2e4` → response contains engine's reply; row counts in `moves` correct.
**Commit:** `feat(game-service): validate, persist, and orchestrate engine replies`

---

### [x] T1.8 — Local orchestration: docker-compose + play shim
**Depends on:** T1.6, T1.7
**Files:** `docker-compose.yml`, `services/engine-worker/Dockerfile`, `services/game-service/Dockerfile`, `cmd/play/main.go` (CLI shim), `Makefile` (`up`, `down`, `run-game`)
**Context:** One command brings up Postgres + engine-worker + game-service; a tiny CLI plays a full game so the slice is demoable without a frontend yet.
**Steps:**
1. `docker-compose.yml`: services `postgres` (with volume + healthcheck), `engine-worker`, `game-service` (depends_on healthy postgres + engine). Run migrations on startup (init container/entrypoint or `make migrate`).
2. `cmd/play`: connects to game-service gRPC, creates a vs-engine game, reads UCI moves from stdin, prints board/FEN + engine replies, announces result.
3. `make up`/`make down`/`make run-game`.
**Acceptance criteria:**
- `make up` → all healthy. `make run-game` → play a full game to a terminal result.
- Restarting `game-service` and calling `GetGame` returns the persisted game (survives restart).
**Verify:** `make up && make run-game` (play a scholar's mate), then `make down`.
**Commit:** `feat(compose): local stack + CLI to play a full game end-to-end`

---

### [x] T1.9 — Baseline CI (GitHub Actions)
**Depends on:** T1.3, T1.4, T1.7
**Files:** `.github/workflows/ci.yml`, `.golangci.yml`
**Context:** Every push builds, tests, lints, and checks proto drift so the slice stays green.
**Steps:**
1. Jobs: `lint` (golangci-lint), `test` (`go test ./...` across modules, with a Postgres service container for store tests), `proto` (`buf generate` + `git diff --exit-code`).
2. Cache Go build/module dirs.
**Acceptance criteria:** workflow passes on a clean checkout; proto-drift job fails if generated code is stale.
**Verify:** push a branch / run `act` locally if available; confirm green.
**Commit:** `ci: build, test, lint, and proto-drift checks`

**✅ Q1 exit:** `make up` → `make run-game` plays a full persisted game vs Stockfish; CI green.

---

# Q2 — Distributed Core & Real-time  (task specs)

Same field format. Detail is slightly coarser because exact shapes depend on Q1
outcomes — expand each into T-level subtasks when you start the epic.

---

### [x] T2.1 — Erlang/OTP session-manager skeleton
**Depends on:** T1.3
**Files:** `services/session-manager/` (rebar3 app: `src/session_manager_app.erl`, `_sup.erl`, `rebar.config`)
**Context:** OTP application with a top supervisor; foundation for actor-per-game.
**Steps:** rebar3 release skeleton; top supervisor; health endpoint; config for gRPC port `:50053`.
**Acceptance:** `rebar3 shell` boots the app; supervisor running.
**Commit:** `feat(session-manager): OTP app + supervision skeleton`

### [x] T2.2 — Actor-per-game `gen_server` + clocks
**Depends on:** T2.1
**Context:** One `gen_server` per live game holding players, spectators, turn, and both chess clocks (increment, flag-fall). A `simple_one_for_one` supervisor spawns them; registry maps game_id→pid.
**Acceptance:** spawn a game process, tick clocks, flag-fall ends the game; killing a process is isolated (supervisor restarts cleanly).
**Commit:** `feat(session-manager): actor-per-game with chess clocks`

### [x] T2.3 — Disconnect/reconnect + supervision policy
**Depends on:** T2.2
**Context:** Grace timer on disconnect; reconnect resumes; define restart strategy + state recovery expectations.
**Acceptance:** disconnect within grace → reconnect resumes same game; exceeding grace → game adjudicated.
**Commit:** `feat(session-manager): reconnect grace + restart strategy`

### [x] T2.4 — gRPC bridge game-service ↔ session-manager
**Depends on:** T2.2, T1.7
**Context:** Define proto for session ops (JoinGame, MoveMade, ClockUpdate). Game-service notifies session-manager of moves; session-manager owns live state + clocks.
**Acceptance:** a move in game-service reflects in the session process state.
**Commit:** `feat(session): grpc bridge between game-service and session-manager`

### [x] T2.5 — GraphQL gateway (schema, queries, mutations)
**Depends on:** T1.7
**Files:** `services/gateway/` (gqlgen setup, resolvers, gRPC clients)
**Context:** Public API on `:8080/graphql`. Queries (game, user, history), mutations (createGame, move, resign). Resolvers call internal gRPC.
**Acceptance:** create + play a game entirely via GraphQL.
**Commit:** `feat(gateway): graphql api over internal grpc`

### [x] T2.6 — GraphQL subscriptions + WebSocket transport
**Depends on:** T2.5
**Context:** `/ws` endpoint; GraphQL subscription for live game updates (moves, clocks). WS upgrade handling.
**Acceptance:** a subscriber receives move/clock events pushed in real time.
**Commit:** `feat(gateway): websocket subscriptions for live games`

### [x] T2.7 — Redis: pub/sub fanout, eval cache, rate limit, sessions
**Depends on:** T2.6
**Context:** Redis pub/sub so any gateway replica pushes to any client; FEN→eval cache read-through in game-service/engine path; token-bucket rate limiting; JWT/session store.
**Acceptance:** two gateway replicas both deliver updates to their clients; repeated position served from cache (observable via a cache-hit metric/log).
**Commit:** `feat(redis): fanout, eval cache, rate limiting, sessions`

### [ ] T2.8 — Matchmaking + auth
**Depends on:** T2.5, T2.7
**Context:** Rating-banded queue, pair→create game→hand to session-manager; bot fallback. JWT auth on gateway.
**Acceptance:** two queued users get paired into a live game; solo user gets a bot.
**Commit:** `feat(matchmaking): rating-banded pairing with bot fallback`

**✅ Q2 exit:** live human-vs-human across two browsers, spectators, reconnect, cached evals.

---

# Q3 — Event-driven Data & Analysis  (task specs)

### [ ] T3.1 — Kafka backbone (KRaft) + topics
**Depends on:** T1.8
**Context:** Add Kafka to compose; create topics `moves`, `game-events`, `analysis-requests`, `analysis-results`. Protobuf-serialized payloads. Define partitioning keys (game_id).
**Acceptance:** produce/consume a test message on each topic.
**Commit:** `feat(kafka): event backbone with core topics`

### [ ] T3.2 — Game-service produces events
**Depends on:** T3.1, T1.7
**Context:** Emit move + game-lifecycle events to Kafka (outbox pattern to avoid dual-write loss).
**Acceptance:** every persisted move produces exactly one `moves` event.
**Commit:** `feat(game-service): emit domain events via outbox`

### [ ] T3.3 — Engine workers consume analysis requests (pull queue)
**Depends on:** T3.1, T1.6
**Context:** Workers join a consumer group on `analysis-requests`; scale = add replicas. Emit `analysis-results`.
**Acceptance:** N workers share the load; results land on the results topic.
**Commit:** `feat(engine-worker): consume analysis-requests as a work queue`

### [ ] T3.4 — Full-game analysis pipeline
**Depends on:** T3.2, T3.3
**Context:** On game end, enqueue per-position analysis; compute accuracy/blunders/mistakes; store per-move eval; expose eval-graph via GraphQL.
**Acceptance:** a finished game yields a complete move-by-move eval graph.
**Commit:** `feat(analysis): async full-game analysis + eval graph`

### [ ] T3.5 — MinIO object storage
**Depends on:** T3.4
**Context:** Store PGN archives + analysis JSON (keyed by game id) + opening books; presigned download URLs via gateway; workers load opening book from MinIO.
**Acceptance:** download a game's PGN + analysis via presigned URL; engine uses book moves in the opening.
**Commit:** `feat(minio): pgn/analysis archival + opening books`

### [ ] T3.6 — Ratings, leaderboards, history, opening explorer
**Depends on:** T3.2
**Context:** Elo/Glicko update on completion; paginated history + leaderboards + opening explorer backed by stored games — all via GraphQL.
**Acceptance:** completing games updates ratings; explorer returns move stats from real games.
**Commit:** `feat(ratings): elo updates, leaderboards, opening explorer`

**✅ Q3 exit:** completed games flow through Kafka → analyzed by a worker pool → stored in Postgres + MinIO → shown as an eval graph.

---

# Q4 — Production Platform  (task specs — full architecture goes live)

### [ ] T4.1 — Production Dockerfiles + Kubernetes manifests
**Depends on:** all services exist
**Context:** Slim multi-stage images for every service; k8s Deployments/Services/ConfigMaps/Secrets; probes; resource requests/limits; HPAs (esp. engine workers); StatefulSets (or managed) for Postgres/Kafka/MinIO.
**Acceptance:** every service runs in a local cluster (kind/k3d) with passing probes.
**Commit:** `infra(k8s): production images and manifests for all services`

### [ ] T4.2 — Helm charts (per-service + umbrella)
**Depends on:** T4.1
**Context:** Chart per service + umbrella platform chart; values for local/staging/prod; templated config/secrets/image tags.
**Acceptance:** `helm install` brings up the whole platform from the umbrella chart.
**Commit:** `infra(helm): per-service charts + umbrella platform chart`

### [ ] T4.3 — Terraform (cluster + managed deps)
**Depends on:** T4.2
**Context:** Modules to provision a cluster (kind/k3d local module; EKS/GKE module for cloud), managed Postgres/Redis/object storage, networking, DNS, TLS, IAM; remote state + per-env workspaces.
**Acceptance:** `terraform apply` stands up an environment reproducibly.
**Commit:** `infra(terraform): provision cluster and managed dependencies`

### [ ] T4.4 — NGINX ingress + TLS + WS routing
**Depends on:** T4.1
**Context:** Ingress controller; route HTTP/GraphQL + WebSocket upgrades; TLS termination; edge rate limiting.
**Acceptance:** external URL serves GraphQL + live WS through the ingress with TLS.
**Commit:** `infra(nginx): ingress, tls, websocket routing`

### [ ] T4.5 — ArgoCD GitOps
**Depends on:** T4.2
**Context:** Install ArgoCD; app-of-apps watching the infra repo; automated sync + rollback; staging→prod promotion.
**Acceptance:** a git commit to the infra repo auto-syncs to the cluster.
**Commit:** `infra(argocd): gitops app-of-apps with auto-sync`

### [ ] T4.6 — Full CI/CD (GitHub Actions)
**Depends on:** T1.9, T4.5
**Context:** Extend CI to build/test/lint Go **and** Erlang; proto drift check; build+push images to a registry on merge; bump image tags for ArgoCD to pick up.
**Acceptance:** merge to main → images pushed → ArgoCD deploys the new version.
**Commit:** `ci: full build/push pipeline feeding argocd`

### [ ] T4.7 — Prometheus + Grafana
**Depends on:** T4.1
**Context:** Instrument every service (RED/USE + chess metrics: games/sec, eval latency, queue depth, cache hit rate, Kafka lag); Grafana dashboards; alert rules (worker saturation, lag, error rate).
**Acceptance:** dashboards populate under load; an alert fires when a worker is saturated.
**Commit:** `infra(observability): prometheus metrics + grafana dashboards`

### [ ] T4.8 — Jaeger + OpenTelemetry tracing
**Depends on:** T4.1
**Context:** OTel SDK in every service; propagate context across gRPC, Kafka, and WS; export to Jaeger.
**Acceptance:** a single trace shows gateway → game-service → session-manager → engine-worker for one move.
**Commit:** `infra(observability): otel tracing end-to-end via jaeger`

### [ ] T4.9 — Load testing (autocannon + k6)
**Depends on:** T4.4, T4.7
**Context:** `load/autocannon/` hammers the GraphQL API; `load/k6/` simulates concurrent live games over WebSockets; capture baselines, tune HPAs, document results.
**Acceptance:** k6 drives thousands of concurrent games; engine-worker HPA scales out; results table in README.
**Commit:** `test(load): autocannon graphql + k6 websocket game scenarios`

### [ ] T4.10 — Hardening, docs, demo
**Depends on:** T4.9
**Context:** Graceful shutdown/draining everywhere; secrets management + network policies; finalize runbook + architecture docs; record demo GIFs/screenshots for the README/resume.
**Acceptance:** rolling deploy drops zero in-flight games; docs + demo assets complete.
**Commit:** `docs: hardening, runbook, and demo assets`

**✅ Q4 exit:** the entire architecture diagram is live in Kubernetes via ArgoCD,
observable in Grafana + Jaeger, load-tested with autoscaling — reproducible from
`terraform apply` + `git push`.

---

## Progress tracker
- Q1: ☑ T1.1 ☑ T1.2 ☑ T1.3 ☑ T1.4 ☑ T1.5 ☑ T1.6 ☑ T1.7 ☑ T1.8 ☑ T1.9
- Q2: ☑ T2.1 ☑ T2.2 ☑ T2.3 ☑ T2.4 ☑ T2.5 ☑ T2.6 ☑ T2.7 ☐ T2.8
- Q3: ☐ T3.1 ☐ T3.2 ☐ T3.3 ☐ T3.4 ☐ T3.5 ☐ T3.6
- Q4: ☐ T4.1 ☐ T4.2 ☐ T4.3 ☐ T4.4 ☐ T4.5 ☐ T4.6 ☐ T4.7 ☐ T4.8 ☐ T4.9 ☐ T4.10
