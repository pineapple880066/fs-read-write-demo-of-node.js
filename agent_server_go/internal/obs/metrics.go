package obs

import (
	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	Registry          *prometheus.Registry
	HTTPRequestsTotal *prometheus.CounterVec
	HTTPDurationMs    *prometheus.HistogramVec
	RetrievalHits     prometheus.Histogram
}

func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	httpRequestsTotal := prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "agent_http_requests_total", Help: "HTTP request count"},
			[]string{"path", "method", "status"},
		)
	httpDurationMs := prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "agent_http_duration_ms", Help: "HTTP request duration(ms)", Buckets: []float64{10, 20, 50, 100, 200, 500, 1000, 2000, 5000}},
			[]string{"path", "method"},
		)
	retrievalHits := prometheus.NewHistogram(
			prometheus.HistogramOpts{Name: "agent_retrieval_hits", Help: "retrieved hits count", Buckets: []float64{1, 3, 5, 8, 10, 20, 40}},
		)

	reg.MustRegister(httpRequestsTotal, httpDurationMs, retrievalHits)

	return &Metrics{
		Registry:          reg,
		HTTPRequestsTotal: httpRequestsTotal,
		HTTPDurationMs:    httpDurationMs,
		RetrievalHits:     retrievalHits,
	}
}
