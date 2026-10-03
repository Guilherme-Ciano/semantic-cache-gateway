package breaker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	"github.com/sony/gobreaker"
)

// StoreConfig parameterises the circuit breaker protecting the vector store.
type StoreConfig struct {
	MaxRequests      uint32
	Interval         gobreaker.DurationFunc
	Timeout          gobreaker.DurationFunc
	FailureThreshold uint32
}

// Store decorates domain.VectorStorePort with a circuit breaker.
//
// Fallback strategy: when the breaker is open, Search returns
// (nil, domain.ErrCircuitOpen). The cache service interprets any search
// error as a miss and routes the request to the LLM transparently — the
// client never observes the vector store outage.
//
// Upsert failures when the breaker is open are silently dropped; the entry
// will be re-populated on the next successful request.
type Store struct {
	inner   domain.VectorStorePort
	breaker *gobreaker.CircuitBreaker
	metrics domain.MetricsRecorder
	log     *slog.Logger
}

// NewStore wraps inner with a circuit breaker configured by cfg.
func NewStore(inner domain.VectorStorePort, cfg StoreConfig, metrics domain.MetricsRecorder, log *slog.Logger) *Store {
	name := "vector_db"
	threshold := cfg.FailureThreshold
	if threshold == 0 {
		threshold = 3
	}

	settings := gobreaker.Settings{
		Name:        name,
		MaxRequests: cfg.MaxRequests,
		Interval:    cfg.Interval,
		Timeout:     cfg.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= threshold
		},
		OnStateChange: func(_ string, from, to gobreaker.State) {
			log.Warn("vector store circuit state changed",
				slog.String("from", from.String()),
				slog.String("to", to.String()),
			)
			if to == gobreaker.StateOpen {
				metrics.RecordBreakerTrip(name)
			}
		},
	}

	return &Store{
		inner:   inner,
		breaker: gobreaker.NewCircuitBreaker(settings),
		metrics: metrics,
		log:     log,
	}
}

// Search runs a similarity query through the circuit breaker.
// Returns (nil, domain.ErrCircuitOpen) when the circuit is open so that the
// cache service can apply the LLM fallback path without any additional branching.
func (s *Store) Search(ctx context.Context, query domain.Vector, limit uint64, threshold float64) ([]domain.CacheEntry, error) {
	result, err := s.breaker.Execute(func() (any, error) {
		return s.inner.Search(ctx, query, limit, threshold)
	})
	if err != nil {
		if isOpenErr(err) {
			return nil, domain.ErrCircuitOpen
		}
		return nil, fmt.Errorf("store breaker: %w", err)
	}
	entries, ok := result.([]domain.CacheEntry)
	if !ok {
		return nil, fmt.Errorf("store breaker: unexpected result type %T", result)
	}
	return entries, nil
}

// Upsert writes an entry through the circuit breaker.
// When the circuit is open the write is a no-op; the entry is not buffered.
func (s *Store) Upsert(ctx context.Context, entry domain.CacheEntry) error {
	_, err := s.breaker.Execute(func() (any, error) {
		return nil, s.inner.Upsert(ctx, entry)
	})
	if err != nil {
		if isOpenErr(err) {
			return domain.ErrCircuitOpen
		}
		return fmt.Errorf("store breaker: %w", err)
	}
	return nil
}

// EnsureCollection delegates directly to the inner store, bypassing the breaker,
// as collection setup is a startup concern that must always succeed.
func (s *Store) EnsureCollection(ctx context.Context, dimension uint64) error {
	return s.inner.EnsureCollection(ctx, dimension)
}
