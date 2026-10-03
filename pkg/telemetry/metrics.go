// Package telemetry owns Prometheus metric registration and provides a
// Recorder that the domain service uses without depending on Prometheus directly.
package telemetry

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "scg"

// Metrics holds all instrumented Prometheus descriptors for the gateway.
type Metrics struct {
	cacheHits    prometheus.Counter
	cacheMisses  prometheus.Counter
	llmDuration  prometheus.Histogram
	storeErrors  prometheus.Counter
	embedLatency prometheus.Histogram
}

// New registers all metrics with the default Prometheus registry and returns
// a ready Metrics instance. Panic on registration conflict is acceptable at
// startup; it indicates a wiring bug, not a runtime condition.
func New() *Metrics {
	return &Metrics{
		cacheHits: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "semantic_cache_hits_total",
			Help:      "Total number of requests served from the semantic cache.",
		}),
		cacheMisses: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "semantic_cache_misses_total",
			Help:      "Total number of requests that bypassed the cache and hit the upstream LLM.",
		}),
		llmDuration: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "llm_request_duration_seconds",
			Help:      "End-to-end latency of upstream LLM calls in seconds.",
			Buckets:   prometheus.DefBuckets,
		}),
		storeErrors: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "vector_store_errors_total",
			Help:      "Total number of errors returned by the vector store.",
		}),
		embedLatency: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "embed_duration_seconds",
			Help:      "Latency of embedding calls in seconds.",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.0},
		}),
	}
}

// RecordCacheHit increments the cache hit counter.
func (m *Metrics) RecordCacheHit() { m.cacheHits.Inc() }

// RecordCacheMiss increments the cache miss counter.
func (m *Metrics) RecordCacheMiss() { m.cacheMisses.Inc() }

// RecordLLMDuration records a completed LLM call latency.
func (m *Metrics) RecordLLMDuration(d time.Duration) {
	m.llmDuration.Observe(d.Seconds())
}

// RecordStoreError increments the vector store error counter.
func (m *Metrics) RecordStoreError() { m.storeErrors.Inc() }

// RecordEmbedDuration records an embedding call latency.
func (m *Metrics) RecordEmbedDuration(d time.Duration) {
	m.embedLatency.Observe(d.Seconds())
}

// Handler returns the standard Prometheus HTTP handler for /metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}
