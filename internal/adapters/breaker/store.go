package breaker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Guilherme-Ciano/semantic-cache-gateway/internal/domain"
	"github.com/sony/gobreaker"
)

type StoreConfig struct {
	MaxRequests      uint32
	Interval         time.Duration
	Timeout          time.Duration
	FailureThreshold uint32
}

type Store struct {
	inner   domain.VectorStorePort
	breaker *gobreaker.CircuitBreaker
	metrics domain.MetricsRecorder
	log     *slog.Logger
}

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

func (s *Store) EnsureCollection(ctx context.Context, dimension uint64) error {
	return s.inner.EnsureCollection(ctx, dimension)
}
