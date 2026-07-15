package graph

import (
	"context"
	"errors"

	authv1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/auth/v1"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/graph/model"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/internal/auth"
)

// startSession mints a session for an authenticated user and sets the cookie.
//
// Every sign-in path funnels through here, so cookie flags and token lifetime
// are decided in exactly one place.
func (r *Resolver) startSession(ctx context.Context, u *authv1.User) (*model.Session, error) {
	token, expiresAt, err := r.Signer.Mint(auth.Identity{
		UserID:   u.GetId(),
		Username: u.GetUsername(),
		IsGuest:  u.GetIsGuest(),
	})
	if err != nil {
		return nil, err
	}
	// Absent over WebSocket, where there is no HTTP response to attach a cookie
	// to. The returned token still works via the Authorization header.
	if w, ok := auth.WriterFromContext(ctx); ok {
		r.Signer.SetCookie(w, token, expiresAt)
	}
	return &model.Session{
		User:      toModelUser(u),
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}

// requireIdentity returns the signed-in caller, or a clean error.
func requireIdentity(ctx context.Context) (*auth.Identity, error) {
	id, err := auth.FromContext(ctx)
	if err != nil {
		return nil, errors.New("you must be signed in to do that")
	}
	return id, nil
}

func toModelUser(u *authv1.User) *model.User {
	if u == nil {
		return nil
	}
	out := &model.User{
		ID:          u.GetId(),
		Username:    u.GetUsername(),
		Elo:         int(u.GetElo()),
		IsGuest:     u.GetIsGuest(),
		GamesPlayed: int(u.GetGamesPlayed()),
		CreatedAt:   u.GetCreatedAt().AsTime(),
	}
	if e := u.GetEmail(); e != "" {
		out.Email = &e
	}
	return out
}
