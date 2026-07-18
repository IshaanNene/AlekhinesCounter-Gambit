package hub

import (
	"context"
	"log/slog"
	"sync"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/eventlog"
)

// Manager owns one hub per game, creating a hub on the first spectator and
// tearing it down (and its Redis reader) when the last one leaves.
//
// Locking: Manager.mu is always taken before a hub's mu, never the reverse, so
// there is no lock-order inversion. Subscribe holds Manager.mu across get-or-
// create and attach; Unsubscribe never holds both locks at once.
type Manager struct {
	stream  *eventlog.Stream
	log     *slog.Logger
	metrics Metrics

	mu   sync.Mutex
	hubs map[string]*hub
}

// NewManager builds a Manager over a move-event stream. A nil metrics is treated
// as Nop.
func NewManager(stream *eventlog.Stream, metrics Metrics, log *slog.Logger) *Manager {
	if metrics == nil {
		metrics = Nop{}
	}
	return &Manager{stream: stream, log: log, metrics: metrics, hubs: make(map[string]*hub)}
}

// Subscribe attaches a spectator to a game, starting the game's hub if it is the
// first. `from` is the last stream id the client already has (empty for a fresh
// viewer); the returned Sub's Initial holds the moves it is missing.
func (m *Manager) Subscribe(gameID, from string) *Sub {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.hubs[gameID]
	if !ok {
		ctx, cancel := context.WithCancel(context.Background())
		h = &hub{
			gameID:  gameID,
			stream:  m.stream,
			log:     m.log,
			metrics: m.metrics,
			subs:    make(map[*Sub]struct{}),
			cancel:  cancel,
		}
		// Preload history before publishing the hub, so the first viewer's backlog
		// is complete, then tail from where the preload stopped (no gap: the tail
		// reads strictly after the last preloaded id). One XRANGE per new game.
		startID := h.loadHistory()
		m.hubs[gameID] = h
		m.metrics.HubOpened()
		go h.run(ctx, startID)
	}
	// attach takes h.mu while we hold m.mu: order m -> h, consistent everywhere.
	return h.attach(from)
}

// Unsubscribe detaches a spectator and shuts the game's hub down once no one is
// left watching.
func (m *Manager) Unsubscribe(gameID string, s *Sub) {
	m.mu.Lock()
	h := m.hubs[gameID]
	m.mu.Unlock()
	if h == nil {
		return
	}
	if !h.detach(s) {
		return // others still watching
	}
	// Possibly the last viewer left. Re-check under m.mu (which also blocks any
	// concurrent Subscribe from attaching) before tearing the hub down.
	m.mu.Lock()
	if cur, ok := m.hubs[gameID]; ok && cur == h && h.isEmpty() {
		delete(m.hubs, gameID)
		h.cancel()
		m.metrics.HubClosed()
	}
	m.mu.Unlock()
}

// HubCount returns the number of live game hubs (for metrics/introspection).
func (m *Manager) HubCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.hubs)
}
