package eventlog

import (
	"context"
	"testing"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/store"
)

// fakeSource is an in-memory OutboxSource: it hands out unpublished rows in id
// order and remembers which ids were marked, so tests can assert the relay's
// fetch/publish/mark bookkeeping without a database.
type fakeSource struct {
	pending []store.OutboxRow
	marked  map[int64]bool
}

func (f *fakeSource) FetchUnpublished(_ context.Context, limit int) ([]store.OutboxRow, error) {
	var out []store.OutboxRow
	for _, r := range f.pending {
		if f.marked[r.ID] {
			continue
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeSource) MarkPublished(_ context.Context, ids []int64) error {
	if f.marked == nil {
		f.marked = make(map[int64]bool)
	}
	for _, id := range ids {
		f.marked[id] = true
	}
	return nil
}

// A disabled stream (nil client) makes Append a no-op, so this exercises the
// relay's own bookkeeping: it should publish every pending row exactly once and
// then have nothing left to do.
func TestRelayDrainMarksEverythingOnce(t *testing.T) {
	src := &fakeSource{pending: []store.OutboxRow{
		{ID: 1, GameID: "g", Ply: 1, UCI: "e2e4"},
		{ID: 2, GameID: "g", Ply: 2, UCI: "e7e5"},
		{ID: 3, GameID: "g", Ply: 3, UCI: "g1f3"},
	}}
	r := NewRelay(src, NewStream(nil, nil), nil)

	n, err := r.drainOnce(context.Background())
	if err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	if n != 3 {
		t.Errorf("published %d rows, want 3", n)
	}
	for _, id := range []int64{1, 2, 3} {
		if !src.marked[id] {
			t.Errorf("row %d was not marked published", id)
		}
	}

	// Caught up: a second cycle has nothing to do.
	n, err = r.drainOnce(context.Background())
	if err != nil {
		t.Fatalf("second drainOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("second cycle published %d rows, want 0", n)
	}
}
