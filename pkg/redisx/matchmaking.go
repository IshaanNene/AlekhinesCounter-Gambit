package redisx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Matchmaking pairs waiting players by rating.
//
// The queue is a sorted set scored by Elo, not a list. A list can only pop the
// longest waiter, which pairs a 900 with a 2400 the moment they queue together;
// scoring by rating lets us search outward from a player's own strength and pair
// them with the closest opponent instead.
//
// Fairness comes from widening the band with waiting time: you first look for a
// near-equal opponent, and settle for a wider gap the longer you have waited,
// so nobody queues forever in a thin rating band.
type Matchmaking struct {
	client *redis.Client
}

// NewMatchmaking builds a matchmaker. A nil client disables it.
func NewMatchmaking(client *redis.Client) *Matchmaking {
	return &Matchmaking{client: client}
}

// Enabled reports whether Redis is attached.
func (m *Matchmaking) Enabled() bool { return m != nil && m.client != nil }

// Band widening: start at ±100 Elo and open by 50 per second waited, capped at
// ±800 (beyond which the game is not really a contest).
const (
	initialBand  = 100.0
	bandPerSec   = 50.0
	maxBand      = 800.0
	queueTTL     = 10 * time.Minute
	matchChanFmt = "mm:matched:%s"
)

// queueKey namespaces by time control: a 3-minute blitz player must never be
// paired into someone's 30-minute classical game.
func queueKey(timeControl string) string { return "mm:queue:" + timeControl }

// waitKey stores when each player joined, so the band can widen with waiting.
func waitKey(timeControl string) string { return "mm:since:" + timeControl }

// tryPair atomically pairs with the nearest waiting opponent, or enqueues.
//
// Atomicity is the whole point: two players calling this at the same instant
// must not both take each other and end up in two different games, and neither
// may be left in the queue after being matched. Redis runs the script to
// completion with nothing interleaved, which a read-then-write in Go could not
// promise across replicas.
var tryPair = redis.NewScript(`
local queue = KEYS[1]
local since = KEYS[2]
local me    = ARGV[1]
local elo   = tonumber(ARGV[2])
local now   = tonumber(ARGV[3])
local initial_band = tonumber(ARGV[4])
local band_per_sec = tonumber(ARGV[5])
local max_band     = tonumber(ARGV[6])

-- Widen my own band by how long I have already waited.
local my_since = tonumber(redis.call('HGET', since, me))
local my_wait = 0
if my_since then my_wait = math.max(0, now - my_since) / 1000.0 end
local my_band = math.min(max_band, initial_band + my_wait * band_per_sec)

-- Nearest first: walk outward from my rating and take the first opponent whose
-- own band also reaches me. Requiring mutual acceptance keeps it symmetric —
-- a long-waiting player can accept a wide gap, a fresh one cannot be dragged
-- into it.
local candidates = redis.call('ZRANGEBYSCORE', queue,
  elo - my_band, elo + my_band, 'WITHSCORES')

local best, best_gap, best_since = nil, nil, nil
for i = 1, #candidates, 2 do
  local other = candidates[i]
  local other_elo = tonumber(candidates[i + 1])
  if other ~= me then
    local other_since = tonumber(redis.call('HGET', since, other))
    local other_wait = 0
    if other_since then other_wait = math.max(0, now - other_since) / 1000.0 end
    local other_band = math.min(max_band, initial_band + other_wait * band_per_sec)
    local gap = math.abs(other_elo - elo)
    if gap <= other_band and (best_gap == nil or gap < best_gap) then
      best, best_gap, best_since = other, gap, other_since
    end
  end
end

if best then
  redis.call('ZREM', queue, best, me)
  redis.call('HDEL', since, best, me)
  -- Return the opponent and how long they had been waiting (ms), so the caller
  -- can record the pairing latency.
  return {best, best_since or now}
end

-- No opponent: wait, remembering when we started so the band can widen.
redis.call('ZADD', queue, elo, me)
if not my_since then redis.call('HSET', since, me, now) end
return false
`)

// Enqueue looks for an opponent, joining the queue if none is available.
// Returns the opponent's id, true when paired, and how long the paired opponent
// had been waiting (zero when not paired).
func (m *Matchmaking) Enqueue(ctx context.Context, userID string, elo int, timeControl string) (string, bool, time.Duration, error) {
	if !m.Enabled() {
		return "", false, 0, nil
	}
	now := time.Now().UnixMilli()
	res, err := tryPair.Run(ctx, m.client,
		[]string{queueKey(timeControl), waitKey(timeControl)},
		userID, elo, now, initialBand, bandPerSec, maxBand,
	).Result()

	// Lua `false` arrives as a nil reply, which go-redis reports as redis.Nil —
	// an error value, not a nil result. That is the "no opponent, you are now
	// waiting" case, not a failure.
	if errors.Is(err, redis.Nil) {
		// Expire the queue so a crashed process cannot strand players in it.
		m.client.Expire(ctx, queueKey(timeControl), queueTTL)
		m.client.Expire(ctx, waitKey(timeControl), queueTTL)
		return "", false, 0, nil
	}
	if err != nil {
		return "", false, 0, fmt.Errorf("matchmaking enqueue: %w", err)
	}

	// Paired: the script returns {opponentID, opponentSinceMs}.
	tuple, ok := res.([]interface{})
	if !ok || len(tuple) < 2 {
		return "", false, 0, nil
	}
	opponent, _ := tuple[0].(string)
	if opponent == "" {
		return "", false, 0, nil
	}
	sinceMs, _ := tuple[1].(int64)
	wait := time.Duration(0)
	if sinceMs > 0 && now > sinceMs {
		wait = time.Duration(now-sinceMs) * time.Millisecond
	}
	return opponent, true, wait, nil
}

// Leave removes a player from the queue (cancelled, or disconnected).
func (m *Matchmaking) Leave(ctx context.Context, userID, timeControl string) error {
	if !m.Enabled() {
		return nil
	}
	pipe := m.client.Pipeline()
	pipe.ZRem(ctx, queueKey(timeControl), userID)
	pipe.HDel(ctx, waitKey(timeControl), userID)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("matchmaking leave: %w", err)
	}
	return nil
}

// QueueDepth reports how many players are waiting on a time control.
func (m *Matchmaking) QueueDepth(ctx context.Context, timeControl string) (int64, error) {
	if !m.Enabled() {
		return 0, nil
	}
	return m.client.ZCard(ctx, queueKey(timeControl)).Result()
}

// NotifyMatched tells a waiting player which game they were paired into.
//
// The player who arrives second creates the game, so the one already waiting has
// no response to learn about it on — pub/sub reaches them wherever their socket
// happens to be, on any gateway replica.
func (m *Matchmaking) NotifyMatched(ctx context.Context, userID, gameID string) error {
	if !m.Enabled() {
		return nil
	}
	return m.client.Publish(ctx, fmt.Sprintf(matchChanFmt, userID), gameID).Err()
}

// WatchMatches yields game ids this player is paired into, until ctx ends.
func (m *Matchmaking) WatchMatches(ctx context.Context, userID string) (<-chan string, error) {
	if !m.Enabled() {
		return nil, fmt.Errorf("matchmaking is not enabled")
	}
	sub := m.client.Subscribe(ctx, fmt.Sprintf(matchChanFmt, userID))
	if _, err := sub.Receive(ctx); err != nil {
		sub.Close()
		return nil, fmt.Errorf("watch matches: %w", err)
	}
	out := make(chan string, 1)
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
				select {
				case out <- msg.Payload:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}
