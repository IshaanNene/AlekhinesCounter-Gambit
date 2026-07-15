package graph

import (
	"log/slog"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/internal/auth"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/internal/pubsub"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/internal/upstream"
)

// This file will not be regenerated automatically.
// It serves as dependency injection for the resolvers.

// Resolver is the dependency-injection root for the GraphQL resolvers. The
// gateway holds no state of its own: every field is resolved over gRPC.
type Resolver struct {
	Upstream *upstream.Clients
	Bus      pubsub.Bus
	Signer   *auth.Signer
	Log      *slog.Logger
}
