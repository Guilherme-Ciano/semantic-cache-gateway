package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain/cache"
	"go.uber.org/zap"
)

// ── Stubs ─────────────────────────────────────────────────────────────────

type stubEmbedder struct{ vec domain.Vector }

func (s *stubEmbedder) Embed(_ context.Context, _ string) (domain.Vector, error) {
	return s.vec, nil
}

type stubStore struct {
	results []domain.CacheEntry
	upserted []domain.CacheEntry
	searchErr error
}

func (s *stubStore) Search(_ context.Context, _ domain.Vector, _ uint64, _ float64) ([]domain.CacheEntry, error) {
	return s.results, s.searchErr
}

func (s *stubStore) Upsert(_ context.Context, e domain.CacheEntry) error {
	s.upserted = append(s.upserted, e)
	return nil
}

func (s *stubStore) EnsureCollection(_ context.Context, _ uint64) error { return nil }

type stubLLM struct {
	resp domain.LLMResponse
	err  error
}

func (s *stubLLM) Complete(_ context.Context, _ domain.LLMRequest) (domain.LLMResponse, error) {
	return s.resp, s.err
}

// ── Helpers ────────────────────────────────────────────────────────────────

func newService(embedder domain.EmbedderPort, store domain.VectorStorePort, llm domain.LLMPort) *cache.Service {
	return cache.NewService(embedder, store, llm, 0.92, 24*time.Hour, 5, zap.NewNop())
}

func baseRequest() domain.LLMRequest {
	return domain.LLMRequest{
		Messages: []domain.Message{{Role: "user", Content: "What is the capital of France?"}},
	}
}

// ── Tests ──────────────────────────────────────────────────────────────────

func TestHandle_CacheHit(t *testing.T) {
	cachedEntry := domain.CacheEntry{
		ID:       uuid.New(),
		Response: "Paris",
		Score:    0.97,
	}

	store := &stubStore{results: []domain.CacheEntry{cachedEntry}}
	llm   := &stubLLM{err: errors.New("should not be called")}
	svc   := newService(&stubEmbedder{vec: make(domain.Vector, 4)}, store, llm)

	resp, result, err := svc.Handle(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsHit {
		t.Fatal("expected cache hit")
	}
	if resp.Content != "Paris" {
		t.Fatalf("expected %q, got %q", "Paris", resp.Content)
	}
}

func TestHandle_CacheMiss_CallsLLM(t *testing.T) {
	store := &stubStore{results: nil}
	llm   := &stubLLM{resp: domain.LLMResponse{Content: "Paris", Raw: []byte("Paris")}}
	svc   := newService(&stubEmbedder{vec: make(domain.Vector, 4)}, store, llm)

	resp, result, err := svc.Handle(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsHit {
		t.Fatal("expected cache miss")
	}
	if resp.Content != "Paris" {
		t.Fatalf("expected %q, got %q", "Paris", resp.Content)
	}
}

func TestHandle_StoreSearchError_FallsThrough(t *testing.T) {
	store := &stubStore{searchErr: errors.New("redis down")}
	llm   := &stubLLM{resp: domain.LLMResponse{Content: "Paris", Raw: []byte("Paris")}}
	svc   := newService(&stubEmbedder{vec: make(domain.Vector, 4)}, store, llm)

	_, result, err := svc.Handle(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsHit {
		t.Fatal("search error should produce a cache miss")
	}
}

func TestHandle_LLMError_PropagatesError(t *testing.T) {
	store := &stubStore{}
	llm   := &stubLLM{err: errors.New("openai timeout")}
	svc   := newService(&stubEmbedder{vec: make(domain.Vector, 4)}, store, llm)

	_, _, err := svc.Handle(context.Background(), baseRequest())
	if err == nil {
		t.Fatal("expected error from LLM to propagate")
	}
}
