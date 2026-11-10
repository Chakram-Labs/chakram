package api

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds Prometheus gauges and counters for the Node API.
type Metrics struct {
	ChainHeight    prometheus.Gauge
	PeerCount      prometheus.Gauge
	CatchingUp     prometheus.Gauge
	APIRequests    *prometheus.CounterVec
	FaucetDrips    prometheus.Counter
}

func newMetrics(reg prometheus.Registerer) *Metrics {
	return &Metrics{
		ChainHeight: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Namespace: "chakram",
			Name:      "chain_height",
			Help:      "Latest committed block height.",
		}),
		PeerCount: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Namespace: "chakram",
			Name:      "peer_count",
			Help:      "Number of connected CometBFT peers.",
		}),
		CatchingUp: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Namespace: "chakram",
			Name:      "catching_up",
			Help:      "1 if the node is catching up via blocksync, 0 if in consensus.",
		}),
		APIRequests: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Namespace: "chakram",
			Name:      "api_requests_total",
			Help:      "Total Node API HTTP requests by path and status.",
		}, []string{"path", "status"}),
		FaucetDrips: promauto.With(reg).NewCounter(prometheus.CounterOpts{
			Namespace: "chakram",
			Name:      "faucet_drips_total",
			Help:      "Total faucet transactions sent (dev mode).",
		}),
	}
}
