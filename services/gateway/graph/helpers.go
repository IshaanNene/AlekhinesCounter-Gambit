package graph

import (
	"context"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/graph/model"
)

// Small conversion helpers shared by the resolvers. They live outside
// schema.resolvers.go because gqlgen regenerates that file and moves stray
// helpers out of it.

// publish fans a game update out to live subscribers. A publish failure must
// never fail the mutation: the move is already committed upstream, so the worst
// case is a client that refreshes instead of being pushed to.
func (r *Resolver) publish(ctx context.Context, g *model.Game) {
	if g == nil || r.Bus == nil {
		return
	}
	if err := r.Bus.Publish(ctx, g.ID, g); err != nil {
		r.Log.Warn("publish game update failed", "game_id", g.ID, "error", err)
	}
}

// deref returns the pointed-to string, or "" for nil — the proto zero value the
// backends already treat as "unset".
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// derefInt returns the pointed-to int, or 0 for nil.
func derefInt(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}
