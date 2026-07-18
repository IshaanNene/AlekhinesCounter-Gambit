// Command fanout broadcasts live game moves to spectators over WebSocket.
//
// It reads the per-game move-event streams that game-service publishes (via the
// transactional outbox) and fans each move out to everyone watching that game,
// with one Redis reader per game no matter how large the crowd. It holds no
// game state of its own and depends only on Redis, so it scales independently of
// the play path.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/config"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/eventlog"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/pkg/redisx"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/fanout/internal/hub"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/fanout/internal/metrics"
	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/fanout/internal/server"
)

func main() {
	log := config.NewLogger()

	addr := config.Getenv("ACG_FANOUT_ADDR", ":8090")
	metricsAddr := config.Getenv("ACG_METRICS_ADDR", ":9101")
	redisAddr := config.Getenv("ACG_REDIS_ADDR", "")

	ctx := context.Background()
	rdb, err := redisx.Dial(ctx, redisAddr)
	if err != nil {
		// Fanout is pointless without the event stream, but degrade rather than
		// crash-loop: serve health, and start delivering the moment Redis returns.
		log.Warn("redis unavailable — fanout will deliver nothing until it is reachable",
			"addr", redisAddr, "error", err)
	}
	if rdb != nil {
		defer rdb.Close()
	}

	m := metrics.New()
	stream := eventlog.NewStream(rdb, log)
	mgr := hub.NewManager(stream, m, log)
	srv := server.New(mgr, log)

	// Metrics + health on their own port, matching the other services (probes and
	// Prometheus scrape the same surface).
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", m.Handler())
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	metricsServer := &http.Server{Addr: metricsAddr, Handler: metricsMux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server failed", "error", err)
		}
	}()

	// The WebSocket server has no read/write timeout: spectator connections are
	// long-lived and mostly idle, and per-message write deadlines live in the
	// handler instead.
	wsServer := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-sigCtx.Done()
		log.Info("shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = wsServer.Shutdown(shutCtx)
		_ = metricsServer.Shutdown(shutCtx)
	}()

	log.Info("fanout started", "addr", addr, "metrics", metricsAddr, "redis", redisAddr)
	if err := wsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("fanout server failed", "error", err)
		os.Exit(1)
	}
}
