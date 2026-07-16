package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"time"

	"google.golang.org/grpc"
)

// ServeMetrics starts an HTTP server exposing /metrics and /healthz on addr.
//
// A separate port from the gRPC service, so scraping and health checks never
// contend with request traffic and can be exposed to the cluster without
// exposing the API. Returns a shutdown func.
func ServeMetrics(addr string, m *Metrics, log *slog.Logger) Shutdown {
	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server failed", "addr", addr, "error", err)
		}
	}()
	log.Info("metrics server listening", "addr", addr)

	return func(ctx context.Context) error { return srv.Shutdown(ctx) }
}

// UnaryServerInterceptor records RED metrics for every gRPC call.
//
// Paired with otelgrpc's own interceptor (added at the server), this gives both
// halves of observability from one middleware stack: otelgrpc produces the
// trace span, this produces the metrics, and neither the handlers nor the
// callers change.
func (m *Metrics) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		// Use the bare method name to keep label cardinality low and readable.
		m.ObserveRPC(path.Base(info.FullMethod), start, err)
		return resp, err
	}
}
