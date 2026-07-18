package server

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/eventlog"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/redisx"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/fanout/internal/hub"
)

type inMsg struct {
	Type string `json:"type"`
	UCI  string `json:"uci"`
	ID   string `json:"id"`
}

// End-to-end: a real WebSocket spectator receives backlog + a synced marker +
// live moves from a real Redis stream. Skips without ACG_REDIS_ADDR.
func TestSpectateEndToEnd(t *testing.T) {
	addr := os.Getenv("ACG_REDIS_ADDR")
	if addr == "" {
		t.Skip("ACG_REDIS_ADDR not set; skipping fanout end-to-end test")
	}
	ctx := context.Background()
	client, err := redisx.Dial(ctx, addr)
	if err != nil {
		t.Skipf("redis unavailable at %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = client.Close() })

	stream := eventlog.NewStream(client, slog.Default())
	gameID := "e2e-" + time.Now().Format("150405.000000")
	t.Cleanup(func() { client.Del(ctx, "game:"+gameID+":events") })

	// One move already on the stream before the spectator connects.
	mustAppend(t, stream, gameID, 1, "e2e4")

	srv := New(hub.NewManager(stream, nil, slog.Default()), slog.Default())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	c := dialWS(t, ts.URL+"/spectate?game="+gameID)
	defer c.Close(websocket.StatusNormalClosure, "")

	// Expect: backlog move e2e4, then synced.
	if got := readMsg(t, c); got.Type != "move" || got.UCI != "e2e4" {
		t.Fatalf("first message = %+v, want move e2e4", got)
	}
	if got := readMsg(t, c); got.Type != "synced" {
		t.Fatalf("second message = %+v, want synced", got)
	}

	// A move published now arrives live.
	mustAppend(t, stream, gameID, 2, "e7e5")
	if got := readMsg(t, c); got.Type != "move" || got.UCI != "e7e5" {
		t.Fatalf("live message = %+v, want move e7e5", got)
	}
}

func mustAppend(t *testing.T, s *eventlog.Stream, gameID string, ply int, uci string) {
	t.Helper()
	if _, err := s.Append(context.Background(), eventlog.MoveEvent{
		GameID: gameID, Ply: ply, UCI: uci, FENAfter: "fen", Status: "IN_PROGRESS",
	}); err != nil {
		t.Fatalf("append %s: %v", uci, err)
	}
}

func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(url, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	return c
}

func readMsg(t *testing.T, c *websocket.Conn) inMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var m inMsg
	if err := wsjson.Read(ctx, c, &m); err != nil {
		t.Fatalf("ws read: %v", err)
	}
	return m
}
