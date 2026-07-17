// Command game-service is the authoritative game engine: it validates moves,
// persists games to PostgreSQL, and orchestrates engine replies.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/config"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/engine"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/kafkax"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/objstore"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/redisx"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/store"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/telemetry"
	authv1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/auth/v1"
	gamev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/game/v1"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/game-service/internal/server"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/game-service/internal/session"
)

var version = "dev"

// migrateWithRetry runs migrations, retrying for a while so the service can start
// before Postgres is fully ready (e.g. under Docker Compose).
func migrateWithRetry(log *slog.Logger, dsn string) error {
	var err error
	for attempt := 1; attempt <= 30; attempt++ {
		if err = store.Migrate(dsn); err == nil {
			log.Info("migrations applied")
			return nil
		}
		log.Warn("waiting for database", "attempt", attempt, "error", err)
		time.Sleep(2 * time.Second)
	}
	return err
}

func main() {
	log := config.NewLogger()

	// Observability. Both degrade to no-ops when unconfigured.
	metrics := telemetry.NewMetrics("game-service")
	tracingShutdown, err := telemetry.InitTracing(context.Background(),
		"game-service", version, config.Getenv("ACG_OTLP_ENDPOINT", ""))
	if err != nil {
		log.Warn("tracing init failed; continuing without it", "error", err)
	}
	defer func() { _ = tracingShutdown(context.Background()) }()
	metricsShutdown := telemetry.ServeMetrics(config.Getenv("ACG_METRICS_ADDR", ":9101"), metrics, log)
	defer func() { _ = metricsShutdown(context.Background()) }()

	addr := config.Getenv("ACG_GAME_ADDR", ":50051")
	dsn := config.Getenv("ACG_POSTGRES_DSN", "postgres://acg:acg@localhost:5433/acg?sslmode=disable")
	engineAddr := config.Getenv("ACG_ENGINE_ADDR", "localhost:50052")
	// Empty disables the evaluation cache; the engine is then asked every time.
	redisAddr := config.Getenv("ACG_REDIS_ADDR", "")
	// Empty disables background analysis; games are still played and stored.
	kafkaBrokers := config.Getenv("ACG_KAFKA_BROKERS", "")
	analysisDepth, _ := strconv.Atoi(config.Getenv("ACG_ANALYSIS_DEPTH", "14"))
	// Empty disables PGN archival; games are still played and stored.
	s3Endpoint := config.Getenv("ACG_S3_ENDPOINT", "")
	// Empty disables live sessions (engine-only games still work).
	sessionAddr := config.Getenv("ACG_SESSION_ADDR", "")

	// Apply migrations on startup (embedded), retrying while Postgres comes up.
	if config.Getenv("ACG_RUN_MIGRATIONS", "true") == "true" {
		if err := migrateWithRetry(log, dsn); err != nil {
			log.Error("migrations failed", "error", err)
			os.Exit(1)
		}
	}

	ctx := context.Background()
	var st *store.Store
	st, err = store.Connect(ctx, dsn)
	if err != nil {
		log.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	// Redis is a cache, not a dependency: log and carry on if it is unreachable.
	rdb, err := redisx.Dial(ctx, redisAddr)
	if err != nil {
		log.Warn("redis unavailable — evaluation cache disabled", "addr", redisAddr, "error", err)
	}
	if rdb != nil {
		defer rdb.Close()
	}
	evalCache := redisx.NewEvalCache(rdb).WithMetrics(metrics.CacheHits, metrics.CacheMisses)

	eng, err := engine.Dial(engineAddr, evalCache, log)
	if err != nil {
		log.Error("failed to dial engine-worker", "addr", engineAddr, "error", err)
		os.Exit(1)
	}
	defer eng.Close()

	// Periodically report cache effectiveness; Q4 turns these into Prometheus
	// counters, but a log line is enough to see it working today.
	go func() {
		for range time.Tick(60 * time.Second) {
			hits, misses, rate := eng.CacheStats()
			if hits+misses > 0 {
				log.Info("eval cache", "hits", hits, "misses", misses,
					"hit_rate", fmt.Sprintf("%.1f%%", rate*100))
			}
		}
	}()

	sess, err := session.Dial(sessionAddr, log)
	if err != nil {
		log.Error("failed to dial session-manager", "addr", sessionAddr, "error", err)
		os.Exit(1)
	}
	defer sess.Close()

	// Declaring the topics here rather than relying on auto-creation: the default
	// is a single partition, which would cap the worker pool at one consumer.
	if err := kafkax.EnsureTopics(ctx, kafkaBrokers, log); err != nil {
		log.Warn("could not ensure kafka topics", "error", err)
	}
	events, err := kafkax.NewProducer(kafkaBrokers, log)
	if err != nil {
		log.Error("failed to create kafka producer", "error", err)
		os.Exit(1)
	}
	defer events.Close()

	// Object storage is an archive, not a dependency: log and carry on if absent.
	objects, err := objstore.Dial(ctx, objstore.Config{
		Endpoint:  s3Endpoint,
		AccessKey: config.Getenv("ACG_S3_ACCESS_KEY", ""),
		SecretKey: config.Getenv("ACG_S3_SECRET_KEY", ""),
		UseSSL:    config.Getenv("ACG_S3_SSL", "false") == "true",
		// The endpoint a browser reaches, when it differs from the internal one.
		PublicEndpoint: config.Getenv("ACG_S3_PUBLIC_ENDPOINT", ""),
		PublicSSL:      config.Getenv("ACG_S3_PUBLIC_SSL", "false") == "true",
		Region:         config.Getenv("ACG_S3_REGION", "us-east-1"),
	})
	if err != nil {
		log.Warn("object storage unavailable — PGN archival disabled", "endpoint", s3Endpoint, "error", err)
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("failed to listen", "addr", addr, "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer(
		// otelgrpc produces the trace span; our interceptor produces the metrics.
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(metrics.UnaryServerInterceptor()),
	)
	gamev1.RegisterGameServiceServer(grpcServer, server.New(st, eng, sess, events, objects, analysisDepth, metrics, log))
	// Identity lives beside the users table but is a separate service: the
	// gateway owns sessions, this only verifies credentials.
	deliverTokens := config.Getenv("ACG_MAIL_ENABLED", "false") == "true"
	authv1.RegisterAuthServiceServer(grpcServer, server.NewAuth(st, log, deliverTokens))

	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(grpcServer, healthSrv)
	reflection.Register(grpcServer)

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-sigCtx.Done()
		log.Info("shutting down")
		grpcServer.GracefulStop()
	}()

	log.Info("game-service started", "version", version, "addr", addr,
		"engine", engineAddr, "session", sessionAddr, "session_enabled", sess.Enabled(),
		"eval_cache", evalCache.Enabled(), "kafka", kafkaBrokers, "analysis", events.Enabled(),
		"object_store", objects.Enabled())
	if err := grpcServer.Serve(lis); err != nil {
		log.Error("grpc serve failed", "error", err)
		os.Exit(1)
	}
}
