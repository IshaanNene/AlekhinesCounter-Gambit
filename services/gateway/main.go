// Command gateway is the public API: it serves GraphQL over HTTP (and, from
// T2.6, WebSocket subscriptions) and translates every request into internal
// gRPC calls. It holds no state of its own.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/config"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/graph"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/graph/generated"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/internal/upstream"
)

var version = "dev"

// shutdownTimeout bounds how long in-flight requests may finish on SIGTERM.
const shutdownTimeout = 15 * time.Second

func main() {
	log := config.NewLogger()

	addr := config.Getenv("ACG_GATEWAY_ADDR", ":8080")
	gameAddr := config.Getenv("ACG_GAME_ADDR_CLIENT", "localhost:50051")
	sessionAddr := config.Getenv("ACG_SESSION_ADDR", "")

	clients, err := upstream.Dial(gameAddr, sessionAddr)
	if err != nil {
		log.Error("failed to dial upstream services", "error", err)
		os.Exit(1)
	}
	defer clients.Close()

	srv := newGraphQLServer(&graph.Resolver{Upstream: clients, Log: log})

	mux := http.NewServeMux()
	mux.Handle("/graphql", srv)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// The playground is a developer convenience; disable it outside dev.
	if config.Getenv("ACG_GRAPHQL_PLAYGROUND", "true") == "true" {
		mux.Handle("/", playground.Handler("Alekhine's Counter-Gambit", "/graphql"))
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-sigCtx.Done()
		log.Info("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			log.Error("graceful shutdown failed", "error", err)
		}
	}()

	log.Info("gateway started", "version", version, "addr", addr,
		"game", gameAddr, "session", sessionAddr, "session_enabled", clients.SessionEnabled())
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http serve failed", "error", err)
		os.Exit(1)
	}
}

// newGraphQLServer builds the gqlgen handler with the transports we support.
func newGraphQLServer(resolver *graph.Resolver) *handler.Server {
	srv := handler.New(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.SetQueryCache(lru.New[*ast.QueryDocument](1000))
	srv.Use(extension.Introspection{})
	return srv
}
