// Package upstream holds the gateway's clients for the internal gRPC services.
// The gateway owns no state: it translates GraphQL to these calls and back.
package upstream

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authv1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/auth/v1"
	gamev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/game/v1"
	sessionv1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/session/v1"
)

// Clients bundles the upstream services the gateway talks to.
type Clients struct {
	Game    gamev1.GameServiceClient
	Auth    authv1.AuthServiceClient
	Session sessionv1.SessionServiceClient

	conns []*grpc.ClientConn
}

// Dial connects to the game-service and, when sessionAddr is non-empty, the
// session-manager. An empty sessionAddr leaves Session nil, in which case live
// clock fields resolve to null instead of failing.
func Dial(gameAddr, sessionAddr string) (*Clients, error) {
	c := &Clients{}

	gameConn, err := grpc.NewClient(gameAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial game-service %q: %w", gameAddr, err)
	}
	c.conns = append(c.conns, gameConn)
	c.Game = gamev1.NewGameServiceClient(gameConn)
	// AuthService lives in the same process as GameService, so it shares the
	// connection rather than opening a second one to the same address.
	c.Auth = authv1.NewAuthServiceClient(gameConn)

	if sessionAddr != "" {
		sessionConn, err := grpc.NewClient(sessionAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, fmt.Errorf("dial session-manager %q: %w", sessionAddr, err)
		}
		c.conns = append(c.conns, sessionConn)
		c.Session = sessionv1.NewSessionServiceClient(sessionConn)
	}
	return c, nil
}

// SessionEnabled reports whether a session-manager is configured.
func (c *Clients) SessionEnabled() bool { return c != nil && c.Session != nil }

// Close closes every upstream connection.
func (c *Clients) Close() error {
	var firstErr error
	for _, conn := range c.conns {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Snapshot fetches live session state for a game.
func (c *Clients) Snapshot(ctx context.Context, gameID string) (*sessionv1.Snapshot, error) {
	if !c.SessionEnabled() {
		return nil, nil
	}
	return c.Session.GetSnapshot(ctx, &sessionv1.GameRef{GameId: gameID})
}
