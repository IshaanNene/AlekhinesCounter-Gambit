// Package server is the fanout WebSocket surface: it accepts spectator
// connections for a game and pushes move deltas as they happen.
//
// Protocol (JSON over WebSocket at /spectate?game=<id>[&from=<streamId>]):
//   - on connect, the moves the client is missing arrive as {"type":"move",...}
//     messages (all of them for a fresh viewer; only those after `from` for a
//     reconnect), followed by a single {"type":"synced"} marker;
//   - thereafter each new move arrives as another "move" message.
//
// Each move carries its stream id, so a client that is dropped for being slow
// simply reconnects with from=<last id it saw> and replays the gap. The endpoint
// is read-only and unauthenticated — spectating a game is public.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/fanout/internal/hub"
)

// writeTimeout bounds a single message write, so a spectator that has stopped
// reading at the TCP level is closed instead of blocking its own goroutine.
const writeTimeout = 10 * time.Second

// Server serves spectator WebSocket connections from a hub Manager.
type Server struct {
	mgr *hub.Manager
	log *slog.Logger
}

// New builds a Server over a hub Manager.
func New(mgr *hub.Manager, log *slog.Logger) *Server {
	return &Server{mgr: mgr, log: log}
}

type syncedMsg struct {
	Type string `json:"type"`
}

// Handler routes the spectate endpoint.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/spectate", s.handleSpectate)
	return mux
}

func (s *Server) handleSpectate(w http.ResponseWriter, r *http.Request) {
	gameID := r.URL.Query().Get("game")
	if gameID == "" {
		http.Error(w, "query parameter 'game' is required", http.StatusBadRequest)
		return
	}
	from := r.URL.Query().Get("from")

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Public read-only fanout: no cookies or credentials cross this socket, so
		// there is nothing for a cross-origin page to abuse.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return // Accept already wrote the response
	}
	defer c.CloseNow()

	// CloseRead handles pings/close frames and gives us a context that is
	// cancelled the moment the spectator disconnects.
	ctx := c.CloseRead(r.Context())

	sub := s.mgr.Subscribe(gameID, from)
	defer s.mgr.Unsubscribe(gameID, sub)

	// 1. Backlog the client is missing, then the caught-up marker.
	for _, d := range sub.Initial {
		if err := s.write(ctx, c, d); err != nil {
			return
		}
	}
	if err := s.write(ctx, c, syncedMsg{Type: "synced"}); err != nil {
		return
	}

	// 2. Live moves until the client leaves or falls too far behind.
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Kicked():
			// Slow consumer: ask it to reconnect (it will replay from its last id).
			_ = c.Close(websocket.StatusTryAgainLater, "fell behind; reconnect with from=<last id>")
			return
		case d := <-sub.Deltas():
			if err := s.write(ctx, c, d); err != nil {
				return
			}
		}
	}
}

func (s *Server) write(ctx context.Context, c *websocket.Conn, v any) error {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(wctx, c, v)
}
