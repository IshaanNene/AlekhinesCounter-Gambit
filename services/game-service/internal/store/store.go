// Package store persists games, moves, and users in PostgreSQL using pgx.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// Game is a stored game row (moves are loaded separately).
type Game struct {
	ID          string
	WhiteID     string
	BlackID     string // empty when VsEngine
	VsEngine    bool
	Status      string // IN_PROGRESS | WHITE_WON | BLACK_WON | DRAW
	EndReason   string // empty while in progress
	FEN         string
	EngineDepth int
	StartedAt   time.Time
	EndedAt     *time.Time
}

// Move is a stored half-move.
type Move struct {
	Ply      int
	UCI      string
	FENAfter string
}

// Store wraps a pgx connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// Connect opens a pool against dsn and verifies connectivity.
func Connect(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// CreateGuestUser inserts a throwaway user and returns its id. Useful for the
// vertical slice where games are created without an auth system.
func (s *Store) CreateGuestUser(ctx context.Context) (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	// Use the full UUID: UUIDv7's leading bytes are a millisecond timestamp, so a
	// short prefix collides for guests created in the same millisecond.
	username := "guest-" + id.String()
	_, err = s.pool.Exec(ctx,
		`INSERT INTO users (id, username) VALUES ($1, $2)`, id.String(), username)
	if err != nil {
		return "", fmt.Errorf("insert user: %w", err)
	}
	return id.String(), nil
}

// CreateGameParams describes a new game.
type CreateGameParams struct {
	WhiteID     string
	BlackID     string // empty => game against the engine
	EngineDepth int
	StartFEN    string
}

// CreateGame inserts a new game and returns it.
func (s *Store) CreateGame(ctx context.Context, p CreateGameParams) (*Game, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	var blackID *string
	vsEngine := p.BlackID == ""
	if !vsEngine {
		blackID = &p.BlackID
	}

	row := s.pool.QueryRow(ctx,
		`INSERT INTO games (id, white_id, black_id, status, fen, engine_depth)
		 VALUES ($1, $2, $3, 'IN_PROGRESS', $4, $5)
		 RETURNING started_at`,
		id.String(), p.WhiteID, blackID, p.StartFEN, p.EngineDepth)
	var startedAt time.Time
	if err := row.Scan(&startedAt); err != nil {
		return nil, fmt.Errorf("insert game: %w", err)
	}

	return &Game{
		ID:          id.String(),
		WhiteID:     p.WhiteID,
		BlackID:     p.BlackID,
		VsEngine:    vsEngine,
		Status:      "IN_PROGRESS",
		FEN:         p.StartFEN,
		EngineDepth: p.EngineDepth,
		StartedAt:   startedAt,
	}, nil
}

// GetGame loads a game and its moves ordered by ply.
func (s *Store) GetGame(ctx context.Context, id string) (*Game, []Move, error) {
	g := &Game{ID: id}
	var blackID *string
	var endReason *string
	err := s.pool.QueryRow(ctx,
		`SELECT white_id, black_id, status, end_reason, fen, engine_depth, started_at, ended_at
		 FROM games WHERE id = $1`, id).
		Scan(&g.WhiteID, &blackID, &g.Status, &endReason, &g.FEN, &g.EngineDepth, &g.StartedAt, &g.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("select game: %w", err)
	}
	g.VsEngine = blackID == nil
	if blackID != nil {
		g.BlackID = *blackID
	}
	if endReason != nil {
		g.EndReason = *endReason
	}

	moves, err := s.gameMoves(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return g, moves, nil
}

// gameMoves loads all moves for a game ordered by ply.
func (s *Store) gameMoves(ctx context.Context, gameID string) ([]Move, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT ply, uci, fen_after FROM moves WHERE game_id = $1 ORDER BY ply`, gameID)
	if err != nil {
		return nil, fmt.Errorf("select moves: %w", err)
	}
	defer rows.Close()

	var moves []Move
	for rows.Next() {
		var m Move
		if err := rows.Scan(&m.Ply, &m.UCI, &m.FENAfter); err != nil {
			return nil, fmt.Errorf("scan move: %w", err)
		}
		moves = append(moves, m)
	}
	return moves, rows.Err()
}

// AppendMove records a move and updates the game's position and status in a
// single transaction. When ended is true, end_reason and ended_at are set.
func (s *Store) AppendMove(ctx context.Context, gameID string, m Move, status, endReason string, ended bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rolled back if not committed

	if _, err := tx.Exec(ctx,
		`INSERT INTO moves (game_id, ply, uci, fen_after) VALUES ($1, $2, $3, $4)`,
		gameID, m.Ply, m.UCI, m.FENAfter); err != nil {
		return fmt.Errorf("insert move: %w", err)
	}

	if ended {
		if _, err := tx.Exec(ctx,
			`UPDATE games SET fen = $1, status = $2, end_reason = $3, ended_at = now() WHERE id = $4`,
			m.FENAfter, status, endReason, gameID); err != nil {
			return fmt.Errorf("update game (ended): %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx,
			`UPDATE games SET fen = $1, status = $2 WHERE id = $3`,
			m.FENAfter, status, gameID); err != nil {
			return fmt.Errorf("update game: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// MoveCount returns the number of moves recorded for a game.
func (s *Store) MoveCount(ctx context.Context, gameID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM moves WHERE game_id = $1`, gameID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count moves: %w", err)
	}
	return n, nil
}
