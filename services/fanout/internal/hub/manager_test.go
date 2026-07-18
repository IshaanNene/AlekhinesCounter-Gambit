package hub

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/eventlog"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/redisx"
)

// TestManagerLifecycle checks hub create-on-first / GC-on-last without Redis.
func TestManagerLifecycle(t *testing.T) {
	mgr := NewManager(eventlog.NewStream(nil, slog.Default()), nil, slog.Default())

	a := mgr.Subscribe("g", "")
	b := mgr.Subscribe("g", "")
	if n := mgr.HubCount(); n != 1 {
		t.Fatalf("two viewers of one game -> %d hubs, want 1", n)
	}

	mgr.Unsubscribe("g", a)
	if n := mgr.HubCount(); n != 1 {
		t.Fatalf("hub GC'd while a viewer remained (%d hubs)", n)
	}
	mgr.Unsubscribe("g", b)
	if n := mgr.HubCount(); n != 0 {
		t.Fatalf("hub not GC'd after last viewer left (%d hubs)", n)
	}
}

// TestManagerFanoutOverRedis exercises the real path: append moves to a game's
// stream and assert a spectator receives them (from the backlog or live). Skips
// without ACG_REDIS_ADDR, like the eventlog integration tests.
func TestManagerFanoutOverRedis(t *testing.T) {
	addr := os.Getenv("ACG_REDIS_ADDR")
	if addr == "" {
		t.Skip("ACG_REDIS_ADDR not set; skipping fanout integration test")
	}
	client, err := redisx.Dial(context.Background(), addr)
	if err != nil {
		t.Skipf("redis unavailable at %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = client.Close() })

	stream := eventlog.NewStream(client, slog.Default())
	ctx := context.Background()
	gameID := "fanout-test-" + time.Now().Format("150405.000000")
	t.Cleanup(func() { client.Del(ctx, "game:"+gameID+":events") })

	// Two moves already on the stream before anyone connects.
	for i, uci := range []string{"e2e4", "e7e5"} {
		if _, err := stream.Append(ctx, eventlog.MoveEvent{
			GameID: gameID, Ply: i + 1, UCI: uci, FENAfter: "fen", Status: "IN_PROGRESS",
		}); err != nil {
			t.Fatalf("append %s: %v", uci, err)
		}
	}

	mgr := NewManager(stream, nil, slog.Default())
	sub := mgr.Subscribe(gameID, "")
	defer mgr.Unsubscribe(gameID, sub)

	// History is preloaded synchronously, so the backlog is complete on attach.
	if len(sub.Initial) != 2 || sub.Initial[0].UCI != "e2e4" || sub.Initial[1].UCI != "e7e5" {
		t.Fatalf("backlog = %+v, want [e2e4 e7e5]", sub.Initial)
	}

	// A move appended now must arrive live.
	if _, err := stream.Append(ctx, eventlog.MoveEvent{
		GameID: gameID, Ply: 3, UCI: "g1f3", FENAfter: "fen", Status: "IN_PROGRESS",
	}); err != nil {
		t.Fatalf("append g1f3: %v", err)
	}
	if uci := nextLive(t, sub); uci != "g1f3" {
		t.Errorf("live move = %q, want g1f3", uci)
	}
}

// nextLive waits for the next live delta (not backlog) on a subscriber.
func nextLive(t *testing.T, sub *Sub) string {
	t.Helper()
	select {
	case d := <-sub.Deltas():
		return d.UCI
	case <-sub.Kicked():
		t.Fatal("subscriber was unexpectedly kicked")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a live move")
	}
	return ""
}
