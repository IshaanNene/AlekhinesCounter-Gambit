package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Audience tracks who is watching each game, and counts distinct daily users.
type Audience struct {
	client *redis.Client
}

// NewAudience builds an audience tracker. A nil client disables it.
func NewAudience(client *redis.Client) *Audience {
	return &Audience{client: client}
}

// Enabled reports whether Redis is attached.
func (a *Audience) Enabled() bool { return a != nil && a.client != nil }

// spectatorTTL bounds how long a spectator set survives without updates, so a
// gateway that dies mid-broadcast cannot leave phantom watchers forever.
const spectatorTTL = 30 * time.Minute

func spectatorsKey(gameID string) string { return "spectators:" + gameID }

// dauKey buckets by UTC day.
func dauKey(t time.Time) string { return "dau:" + t.UTC().Format("2006-01-02") }

// Watch adds a viewer to a game's audience.
//
// A set, not a list: viewers must be distinct (one person with two tabs is one
// spectator), and membership tests and removals need to be O(1).
func (a *Audience) Watch(ctx context.Context, gameID, userID string) error {
	if !a.Enabled() {
		return nil
	}
	pipe := a.client.Pipeline()
	pipe.SAdd(ctx, spectatorsKey(gameID), userID)
	pipe.Expire(ctx, spectatorsKey(gameID), spectatorTTL)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("watch game %q: %w", gameID, err)
	}
	return nil
}

// Unwatch removes a viewer.
func (a *Audience) Unwatch(ctx context.Context, gameID, userID string) error {
	if !a.Enabled() {
		return nil
	}
	return a.client.SRem(ctx, spectatorsKey(gameID), userID).Err()
}

// SpectatorCount returns how many distinct viewers a game has.
func (a *Audience) SpectatorCount(ctx context.Context, gameID string) (int64, error) {
	if !a.Enabled() {
		return 0, nil
	}
	return a.client.SCard(ctx, spectatorsKey(gameID)).Result()
}

// Spectators lists a game's viewers.
func (a *Audience) Spectators(ctx context.Context, gameID string) ([]string, error) {
	if !a.Enabled() {
		return nil, nil
	}
	return a.client.SMembers(ctx, spectatorsKey(gameID)).Result()
}

// ── Daily active users ──────────────────────────────────────────────────────

// RecordActive counts a user toward today's distinct total.
//
// HyperLogLog, not a set: an exact set of user ids costs memory linear in the
// number of users, while HLL answers the same question in a fixed ~12KB with
// ~0.81% error. For a "how many people played today?" figure that trade is
// obviously right — nobody makes a decision on the 1% .
func (a *Audience) RecordActive(ctx context.Context, userID string) error {
	if !a.Enabled() {
		return nil
	}
	key := dauKey(time.Now())
	pipe := a.client.Pipeline()
	pipe.PFAdd(ctx, key, userID)
	// Keep a rolling ~5 weeks so trends are visible without unbounded growth.
	pipe.Expire(ctx, key, 35*24*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

// DailyActive returns the approximate distinct users for a given day.
func (a *Audience) DailyActive(ctx context.Context, day time.Time) (int64, error) {
	if !a.Enabled() {
		return 0, nil
	}
	return a.client.PFCount(ctx, dauKey(day)).Result()
}

// ActiveOverDays merges several days' HLLs into one distinct count.
//
// This is HLL's real party trick: unions are lossless, so weekly actives come
// from merging seven daily sketches — no separate weekly counter, and no
// double-counting someone who played on three of those days.
func (a *Audience) ActiveOverDays(ctx context.Context, days int) (int64, error) {
	if !a.Enabled() || days <= 0 {
		return 0, nil
	}
	keys := make([]string, 0, days)
	for i := 0; i < days; i++ {
		keys = append(keys, dauKey(time.Now().AddDate(0, 0, -i)))
	}
	return a.client.PFCount(ctx, keys...).Result()
}

// ── Token revocation ────────────────────────────────────────────────────────

// Revocations denies specific sessions before their JWT expires.
//
// This is the one part of "session storage" a stateless JWT genuinely needs.
// The token proves its own validity, so signing out cannot un-sign it — the only
// way to end a session early is to remember the ones we refuse. Entries expire
// exactly when the token would have anyway, so the list stays small.
type Revocations struct {
	client *redis.Client
}

// NewRevocations builds a revocation list. A nil client disables it, in which
// case tokens simply live to their natural expiry.
func NewRevocations(client *redis.Client) *Revocations {
	return &Revocations{client: client}
}

// Enabled reports whether Redis is attached.
func (r *Revocations) Enabled() bool { return r != nil && r.client != nil }

// Revoke denies a token until `until`.
func (r *Revocations) Revoke(ctx context.Context, tokenID string, until time.Time) error {
	if !r.Enabled() {
		return nil
	}
	ttl := time.Until(until)
	if ttl <= 0 {
		return nil // already expired: nothing to deny
	}
	return r.client.Set(ctx, "revoked:"+tokenID, "1", ttl).Err()
}

// IsRevoked reports whether a token has been denied.
//
// Fails open: if Redis is unreachable we honour the token's own signature and
// expiry rather than locking every user out of the platform.
func (r *Revocations) IsRevoked(ctx context.Context, tokenID string) bool {
	if !r.Enabled() {
		return false
	}
	n, err := r.client.Exists(ctx, "revoked:"+tokenID).Result()
	return err == nil && n > 0
}
