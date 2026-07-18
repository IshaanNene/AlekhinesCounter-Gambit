package store

import (
	"context"
	"fmt"
)

// OutboxRow is one durable move event awaiting publication to the event stream.
// It carries everything a downstream consumer needs to reconstruct the move
// without re-reading the games/moves tables.
type OutboxRow struct {
	ID        int64
	GameID    string
	Ply       int
	UCI       string
	FENAfter  string
	Status    string
	EndReason string
	Ended     bool
}

// FetchUnpublished returns the oldest unpublished outbox rows, in insertion
// order, up to limit. Ordering by id (a BIGSERIAL) preserves per-game ply order
// because AppendMove writes moves for one game strictly in sequence.
//
// The relay is the only reader, so no locking is needed: it fetches a batch,
// publishes it, then marks it published before fetching again.
func (s *Store) FetchUnpublished(ctx context.Context, limit int) ([]OutboxRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, game_id, ply, uci, fen_after, status, end_reason, ended
		   FROM move_outbox
		  WHERE published_at IS NULL
		  ORDER BY id
		  LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("fetch outbox: %w", err)
	}
	defer rows.Close()

	out := make([]OutboxRow, 0, limit)
	for rows.Next() {
		var r OutboxRow
		if err := rows.Scan(&r.ID, &r.GameID, &r.Ply, &r.UCI, &r.FENAfter,
			&r.Status, &r.EndReason, &r.Ended); err != nil {
			return nil, fmt.Errorf("scan outbox row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkPublished stamps published_at on the given rows so the relay never
// republishes them. Publication is at-least-once: a crash between publishing and
// this call replays the batch, which downstream consumers tolerate by keying on
// (game_id, ply).
func (s *Store) MarkPublished(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE move_outbox SET published_at = now() WHERE id = ANY($1)`, ids); err != nil {
		return fmt.Errorf("mark outbox published: %w", err)
	}
	return nil
}
