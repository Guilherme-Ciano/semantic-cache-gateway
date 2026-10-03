package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	"go.uber.org/zap"
)

// Result is returned by Lookup and signals whether the entry came from cache.
type Result struct {
	Entry  domain.CacheEntry
	IsHit  bool
}

// Service orchestrates the semantic cache logic, decoupled from any
// particular embedding model or vector store implementation.
type Service struct {
	embedder  domain.EmbedderPort
	store     domain.VectorStorePort
	llm       domain.LLMPort
	threshold float64
	ttl       time.Duration
	limit     uint64
	log       *zap.Logger
}

// NewService constructs a Service with the required ports and cache parameters.
func NewService(
	embedder domain.EmbedderPort,
	store domain.VectorStorePort,
	llm domain.LLMPort,
	threshold float64,
	ttl time.Duration,
	limit uint64,
	log *zap.Logger,
) *Service {
	return &Service{
		embedder:  embedder,
		store:     store,
		llm:       llm,
		threshold: threshold,
		ttl:       ttl,
		limit:     limit,
		log:       log,
	}
}

// Handle processes an LLM request by first consulting the semantic cache.
// On a miss it calls the upstream LLM and stores the result asynchronously.
func (s *Service) Handle(ctx context.Context, req domain.LLMRequest) (domain.LLMResponse, Result, error) {
	queryText := extractQueryText(req)

	embedding, err := s.embedder.Embed(ctx, queryText)
	if err != nil {
		return domain.LLMResponse{}, Result{}, fmt.Errorf("embedding query: %w", err)
	}

	candidates, err := s.store.Search(ctx, embedding, s.limit, s.threshold)
	if err != nil {
		s.log.Warn("vector store search failed, falling through to LLM", zap.Error(err))
	}

	if len(candidates) > 0 {
		hit := candidates[0]
		s.log.Info("cache hit",
			zap.String("entry_id", hit.ID.String()),
			zap.Float32("score", hit.Score),
		)
		return domain.LLMResponse{Content: hit.Response, Raw: []byte(hit.Response)},
			Result{Entry: hit, IsHit: true},
			nil
	}

	resp, err := s.llm.Complete(ctx, req)
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

	go s.persistAsync(entry)

	return resp, Result{Entry: entry, IsHit: false}, nil
}

func (s *Service) persistAsync(entry domain.CacheEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.store.Upsert(ctx, entry); err != nil {
		s.log.Error("failed to persist cache entry",
			zap.String("entry_id", entry.ID.String()),
			zap.Error(err),
		)
	}
}

// extractQueryText produces a canonical string from an LLMRequest for embedding.
// It concatenates the content of all user-role messages.
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
