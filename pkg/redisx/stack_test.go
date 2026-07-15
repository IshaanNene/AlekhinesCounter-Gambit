package redisx

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestPositionKeyIgnoresMoveCounters(t *testing.T) {
	// The same position reached by different move orders (a transposition) must
	// key identically; only the counters differ.
	a := "rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 1"
	b := "rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq e3 5 12"
	if positionKey(a) != positionKey(b) {
		t.Errorf("transposition keys differ:\n %q\n %q", positionKey(a), positionKey(b))
	}
	// But a genuinely different position must not collide.
	c := "rnbqkbnr/pppppppp/8/8/3P4/8/PPP1PPPP/RNBQKBNR b KQkq d3 0 1"
	if positionKey(a) == positionKey(c) {
		t.Error("different positions share a key")
	}
}

func TestNoveltyDetectsUnseenPosition(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	n := NewNovelty(client)

	// Isolate this test's filter from any other run.
	uniq := fmt.Sprintf("%d", time.Now().UnixNano())
	known := []string{
		"pos-a-" + uniq + " w KQkq -",
		"pos-b-" + uniq + " b KQkq -",
	}
	fresh := "pos-NEW-" + uniq + " w KQkq -"

	if err := n.Record(ctx, known); err != nil {
		t.Skipf("BF.MADD unavailable (needs redis-stack): %v", err)
	}
	t.Cleanup(func() { client.Del(ctx, positionsFilter) })

	// A game replaying known positions then diverging: the novelty is the first
	// position never seen.
	history := append(append([]string{}, known...), fresh)
	got, idx, found := n.FirstNovelty(ctx, history)
	if !found {
		t.Fatal("expected a novelty for an unseen position")
	}
	if got != fresh || idx != 2 {
		t.Errorf("novelty = %q at %d, want %q at 2", got, idx, fresh)
	}
}

// The property that makes this sound: a Bloom filter never misses a member, so
// "definitely absent" proves novelty. Every recorded position must report seen.
func TestNoveltyNeverReportsKnownPositionAsNew(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	n := NewNovelty(client)

	uniq := fmt.Sprintf("%d", time.Now().UnixNano())
	positions := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		positions = append(positions, fmt.Sprintf("p%d-%s w - -", i, uniq))
	}
	if err := n.Record(ctx, positions); err != nil {
		t.Skipf("BF.MADD unavailable (needs redis-stack): %v", err)
	}
	t.Cleanup(func() { client.Del(ctx, positionsFilter) })

	// No false negatives: replaying only known positions must find no novelty.
	if fen, _, found := n.FirstNovelty(ctx, positions); found {
		t.Errorf("reported %q as novel, but every position was recorded", fen)
	}
}

func TestPGNDedupe(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	n := NewNovelty(client)
	t.Cleanup(func() { client.Del(ctx, pgnFilter) })

	moves := []string{"e2e4", "e7e5", "g1f3", fmt.Sprintf("b8c6-%d", time.Now().UnixNano())}

	dup, err := n.SeenPGN(ctx, moves)
	if err != nil {
		t.Skipf("BF.ADD unavailable (needs redis-stack): %v", err)
	}
	if dup {
		t.Error("first import should not be a duplicate")
	}
	dup, err = n.SeenPGN(ctx, moves)
	if err != nil {
		t.Fatal(err)
	}
	if !dup {
		t.Error("re-importing the same game should be detected as a duplicate")
	}
}

func TestNilNoveltyDegrades(t *testing.T) {
	n := NewNovelty(nil)
	if n.Enabled() {
		t.Error("nil client should report disabled")
	}
	if _, _, found := n.FirstNovelty(context.Background(), []string{"x"}); found {
		t.Error("disabled novelty should find nothing")
	}
	if err := n.Record(context.Background(), []string{"x"}); err != nil {
		t.Errorf("disabled Record should no-op, got %v", err)
	}
}

// ── Integrity / fair play ───────────────────────────────────────────────────

