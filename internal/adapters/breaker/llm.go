// Package breaker provides Circuit Breaker decorators for domain ports.
// Each decorator wraps an existing port implementation and adds fault detection,
// open-circuit fast-fail, and half-open recovery without touching domain logic.
package breaker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	"github.com/sony/gobreaker"
)

// LLMConfig parameterises the circuit breaker protecting the upstream LLM.
type LLMConfig struct {
	// MaxRequests is the number of requests allowed in half-open state.
	MaxRequests uint32
	// Interval is the rolling window duration for the closed-state error counter.
	Interval gobreaker.DurationFunc
	// Timeout is how long the breaker stays open before probing again.
	Timeout gobreaker.DurationFunc
	// FailureThreshold is the number of consecutive failures that trip the breaker.
	FailureThreshold uint32
}

// LLM decorates domain.LLMPort with a circuit breaker.
// When the breaker is open it returns domain.ErrCircuitOpen immediately,
// preventing cascading failures toward the upstream provider.
type LLM struct {
	inner   domain.LLMPort
	breaker *gobreaker.CircuitBreaker
	metrics domain.MetricsRecorder
	log     *slog.Logger
}

// NewLLM wraps inner with a circuit breaker configured by cfg.
func NewLLM(inner domain.LLMPort, cfg LLMConfig, metrics domain.MetricsRecorder, log *slog.Logger) *LLM {
	name := "llm"
	threshold := cfg.FailureThreshold
	if threshold == 0 {
		threshold = 5
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
			log.Warn("LLM circuit state changed",
				slog.String("from", from.String()),
				slog.String("to", to.String()),
			)
			if to == gobreaker.StateOpen {
				metrics.RecordBreakerTrip(name)
			}
		},
	}

	return &LLM{
		inner:   inner,
		breaker: gobreaker.NewCircuitBreaker(settings),
		metrics: metrics,
		log:     log,
	}
}

// Complete executes the wrapped LLM call through the circuit breaker.
// Returns domain.ErrCircuitOpen when the breaker is in open state.
func (l *LLM) Complete(ctx context.Context, req domain.LLMRequest) (domain.LLMResponse, error) {
	result, err := l.breaker.Execute(func() (any, error) {
		return l.inner.Complete(ctx, req)
	})
	if err != nil {
		if isOpenErr(err) {
			return domain.LLMResponse{}, domain.ErrCircuitOpen
		}
		return domain.LLMResponse{}, fmt.Errorf("llm breaker: %w", err)
	}
	resp, ok := result.(domain.LLMResponse)
	if !ok {
		return domain.LLMResponse{}, fmt.Errorf("llm breaker: unexpected result type %T", result)
	}
	return resp, nil
}

func isOpenErr(err error) bool {
	return errors.Is(err, gobreaker.ErrOpenState) ||
		errors.Is(err, gobreaker.ErrTooManyRequests)
}
