-- +goose Up

-- ── Game reports ────────────────────────────────────────────────────────────
-- One row per analysed game. Separate from `games` because analysis is async
-- and optional: a game is complete and playable long before (or without) a
-- report, and a missing row is a meaningful state rather than a NULL soup on
-- the games table.
-- +goose StatementBegin
CREATE TABLE game_analysis (
    game_id     UUID PRIMARY KEY REFERENCES games(id) ON DELETE CASCADE,
    depth       INTEGER NOT NULL,

    -- The first position in the game never previously seen on this platform.
    -- NULL when every position was already known.
    novelty_fen TEXT,
    novelty_ply INTEGER,

    white_accuracy    REAL,
    white_acpl        REAL,
    white_match_rate  REAL,
    white_blunders    INTEGER NOT NULL DEFAULT 0,
    white_mistakes    INTEGER NOT NULL DEFAULT 0,
    white_inaccuracies INTEGER NOT NULL DEFAULT 0,

    black_accuracy    REAL,
    black_acpl        REAL,
    black_match_rate  REAL,
    black_blunders    INTEGER NOT NULL DEFAULT 0,
    black_mistakes    INTEGER NOT NULL DEFAULT 0,
    black_inaccuracies INTEGER NOT NULL DEFAULT 0,

    analyzed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- ── Per-move verdicts ───────────────────────────────────────────────────────
-- +goose StatementBegin
CREATE TABLE move_analysis (
    game_id        UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    ply            INTEGER NOT NULL,
    uci            TEXT NOT NULL,
    best_uci       TEXT NOT NULL,
    -- Centipawns from the side-to-move's perspective, mates collapsed onto the
    -- same scale.
    eval_before_cp INTEGER NOT NULL,
    eval_after_cp  INTEGER NOT NULL,
    centipawn_loss INTEGER NOT NULL,
    quality        TEXT NOT NULL,
    matched_engine BOOLEAN NOT NULL DEFAULT false,
    mate_before    BOOLEAN NOT NULL DEFAULT false,
    mate_after     BOOLEAN NOT NULL DEFAULT false,
    -- The primary key doubles as the idempotency guard: Kafka is at-least-once,
    -- so a replayed game must overwrite its old verdicts rather than duplicate
    -- them (see the ON CONFLICT in the writer).
    PRIMARY KEY (game_id, ply)
);
-- +goose StatementEnd

-- The eval graph is always read whole and in order.
-- +goose StatementBegin
CREATE INDEX idx_move_analysis_game ON move_analysis (game_id, ply);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS move_analysis;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS game_analysis;
-- +goose StatementEnd
