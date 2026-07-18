-- +goose Up

-- ── Move outbox (transactional outbox pattern) ──────────────────────────────
-- A move is only useful downstream (live fanout, session-state rebuild) if the
-- event announcing it is never lost. Writing the move to `moves` and publishing
-- to Redis as two separate steps is a dual-write: a crash between them either
-- loses the event or (on retry) double-publishes.
--
-- Instead, AppendMove writes the move and one row here in the SAME transaction,
-- so the event is durable exactly when the move is. A relay then tails this
-- table and publishes to the per-game event stream, marking each row published.
-- At-least-once: consumers key on (game_id, ply) and tolerate a replay.
-- +goose StatementBegin
CREATE TABLE move_outbox (
    id           BIGSERIAL PRIMARY KEY,
    game_id      UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    ply          INTEGER NOT NULL,          -- half-move number, mirrors moves.ply
    uci          TEXT NOT NULL,
    fen_after    TEXT NOT NULL,
    status       TEXT NOT NULL,             -- game status after this move
    end_reason   TEXT NOT NULL DEFAULT '',  -- set only when the move ended the game
    ended        BOOLEAN NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,               -- NULL until the relay has published it
    UNIQUE (game_id, ply)                   -- one outbox row per move, like moves
);
-- +goose StatementEnd

-- The relay only ever scans rows it has not published yet. A partial index keeps
-- that poll O(backlog) instead of O(all-moves-ever), and stays tiny because rows
-- leave it as soon as they are published.
-- +goose StatementBegin
CREATE INDEX idx_move_outbox_unpublished ON move_outbox (id) WHERE published_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS move_outbox;
-- +goose StatementEnd
