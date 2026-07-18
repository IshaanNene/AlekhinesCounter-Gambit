// Package hub fans a single game's move-event stream out to many spectators.
//
// The scaling idea is one reader per game, not one per viewer: however many
// thousands of spectators watch a game, exactly one goroutine tails its Redis
// stream and broadcasts each move to all of them from memory. A late joiner gets
// the moves so far from the in-memory history (no extra Redis read), then live
// updates — handed over atomically so it can never miss or double a move.
//
// A slow spectator is dropped rather than allowed to stall the broadcast; it
// reconnects with the id of the last move it saw and replays the gap from the
// same history. Redis is read once per game regardless of the crowd size.
package hub

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/eventlog"
)

// sendBuffer bounds a spectator's pending-delta queue. Chess moves are seconds
// apart, so this is generous; a viewer that cannot keep up with it is genuinely
// stalled and is dropped to reconnect.
const sendBuffer = 256

// readBlock bounds one blocking stream read, so the reader loop notices its hub
// was closed promptly rather than after the next move.
const readBlock = 5 * time.Second

// Delta is one move as sent to a spectator. It carries the resulting FEN (so a
// client can render immediately) and the stream id (so a reconnecting client can
// resume exactly where it left off).
type Delta struct {
	Type      string `json:"type"` // always "move"
	ID        string `json:"id"`
	Ply       int    `json:"ply"`
	UCI       string `json:"uci"`
	FEN       string `json:"fen"`
	Status    string `json:"status"`
	EndReason string `json:"endReason,omitempty"`
	Ended     bool   `json:"ended"`
}

func deltaFrom(e eventlog.Entry) Delta {
	return Delta{
		Type:      "move",
		ID:        e.ID,
		Ply:       e.Event.Ply,
		UCI:       e.Event.UCI,
		FEN:       e.Event.FENAfter,
		Status:    e.Event.Status,
		EndReason: e.Event.EndReason,
		Ended:     e.Event.Ended,
	}
}

// Metrics receives fanout lifecycle events; all methods must be safe for
// concurrent use. Use Nop when metrics are not wired.
type Metrics interface {
	HubOpened()
	HubClosed()
	SubAdded()
	SubRemoved()
	DeltaBroadcast()
	SubDropped()
}

// Nop is a Metrics that does nothing.
type Nop struct{}

func (Nop) HubOpened()      {}
func (Nop) HubClosed()      {}
func (Nop) SubAdded()       {}
func (Nop) SubRemoved()     {}
func (Nop) DeltaBroadcast() {}
func (Nop) SubDropped()     {}

// Sub is one spectator's view onto a hub: the backlog it should send first, then
// a stream of live deltas, plus a kick signal when it has fallen too far behind.
type Sub struct {
	// Initial is the history (already filtered to what the client is missing)
	// to send before live deltas. Read-only after Subscribe returns.
	Initial []Delta

	ch       chan Delta
	kick     chan struct{}
	kickOnce sync.Once
}

// Deltas is the stream of live moves after the initial backlog.
func (s *Sub) Deltas() <-chan Delta { return s.ch }

// Kicked is closed when the hub drops this subscriber for being too slow. The
// caller should close the connection; the client reconnects and replays.
func (s *Sub) Kicked() <-chan struct{} { return s.kick }

func (s *Sub) drop() { s.kickOnce.Do(func() { close(s.kick) }) }

// hub tails one game's stream and broadcasts to its subscribers.
type hub struct {
	gameID  string
	stream  *eventlog.Stream
	log     *slog.Logger
	metrics Metrics

	mu      sync.Mutex
	history []Delta
	subs    map[*Sub]struct{}
	cancel  context.CancelFunc
}

// attach registers a subscriber and snapshots the backlog it is missing, both
// under the same lock as broadcast — so every delta is either in the snapshot or
// delivered live, never lost between the two and never sent twice.
func (h *hub) attach(from string) *Sub {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &Sub{
		Initial: filterAfter(h.history, from),
		ch:      make(chan Delta, sendBuffer),
		kick:    make(chan struct{}),
	}
	h.subs[s] = struct{}{}
	h.metrics.SubAdded()
	return s
}

// detach removes a subscriber and reports whether the hub is now empty.
func (h *hub) detach(s *Sub) (empty bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		h.metrics.SubRemoved()
	}
	return len(h.subs) == 0
}

func (h *hub) isEmpty() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) == 0
}

// broadcast appends a delta to history and delivers it to every subscriber. A
// subscriber whose buffer is full is dropped (kicked) rather than blocking the
// broadcast for everyone else.
func (h *hub) broadcast(d Delta) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.history = append(h.history, d)
	h.metrics.DeltaBroadcast()
	for s := range h.subs {
		select {
		case s.ch <- d:
		default:
			delete(h.subs, s)
			s.drop()
			h.metrics.SubRemoved()
			h.metrics.SubDropped()
		}
	}
}

// loadHistory preloads the game's existing moves into memory so the first
// spectator's backlog is complete (and its "synced" marker truthful). It returns
// the id to start tailing from. Done once per hub, before it is published.
func (h *hub) loadHistory() (startID string) {
	entries, err := h.stream.Replay(context.Background(), h.gameID, "")
	if err != nil {
		h.log.Warn("history preload failed; will tail from start", "game_id", h.gameID, "error", err)
		return "0"
	}
	startID = "0"
	for _, e := range entries {
		h.history = append(h.history, deltaFrom(e))
		startID = e.ID
	}
	return startID
}

// run tails the game's stream from startID and broadcasts new moves until the
// hub is cancelled. History up to startID is already in memory (loadHistory).
func (h *hub) run(ctx context.Context, startID string) {
	// With no stream configured there is nothing to read and Read returns
	// immediately, so idle on the context instead of spinning.
	if !h.stream.Enabled() {
		<-ctx.Done()
		return
	}
	lastID := startID
	for {
		if ctx.Err() != nil {
			return
		}
		entries, err := h.stream.Read(ctx, h.gameID, lastID, readBlock)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			h.log.Warn("stream read failed; retrying", "game_id", h.gameID, "error", err)
			if sleep(ctx, time.Second) {
				return
			}
			continue
		}
		for _, e := range entries {
			h.broadcast(deltaFrom(e))
			lastID = e.ID
		}
	}
}

// filterAfter returns the deltas whose id sorts after `from`. Redis stream ids
// are monotonic and lexicographically ordered within a stream, so a string
// compare is the correct "newer than" test. An empty `from` returns everything.
func filterAfter(history []Delta, from string) []Delta {
	if from == "" {
		out := make([]Delta, len(history))
		copy(out, history)
		return out
	}
	out := make([]Delta, 0, len(history))
	for _, d := range history {
		if d.ID > from {
			out = append(out, d)
		}
	}
	return out
}

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
