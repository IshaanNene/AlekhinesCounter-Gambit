package redisx

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Novelty answers "has this position ever occurred on this platform?" using a
// Bloom filter.
//
// The point is a *theoretical novelty* — in chess annotation, the first move of
// a game that departs from all known prior play. Detecting it means testing
// every position against every position ever reached, which as an exact set is
// millions of 60-byte FENs; as a Bloom filter it is a few megabytes.
//
// The error direction is what makes this sound. A Bloom filter answers either
// "definitely absent" or "possibly present" — it never misses a member. So
// "definitely absent" *proves* novelty, which is the claim we actually want to
// make. The uncertain answer ("seen before") is the boring one, and it is the
// one we can confirm against Postgres if it ever matters.
type Novelty struct {
	client *redis.Client
}

// NewNovelty builds a novelty index. A nil client disables it.
func NewNovelty(client *redis.Client) *Novelty {
	return &Novelty{client: client}
}

// Enabled reports whether Redis is attached.
func (n *Novelty) Enabled() bool { return n != nil && n.client != nil }

const (
	// positionsFilter holds every position ever reached in a finished game.
	positionsFilter = "bloom:positions"
	// pgnFilter dedupes imported games by content hash.
	pgnFilter = "bloom:pgn"

	// A 0.1% false-positive rate over an initial 1M positions. Wrong here only
	// means "we think this position is known when it is new" — we under-report a
	// novelty, never invent one. Scaling filters grow beyond the initial
	// capacity at a modest accuracy cost, so the estimate need not be perfect.
	bloomErrorRate = 0.001
	bloomCapacity  = 1_000_000
)

// truthy normalises a Bloom reply.
//
// The RESP protocol version decides the type: RESP2 answers these commands with
// integers, RESP3 with booleans, and go-redis v9 negotiates RESP3 by default.
// Asserting int64 alone silently yields false on a RESP3 server, which here
// meant every known position was reported as a novelty.
func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	case float64:
		return t != 0
	default:
		return false
	}
}

// positionKey strips the move counters from a FEN.
//
// Two games reaching the same position by different move orders (a
// transposition) are the same position, and the half-move/full-move counters
// would make them look different. Only placement, side to move, castling, and
// en passant identify a position.
func positionKey(fen string) string {
	fields := strings.Fields(fen)
	if len(fields) >= 4 {
		return strings.Join(fields[:4], " ")
	}
	return fen
}

// ensureFilter creates a filter with our chosen error rate.
//
// Without this, first use auto-creates one with Redis's defaults (1% error,
// capacity 100), which would saturate almost immediately and report everything
// as already-seen.
func (n *Novelty) ensureFilter(ctx context.Context, key string, capacity int64) {
	if !n.Enabled() {
		return
	}
	// BF.RESERVE errors if it already exists, which is the normal case.
	n.client.Do(ctx, "BF.RESERVE", key, bloomErrorRate, capacity, "EXPANSION", 2)
}

// FirstNovelty returns the first position in the sequence never seen before, and
// its index. found is false when every position was already known.
//
// Positions are checked in one BF.MEXISTS rather than a call each: a 40-move
// game is 80 positions, and 80 round trips per finished game would cost more
// than the filter saves.
func (n *Novelty) FirstNovelty(ctx context.Context, fenHistory []string) (fen string, index int, found bool) {
	if !n.Enabled() || len(fenHistory) == 0 {
		return "", 0, false
	}
	n.ensureFilter(ctx, positionsFilter, bloomCapacity)

	args := make([]any, 0, len(fenHistory)+2)
	args = append(args, "BF.MEXISTS", positionsFilter)
	for _, f := range fenHistory {
		args = append(args, positionKey(f))
	}
	res, err := n.client.Do(ctx, args...).Slice()
	if err != nil || len(res) != len(fenHistory) {
		return "", 0, false // never fail a game report over a cache
	}

	for i, v := range res {
		if !truthy(v) {
			// Definitely absent: a genuine novelty, not a probabilistic guess.
			return fenHistory[i], i, true
		}
	}
	return "", 0, false
}

// Record adds every position of a game to the filter.
func (n *Novelty) Record(ctx context.Context, fenHistory []string) error {
	if !n.Enabled() || len(fenHistory) == 0 {
		return nil
	}
	n.ensureFilter(ctx, positionsFilter, bloomCapacity)

	args := make([]any, 0, len(fenHistory)+2)
	args = append(args, "BF.MADD", positionsFilter)
	for _, f := range fenHistory {
		args = append(args, positionKey(f))
	}
	if err := n.client.Do(ctx, args...).Err(); err != nil {
		return fmt.Errorf("record positions: %w", err)
	}
	return nil
}

// KnownPositions estimates how many distinct positions the filter holds.
func (n *Novelty) KnownPositions(ctx context.Context) (int64, error) {
	if !n.Enabled() {
		return 0, nil
	}
	res, err := n.client.Do(ctx, "BF.CARD", positionsFilter).Int64()
	if err != nil {
		return 0, nil // filter not created yet
	}
	return res, nil
}

// ── Duplicate detection ─────────────────────────────────────────────────────

// SeenPGN reports whether a game's moves have been imported before, recording it
// if not. Returns true when it is a duplicate.
//
// Here the false-positive direction cuts the other way: we may occasionally
// reject a genuinely new game as a duplicate. For bulk import that is an
// acceptable trade against holding every hash in memory — and the caller can
// confirm against Postgres when the answer matters.
func (n *Novelty) SeenPGN(ctx context.Context, moves []string) (bool, error) {
	if !n.Enabled() || len(moves) == 0 {
		return false, nil
	}
	n.ensureFilter(ctx, pgnFilter, bloomCapacity)

	sum := sha1.Sum([]byte(strings.Join(moves, " "))) //nolint:gosec // dedupe key, not a security boundary
	hash := hex.EncodeToString(sum[:])

	// BF.ADD reports whether the item was newly added. Test-and-set in one call,
	// so two concurrent imports cannot both believe they are first.
	added, err := n.client.Do(ctx, "BF.ADD", pgnFilter, hash).Result()
	if err != nil {
		return false, fmt.Errorf("pgn dedupe: %w", err)
	}
	// Not newly added => we have seen this game before.
	return !truthy(added), nil
}
