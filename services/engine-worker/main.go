// Command engine-worker is a stateless gRPC service that wraps a UCI chess
// engine (Stockfish). Any Analyze request may be routed to any replica.
package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/config"
	enginev1 "github.com/IshaanNene/AlekhinesCounter-Gambit/proto/gen/go/engine/v1"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/engine-worker/internal/server"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/engine-worker/internal/uci"
)

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

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("failed to listen", "addr", addr, "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	enginev1.RegisterEngineServiceServer(grpcServer, server.New(engine))

	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(grpcServer, healthSrv)
	reflection.Register(grpcServer)

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		log.Info("shutting down")
		grpcServer.GracefulStop()
	}()

	log.Info("engine-worker started", "version", version, "addr", addr, "engine", stockfishPath)
	if err := grpcServer.Serve(lis); err != nil {
		log.Error("grpc serve failed", "error", err)
		os.Exit(1)
	}
}
