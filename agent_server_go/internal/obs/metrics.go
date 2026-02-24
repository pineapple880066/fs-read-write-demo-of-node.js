package obs

import (
	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	// 使用自定义 Registry，避免默认全局注册器重复注册冲突
	Registry          *prometheus.Registry
	HTTPRequestsTotal *prometheus.CounterVec
	HTTPDurationMs    *prometheus.HistogramVec
	RetrievalHits     prometheus.Histogram
}

func NewMetrics() *Metrics {
	// 统一在一个地方创建并注册所有 Prometheus 指标
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

	// MustRegister 在重复注册时会 panic，因此这里统一集中管理
	reg.MustRegister(httpRequestsTotal, httpDurationMs, retrievalHits)

	return &Metrics{
		Registry:          reg,
		HTTPRequestsTotal: httpRequestsTotal,
		HTTPDurationMs:    httpDurationMs,
		RetrievalHits:     retrievalHits,
	}
}
