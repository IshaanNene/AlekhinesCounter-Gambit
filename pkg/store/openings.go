package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/chess"
)

// OpeningMove is one continuation from a position, with its record.
type OpeningMove struct {
	UCI       string
	SAN       string
	WhiteWins int64
	BlackWins int64
	Draws     int64
	Total     int64
}

// PositionKey reduces a FEN to the fields that identify a position: placement,
// side to move, castling, and en passant. The move counters are dropped so a
// transposition — the same position reached by a different move order — maps to
// the same key, which is exactly what an explorer must collapse.
func PositionKey(fen string) string {
	fields := strings.Fields(fen)
	if len(fields) >= 4 {
		return strings.Join(fields[:4], " ")
	}
	return fen
}

// IngestGameOpening folds a finished game's moves into the opening statistics.
//
// Idempotent: the opening_ingested guard means a game is counted exactly once,
// even though Kafka may deliver its completion event more than once. The insert
// there is the lock — if the row already exists, the whole thing is a no-op.
func (s *Store) IngestGameOpening(ctx context.Context, gameID, result string, fenHistory, ucis []string) error {
	if len(ucis) == 0 || len(fenHistory) != len(ucis)+1 {
		return nil // nothing to fold, or malformed
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rolled back unless committed

	// Claim the game. ON CONFLICT DO NOTHING + RowsAffected is the idempotency
	// check: zero rows means another delivery already ingested it.
	tag, err := tx.Exec(ctx,
		`INSERT INTO opening_ingested (game_id) VALUES ($1) ON CONFLICT DO NOTHING`, gameID)
	if err != nil {
		return fmt.Errorf("claim game for ingest: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil // already ingested
	}

	wWin, bWin, draw := resultDeltas(result)

	// An explorer over the whole game would drown the opening in endgame noise, so
	// only the first N plies are folded — where opening theory actually lives.
	const openingPlies = 24
	limit := len(ucis)
	if limit > openingPlies {
		limit = openingPlies
	}

	for i := 0; i < limit; i++ {
		key := PositionKey(fenHistory[i])
		san := sanFor(fenHistory[i], ucis[i])
		if _, err := tx.Exec(ctx,
			`INSERT INTO opening_moves (position_key, uci, san, white_wins, black_wins, draws)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (position_key, uci) DO UPDATE SET
			     white_wins = opening_moves.white_wins + EXCLUDED.white_wins,
			     black_wins = opening_moves.black_wins + EXCLUDED.black_wins,
			     draws      = opening_moves.draws + EXCLUDED.draws`,
			key, ucis[i], san, wWin, bWin, draw); err != nil {
			return fmt.Errorf("upsert opening move: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit opening ingest: %w", err)
	}
	return nil
}

// OpeningExplorer returns the moves played from a position, most popular first.
func (s *Store) OpeningExplorer(ctx context.Context, fen string, limit int) ([]OpeningMove, error) {
	if limit <= 0 || limit > 50 {
		limit = 15
	}
	key := PositionKey(fen)
	rows, err := s.pool.Query(ctx,
		`SELECT uci, san, white_wins, black_wins, draws,
		        (white_wins + black_wins + draws) AS total
		   FROM opening_moves
		  WHERE position_key = $1
		  ORDER BY total DESC, uci ASC
		  LIMIT $2`, key, limit)
	if err != nil {
		return nil, fmt.Errorf("opening explorer: %w", err)
	}
	defer rows.Close()

	var out []OpeningMove
	for rows.Next() {
		var m OpeningMove
		if err := rows.Scan(&m.UCI, &m.SAN, &m.WhiteWins, &m.BlackWins, &m.Draws, &m.Total); err != nil {
			return nil, fmt.Errorf("scan opening move: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// resultDeltas maps a game result to which counter it increments.
func resultDeltas(result string) (white, black, draw int64) {
	switch result {
	case "WHITE_WON":
		return 1, 0, 0
	case "BLACK_WON":
		return 0, 1, 0
	default: // DRAW, or anything unexpected, counts as a draw for the aggregate
		return 0, 0, 1
	}
}

// sanFor renders a move as SAN in a position, falling back to UCI if the FEN or
// move will not parse — a display nicety must never fail an ingest.
func sanFor(fen, uci string) string {
	board, err := chess.ParseFEN(fen)
	if err != nil {
		return uci
	}
	m, err := chess.ParseUCIMove(uci)
	if err != nil {
		return uci
	}
	return board.SAN(m)
}
