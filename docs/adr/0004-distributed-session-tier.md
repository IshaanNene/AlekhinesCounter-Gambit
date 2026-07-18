# ADR-0004: A no-SPOF distributed session tier

- **Status:** Accepted
- **Date:** 2026-07-18

## Context

The session-manager owns the *live* state of a game — whose turn it is and each
side's clock — as one supervised Erlang process per game. Through Q4 it ran as a
**single replica** (`values.yaml: replicas: 1 # ... deferred`): the last stateful
single point of failure in an otherwise horizontally-scalable platform. Killing
it dropped every live game's clock, and the honest chaos results said so.

The forces: this state is genuinely ephemeral (a ticking clock), so it cannot
just be reloaded from Postgres like a move list; but it also cannot be allowed to
die with one node. And the fix has to be *real* distributed-systems work — the
kind an interviewer probes — not a reverse-proxy trick.

## Decision

Make the session-manager a **clustered Erlang application** with three moving parts.

- **Cluster-wide registry — `syn`.** A game registered on any node is addressable
  from any node; `gen_server:call` is already location-transparent, so this is
  most of the battle. We chose `syn` over OTP's built-in `global` (whose
  fully-connected locking scales poorly under churn) and over `gproc` (its
  distributed mode is not first-class). Membership is derived from a `syn` group
  of session-manager nodes — not raw `nodes()` — so a stray connected node (a
  remote shell) is never handed games.
- **Ownership — rendezvous (HRW) hashing.** Each game's owner is the node scoring
  the highest `phash2({game_id, node})`. Every node computes the same owner with
  no coordination, and — unlike a modulo ring — a node joining or leaving moves
  only the games that actually hash to/from it. Create is routed to the owner.
- **Recovery — a Redis checkpoint, restored lazily.** Each game checkpoints its
  clock/turn to Redis on every move and on finish. When a node dies, the next
  call for one of its games finds no registration, so the routing layer asks the
  game's **new** owner to restore it from the checkpoint and forwards — the new
  owner reconstructs the clock and **deducts the wall time that elapsed during the
  outage** from the side on the move (so a game that flagged during the outage is
  settled correctly). Postgres stays the source of truth for *moves*; this is only
  the live overlay.

## Consequences

- **Easier:** the tier scales horizontally and survives node loss with zero lost
  games. Proven by an automated two-node test that halts the owning node
  mid-game and asserts the game re-homes with clocks intact — now run in CI.
- **Harder / deferred:** we chose **lazy** restore (on the next call) over eager
  (scan-and-restore on node-down). It is far simpler and equally correct, at the
  cost that a game with no activity during an outage is only re-armed when next
  touched — the flag-fall *outcome* is still correct (computed from the checkpoint
  timestamp on restore), only its *notification* is deferred to that moment. Good
  enough; eager restore is a future option.
- **Cost:** the checkpoint is a synchronous Redis write on the move path (chosen
  for durability, like the Kafka acks elsewhere); a new Erlang Redis dependency
  (`eredis`); and a shared distribution cookie to manage as a secret.
- **Why not rebuild from the event log instead of a separate checkpoint?** The
  event log (ADR-referenced `pkg/eventlog`) carries moves, not clock times; the
  session is authoritative for clocks. A dedicated clock checkpoint is smaller and
  lets restore be O(1) rather than a full replay.
