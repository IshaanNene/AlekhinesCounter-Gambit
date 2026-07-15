package redisx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Leaderboard mirrors player ratings into a sorted set.
//
// Postgres can already do `ORDER BY elo DESC LIMIT 20` off an index, so the top
// page is not the reason this exists. The reason is *rank*: answering "where do
// I stand?" in SQL means counting every player above you, which is a scan that
// grows with the player base. `ZREVRANK` answers it in O(log N), so a player's
// rank can sit on their profile without costing a table scan per page view.
//
// Postgres stays authoritative. This is a derived index that can be rebuilt from
// it at any time, so a Redis flush costs a rebuild, never a rating.
type Leaderboard struct {
	client *redis.Client
}

// NewLeaderboard builds a leaderboard view. A nil client disables it.
func NewLeaderboard(client *redis.Client) *Leaderboard {
	return &Leaderboard{client: client}
}

// Enabled reports whether Redis is attached.
func (l *Leaderboard) Enabled() bool { return l != nil && l.client != nil }

const (
	// ratingsKey scores every rated account by Elo.
	ratingsKey = "lb:elo"
	// namesKey maps id → username so a leaderboard page needs no database round
	// trip per entry.
	namesKey = "lb:names"
	// activeKey scores players by last-seen, for "most active" and presence.
	activeKey = "presence:active"
)

// Entry is one leaderboard row.
type Entry struct {
	Rank     int64
	UserID   string
	Username string
	Elo      int
}

// Upsert records a player's rating. Guests are excluded by the caller.
func (l *Leaderboard) Upsert(ctx context.Context, userID, username string, elo int) error {
	if !l.Enabled() {
		return nil
	}
	pipe := l.client.Pipeline()
	pipe.ZAdd(ctx, ratingsKey, redis.Z{Score: float64(elo), Member: userID})
	pipe.HSet(ctx, namesKey, userID, username)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("leaderboard upsert: %w", err)
	}
	return nil
}

// Remove drops a player (e.g. account deletion).
func (l *Leaderboard) Remove(ctx context.Context, userID string) error {
	if !l.Enabled() {
		return nil
	}
	pipe := l.client.Pipeline()
	pipe.ZRem(ctx, ratingsKey, userID)
	pipe.HDel(ctx, namesKey, userID)
	_, err := pipe.Exec(ctx)
	return err
}

// Rank returns a player's 1-based position, or 0 when they are unranked.
func (l *Leaderboard) Rank(ctx context.Context, userID string) (int64, error) {
	if !l.Enabled() {
		return 0, nil
	}
	rank, err := l.client.ZRevRank(ctx, ratingsKey, userID).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil // not on the board
	}
	if err != nil {
		return 0, fmt.Errorf("leaderboard rank: %w", err)
	}
	return rank + 1, nil // ZREVRANK is 0-based
}

// Top returns the highest-rated players.
func (l *Leaderboard) Top(ctx context.Context, limit int) ([]Entry, error) {
	if !l.Enabled() {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := l.client.ZRevRangeWithScores(ctx, ratingsKey, 0, int64(limit-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("leaderboard top: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(rows))
	for _, z := range rows {
		ids = append(ids, z.Member.(string))
	}
	// One HMGET for every name, rather than a lookup per row.
	names, err := l.client.HMGet(ctx, namesKey, ids...).Result()
	if err != nil {
		return nil, fmt.Errorf("leaderboard names: %w", err)
	}

	out := make([]Entry, 0, len(rows))
	for i, z := range rows {
		name, _ := names[i].(string)
		out = append(out, Entry{
			Rank:     int64(i + 1),
			UserID:   ids[i],
			Username: name,
			Elo:      int(z.Score),
		})
	}
	return out, nil
}

// Size reports how many players are ranked.
func (l *Leaderboard) Size(ctx context.Context) (int64, error) {
	if !l.Enabled() {
		return 0, nil
	}
	return l.client.ZCard(ctx, ratingsKey).Result()
}

// ── Presence ────────────────────────────────────────────────────────────────

// presenceWindow is how long after their last action a player counts as online.
const presenceWindow = 5 * time.Minute

// Touch marks a player active now.
//
// A sorted set scored by timestamp rather than a key-per-user with a TTL,
// because this one structure answers both "is X online?" and "who is online?" —
// the latter would otherwise need a SCAN across the keyspace.
func (l *Leaderboard) Touch(ctx context.Context, userID string) error {
	if !l.Enabled() {
		return nil
	}
	return l.client.ZAdd(ctx, activeKey,
		redis.Z{Score: float64(time.Now().Unix()), Member: userID}).Err()
}

// OnlineCount returns how many players acted within the presence window,
// pruning anyone older first so the set cannot grow without bound.
func (l *Leaderboard) OnlineCount(ctx context.Context) (int64, error) {
	if !l.Enabled() {
		return 0, nil
	}
	cutoff := time.Now().Add(-presenceWindow).Unix()
	pipe := l.client.Pipeline()
	pipe.ZRemRangeByScore(ctx, activeKey, "-inf", fmt.Sprintf("(%d", cutoff))
	count := pipe.ZCard(ctx, activeKey)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("online count: %w", err)
	}
	return count.Val(), nil
}

// IsOnline reports whether a player acted within the presence window.
func (l *Leaderboard) IsOnline(ctx context.Context, userID string) (bool, error) {
	if !l.Enabled() {
		return false, nil
	}
	score, err := l.client.ZScore(ctx, activeKey, userID).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return time.Unix(int64(score), 0).After(time.Now().Add(-presenceWindow)), nil
}
