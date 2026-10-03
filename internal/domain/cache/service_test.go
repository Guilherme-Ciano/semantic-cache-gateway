package cache_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain/cache"
)

type mockEmbedder struct {
	vec domain.Vector
	err error
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) (domain.Vector, error) {
	return m.vec, m.err
}

type mockStore struct {
	results   []domain.CacheEntry
	searchErr error
	upsertErr error
	upserted  []domain.CacheEntry
}

func (m *mockStore) Search(_ context.Context, _ domain.Vector, _ uint64, _ float64) ([]domain.CacheEntry, error) {
	return m.results, m.searchErr
}

func (m *mockStore) Upsert(_ context.Context, e domain.CacheEntry) error {
	m.upserted = append(m.upserted, e)
	return m.upsertErr
}

func (m *mockStore) EnsureCollection(_ context.Context, _ uint64) error { return nil }

type mockLLM struct {
	resp  domain.LLMResponse
	err   error
	calls int
}

func (m *mockLLM) Complete(_ context.Context, _ domain.LLMRequest) (domain.LLMResponse, error) {
	m.calls++
	return m.resp, m.err
}

type mockMetrics struct {
	hits           int
	misses         int
	llmDurations   []time.Duration
	storeErrors    int
	embedDurations []time.Duration
}

func (m *mockMetrics) RecordCacheHit()                     { m.hits++ }
func (m *mockMetrics) RecordCacheMiss()                    { m.misses++ }
func (m *mockMetrics) RecordLLMDuration(d time.Duration)   { m.llmDurations = append(m.llmDurations, d) }
func (m *mockMetrics) RecordStoreError()                   { m.storeErrors++ }
func (m *mockMetrics) RecordEmbedDuration(d time.Duration) { m.embedDurations = append(m.embedDurations, d) }
func (m *mockMetrics) RecordBreakerTrip(_ string)          {}

var defaultVec = domain.Vector{0.1, 0.2, 0.3, 0.4}

