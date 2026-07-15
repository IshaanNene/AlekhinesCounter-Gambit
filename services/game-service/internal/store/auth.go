package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/rating"
)

// Auth errors, mapped to gRPC codes by the server.
var (
	ErrUserExists     = errors.New("username or email already taken")
	ErrBadCredentials = errors.New("invalid credentials")
	ErrTokenInvalid   = errors.New("login token is invalid, expired, or already used")
	ErrNoEmail        = errors.New("account has no email address")
	ErrNotUpgradable  = errors.New("account is not a guest and cannot be upgraded")
)

// User is an account.
type User struct {
	ID           string
	Username     string
	Email        string // empty when unset
	PasswordHash string // empty for guest / passwordless accounts
	Elo          int
	IsGuest      bool
	GamesPlayed  int
	CreatedAt    time.Time
}

// userColumns is the shared projection; games_played is derived rather than
// denormalised so it can never drift from the games table.
const userColumns = `
	u.id, u.username, COALESCE(u.email, ''), COALESCE(u.password_hash, ''),
	u.elo, u.is_guest, u.created_at,
	(SELECT count(*) FROM games g
	  WHERE (g.white_id = u.id OR g.black_id = u.id) AND g.status <> 'IN_PROGRESS')`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash,
		&u.Elo, &u.IsGuest, &u.CreatedAt, &u.GamesPlayed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	return &u, nil
}

// GetUser loads an account by id.
func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users u WHERE u.id = $1`, id))
}

// FindUserByIdentifier looks up an account by username or email, case
// insensitively — players should not have to remember which they signed up with,
// or how they capitalised it.
func (s *Store) FindUserByIdentifier(ctx context.Context, identifier string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users u
		 WHERE lower(u.username) = lower($1) OR lower(u.email) = lower($1)
		 LIMIT 1`, identifier))
}

// CreateGuestUser mints an anonymous account.
func (s *Store) CreateGuestUser(ctx context.Context) (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	// Use the full UUID: UUIDv7's leading bytes are a millisecond timestamp, so a
	// short prefix collides for guests created in the same millisecond.
	username := "guest-" + id.String()
	_, err = s.pool.Exec(ctx,
		`INSERT INTO users (id, username, elo, is_guest) VALUES ($1, $2, $3, true)`,
		id.String(), username, rating.DefaultRating)
	if err != nil {
		return "", fmt.Errorf("insert user: %w", err)
	}
	return id.String(), nil
}

// RegisterParams describes a new password account.
type RegisterParams struct {
	Username     string
	PasswordHash string
	Email        string // optional
	// UpgradeUserID, when set, converts that guest in place instead of creating
	// a new row, so games and rating earned as a guest are preserved.
	UpgradeUserID string
}

// Register creates a password account, or upgrades a guest into one.
func (s *Store) Register(ctx context.Context, p RegisterParams) (*User, error) {
	email := nullIfEmpty(p.Email)

	if p.UpgradeUserID != "" {
		return s.upgradeGuest(ctx, p, email)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO users (id, username, email, password_hash, elo, is_guest)
		 VALUES ($1, $2, $3, $4, $5, false)`,
		id.String(), p.Username, email, p.PasswordHash, rating.DefaultRating)
	if isUniqueViolation(err) {
		return nil, ErrUserExists
	}
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}
	return s.GetUser(ctx, id.String())
}

// upgradeGuest converts an existing guest row into a full account, keeping its
// id so every game already pointing at it stays valid.
func (s *Store) upgradeGuest(ctx context.Context, p RegisterParams, email *string) (*User, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users
		    SET username = $1, email = $2, password_hash = $3, is_guest = false
		  WHERE id = $4 AND is_guest = true`,
		p.Username, email, p.PasswordHash, p.UpgradeUserID)
	if isUniqueViolation(err) {
		return nil, ErrUserExists
	}
	if err != nil {
		return nil, fmt.Errorf("upgrade guest: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either no such user, or it is already a real account.
		return nil, ErrNotUpgradable
	}
	return s.GetUser(ctx, p.UpgradeUserID)
}

// ── Passwordless sign-in tokens ─────────────────────────────────────────────

// loginTokenTTL bounds how long a one-time sign-in token stays usable. Short
// enough that a leaked link ages out quickly, long enough to survive a slow
// inbox.
const loginTokenTTL = 15 * time.Minute

// NewLoginToken issues a single-use token for the account with this email.
//
// Only the SHA-256 of the token is stored: a database leak must not hand out
// working sign-in links. The plaintext is returned once, to be delivered.
func (s *Store) NewLoginToken(ctx context.Context, email string) (token string, userID string, expiresAt time.Time, err error) {
	u, err := s.FindUserByIdentifier(ctx, email)
	if err != nil {
		return "", "", time.Time{}, err
	}
	if u.Email == "" {
		return "", "", time.Time{}, ErrNoEmail
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", time.Time{}, fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	expiresAt = time.Now().Add(loginTokenTTL)

	if _, err := s.pool.Exec(ctx,
		`INSERT INTO login_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hashToken(token), u.ID, expiresAt); err != nil {
		return "", "", time.Time{}, fmt.Errorf("insert login token: %w", err)
	}
	return token, u.ID, expiresAt, nil
}

// RedeemLoginToken consumes a token and returns its user.
//
// The UPDATE ... WHERE consumed_at IS NULL is the whole safety property: the
// database decides the winner, so two concurrent redemptions cannot both
// succeed and a token is single-use even under a race.
func (s *Store) RedeemLoginToken(ctx context.Context, token string) (*User, error) {
	var userID string
	err := s.pool.QueryRow(ctx,
		`UPDATE login_tokens
		    SET consumed_at = now()
		  WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > now()
		  RETURNING user_id`,
		hashToken(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTokenInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("redeem login token: %w", err)
	}
	return s.GetUser(ctx, userID)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func nullIfEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// isUniqueViolation reports whether err is a Postgres unique-constraint error
// (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// pgx surfaces this as *pgconn.PgError; matching on the code string avoids
	// importing pgconn just for one constant.
	return strings.Contains(err.Error(), "23505") ||
		strings.Contains(strings.ToLower(err.Error()), "duplicate key")
}