func TestIntegrityCleanPlayerIsNotFlagged(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	i := NewIntegrity(client)

	user := fmt.Sprintf("clean-%d", time.Now().UnixNano())
	t.Cleanup(func() { client.Del(ctx, matchSeries(user), aclSeries(user)) })

	// A normal club player: agrees with the engine about half the time, loses
	// ~40 centipawns a move.
	for n := 0; n < 10; n++ {
		err := i.Record(ctx, GameSignals{
			PlayerID: user, EngineMatchRate: 0.52, AvgCentipawnLoss: 42, MoveCount: 40,
			At: time.Now().Add(-time.Duration(n) * time.Hour),
		})
		if err != nil {
			t.Skipf("TS.ADD unavailable (needs redis-stack): %v", err)
		}
	}

	p, err := i.Evaluate(ctx, user, 30)
	if err != nil {
		t.Fatal(err)
	}
	if p.Flagged {
		t.Errorf("clean player flagged: suspicion=%.2f reasons=%v", p.Suspicion, p.Reasons)
	}
	if p.Games != 10 {
		t.Errorf("games = %d, want 10", p.Games)
	}
}

func TestIntegrityEngineLikePlayerIsFlagged(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	i := NewIntegrity(client)

	user := fmt.Sprintf("suspect-%d", time.Now().UnixNano())
	t.Cleanup(func() { client.Del(ctx, matchSeries(user), aclSeries(user)) })

	// Sustained near-perfect agreement across many games.
	for n := 0; n < 12; n++ {
		err := i.Record(ctx, GameSignals{
			PlayerID: user, EngineMatchRate: 0.97, AvgCentipawnLoss: 3, MoveCount: 45,
			At: time.Now().Add(-time.Duration(n) * time.Hour),
		})
		if err != nil {
			t.Skipf("TS.ADD unavailable (needs redis-stack): %v", err)
		}
	}

	p, err := i.Evaluate(ctx, user, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Flagged {
		t.Errorf("engine-like player not flagged: suspicion=%.2f", p.Suspicion)
	}
	if len(p.Reasons) == 0 {
		t.Error("a flag must come with reasons a reviewer can read")
	}
}

// One brilliant game is not a pattern, and must never flag anyone.
func TestIntegrityRequiresEnoughGamesToFlag(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	i := NewIntegrity(client)

	user := fmt.Sprintf("onegame-%d", time.Now().UnixNano())
	t.Cleanup(func() { client.Del(ctx, matchSeries(user), aclSeries(user)) })

	err := i.Record(ctx, GameSignals{
		PlayerID: user, EngineMatchRate: 1.0, AvgCentipawnLoss: 0, MoveCount: 30,
	})
	if err != nil {
		t.Skipf("TS.ADD unavailable (needs redis-stack): %v", err)
	}

	p, _ := i.Evaluate(ctx, user, 30)
	if p.Flagged {
		t.Errorf("flagged on a single game (suspicion=%.2f) — one game is not a pattern", p.Suspicion)
	}
}

// A short game's stats are noise: a 4-move book opening is 100% "engine match".
func TestIntegrityIgnoresShortGames(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	i := NewIntegrity(client)

	user := fmt.Sprintf("short-%d", time.Now().UnixNano())
	t.Cleanup(func() { client.Del(ctx, matchSeries(user), aclSeries(user)) })

	if err := i.Record(ctx, GameSignals{
		PlayerID: user, EngineMatchRate: 1.0, AvgCentipawnLoss: 0, MoveCount: 4,
	}); err != nil {
		t.Skipf("redis-stack unavailable: %v", err)
	}
	p, _ := i.Evaluate(ctx, user, 30)
	if p.Games != 0 {
		t.Errorf("recorded a 4-move game (games=%d); too short to signal anything", p.Games)
	}
}

func TestNilIntegrityDegrades(t *testing.T) {
	i := NewIntegrity(nil)
	if i.Enabled() {
		t.Error("nil client should report disabled")
	}
	if err := i.Record(context.Background(), GameSignals{PlayerID: "x", MoveCount: 40}); err != nil {
		t.Errorf("disabled Record should no-op, got %v", err)
	}
	p, err := i.Evaluate(context.Background(), "x", 30)
	if err != nil || p.Flagged {
		t.Errorf("disabled Evaluate should be inert, got %+v err=%v", p, err)
	}
}
