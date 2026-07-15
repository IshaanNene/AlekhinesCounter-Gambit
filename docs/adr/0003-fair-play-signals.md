# ADR-0003: Fair-play signals, and their limits

- **Status:** Accepted
- **Date:** 2026-07-15

## Context

Cheating is the defining problem of online chess: an engine is one alt-tab away,
and a platform that ignores it is not really a chess platform. The Q3 analysis
pipeline already evaluates every position of every finished game, so the raw
material for detection — what the engine would have played, and what the player
actually played — is a by-product of work already being done.

The temptation is to turn that into a verdict. We deliberately do not.

## Decision

**Compute signals, not verdicts.** For each finished game we record two published
indicators per player:

- **Engine match rate** — the fraction of moves matching the engine's first choice.
- **Average centipawn loss** — the mean cost of the player's moves.

These are aggregated per player over a rolling window and turned into a 0–1
*suspicion* heuristic. Crossing a conservative threshold sets a **flag for human
review**, with written reasons attached. Nothing is ever actioned automatically.

**Guardrails, encoded in the code and its tests:**

- Games under 10 moves are ignored entirely — a four-move book opening is 100%
  "engine match" and means nothing.
- Fewer than 5 games never flags: one brilliant game is not a pattern.
- Both signals must agree. A high match rate alone is unremarkable in a forced
  endgame; a low centipawn loss alone is unremarkable in a quiet draw.
- A flag carries human-readable reasons, so a reviewer sees the argument rather
  than a number.

**Storage: RedisTimeSeries, not Prometheus.** These are per-player series — one
per account, potentially millions. That is precisely the high-cardinality shape
Prometheus warns against; a `player_id` label would multiply every series by the
user base. Prometheus is for service-level signals an operator reads.
RedisTimeSeries is for per-entity domain data the application reads. They do not
overlap, and using Prometheus here would be a category error.

## Consequences

- **False positives are the cost we optimise against.** A wrongly accused player
  leaves and does not come back; a missed cheat costs one game. The thresholds
  are deliberately conservative in that direction, and the tests assert that a
  clean club player (52% match, 42 centipawn loss) is never flagged.
- **This is not production-grade.** Real detection models strength against rating
  and time control, weights positions by difficulty, and examines move timing.
  What is here is a defensible first pass, and honest about being one.
- **It is a signal for review, not evidence.** Nothing in this system should ever
  be quoted as proof that someone cheated.
