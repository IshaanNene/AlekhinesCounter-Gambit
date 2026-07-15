package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/graph/model"
)

// Redis is a Bus backed by Redis pub/sub.
//
// This is what makes the gateway horizontally scalable. With the in-process bus,
// a move handled by replica A only reaches sockets held by replica A; a spectator
// connected to replica B would sit there watching a frozen board. Redis carries
// the update to every replica, so fanout no longer depends on which one you
// happened to land on.
//
// Delivery is at-most-once and only to *currently connected* subscribers, which
// is the right trade here: every payload is a complete game snapshot, so a
// dropped message is corrected by the next one, and a reconnecting client
// re-reads state on subscribe.
type Redis struct {
	client *redis.Client
	log    *slog.Logger
}

// NewRedis builds a Redis-backed bus.
func NewRedis(client *redis.Client, log *slog.Logger) *Redis {
	return &Redis{client: client, log: log}
}

var _ Bus = (*Redis)(nil)

// channel namespaces one topic per game, so a replica only receives traffic for
// games it actually has subscribers for.
func channel(gameID string) string { return "game:" + gameID }

// Publish broadcasts a game snapshot to every replica.
func (r *Redis) Publish(ctx context.Context, gameID string, g *model.Game) error {
	payload, err := json.Marshal(g)
	if err != nil {
		return fmt.Errorf("marshal game %q: %w", gameID, err)
	}
	if err := r.client.Publish(ctx, channel(gameID), payload).Err(); err != nil {
		return fmt.Errorf("publish game %q: %w", gameID, err)
	}
	return nil
}

// Subscribe returns a channel of updates for gameID, closed when ctx ends.
func (r *Redis) Subscribe(ctx context.Context, gameID string) (<-chan *model.Game, error) {
	sub := r.client.Subscribe(ctx, channel(gameID))
	// Confirm the subscription is live before returning: otherwise we could report
	// success and silently miss the very next publish.
	if _, err := sub.Receive(ctx); err != nil {
		sub.Close()
		return nil, fmt.Errorf("subscribe to game %q: %w", gameID, err)
	}

	out := make(chan *model.Game, subscriberBuffer)
	go func() {
		defer close(out)
		defer sub.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-sub.Channel():
				if !ok {
					return
				}
				var g model.Game
				if err := json.Unmarshal([]byte(msg.Payload), &g); err != nil {
					r.log.Warn("dropping malformed game update", "game_id", gameID, "error", err)
					continue
				}
				select {
				case out <- &g:
				case <-ctx.Done():
					return
				default:
					// Slow client: drop rather than stall this replica's whole
					// subscription goroutine. The next update carries full state.
					r.log.Debug("dropped update for slow subscriber", "game_id", gameID)
				}
			}
		}
	}()
	return out, nil
}

// Close releases the client's resources. The client itself is owned by main.
func (r *Redis) Close() error { return nil }
