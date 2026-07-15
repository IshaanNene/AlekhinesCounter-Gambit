// Package session is a thin gRPC client for the Erlang session-manager's
// SessionService, which owns live session state (turn, clocks, presence).
//
// The client is optional: when the session-manager address is unset, Dial
// returns a disabled client whose calls are no-ops. That keeps games playable
// against the engine (which need no live session) even if the session-manager is
// not deployed, and lets the game-service degrade rather than fail when the
// session tier is unavailable.
package session

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	sessionv1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/session/v1"
)

// Client wraps a SessionService gRPC connection.
type Client struct {
	conn   *grpc.ClientConn
	client sessionv1.SessionServiceClient
	log    *slog.Logger
}

// Dial connects to the session-manager at addr. An empty addr yields a disabled
// client whose methods are no-ops.
func Dial(addr string, log *slog.Logger) (*Client, error) {
	if addr == "" {
		return &Client{log: log}, nil
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial session-manager %q: %w", addr, err)
	}
	return &Client{conn: conn, client: sessionv1.NewSessionServiceClient(conn), log: log}, nil
}

// Enabled reports whether a session-manager is configured.
func (c *Client) Enabled() bool { return c != nil && c.client != nil }

// Close closes the underlying connection, if any.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// CreateParams describes a new live session.
type CreateParams struct {
	GameID      string
	WhiteID     string
	BlackID     string // empty for a game against the engine
	InitialMs   int64
	IncrementMs int64
	GraceMs     int64
}

// Create starts a live session for a game. Zero-valued durations let the
// session-manager apply its defaults.
func (c *Client) Create(ctx context.Context, p CreateParams) error {
	if !c.Enabled() {
		return nil
	}
	_, err := c.client.CreateSession(ctx, &sessionv1.CreateSessionRequest{
		GameId:      p.GameID,
		WhiteId:     p.WhiteID,
		BlackId:     p.BlackID,
		InitialMs:   p.InitialMs,
		IncrementMs: p.IncrementMs,
		GraceMs:     p.GraceMs,
	})
	if err != nil {
		return fmt.Errorf("create session %q: %w", p.GameID, err)
	}
	return nil
}

// MoveMade tells the session a validated move was played, so it can switch the
// turn and apply the clock. Returns the resulting snapshot.
func (c *Client) MoveMade(ctx context.Context, gameID, playerID string) (*sessionv1.Snapshot, error) {
	if !c.Enabled() {
		return nil, nil
	}
	snap, err := c.client.MoveMade(ctx, &sessionv1.PlayerRef{GameId: gameID, PlayerId: playerID})
	if err != nil {
		return nil, fmt.Errorf("session move %q: %w", gameID, err)
	}
	return snap, nil
}

// Snapshot returns the current live session state.
func (c *Client) Snapshot(ctx context.Context, gameID string) (*sessionv1.Snapshot, error) {
	if !c.Enabled() {
		return nil, nil
	}
	snap, err := c.client.GetSnapshot(ctx, &sessionv1.GameRef{GameId: gameID})
	if err != nil {
		return nil, fmt.Errorf("session snapshot %q: %w", gameID, err)
	}
	return snap, nil
}

// Winner identifies the winning side of a finished game.
type Winner int

const (
	// WinnerNone is a draw.
	WinnerNone Winner = iota
	WinnerWhite
	WinnerBlack
)

// End closes the live session because the game itself ended for a chess reason
// the session cannot observe (checkmate, stalemate, draw). Without this the
// session's clocks would keep running and eventually flag-fall a finished game.
func (c *Client) End(ctx context.Context, gameID string, winner Winner, reason string) error {
	if !c.Enabled() {
		return nil
	}
	side := sessionv1.Side_SIDE_UNSPECIFIED
	switch winner {
	case WinnerWhite:
		side = sessionv1.Side_SIDE_WHITE
	case WinnerBlack:
		side = sessionv1.Side_SIDE_BLACK
	case WinnerNone:
		// leave unspecified: a draw has no winner
	}
	_, err := c.client.EndSession(ctx, &sessionv1.EndSessionRequest{
		GameId: gameID,
		Winner: side,
		Reason: reason,
	})
	if err != nil {
		return fmt.Errorf("end session %q: %w", gameID, err)
	}
	return nil
}

// Resign ends the session in the opponent's favour.
func (c *Client) Resign(ctx context.Context, gameID, playerID string) (*sessionv1.Snapshot, error) {
	if !c.Enabled() {
		return nil, nil
	}
	snap, err := c.client.Resign(ctx, &sessionv1.PlayerRef{GameId: gameID, PlayerId: playerID})
	if err != nil {
		return nil, fmt.Errorf("session resign %q: %w", gameID, err)
	}
	return snap, nil
}
