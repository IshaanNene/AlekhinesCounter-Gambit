package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/rating"
)

// GameSummary is a history row: enough to list a game without loading its moves.
type GameSummary struct {
	ID        string
	WhiteID   string
	WhiteName string
	BlackID   string // empty for engine games
	BlackName string // "Stockfish" for engine games
	VsEngine  bool
	Rated     bool
	Status    string
	EndReason string
	MoveCount int
	EloDelta  *int // this user's rating change, nil when unrated
	StartedAt time.Time
	EndedAt   *time.Time
}

// ListGamesForUser returns a user's games, most recent first.
//
// The rating delta is projected relative to *this* user, so the caller does not
// have to work out which colour they were.
func (s *Store) ListGamesForUser(ctx context.Context, userID string, limit, offset int) ([]GameSummary, error) {
	// Bound the page size: an unbounded limit from the edge is a denial-of-service
	// waiting to happen.
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.pool.Query(ctx,
		`SELECT g.id, g.white_id, w.username, g.black_id, b.username,
		        g.vs_engine, g.rated, g.status, COALESCE(g.end_reason, ''),
		        (SELECT count(*) FROM moves m WHERE m.game_id = g.id),
		        CASE WHEN g.white_id = $1 THEN g.white_elo_delta ELSE g.black_elo_delta END,
		        g.started_at, g.ended_at
		   FROM games g
		   JOIN users w ON w.id = g.white_id
		   LEFT JOIN users b ON b.id = g.black_id
		  WHERE g.white_id = $1 OR g.black_id = $1
		  ORDER BY g.started_at DESC
		  LIMIT $2 OFFSET $3`,
		userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list games: %w", err)
	}
	defer rows.Close()

	out := make([]GameSummary, 0, limit)
	for rows.Next() {
		var g GameSummary
		var blackID, blackName *string
		if err := rows.Scan(&g.ID, &g.WhiteID, &g.WhiteName, &blackID, &blackName,
			&g.VsEngine, &g.Rated, &g.Status, &g.EndReason, &g.MoveCount,
			&g.EloDelta, &g.StartedAt, &g.EndedAt); err != nil {
			return nil, fmt.Errorf("scan game summary: %w", err)
		}
		if blackID != nil {
			g.BlackID = *blackID
		}
		switch {
		case blackName != nil:
			g.BlackName = *blackName
		case g.VsEngine:
			g.BlackName = "Stockfish"
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CountGamesForUser returns how many games a user has, for pagination.
func (s *Store) CountGamesForUser(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM games WHERE white_id = $1 OR black_id = $1`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count games: %w", err)
	}
	return n, nil
}

// LeaderboardEntry is one row of the rankings.
type LeaderboardEntry struct {
	Rank        int
	UserID      string
	Username    string
	Elo         int
	GamesPlayed int
}

// Leaderboard returns the highest-rated real accounts.
//
// Guests are excluded: they are throwaway identities, and letting them rank
// would make the board meaningless.
func (s *Store) Leaderboard(ctx context.Context, limit int) ([]LeaderboardEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx,
		`SELECT u.id, u.username, u.elo,
		        (SELECT count(*) FROM games g
		          WHERE (g.white_id = u.id OR g.black_id = u.id) AND g.status <> 'IN_PROGRESS')
		   FROM users u
		  WHERE u.is_guest = false
		  ORDER BY u.elo DESC, u.username ASC
		  LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("leaderboard: %w", err)
	}
	defer rows.Close()

	out := make([]LeaderboardEntry, 0, limit)
	for rows.Next() {
		var e LeaderboardEntry
		if err := rows.Scan(&e.UserID, &e.Username, &e.Elo, &e.GamesPlayed); err != nil {
			return nil, fmt.Errorf("scan leaderboard: %w", err)
		}
		e.Rank = len(out) + 1
		out = append(out, e)
	}
	return out, rows.Err()
}

// ApplyRatings updates both players' Elo for a finished rated game and records
// the before/after snapshot on the game row.
//
// Runs in one transaction and is idempotent: the guard `WHERE rated AND
// white_elo_delta IS NULL` means a retry (or two racing callers) cannot apply
// the same result twice. Returns false when there was nothing to do.
func (s *Store) ApplyRatings(ctx context.Context, gameID string, outcome rating.Outcome) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rolled back unless committed

	// Lock the game row and confirm it is rated and not yet scored.
	var whiteID, blackID string
	err = tx.QueryRow(ctx,
		`SELECT white_id, black_id FROM games
		  WHERE id = $1 AND rated = true AND status <> 'IN_PROGRESS'
		        AND white_elo_delta IS NULL AND black_id IS NOT NULL
		  FOR UPDATE`, gameID).Scan(&whiteID, &blackID)
	if err != nil {
		return false, nil //nolint:nilerr // not rated, already scored, or absent
	}

	whiteElo, whiteGames, err := playerRating(ctx, tx, whiteID)
	if err != nil {
		return false, err
	}
	blackElo, blackGames, err := playerRating(ctx, tx, blackID)
	if err != nil {
		return false, err
	}

	whiteDelta, blackDelta := rating.Update(whiteElo, blackElo, outcome, whiteGames, blackGames)

	if _, err := tx.Exec(ctx, `UPDATE users SET elo = elo + $1 WHERE id = $2`, whiteDelta, whiteID); err != nil {
		return false, fmt.Errorf("update white elo: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET elo = elo + $1 WHERE id = $2`, blackDelta, blackID); err != nil {
		return false, fmt.Errorf("update black elo: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE games
		    SET white_elo_before = $1, black_elo_before = $2,
		        white_elo_delta = $3, black_elo_delta = $4
		  WHERE id = $5`,
		whiteElo, blackElo, whiteDelta, blackDelta, gameID); err != nil {
		return false, fmt.Errorf("record elo snapshot: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit ratings: %w", err)
	}
	return true, nil
}

// playerRating reads a user's rating and completed-game count inside a tx.
func playerRating(ctx context.Context, tx pgx.Tx, userID string) (elo, games int, err error) {
	err = tx.QueryRow(ctx,
		`SELECT u.elo,
		        (SELECT count(*) FROM games g
		          WHERE (g.white_id = u.id OR g.black_id = u.id) AND g.status <> 'IN_PROGRESS')
		   FROM users u WHERE u.id = $1`, userID).Scan(&elo, &games)
	if err != nil {
		return 0, 0, fmt.Errorf("read rating for %s: %w", userID, err)
	}
	return elo, games, nil
}
