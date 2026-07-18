package eventlog

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/redisx"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/store"
)

// testRedis dials the Redis at ACG_REDIS_ADDR, skipping the test when it is
// absent — the same convention as pkg/redisx's own tests, so the suite stays
// green with no infrastructure and exercises real Streams when it is present.
func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("ACG_REDIS_ADDR")
	if addr == "" {
		t.Skip("ACG_REDIS_ADDR not set; skipping event-stream integration test")
	}
	client, err := redisx.Dial(context.Background(), addr)
	if err != nil {
		t.Skipf("redis unavailable at %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func uniqueGameID() string {
	return "test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func TestStreamAppendReplay(t *testing.T) {
	client := testRedis(t)
	s := NewStream(client, nil)
	ctx := context.Background()
	gameID := uniqueGameID()
	t.Cleanup(func() { client.Del(ctx, key(gameID)) })

	moves := []string{"e2e4", "e7e5", "g1f3"}
	for i, uci := range moves {
		if _, err := s.Append(ctx, MoveEvent{
			GameID: gameID, Ply: i + 1, UCI: uci, FENAfter: "fen-" + uci, Status: "IN_PROGRESS",
		}); err != nil {
			t.Fatalf("append %s: %v", uci, err)
		}
	}

	// Full replay returns every move in ply order.
	entries, err := s.Replay(ctx, gameID, "")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(entries) != len(moves) {
		t.Fatalf("replay returned %d entries, want %d", len(entries), len(moves))
	}
	for i, e := range entries {
		if e.Event.UCI != moves[i] || e.Event.Ply != i+1 {
			t.Errorf("entry %d = {ply %d, uci %s}, want {ply %d, uci %s}",
				i, e.Event.Ply, e.Event.UCI, i+1, moves[i])
		}
	}

	// Replay after the first entry returns only what a reconnecting client missed.
	rest, err := s.Replay(ctx, gameID, entries[0].ID)
	if err != nil {
		t.Fatalf("replay after id: %v", err)
	}
	if len(rest) != len(moves)-1 || rest[0].Event.UCI != moves[1] {
		t.Errorf("incremental replay = %d entries starting %q, want %d starting %q",
			len(rest), firstUCI(rest), len(moves)-1, moves[1])
	}
}

// The relay should move rows from an OutboxSource into the real stream, in order.
func TestRelayPublishesToStream(t *testing.T) {
	client := testRedis(t)
	s := NewStream(client, nil)
	ctx := context.Background()
	gameID := uniqueGameID()
	t.Cleanup(func() { client.Del(ctx, key(gameID)) })

	src := &fakeSource{pending: []store.OutboxRow{
		{ID: 1, GameID: gameID, Ply: 1, UCI: "d2d4", FENAfter: "f1", Status: "IN_PROGRESS"},
		{ID: 2, GameID: gameID, Ply: 2, UCI: "d7d5", FENAfter: "f2", Status: "IN_PROGRESS"},
	}}
	r := NewRelay(src, s, nil)

	n, err := r.drainOnce(ctx)
	if err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	if n != 2 {
		t.Fatalf("relay published %d rows, want 2", n)
	}

	entries, err := s.Replay(ctx, gameID, "")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(entries) != 2 || entries[0].Event.UCI != "d2d4" || entries[1].Event.UCI != "d7d5" {
		t.Errorf("stream after relay = %d entries %q/%q, want 2 d2d4/d7d5",
			len(entries), firstUCI(entries), lastUCI(entries))
	}
}

func firstUCI(e []Entry) string {
	if len(e) == 0 {
		return ""
	}
	return e[0].Event.UCI
}

func lastUCI(e []Entry) string {
	if len(e) == 0 {
		return ""
	}
	return e[len(e)-1].Event.UCI
}
