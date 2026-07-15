package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Integrity records per-player fair-play signals as time series.
//
// # Why RedisTimeSeries and not Prometheus
//
// These are *per-player* series — one per account, potentially millions. That is
// exactly the high-cardinality shape Prometheus warns against: a `player_id`
// label would multiply every series by the user base and melt the TSDB.
// Prometheus is for service-level signals an operator reads (latency, error
// rate, queue depth). This is per-entity domain data the *application* reads to
// build a case for review. Different tools, no overlap.
//
// # What this is, and is not
//
// It computes *signals*, not verdicts. Engine-match rate and centipawn loss are
// the standard published indicators, but a strong player has a legitimately high
// match rate in simple positions, and one clean game proves nothing. So this
// only ever produces a flag for a human to review. Automated bans on a
// statistic ruin innocent people, and no amount of arithmetic here changes that.
type Integrity struct {
	client *redis.Client
}

// NewIntegrity builds the signal store. A nil client disables it.
func NewIntegrity(client *redis.Client) *Integrity {
	return &Integrity{client: client}
}

// Enabled reports whether Redis is attached.
func (i *Integrity) Enabled() bool { return i != nil && i.client != nil }

// Retention: 180 days of raw samples is enough to see a career change shape,
// while bounding what one account can cost us.
const integrityRetention = 180 * 24 * time.Hour

// Series names, per player.
func matchSeries(userID string) string { return "ts:match:" + userID }
func aclSeries(userID string) string   { return "ts:acl:" + userID }

// ensureSeries creates a series with retention and labels.
//
// Labels let us query across players (TS.MRANGE by label) without keeping a
// separate index of who has a series.
func (i *Integrity) ensureSeries(ctx context.Context, key, userID, metric string) {
	if !i.Enabled() {
		return
	}
	// TS.CREATE errors when the series exists, which is the normal case.
	i.client.Do(ctx, "TS.CREATE", key,
		"RETENTION", int64(integrityRetention/time.Millisecond),
		"DUPLICATE_POLICY", "LAST",
		"LABELS", "metric", metric, "player", userID)
}

// GameSignals is one game's fair-play summary for one player.
type GameSignals struct {
	PlayerID string
	// EngineMatchRate is the fraction of moves matching the engine's top choice.
	EngineMatchRate float64
	// AvgCentipawnLoss is the mean cost of the player's moves.
	AvgCentipawnLoss float64
	MoveCount        int
	At               time.Time
}

// Record appends one game's signals to a player's series.
func (i *Integrity) Record(ctx context.Context, s GameSignals) error {
	if !i.Enabled() || s.PlayerID == "" {
		return nil
	}
	// Too few moves to mean anything: a 4-move game with 100% match rate is a
	// book opening, not evidence.
	if s.MoveCount < 10 {
		return nil
	}
	i.ensureSeries(ctx, matchSeries(s.PlayerID), s.PlayerID, "engine_match_rate")
	i.ensureSeries(ctx, aclSeries(s.PlayerID), s.PlayerID, "avg_centipawn_loss")

	at := s.At
	if at.IsZero() {
		at = time.Now()
	}
	ts := at.UnixMilli()

	pipe := i.client.Pipeline()
	pipe.Do(ctx, "TS.ADD", matchSeries(s.PlayerID), ts, s.EngineMatchRate)
	pipe.Do(ctx, "TS.ADD", aclSeries(s.PlayerID), ts, s.AvgCentipawnLoss)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("record integrity signals: %w", err)
	}
	return nil
}

// Profile is a player's aggregated fair-play picture.
type Profile struct {
	PlayerID string
	Games    int
	// AvgMatchRate across the window, 0..1.
	AvgMatchRate float64
	// AvgCentipawnLoss across the window.
	AvgCentipawnLoss float64
	// Suspicion is a 0..1 heuristic, NOT a probability of cheating.
	Suspicion float64
	// Flagged is true when the profile warrants a human look.
	Flagged bool
	// Reasons explains the score in words, so a reviewer sees the argument
	// rather than a bare number.
	Reasons []string
}

// Thresholds. Deliberately conservative: a false accusation costs a real person
// far more than a missed detection costs the platform.
const (
	// Sustained agreement with the engine above this is unusual even for a
	// strong player across many games.
	suspiciousMatchRate = 0.85
	// Average loss below this is close to engine-perfect play.
	suspiciousACL = 10.0
	// Fewer games than this is not a pattern.
	minGamesForFlag = 5
	// Only flag on a clearly abnormal score.
	flagThreshold = 0.75
)

// Evaluate builds a player's profile over the last `days`.
func (i *Integrity) Evaluate(ctx context.Context, userID string, days int) (*Profile, error) {
	if !i.Enabled() {
		return &Profile{PlayerID: userID}, nil
	}
	if days <= 0 {
		days = 30
	}
	from := time.Now().AddDate(0, 0, -days).UnixMilli()
	now := time.Now().UnixMilli()

	matches, err := i.rangeValues(ctx, matchSeries(userID), from, now)
	if err != nil {
		return &Profile{PlayerID: userID}, nil // no series yet
	}
	acls, _ := i.rangeValues(ctx, aclSeries(userID), from, now)

	p := &Profile{PlayerID: userID, Games: len(matches)}
	if len(matches) == 0 {
		return p, nil
	}
	p.AvgMatchRate = mean(matches)
	p.AvgCentipawnLoss = mean(acls)

	// Two independent signals, averaged. Neither alone is convincing: a player
	// can have a high match rate in a forced endgame, or a low loss in a quiet
	// draw. Both, sustained, is the pattern worth a look.
	matchSignal := clamp01((p.AvgMatchRate - suspiciousMatchRate) / (1 - suspiciousMatchRate))
	aclSignal := clamp01((suspiciousACL - p.AvgCentipawnLoss) / suspiciousACL)
	p.Suspicion = (matchSignal + aclSignal) / 2

	if matchSignal > 0 {
		p.Reasons = append(p.Reasons, fmt.Sprintf(
			"matches the engine's first choice in %.0f%% of moves across %d games",
			p.AvgMatchRate*100, p.Games))
	}
	if aclSignal > 0 {
		p.Reasons = append(p.Reasons, fmt.Sprintf(
			"average centipawn loss of %.1f is close to engine-perfect", p.AvgCentipawnLoss))
	}

	// A flag is an invitation to review, never a verdict — and never on a sample
	// too small to mean anything.
	p.Flagged = p.Games >= minGamesForFlag && p.Suspicion >= flagThreshold
	if p.Flagged {
		p.Reasons = append(p.Reasons, "flagged for human review — not an accusation")
	}
	return p, nil
}

// rangeValues reads a series' values over a window.
func (i *Integrity) rangeValues(ctx context.Context, key string, from, to int64) ([]float64, error) {
	res, err := i.client.Do(ctx, "TS.RANGE", key, from, to).Slice()
	if err != nil {
		return nil, err
	}
	out := make([]float64, 0, len(res))
	for _, row := range res {
		pair, ok := row.([]any)
		if !ok || len(pair) != 2 {
			continue
		}
		// TS.RANGE returns values as strings.
		switch v := pair[1].(type) {
		case string:
			var f float64
			if _, err := fmt.Sscanf(v, "%g", &f); err == nil {
				out = append(out, f)
			}
		case float64:
			out = append(out, v)
		}
	}
	return out, nil
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
