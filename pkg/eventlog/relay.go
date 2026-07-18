package eventlog

import (
	"context"
	"log/slog"
	"time"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/store"
)

// OutboxSource is the slice of the store the relay needs: read pending move
// events and mark them published. Narrow by design, so the relay is trivial to
// fake in tests.
type OutboxSource interface {
	FetchUnpublished(ctx context.Context, limit int) ([]store.OutboxRow, error)
	MarkPublished(ctx context.Context, ids []int64) error
}

// Relay drains the transactional outbox into the per-game event streams. It is
// the one component that turns "durably recorded" into "visible downstream":
// AppendMove commits move+outbox atomically, and the relay publishes the outbox
// row to Redis, so the two-step (DB write, then broadcast) is crash-safe.
type Relay struct {
	src    OutboxSource
	stream *Stream
	log    *slog.Logger
	batch  int
	idle   time.Duration
}

// NewRelay builds a relay. batch bounds one publish cycle; idle is how long to
// wait after catching up before polling again (a small value keeps fanout
// latency low; a LISTEN/NOTIFY wake-up is a future optimisation).
func NewRelay(src OutboxSource, stream *Stream, log *slog.Logger) *Relay {
	return &Relay{src: src, stream: stream, log: log, batch: 200, idle: 200 * time.Millisecond}
}

// Run publishes until ctx is cancelled. It drains the backlog as fast as it can
// (looping immediately whenever it published a full-ish batch), then idles.
func (r *Relay) Run(ctx context.Context) {
	if !r.stream.Enabled() {
		if r.log != nil {
			r.log.Info("event stream disabled — outbox relay not started")
		}
		return
	}
	if r.log != nil {
		r.log.Info("outbox relay started")
	}
	for {
		if ctx.Err() != nil {
			return
		}
		published, err := r.drainOnce(ctx)
		if err != nil {
			if r.log != nil {
				r.log.Warn("outbox relay cycle failed; backing off", "error", err)
			}
			if sleep(ctx, r.idle) {
				return
			}
			continue
		}
		// A short backlog was cleared; only idle once we're caught up. A full
		// batch means there may be more waiting, so loop straight away.
		if published < r.batch {
			if sleep(ctx, r.idle) {
				return
			}
		}
	}
}

// drainOnce publishes one batch and returns how many rows it marked published.
//
// Rows are published in id order (which is per-game ply order). On the first
// publish failure it stops, marks the rows it did publish, and returns the error
// so the caller backs off; the unpublished remainder is retried next cycle. If a
// crash lands between Append and MarkPublished the batch is replayed — consumers
// dedupe on (game_id, ply), so at-least-once is safe.
func (r *Relay) drainOnce(ctx context.Context) (int, error) {
	rows, err := r.src.FetchUnpublished(ctx, r.batch)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	published := make([]int64, 0, len(rows))
	var appendErr error
	for _, row := range rows {
		if _, err := r.stream.Append(ctx, MoveEvent{
			GameID:    row.GameID,
			Ply:       row.Ply,
			UCI:       row.UCI,
			FENAfter:  row.FENAfter,
			Status:    row.Status,
			EndReason: row.EndReason,
			Ended:     row.Ended,
		}); err != nil {
			appendErr = err
			break
		}
		published = append(published, row.ID)
	}

	if err := r.src.MarkPublished(ctx, published); err != nil {
		return len(published), err
	}
	return len(published), appendErr
}

// sleep waits d or until ctx is done; it reports whether ctx ended.
func sleep(ctx context.Context, d time.Duration) (done bool) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}
