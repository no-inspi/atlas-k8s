package server

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/no-inspi/cluster-atlas/internal/stream"
)

// MetricsHandler expose les métriques du backend. Il est servi sur un port à
// part (--metrics-addr) que l'Ingress ne publie pas. Registre dédié : plusieurs
// instances (tests) ne se marchent pas dessus.
func MetricsHandler(hub *stream.Hub) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "atlas_stream_clients", Help: "Navigateurs connectés à /api/stream."},
			func() float64 { return float64(hub.Stats().Clients) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "atlas_stream_rev", Help: "Révision courante de l'état diffusé."},
			func() float64 { return float64(hub.Stats().Rev) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "atlas_stream_messages_total", Help: "Messages écrits sur les WebSockets du flux."},
			func() float64 { return float64(hub.Stats().MessagesSent) }),
	)
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
