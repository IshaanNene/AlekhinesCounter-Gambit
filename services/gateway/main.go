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
	coderws "github.com/coder/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/config"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/redisx"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/graph"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/graph/generated"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/internal/auth"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/internal/pubsub"
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
	// Empty falls back to in-process fanout and no rate limiting (single replica).
	redisAddr := config.Getenv("ACG_REDIS_ADDR", "")

	// Fail fast on a missing secret rather than silently minting forgeable
	// sessions with a default one.
	secret := config.Getenv("ACG_SESSION_SECRET", "")
	if secret == "" {
		log.Error("ACG_SESSION_SECRET is required (32+ bytes)")
		os.Exit(1)
	}
	signer, err := auth.NewSigner(secret, auth.DefaultTTL,
		config.Getenv("ACG_COOKIE_SECURE", "false") == "true")
	if err != nil {
		log.Error("invalid session secret", "error", err)
		os.Exit(1)
	}

	clients, err := upstream.Dial(gameAddr, sessionAddr)
	if err != nil {
		log.Error("failed to dial upstream services", "error", err)
		os.Exit(1)
	}
	defer clients.Close()

	// Redis carries updates between gateway replicas. Without it we fall back to
	// in-process fanout, which is correct for exactly one replica: a move handled
	// here would never reach a socket held by another.
	rdb, err := redisx.Dial(context.Background(), redisAddr)
	if err != nil {
		log.Warn("redis unavailable — falling back to in-process fanout and no rate limiting",
			"addr", redisAddr, "error", err)
	}
	if rdb != nil {
		defer rdb.Close()
	}

	var bus pubsub.Bus
	if rdb != nil {
		bus = pubsub.NewRedis(rdb, log)
	} else {
		bus = pubsub.NewMemory()
	}
	defer bus.Close()

	// 20 requests/second sustained, bursting to 60: comfortably above a human
	// playing chess, low enough to blunt a script.
	limiter := redisx.NewLimiter(rdb, 20, 60)

	srv := newGraphQLServer(&graph.Resolver{
		Upstream:    clients,
		Bus:         bus,
		Signer:      signer,
		Matchmaking: redisx.NewMatchmaking(rdb),
		Log:         log,
	}, signer)

	// Order matters: auth runs first so the limiter can key on the user id and
	// only fall back to IP for anonymous callers.
	authed := signer.Middleware(auth.RateLimitMiddleware(limiter)(srv))

	mux := http.NewServeMux()
	mux.Handle("/graphql", authed)
	// Same handler: it serves POST queries and upgrades WebSocket subscriptions.
	// /ws is the documented subscription endpoint.
	mux.Handle("/ws", authed)
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
		"game", gameAddr, "session", sessionAddr, "session_enabled", clients.SessionEnabled(),
		"redis", redisAddr, "fanout", fanoutKind(rdb))
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http serve failed", "error", err)
		os.Exit(1)
	}
}

// fanoutKind names the bus in the startup log, so it is obvious at a glance
// whether this process can serve more than one replica.
func fanoutKind(rdb *redis.Client) string {
	if rdb != nil {
		return "redis"
	}
	return "in-process"
}

// newGraphQLServer builds the gqlgen handler with the transports we support.
func newGraphQLServer(resolver *graph.Resolver, signer *auth.Signer) *handler.Server {
	srv := handler.New(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.Websocket{
		Implementation: transport.CoderWebsocketImplementation{
			AcceptOptions: coderws.AcceptOptions{
				// The web client is served from a different origin in dev. Tighten
				// this to the real origin list before exposing the gateway publicly.
				InsecureSkipVerify: true,
			},
		},
		KeepAlivePingInterval: 10 * time.Second,
		// Authenticate the socket itself. The browser's cookie rides the upgrade
		// request and the HTTP middleware already handled it; this covers every
		// other client, which cannot set a cookie on a WebSocket handshake.
		InitFunc: func(ctx context.Context, initPayload transport.InitPayload) (context.Context, *transport.InitPayload, error) {
			return signer.VerifyInitPayload(ctx, initPayload), nil, nil
		},
	})
	srv.SetQueryCache(lru.New[*ast.QueryDocument](1000))
	srv.Use(extension.Introspection{})
	return srv
}
