-- +goose Up

-- ── Opening explorer ────────────────────────────────────────────────────────
-- For every position reached in a finished game, which move was played next and
-- how the game turned out. Aggregated so the explorer answers "from here, what
-- do people play, and how does it score?" without scanning the games table.
--
-- Keyed by the position (placement + side + castling + en passant, the
-- repetition-relevant FEN fields) so transpositions — the same position via
-- different move orders — collapse into one row, which is the whole point of an
-- explorer.
-- +goose StatementBegin
CREATE TABLE opening_moves (
    position_key TEXT NOT NULL,   -- FEN's first four fields
    uci          TEXT NOT NULL,   -- the move played from that position
    san          TEXT NOT NULL,   -- human-readable form, stored so reads need no board
    white_wins   BIGINT NOT NULL DEFAULT 0,
    black_wins   BIGINT NOT NULL DEFAULT 0,
    draws        BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (position_key, uci)
);
-- +goose StatementEnd

-- The explorer reads all moves from a position, ordered by popularity, so the
-- primary key already covers lookups. A covering sum for ordering would be a
-- premature optimisation at this scale.

-- Which finished games have already been folded into the counts, so a Kafka
-- replay (at-least-once delivery) cannot double-count a game's moves.
-- +goose StatementBegin
CREATE TABLE opening_ingested (
    game_id     UUID PRIMARY KEY REFERENCES games(id) ON DELETE CASCADE,
    ingested_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS opening_moves;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS opening_ingested;
-- +goose StatementEnd
