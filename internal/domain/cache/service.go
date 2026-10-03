package cache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
)

type Result struct {
	Entry domain.CacheEntry
	IsHit bool
}

type Service struct {
	embedder  domain.EmbedderPort
	store     domain.VectorStorePort
	llm       domain.LLMPort
	metrics   domain.MetricsRecorder
	threshold float64
	ttl       time.Duration
	limit     uint64
	log       *slog.Logger
	wg        sync.WaitGroup
}

func NewService(
	embedder domain.EmbedderPort,
	store domain.VectorStorePort,
	llm domain.LLMPort,
	metrics domain.MetricsRecorder,
	threshold float64,
	ttl time.Duration,
	limit uint64,
	log *slog.Logger,
) *Service {
	return &Service{
		embedder:  embedder,
		store:     store,
		llm:       llm,
		metrics:   metrics,
		threshold: threshold,
		ttl:       ttl,
		limit:     limit,
		log:       log,
	}
}

// Handle processes an LLM request through the semantic cache.
func (s *Service) Handle(ctx context.Context, req domain.LLMRequest) (domain.LLMResponse, Result, error) {
	queryText := extractQueryText(req)

	embedStart := time.Now()
	embedding, err := s.embedder.Embed(ctx, queryText)
	s.metrics.RecordEmbedDuration(time.Since(embedStart))
	if err != nil {
		return domain.LLMResponse{}, Result{}, fmt.Errorf("embedding query: %w", err)
	}

	candidates, err := s.store.Search(ctx, embedding, s.limit, s.threshold)
	if err != nil {
		s.metrics.RecordStoreError()
		if errors.Is(err, domain.ErrCircuitOpen) {
			s.log.WarnContext(ctx, "vector store circuit open, bypassing cache")
		} else {
			s.log.WarnContext(ctx, "vector store search failed, falling through to LLM",
				slog.String("error", err.Error()),
			)
		}
	}

	if len(candidates) > 0 {
		hit := candidates[0]
		s.metrics.RecordCacheHit()
		s.log.InfoContext(ctx, "cache hit",
			slog.String("entry_id", hit.ID.String()),
			slog.Float64("score", float64(hit.Score)),
		)
		return domain.LLMResponse{Content: hit.Response, Raw: []byte(hit.Response)},
			Result{Entry: hit, IsHit: true},
			nil
	}

	s.metrics.RecordCacheMiss()

	llmStart := time.Now()
	resp, err := s.llm.Complete(ctx, req)
	s.metrics.RecordLLMDuration(time.Since(llmStart))
	if err != nil {
		return domain.LLMResponse{}, Result{}, fmt.Errorf("upstream LLM: %w", err)
	}

	entry := domain.CacheEntry{
		ID:        uuid.New(),
		Query:     queryText,
		Response:  resp.Content,
		Embedding: embedding,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(s.ttl),
	}

	s.schedulePersist(entry)

	return resp, Result{Entry: entry, IsHit: false}, nil
}

// Drain blocks until all in-flight persist goroutines complete.
func (s *Service) Drain() {
	s.wg.Wait()
}

func (s *Service) schedulePersist(entry domain.CacheEntry) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := s.store.Upsert(ctx, entry); err != nil {
			if !errors.Is(err, domain.ErrCircuitOpen) {
				s.log.Error("failed to persist cache entry",
					slog.String("entry_id", entry.ID.String()),
					slog.String("error", err.Error()),
				)
			}
		}
	}()
}

// extractQueryText extracts user content or falls back to raw request bytes.
func extractQueryText(req domain.LLMRequest) string {
	if len(req.Messages) == 0 {
		return string(req.Raw)
	}

	var buf []byte
	for _, m := range req.Messages {
		if m.Role == "user" {
			if len(buf) > 0 {
				buf = append(buf, '\n')
			}
			buf = append(buf, m.Content...)
		}
	}

	if len(buf) == 0 {
		for _, m := range req.Messages {
			buf = append(buf, m.Content...)
		}
	}

	return string(buf)
}

