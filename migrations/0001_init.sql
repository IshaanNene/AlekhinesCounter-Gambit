-- +goose Up
-- +goose StatementBegin
CREATE TABLE users (
    id         UUID PRIMARY KEY,
    username   TEXT NOT NULL UNIQUE,
    elo        INTEGER NOT NULL DEFAULT 1200,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE games (
    id         UUID PRIMARY KEY,
    white_id   UUID NOT NULL REFERENCES users(id),
    black_id   UUID REFERENCES users(id),           -- NULL => game against the engine
    status     TEXT NOT NULL DEFAULT 'IN_PROGRESS', -- IN_PROGRESS | WHITE_WON | BLACK_WON | DRAW
    end_reason TEXT,                                -- CHECKMATE | STALEMATE | ... | RESIGNATION
    fen        TEXT NOT NULL,                        -- current position
    engine_depth INTEGER NOT NULL DEFAULT 0,        -- search depth for engine replies (0 => default)
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at   TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE moves (
    id         BIGSERIAL PRIMARY KEY,
    game_id    UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    ply        INTEGER NOT NULL,   -- half-move number, starts at 1
    uci        TEXT NOT NULL,      -- move in UCI long algebraic notation
    fen_after  TEXT NOT NULL,      -- resulting position
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (game_id, ply)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_moves_game_id ON moves (game_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_games_status ON games (status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS moves;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS games;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
