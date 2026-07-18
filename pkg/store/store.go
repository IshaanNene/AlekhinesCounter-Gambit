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

// Timeouts that keep a database problem from becoming a stuck request. Chaos
// testing showed that with neither bound, an unreachable Postgres stalled every
// DB-backed read on the connection dial — the gRPC handler behind it hung, and
// the request's connection with it, instead of failing fast and freeing up.
const (
	// connectTimeout bounds establishing one connection, so a query against a
	// down Postgres fails in seconds instead of blocking indefinitely.
	connectTimeout = 3 * time.Second
	// statementTimeout is a server-side per-statement cap for the other failure
	// mode: a live but slow or lock-blocked query. Postgres itself aborts the
	// statement, so no single request can pin a pooled connection.
	statementTimeout = "5000" // milliseconds
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// ErrNotInProgress means the game exists but is already finished.
var ErrNotInProgress = errors.New("game is not in progress")

// ErrSeatUnavailable means the Black seat is taken, the game is not joinable, or
// the caller is already White.
var ErrSeatUnavailable = errors.New("this game cannot be joined")

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
	Rated       bool
	InitialMs   int64
	IncrementMs int64
	// AwaitingOpponent is true while a human game has no Black player yet.
	AwaitingOpponent bool
	StartedAt        time.Time
	EndedAt          *time.Time
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
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.ConnConfig.ConnectTimeout = connectTimeout
	// RuntimeParams is populated by ParseConfig; this is applied with SET on every
	// new connection, so it covers reconnects too.
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = statementTimeout

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
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

// CreateGameParams describes a new game.
type CreateGameParams struct {
	WhiteID string
	// BlackID may be empty: with VsEngine false that leaves an open seat another
	// player can join.
	BlackID     string
	VsEngine    bool
	EngineDepth int
	StartFEN    string
	// Rated games move both players' Elo on completion. Ignored for engine games.
	Rated bool
	// Time control for the live session, stored so a game whose seat is still
	// open can start its clock correctly whenever an opponent joins.
	InitialMs   int64
	IncrementMs int64
}

// CreateGame inserts a new game and returns it.
func (s *Store) CreateGame(ctx context.Context, p CreateGameParams) (*Game, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	var blackID *string
	vsEngine := p.VsEngine
	if !vsEngine && p.BlackID != "" {
		blackID = &p.BlackID
	}

	// Rated only when two real players meet: engine games must not move Elo.
	rated := !vsEngine && p.Rated

	row := s.pool.QueryRow(ctx,
		`INSERT INTO games (id, white_id, black_id, status, fen, engine_depth, vs_engine, rated,
		                    initial_ms, increment_ms)
		 VALUES ($1, $2, $3, 'IN_PROGRESS', $4, $5, $6, $7, $8, $9)
		 RETURNING started_at`,
		id.String(), p.WhiteID, blackID, p.StartFEN, p.EngineDepth, vsEngine, rated,
		p.InitialMs, p.IncrementMs)
	var startedAt time.Time
	if err := row.Scan(&startedAt); err != nil {
		return nil, fmt.Errorf("insert game: %w", err)
	}

	return &Game{
		ID:               id.String(),
		WhiteID:          p.WhiteID,
		BlackID:          p.BlackID,
		VsEngine:         vsEngine,
		Status:           "IN_PROGRESS",
		FEN:              p.StartFEN,
		EngineDepth:      p.EngineDepth,
		Rated:            rated,
		InitialMs:        p.InitialMs,
		IncrementMs:      p.IncrementMs,
		AwaitingOpponent: !vsEngine && p.BlackID == "",
		StartedAt:        startedAt,
	}, nil
}

// JoinGame claims the open Black seat.
//
// The guard clause is the whole concurrency story: `WHERE black_id IS NULL`
// means the database picks exactly one winner when two players race to join, and
// `white_id <> $2` stops someone playing themselves.
func (s *Store) JoinGame(ctx context.Context, gameID, playerID string) (*Game, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE games SET black_id = $1
		  WHERE id = $2 AND black_id IS NULL AND vs_engine = false
		        AND status = 'IN_PROGRESS' AND white_id <> $1`,
		playerID, gameID)
	if err != nil {
		return nil, fmt.Errorf("join game: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrSeatUnavailable
	}
	g, _, err := s.GetGame(ctx, gameID)
	return g, err
}

// GetGame loads a game and its moves ordered by ply.
func (s *Store) GetGame(ctx context.Context, id string) (*Game, []Move, error) {
	g := &Game{ID: id}
	var blackID *string
	var endReason *string
	err := s.pool.QueryRow(ctx,
		`SELECT white_id, black_id, status, end_reason, fen, engine_depth, vs_engine, rated,
		        initial_ms, increment_ms, started_at, ended_at
		 FROM games WHERE id = $1`, id).
		Scan(&g.WhiteID, &blackID, &g.Status, &endReason, &g.FEN, &g.EngineDepth,
			&g.VsEngine, &g.Rated, &g.InitialMs, &g.IncrementMs, &g.StartedAt, &g.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("select game: %w", err)
	}
	if blackID != nil {
		g.BlackID = *blackID
	}
	g.AwaitingOpponent = !g.VsEngine && blackID == nil && g.Status == "IN_PROGRESS"
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

	// Transactional outbox: the event announcing this move is written in the same
	// transaction as the move itself, so it is durable exactly when the move is —
	// never a move persisted without its event, nor an event without its move.
	// A relay (pkg/eventlog) publishes these to the per-game stream.
	if _, err := tx.Exec(ctx,
		`INSERT INTO move_outbox (game_id, ply, uci, fen_after, status, end_reason, ended)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		gameID, m.Ply, m.UCI, m.FENAfter, status, endReason, ended); err != nil {
		return fmt.Errorf("insert outbox: %w", err)
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

// EndGame closes a game that ended without a move being played (e.g. a
// resignation). It is a no-op on a game that is already finished, so a repeated
// or racing request cannot overwrite the original result.
func (s *Store) EndGame(ctx context.Context, gameID, status, endReason string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE games SET status = $1, end_reason = $2, ended_at = now()
		 WHERE id = $3 AND status = 'IN_PROGRESS'`,
		status, endReason, gameID)
	if err != nil {
		return fmt.Errorf("end game: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotInProgress
	}
	return nil
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
