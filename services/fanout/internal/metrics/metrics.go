// Package metrics is the fanout service's Prometheus instrumentation. It
// implements hub.Metrics, so the hub reports lifecycle events straight into the
// counters and gauges that drive the "spectators" panels on the Grafana wall.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the fanout gauges/counters and their private registry.
type Metrics struct {
	reg *prometheus.Registry

	hubs   prometheus.Gauge   // live game hubs (one per watched game)
	subs   prometheus.Gauge   // connected spectators, across all games
	deltas prometheus.Counter // moves broadcast
	drops  prometheus.Counter // spectators dropped for falling behind
}

// New builds the fanout metrics, labelled by service like the other services.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	labels := prometheus.Labels{"service": "fanout"}
	m := &Metrics{
		reg: reg,
		hubs: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "acg_fanout_hubs", Help: "Live game fanout hubs.", ConstLabels: labels,
		}),
		subs: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "acg_fanout_spectators", Help: "Connected spectators.", ConstLabels: labels,
		}),
		deltas: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "acg_fanout_deltas_total", Help: "Move deltas broadcast to spectators.", ConstLabels: labels,
		}),
		drops: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "acg_fanout_dropped_total", Help: "Spectators dropped for falling behind.", ConstLabels: labels,
		}),
	}
	reg.MustRegister(m.hubs, m.subs, m.deltas, m.drops)
	return m
}

// hub.Metrics implementation.

func (m *Metrics) HubOpened()      { m.hubs.Inc() }
func (m *Metrics) HubClosed()      { m.hubs.Dec() }
func (m *Metrics) SubAdded()       { m.subs.Inc() }
func (m *Metrics) SubRemoved()     { m.subs.Dec() }
func (m *Metrics) DeltaBroadcast() { m.deltas.Inc() }
func (m *Metrics) SubDropped()     { m.drops.Inc() }

// Handler serves the metrics for Prometheus to scrape.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
