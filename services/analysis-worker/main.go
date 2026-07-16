// Command analysis-worker consumes analysis requests from Kafka, evaluates each
// position with the engine, and stores a game report.
//
// It is one member of a consumer group: run N of these and Kafka divides the
// partitions between them. That is the whole scaling story — no coordination,
// no change to the producer, just more replicas.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/config"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/engine"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/kafkax"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/redisx"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/store"
	analysisv1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/analysis/v1"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/analysis-worker/internal/worker"
)

var version = "dev"

// consumerGroup is shared by every replica, so they split the work rather than
// each analysing every game.
const consumerGroup = "analysis-workers"

func main() {
	log := config.NewLogger()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	brokers := config.Getenv("ACG_KAFKA_BROKERS", "")
	if brokers == "" {
		log.Error("ACG_KAFKA_BROKERS is required: this service exists to consume the queue")
		os.Exit(1)
	}
	dsn := config.Getenv("ACG_POSTGRES_DSN", "postgres://acg:acg@localhost:5433/acg?sslmode=disable")
	engineAddr := config.Getenv("ACG_ENGINE_ADDR", "localhost:50052")
	redisAddr := config.Getenv("ACG_REDIS_ADDR", "")
	depth, _ := strconv.Atoi(config.Getenv("ACG_ANALYSIS_DEPTH", "14"))

	// Postgres and the engine are hard requirements; Redis is enrichment.
	st, err := connectStore(ctx, log, dsn)
	if err != nil {
		log.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	rdb, err := redisx.Dial(ctx, redisAddr)
	if err != nil {
		log.Warn("redis unavailable — no eval cache, novelty, or fair-play signals",
			"addr", redisAddr, "error", err)
	}
	if rdb != nil {
		defer rdb.Close()
	}

	// The eval cache matters most here: analysis re-walks openings every game, so
	// most early positions are already known.
	evalCache := redisx.NewEvalCache(rdb)
	eng, err := engine.Dial(engineAddr, evalCache, log)
	if err != nil {
		log.Error("failed to dial engine-worker", "addr", engineAddr, "error", err)
		os.Exit(1)
	}
	defer eng.Close()

	w := worker.New(eng, st, redisx.NewNovelty(rdb), redisx.NewIntegrity(rdb), depth, log)

	producer, err := kafkax.NewProducer(brokers, log)
	if err != nil {
		log.Error("failed to create kafka producer", "error", err)
		os.Exit(1)
	}
	defer producer.Close()

	consumer, err := kafkax.NewConsumer(brokers, consumerGroup, log, kafkax.TopicAnalysisRequested)
	if err != nil {
		log.Error("failed to join consumer group", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	log.Info("analysis-worker started", "version", version, "brokers", brokers,
		"group", consumerGroup, "engine", engineAddr, "depth", depth,
		"eval_cache", evalCache.Enabled())

	err = consumer.Run(ctx, func(ctx context.Context, key string, value []byte) error {
		var req analysisv1.AnalysisRequested
		if err := proto.Unmarshal(value, &req); err != nil {
			// A malformed record will never parse, so retrying it forever would
			// wedge the partition. Log and let the offset advance.
			log.Error("dropping unparseable request", "key", key, "error", err)
			return nil
		}

		started := time.Now()
		report, err := w.Analyze(ctx, &req)
		if err != nil {
			// Returning the error leaves the offset uncommitted, so the game is
			// retried — the right call for a transient engine or network fault.
			return err
		}
		if err := st.SaveAnalysis(ctx, report); err != nil {
			return err
		}
		// Best-effort: the report is already durable, so a failed notification
		// must not cause the whole game to be analysed again.
		if err := producer.Publish(ctx, kafkax.TopicAnalysisCompleted, report.GetGameId(), report); err != nil {
			log.Warn("failed to publish completion", "game_id", report.GetGameId(), "error", err)
		}

		hits, misses, rate := eng.CacheStats()
		log.Info("game analysed",
			"game_id", report.GetGameId(),
			"moves", len(report.GetMoves()),
			"white_accuracy", int(report.GetWhite().GetAccuracy()),
			"black_accuracy", int(report.GetBlack().GetAccuracy()),
			"novelty_ply", report.GetNoveltyPly(),
			"took", time.Since(started).Round(time.Millisecond).String(),
			"cache_hits", hits, "cache_misses", misses, "hit_rate", rate)
		return nil
	})
	if err != nil {
		log.Error("consumer stopped", "error", err)
		os.Exit(1)
	}
	log.Info("analysis-worker stopped")
}

// connectStore retries while Postgres comes up under Compose.
func connectStore(ctx context.Context, log *slog.Logger, dsn string) (*store.Store, error) {
	var lastErr error
	for attempt := 1; attempt <= 30; attempt++ {
		st, err := store.Connect(ctx, dsn)
		if err == nil {
			return st, nil
		}
		lastErr = err
		log.Warn("waiting for database", "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, lastErr
}
