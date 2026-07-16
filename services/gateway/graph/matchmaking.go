package graph

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"

	authv1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/auth/v1"
	gamev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/game/v1"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/graph/model"
)

// timeControlKey namespaces the queue.
//
// Players are only ever paired within an identical time control: a 3-minute
// blitz player must never be dropped into someone's 30-minute classical game.
// The key is the control itself, so that separation is structural rather than a
// check somebody could forget.
func timeControlKey(in model.QueueInput) string {
	inc := 0
	if in.IncrementMs != nil {
		inc = *in.IncrementMs
	}
	return fmt.Sprintf("%d+%d", in.InitialMs, inc)
}

// enterQueue pairs the caller with a waiting opponent, or enqueues them.
func (r *Resolver) enterQueue(ctx context.Context, input model.QueueInput) (*model.QueueTicket, error) {
	id, err := requireIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if !r.Matchmaking.Enabled() {
		return nil, fmt.Errorf("matchmaking is unavailable right now")
	}

	// The rating decides who you are paired with, so it must come from the
	// account, never from the client.
	user, err := r.Upstream.Auth.GetUser(ctx, &authv1.GetUserRequest{UserId: id.UserID})
	if err != nil {
		return nil, err
	}
	elo := int(user.GetUser().GetElo())
	tc := timeControlKey(input)

	opponent, paired, err := r.Matchmaking.Enqueue(ctx, id.UserID, elo, tc)
	if err != nil {
		return nil, err
	}
	depth, _ := r.Matchmaking.QueueDepth(ctx, tc)

	if !paired {
		return &model.QueueTicket{Matched: false, QueueDepth: int(depth)}, nil
	}

	// Paired: whoever arrived second creates the game.
	white, black := assignColours(id.UserID, opponent)
	inc := 0
	if input.IncrementMs != nil {
		inc = *input.IncrementMs
	}
	resp, err := r.Upstream.Game.CreateGame(ctx, &gamev1.CreateGameRequest{
		WhiteId:     white,
		BlackId:     black,
		VsEngine:    false,
		InitialMs:   int64(input.InitialMs),
		IncrementMs: int64(inc),
		Rated:       true, // a matched game is always rated; that is the point
	})
	if err != nil {
		// Put the opponent back rather than stranding them: they are no longer in
		// the queue (the pairing script removed both) and have no game either.
		if _, _, reErr := r.Matchmaking.Enqueue(ctx, opponent, elo, tc); reErr != nil {
			r.Log.Error("could not requeue opponent after a failed pairing",
				"opponent", opponent, "error", reErr)
		}
		return nil, err
	}

	g := toModelGame(resp.GetGame())
	// The opponent has been waiting and has no response to learn this on.
	if err := r.Matchmaking.NotifyMatched(ctx, opponent, g.ID); err != nil {
		// They will still find the game from their history; do not fail ours.
		r.Log.Warn("could not notify the matched opponent", "opponent", opponent, "error", err)
	}
	return &model.QueueTicket{Matched: true, Game: g, QueueDepth: int(depth)}, nil
}

// assignColours picks who plays White, at random.
//
// Deterministic assignment (first-to-queue plays White, say) would hand a real
// edge to whoever refreshes fastest — White scores about 54% across millions of
// games. A coin flip keeps a rated ladder honest.
func assignColours(a, b string) (white, black string) {
	n, err := rand.Int(rand.Reader, big.NewInt(2))
	if err != nil || n.Int64() == 0 {
		return a, b
	}
	return b, a
}

// leaveQueue removes the caller from the queue.
func (r *Resolver) leaveQueue(ctx context.Context, input model.QueueInput) (bool, error) {
	id, err := requireIdentity(ctx)
	if err != nil {
		return false, err
	}
	if !r.Matchmaking.Enabled() {
		return true, nil
	}
	if err := r.Matchmaking.Leave(ctx, id.UserID, timeControlKey(input)); err != nil {
		return false, err
	}
	return true, nil
}

// matchFound streams the game the caller is paired into.
func (r *Resolver) matchFound(ctx context.Context) (<-chan *model.Game, error) {
	id, err := requireIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if !r.Matchmaking.Enabled() {
		return nil, fmt.Errorf("matchmaking is unavailable right now")
	}

	ids, err := r.Matchmaking.WatchMatches(ctx, id.UserID)
	if err != nil {
		return nil, err
	}

	out := make(chan *model.Game, 1)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case gameID, ok := <-ids:
				if !ok {
					return
				}
				// Only the id travels through Redis; read the game itself from the
				// service that owns it rather than trusting a broadcast payload.
				resp, err := r.Upstream.Game.GetGame(ctx, &gamev1.GetGameRequest{GameId: gameID})
				if err != nil {
					r.Log.Warn("matched into an unreadable game", "game_id", gameID, "error", err)
					continue
				}
				select {
				case out <- toModelGame(resp.GetGame()):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}
