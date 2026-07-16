package telemetry

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the platform's Prometheus collectors.
//
// A small, deliberate set. Prometheus rewards low cardinality, so labels are
// bounded things (service, gRPC method, outcome) — never a user or game id,
// which would mint a new time series per entity and melt the TSDB. High-
// cardinality per-entity data lives in RedisTimeSeries instead (see ADR-0003).
type Metrics struct {
	reg *prometheus.Registry

	// RED metrics for gRPC: Rate, Errors, Duration by method.
	RPCRequests *prometheus.CounterVec
	RPCDuration *prometheus.HistogramVec

	// Chess-specific gauges and counters an operator actually watches.
	GamesActive     prometheus.Gauge
	MovesTotal      prometheus.Counter
	GamesFinished   *prometheus.CounterVec // by result
	EngineAnalyses  prometheus.Counter
	CacheHits       prometheus.Counter
	CacheMisses     prometheus.Counter
	MatchmakingWait prometheus.Histogram
}

// NewMetrics builds and registers the collectors under a service label.
func NewMetrics(service string) *Metrics {
	reg := prometheus.NewRegistry()
	labels := prometheus.Labels{"service": service}

	m := &Metrics{
		reg: reg,
		RPCRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "acg_rpc_requests_total", Help: "gRPC requests by method and outcome.",
			ConstLabels: labels,
		}, []string{"method", "outcome"}),
		RPCDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "acg_rpc_duration_seconds", Help: "gRPC handler latency by method.",
			ConstLabels: labels,
			// Buckets tuned for a mix of sub-ms DB calls and multi-second engine calls.
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		}, []string{"method"}),
		GamesActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "acg_games_active", Help: "Games currently in progress.", ConstLabels: labels,
		}),
		MovesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "acg_moves_total", Help: "Moves played.", ConstLabels: labels,
		}),
		GamesFinished: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "acg_games_finished_total", Help: "Finished games by result.", ConstLabels: labels,
		}, []string{"result"}),
		EngineAnalyses: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "acg_engine_analyses_total", Help: "Positions analysed by the engine.", ConstLabels: labels,
		}),
		CacheHits: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "acg_eval_cache_hits_total", Help: "Evaluation cache hits.", ConstLabels: labels,
		}),
		CacheMisses: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "acg_eval_cache_misses_total", Help: "Evaluation cache misses.", ConstLabels: labels,
		}),
		MatchmakingWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "acg_matchmaking_wait_seconds", Help: "Time from queue to pairing.",
			ConstLabels: labels,
			Buckets:     []float64{.5, 1, 2, 5, 10, 30, 60, 120},
		}),
	}

	// Go runtime and process metrics come free and are genuinely useful (GC,
	// goroutines, memory, fds).
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.RPCRequests, m.RPCDuration, m.GamesActive, m.MovesTotal, m.GamesFinished,
		m.EngineAnalyses, m.CacheHits, m.CacheMisses, m.MatchmakingWait,
	)
	return m
}

// Handler serves the metrics for Prometheus to scrape at /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// ObserveRPC records one gRPC call's outcome and latency. err nil means success.
func (m *Metrics) ObserveRPC(method string, start time.Time, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	m.RPCRequests.WithLabelValues(method, outcome).Inc()
	m.RPCDuration.WithLabelValues(method).Observe(time.Since(start).Seconds())
}
