package breaker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	"github.com/sony/gobreaker"
)

type LLMConfig struct {
	MaxRequests      uint32
	Interval         gobreaker.DurationFunc
	Timeout          gobreaker.DurationFunc
	FailureThreshold uint32
}

type LLM struct {
	inner   domain.LLMPort
	breaker *gobreaker.CircuitBreaker
	metrics domain.MetricsRecorder
	log     *slog.Logger
}

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

