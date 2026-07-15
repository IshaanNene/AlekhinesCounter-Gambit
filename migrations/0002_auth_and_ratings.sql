-- +goose Up

-- ── Accounts ────────────────────────────────────────────────────────────────
-- Users may authenticate several ways, so credentials are all optional:
--   guest         → no password, no email
--   password      → password_hash set
--   passwordless  → email set, sign-in via a one-time token in login_tokens
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN password_hash TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users ADD COLUMN email TEXT;
-- +goose StatementEnd

-- Case-insensitive uniqueness: Bob@x.com and bob@x.com are the same person.
-- +goose StatementBegin
CREATE UNIQUE INDEX idx_users_email_lower ON users (lower(email)) WHERE email IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_users_username_lower ON users (lower(username));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users ADD COLUMN is_guest BOOLEAN NOT NULL DEFAULT true;
-- +goose StatementEnd

-- Existing rows were all guests (the only kind that existed before now).
-- +goose StatementBegin
UPDATE users SET is_guest = true WHERE password_hash IS NULL AND email IS NULL;
-- +goose StatementEnd

-- ── One-time sign-in tokens (passwordless) ──────────────────────────────────
-- Only the hash is stored: a database leak must not yield usable sign-in links.
-- +goose StatementBegin
CREATE TABLE login_tokens (
    token_hash  TEXT PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_login_tokens_user ON login_tokens (user_id);
-- +goose StatementEnd

-- ── Games: engine vs open seat ──────────────────────────────────────────────
-- black_id NULL used to mean "against the engine". With joinable games it can
-- also mean "waiting for an opponent", so the distinction becomes explicit.
-- +goose StatementBegin
ALTER TABLE games ADD COLUMN vs_engine BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE games SET vs_engine = true WHERE black_id IS NULL;
-- +goose StatementEnd

-- Time control, kept on the game: an open seat must still know its clock when
-- an opponent joins minutes later and the live session is finally created.
-- +goose StatementBegin
ALTER TABLE games
    ADD COLUMN initial_ms   BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN increment_ms BIGINT NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- ── Ratings ─────────────────────────────────────────────────────────────────
-- Snapshot each side's rating around the game so history is auditable and a
-- later rating-formula change cannot rewrite the past.
-- +goose StatementBegin
ALTER TABLE games
    ADD COLUMN white_elo_before INTEGER,
    ADD COLUMN black_elo_before INTEGER,
    ADD COLUMN white_elo_delta  INTEGER,
    ADD COLUMN black_elo_delta  INTEGER,
    ADD COLUMN rated BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- ── Indexes for history and leaderboards ────────────────────────────────────
-- +goose StatementBegin
CREATE INDEX idx_games_white_started ON games (white_id, started_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_games_black_started ON games (black_id, started_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_users_elo ON users (elo DESC) WHERE is_guest = false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS login_tokens;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_games_white_started;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_games_black_started;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_users_elo;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_users_email_lower;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_users_username_lower;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE games
    DROP COLUMN IF EXISTS white_elo_before,
    DROP COLUMN IF EXISTS black_elo_before,
    DROP COLUMN IF EXISTS white_elo_delta,
    DROP COLUMN IF EXISTS black_elo_delta,
    DROP COLUMN IF EXISTS rated,
    DROP COLUMN IF EXISTS vs_engine,
    DROP COLUMN IF EXISTS initial_ms,
    DROP COLUMN IF EXISTS increment_ms;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users
    DROP COLUMN IF EXISTS password_hash,
    DROP COLUMN IF EXISTS email,
    DROP COLUMN IF EXISTS is_guest;
-- +goose StatementEnd
