package telemetry

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "scg"

type Metrics struct {
	cacheHits    prometheus.Counter
	cacheMisses  prometheus.Counter
	llmDuration  prometheus.Histogram
	storeErrors  prometheus.Counter
	embedLatency prometheus.Histogram
	breakerTrips *prometheus.CounterVec
}

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
			Help:      "Total number of errors returned by the vector store (includes open-circuit fast-fails).",
		}),
		embedLatency: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "embed_duration_seconds",
			Help:      "Latency of embedding calls in seconds.",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.0},
		}),
		breakerTrips: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "circuit_breaker_trips_total",
			Help:      "Total number of circuit breaker transitions to the open state, by component.",
		}, []string{"component"}),
	}
}

func (m *Metrics) RecordCacheHit()                     { m.cacheHits.Inc() }
func (m *Metrics) RecordCacheMiss()                    { m.cacheMisses.Inc() }
func (m *Metrics) RecordLLMDuration(d time.Duration)   { m.llmDuration.Observe(d.Seconds()) }
func (m *Metrics) RecordStoreError()                   { m.storeErrors.Inc() }
func (m *Metrics) RecordEmbedDuration(d time.Duration) { m.embedLatency.Observe(d.Seconds()) }
func (m *Metrics) RecordBreakerTrip(component string)  { m.breakerTrips.WithLabelValues(component).Inc() }

func Handler() http.Handler { return promhttp.Handler() }

