// Command engine-worker is a stateless gRPC service that wraps a UCI chess
// engine (Stockfish). Any Analyze request may be routed to any replica.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/config"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/objstore"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/openingbook"
	enginev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/engine/v1"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/engine-worker/internal/server"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/engine-worker/internal/uci"
)

// bookKey is where the opening book lives in the books bucket.
const bookKey = "mainline.json"

// version is overridable at build time via -ldflags.
var version = "dev"

func main() {
	log := config.NewLogger()

	addr := config.Getenv("ACG_ENGINE_ADDR", ":50052")
	stockfishPath := config.Getenv("ACG_STOCKFISH_PATH", "stockfish")

	engine, err := uci.New(stockfishPath)
	if err != nil {
		log.Error("failed to start engine", "path", stockfishPath, "error", err)
		os.Exit(1)
	}
	defer engine.Close()

	// The opening book is enrichment for play, not a dependency. Load it from
	// object storage (seeding the bucket on first run) so every replica serves
	// the same book; fall back to the built-in seed when storage is absent.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	book := loadBook(ctx, dialObjectStore(ctx, log), log)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("failed to listen", "addr", addr, "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	enginev1.RegisterEngineServiceServer(grpcServer, server.New(engine, book))

	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(grpcServer, healthSrv)
	reflection.Register(grpcServer)

	// Graceful shutdown on SIGINT/SIGTERM (ctx is wired above).
	go func() {
		<-ctx.Done()
		log.Info("shutting down")
		grpcServer.GracefulStop()
	}()

	log.Info("engine-worker started", "version", version, "addr", addr,
		"engine", stockfishPath, "book_positions", book.Len())
	if err := grpcServer.Serve(lis); err != nil {
		log.Error("grpc serve failed", "error", err)
		os.Exit(1)
	}
}

// dialObjectStore connects to the object store for the opening book, or returns
// nil (a no-op store) when none is configured — the book then falls back to the
// built-in seed.
func dialObjectStore(ctx context.Context, log *slog.Logger) *objstore.Store {
	objects, err := objstore.Dial(ctx, objstore.Config{
		Endpoint:  config.Getenv("ACG_S3_ENDPOINT", ""),
		AccessKey: config.Getenv("ACG_S3_ACCESS_KEY", ""),
		SecretKey: config.Getenv("ACG_S3_SECRET_KEY", ""),
		UseSSL:    config.Getenv("ACG_S3_SSL", "false") == "true",
		Region:    config.Getenv("ACG_S3_REGION", "us-east-1"),
	})
	if err != nil {
		log.Warn("object storage unavailable — using the built-in opening book", "error", err)
		return nil
	}
	return objects
}

// loadBook fetches the opening book from object storage, seeding and uploading a
// default the first time so the bucket is populated and every replica serves the
// same book thereafter. Without object storage it returns the built-in seed, so
// play still gets opening variety.
func loadBook(ctx context.Context, objects *objstore.Store, log *slog.Logger) *openingbook.Book {
	if !objects.Enabled() {
		log.Info("opening book: no object storage; using the built-in seed")
		return openingbook.Seed()
	}

	gctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	data, err := objects.Get(gctx, objstore.BucketBooks, bookKey)
	cancel()
	if err == nil {
		if book, lerr := openingbook.Load(data); lerr == nil {
			log.Info("opening book loaded from object storage", "positions", book.Len(), "key", bookKey)
			return book
		} else {
			log.Warn("opening book in storage is unreadable; reseeding", "error", lerr)
		}
	}

	// Absent or unreadable: seed and upload for next time (deterministic, so a
	// racing replica writing the same bytes is harmless).
	book := openingbook.Seed()
	if blob, merr := book.Marshal(); merr == nil {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if perr := objects.Put(pctx, objstore.BucketBooks, bookKey, blob, "application/json"); perr != nil {
			log.Warn("failed to upload seed opening book", "error", perr)
		} else {
			log.Info("seeded opening book into object storage", "positions", book.Len(), "key", bookKey)
		}
		cancel()
	}
	return book
}
