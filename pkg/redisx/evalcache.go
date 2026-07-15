package redisx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// EvalTTL is how long an engine evaluation stays cached.
//
// A position's evaluation at a fixed depth is deterministic, so this could be
// near-permanent; the TTL exists to reclaim space for positions nobody revisits
// and to let an engine upgrade age out old verdicts.
const EvalTTL = 24 * time.Hour

// Eval is a cached engine verdict for one position at one depth.
type Eval struct {
	BestMove string   `json:"bestmove"`
	ScoreCP  int32    `json:"score_cp"`
	Mate     bool     `json:"mate"`
	MateIn   int32    `json:"mate_in"`
	Depth    uint32   `json:"depth"`
	PV       []string `json:"pv,omitempty"`
}

// EvalCache is a position→evaluation cache keyed by FEN and search depth.
//
// Chess is unusually well suited to this: openings and common middlegames recur
// constantly across games, and evaluating a position is expensive while looking
// it up is not. A nil cache is valid and simply never hits.
type EvalCache struct {
	client *redis.Client
	hits   atomic.Int64
	misses atomic.Int64
	errs   atomic.Int64
}

// NewEvalCache builds a cache. A nil client disables it.
func NewEvalCache(client *redis.Client) *EvalCache {
	return &EvalCache{client: client}
}

// Enabled reports whether a Redis client is attached.
func (c *EvalCache) Enabled() bool { return c != nil && c.client != nil }

// key namespaces by depth: the same position searched deeper is a different
// answer, so they must not share an entry.
func evalKey(fen string, depth uint32) string {
	return fmt.Sprintf("eval:d%d:%s", depth, fen)
}

// Get returns a cached evaluation, if present.
//
// A cache error is reported as a miss, never an error: the caller can always
// ask the engine, and failing a move because Redis hiccuped would be absurd.
func (c *EvalCache) Get(ctx context.Context, fen string, depth uint32) (*Eval, bool) {
	if !c.Enabled() {
		return nil, false
	}
	raw, err := c.client.Get(ctx, evalKey(fen, depth)).Bytes()
	if errors.Is(err, redis.Nil) {
		c.misses.Add(1)
		return nil, false
	}
	if err != nil {
		c.errs.Add(1)
		c.misses.Add(1)
		return nil, false
	}
	var e Eval
	if err := json.Unmarshal(raw, &e); err != nil {
		// A corrupt entry is worse than none: drop it and treat as a miss.
		c.client.Del(ctx, evalKey(fen, depth))
		c.misses.Add(1)
		return nil, false
	}
	c.hits.Add(1)
	return &e, true
}

// Put stores an evaluation. Failures are swallowed: a cache write that does not
// happen costs a recomputation, nothing more.
func (c *EvalCache) Put(ctx context.Context, fen string, depth uint32, e *Eval) {
	if !c.Enabled() || e == nil {
		return
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	if err := c.client.Set(ctx, evalKey(fen, depth), raw, EvalTTL).Err(); err != nil {
		c.errs.Add(1)
	}
}

// Stats returns cumulative counters for logging and metrics.
func (c *EvalCache) Stats() (hits, misses, errs int64) {
	if c == nil {
		return 0, 0, 0
	}
	return c.hits.Load(), c.misses.Load(), c.errs.Load()
}

// HitRate returns the fraction of lookups served from cache, or 0 with no data.
func (c *EvalCache) HitRate() float64 {
	hits, misses, _ := c.Stats()
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}
