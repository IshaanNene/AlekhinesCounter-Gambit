# ADR-0001: Overall architecture and technology choices

- **Status:** Accepted
- **Date:** 2026-07-14

## Context

We are building a distributed chess platform (see [ROADMAP.md](../../ROADMAP.md))
as a portfolio/resume project. The goals are twofold: (1) a genuinely working
system where a user can play, spectate, and analyze chess, and (2) demonstrable
breadth across the modern distributed-systems stack, with every technology doing
a *real* job rather than being bolted on for show.

Chess maps unusually well onto distributed-systems patterns: move validation is
CPU work, engine analysis is embarrassingly parallel, live games are stateful
long-lived sessions, and finished games are a natural event stream to process
asynchronously. That gives each technology an honest role.

## Decision

**Service boundaries.**
- **game-service (Go)** — authoritative move validation and persistence.
- **engine-worker (Go + Stockfish)** — stateless UCI wrapper; horizontally scalable.
- **session-manager (Erlang/OTP)** — one supervised actor per live game (clocks,
  players, spectators, reconnects). This is the showcase of the project.
- **gateway (Go)** — public GraphQL + WebSocket API; translates to internal gRPC.

**Internal vs external APIs.** Internal service-to-service calls use **gRPC +
Protocol Buffers** (typed, fast, codegen'd, good streaming). The public API is
**GraphQL over HTTP/WebSocket** (flexible client queries + subscriptions for live
boards). The two are bridged in the gateway.

**Language split.** **Go** for the services (excellent gRPC/protobuf tooling,
strong Kubernetes ecosystem, fast builds). **Erlang/OTP** for the session manager
because per-game actors with supervision trees, cheap processes, and built-in
fault tolerance are exactly what OTP was designed for — it is the most defensible
"right tool" choice in the project.

**State & data.** **PostgreSQL** for durable state (users, games, moves, ratings).
**Redis** for the position→eval cache, WebSocket pub/sub fanout, rate limiting, and
sessions. **Kafka** as the event backbone decoupling live play from the async
analysis pipeline. **MinIO** (S3-compatible) for PGN archives, analysis artifacts,
and opening books.

**Async analysis.** Finished games emit events to Kafka; a pool of engine workers
consumes analysis requests as a work queue and emits results — the parallel,
scalable heart of the system.

**Ops & delivery (Q4).** Everything containerized with **Docker**, orchestrated on
**Kubernetes**, packaged with **Helm**, provisioned with **Terraform**, fronted by
**NGINX** ingress, delivered via **ArgoCD** GitOps, built by **GitHub Actions**.
Observability via **Prometheus + Grafana** (metrics) and **Jaeger + OpenTelemetry**
(tracing). Load tested with **autocannon** (GraphQL) and **k6** (WebSocket games).

**Repository shape.** A single Go module monorepo (see TASKS.md Global Context);
Erlang service builds separately with rebar3.

**Build order.** Vertical slice first (Q1) so there is always something demoable,
then real-time/distributed core (Q2), event-driven data (Q3), and the full
production platform (Q4). Rationale: de-risk by always having a runnable system.

## Consequences

- **Easier:** each service scales and deploys independently; the async pipeline
  absorbs load spikes; the Erlang boundary isolates live-game state and failures.
- **Harder:** more moving parts and cross-service concerns (tracing, schema
  evolution, local orchestration) — mitigated by protobuf contracts, a single
  `make up`, and observability landing in Q4.
- **Deferred:** a polished web frontend (a CLI shim drives Q1); managed cloud
  services (local Docker Compose stands in until Q4 Terraform); authn hardening.