func cachedEntry(response string, score float32) domain.CacheEntry {
	return domain.CacheEntry{
		ID:        uuid.New(),
		Query:     "What is the capital of France?",
		Response:  response,
		Embedding: defaultVec,
		Score:     score,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
}

func newService(embedder domain.EmbedderPort, store domain.VectorStorePort, llm domain.LLMPort, rec domain.MetricsRecorder) *cache.Service {
	return cache.NewService(embedder, store, llm, rec, 0.92, 24*time.Hour, 5, slog.Default())
}

func baseRequest() domain.LLMRequest {
	return domain.LLMRequest{
		Model:    "gpt-4o-mini",
		Messages: []domain.Message{{Role: "user", Content: "What is the capital of France?"}},
	}
}

func TestHandle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string

		embedVec domain.Vector
		embedErr error

		storeResults   []domain.CacheEntry
		storeSearchErr error

		llmResp domain.LLMResponse
		llmErr  error

		wantIsHit       bool
		wantContent     string
		wantErr         bool
		wantHitCount    int
		wantMissCount   int
		wantLLMCalls    int
		wantStoreErrors int
	}{
		{
			name:          "cache hit returns stored response without calling LLM",
			embedVec:      defaultVec,
			storeResults:  []domain.CacheEntry{cachedEntry("Paris", 0.97)},
			wantIsHit:     true,
			wantContent:   "Paris",
			wantHitCount:  1,
			wantMissCount: 0,
			wantLLMCalls:  0,
		},
		{
			name:          "cache miss calls LLM and returns its response",
			embedVec:      defaultVec,
			storeResults:  nil,
			llmResp:       domain.LLMResponse{Content: "Paris", Raw: []byte("Paris")},
			wantIsHit:     false,
			wantContent:   "Paris",
			wantHitCount:  0,
			wantMissCount: 1,
			wantLLMCalls:  1,
		},
		{
			name:            "store search error falls through to LLM",
			embedVec:        defaultVec,
			storeSearchErr:  errors.New("qdrant unavailable"),
			llmResp:         domain.LLMResponse{Content: "Paris", Raw: []byte("Paris")},
			wantIsHit:       false,
			wantContent:     "Paris",
			wantLLMCalls:    1,
			wantMissCount:   1,
			wantStoreErrors: 1,
		},
		{
			name:         "embed failure returns error without touching store or LLM",
			embedErr:     errors.New("openai embedding timeout"),
			wantErr:      true,
			wantLLMCalls: 0,
		},
		{
			name:          "LLM failure on cache miss propagates error",
			embedVec:      defaultVec,
			storeResults:  nil,
			llmErr:        errors.New("openai rate limit"),
			wantErr:       true,
			wantMissCount: 1,
			wantLLMCalls:  1,
		},
		{
			name: "highest-scored candidate selected when multiple hits returned",
			embedVec: defaultVec,
			storeResults: []domain.CacheEntry{
				cachedEntry("Paris (best)", 0.98),
				cachedEntry("Paris (second)", 0.94),
			},
			wantIsHit:    true,
			wantContent:  "Paris (best)",
			wantHitCount: 1,
		},
		{
			name:          "empty messages fall back to raw bytes for query text",
			embedVec:      defaultVec,
			storeResults:  nil,
			llmResp:       domain.LLMResponse{Content: "42", Raw: []byte("42")},
			wantIsHit:     false,
			wantContent:   "42",
			wantMissCount: 1,
			wantLLMCalls:  1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			embedder := &mockEmbedder{vec: tc.embedVec, err: tc.embedErr}
			store := &mockStore{results: tc.storeResults, searchErr: tc.storeSearchErr}
			llm := &mockLLM{resp: tc.llmResp, err: tc.llmErr}
			rec := &mockMetrics{}

			svc := newService(embedder, store, llm, rec)

			req := baseRequest()
			if tc.name == "empty messages fall back to raw bytes for query text" {
				req.Messages = nil
				req.Raw = []byte("what is 6×7?")
			}

			resp, result, err := svc.Handle(context.Background(), req)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.IsHit != tc.wantIsHit {
				t.Errorf("IsHit = %v, want %v", result.IsHit, tc.wantIsHit)
			}
			if resp.Content != tc.wantContent {
				t.Errorf("Content = %q, want %q", resp.Content, tc.wantContent)
			}
			if rec.hits != tc.wantHitCount {
				t.Errorf("hit counter = %d, want %d", rec.hits, tc.wantHitCount)
			}
			if rec.misses != tc.wantMissCount {
				t.Errorf("miss counter = %d, want %d", rec.misses, tc.wantMissCount)
			}
			if llm.calls != tc.wantLLMCalls {
				t.Errorf("LLM calls = %d, want %d", llm.calls, tc.wantLLMCalls)
			}
			if rec.storeErrors != tc.wantStoreErrors {
				t.Errorf("store error counter = %d, want %d", rec.storeErrors, tc.wantStoreErrors)
			}
		})
	}
}

func TestExtractQueryText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  domain.LLMRequest
		want string
	}{
		{
			name: "single user message",
			req: domain.LLMRequest{
				Messages: []domain.Message{{Role: "user", Content: "hello"}},
			},
			want: "hello",
		},
		{
			name: "system + user messages — only user content extracted",
			req: domain.LLMRequest{
				Messages: []domain.Message{
					{Role: "system", Content: "You are helpful."},
					{Role: "user", Content: "What time is it?"},
				},
			},
			want: "What time is it?",
		},
		{
			name: "multiple user turns concatenated with newline",
			req: domain.LLMRequest{
				Messages: []domain.Message{
					{Role: "user", Content: "First question."},
					{Role: "assistant", Content: "Answer."},
					{Role: "user", Content: "Follow-up question."},
				},
			},
			want: "First question.\nFollow-up question.",
		},
		{
			name: "no user messages — all content used",
			req: domain.LLMRequest{
				Messages: []domain.Message{
					{Role: "system", Content: "System prompt only."},
				},
			},
			want: "System prompt only.",
		},
		{
			name: "no messages — raw bytes used",
			req:  domain.LLMRequest{Raw: []byte("raw query")},
			want: "raw query",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			embedder := &mockEmbedder{vec: defaultVec}
			store := &mockStore{}
			llm := &mockLLM{resp: domain.LLMResponse{Content: tc.want, Raw: []byte(tc.want)}}
			rec := &mockMetrics{}

			svc := newService(embedder, store, llm, rec)
			_, _, err := svc.Handle(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

