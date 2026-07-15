// Package pubsub fans game updates out to GraphQL subscribers.
//
// The Bus interface exists so the in-process implementation used here can be
// swapped for a Redis-backed one without touching the resolvers. That matters
// because Memory only reaches subscribers connected to *this* gateway replica;
// once the gateway is scaled out, a shared broker is required so a move handled
// by replica A still reaches a spectator holding a socket on replica B.
package pubsub

import (
	"context"
	"sync"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/graph/model"
)

// Bus delivers game snapshots to subscribers keyed by game id.
type Bus interface {
	// Publish delivers g to every current subscriber of gameID.
	Publish(ctx context.Context, gameID string, g *model.Game) error
	// Subscribe returns a channel of updates for gameID. The channel is closed
	// when ctx is cancelled (i.e. when the client disconnects).
	Subscribe(ctx context.Context, gameID string) (<-chan *model.Game, error)
	// Close releases any resources held by the bus.
	Close() error
}

// subscriberBuffer is how many updates may queue for a single slow client
// before we start dropping. Chess generates updates at human speed, so a small
// buffer is plenty; dropping beats blocking the publisher.
const subscriberBuffer = 8

// Memory is an in-process Bus. It is correct for a single gateway replica.
type Memory struct {
	mu     sync.RWMutex
	subs   map[string]map[int]chan *model.Game
	nextID int
}

// NewMemory builds an in-process bus.
func NewMemory() *Memory {
	return &Memory{subs: make(map[string]map[int]chan *model.Game)}
}

var _ Bus = (*Memory)(nil)

// Subscribe registers a listener for gameID and unregisters it when ctx ends.
func (m *Memory) Subscribe(ctx context.Context, gameID string) (<-chan *model.Game, error) {
	ch := make(chan *model.Game, subscriberBuffer)

	m.mu.Lock()
	id := m.nextID
	m.nextID++
	if m.subs[gameID] == nil {
		m.subs[gameID] = make(map[int]chan *model.Game)
	}
	m.subs[gameID][id] = ch
	m.mu.Unlock()

	// Tear down when the client goes away.
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		defer m.mu.Unlock()
		if subs, ok := m.subs[gameID]; ok {
			if c, ok := subs[id]; ok {
				delete(subs, id)
				close(c)
			}
			if len(subs) == 0 {
				delete(m.subs, gameID)
			}
		}
	}()

	return ch, nil
}

// Publish sends g to each subscriber, skipping any whose buffer is full rather
// than blocking the caller (a stalled client must not stall a move).
func (m *Memory) Publish(_ context.Context, gameID string, g *model.Game) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, ch := range m.subs[gameID] {
		select {
		case ch <- g:
		default: // slow consumer: drop this update
		}
	}
	return nil
}

// Close closes every open subscriber channel.
func (m *Memory) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for gameID, subs := range m.subs {
		for id, ch := range subs {
			close(ch)
			delete(subs, id)
		}
		delete(m.subs, gameID)
	}
	return nil
}

// SubscriberCount reports how many listeners a game currently has (used in tests).
func (m *Memory) SubscriberCount(gameID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.subs[gameID])
}
