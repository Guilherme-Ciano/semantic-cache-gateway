// Package domain defines the core business entities and port interfaces
// for the Semantic Cache Gateway. No infrastructure dependencies belong here.
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MetricsRecorder abstracts metric instrumentation so the domain layer never
// imports an observability library directly.
type MetricsRecorder interface {
	RecordCacheHit()
	RecordCacheMiss()
	RecordLLMDuration(d time.Duration)
	RecordStoreError()
	RecordEmbedDuration(d time.Duration)
}

// Vector is a typed alias for a dense float32 embedding.
type Vector []float32

// CacheEntry represents a stored question-answer pair bound to its embedding.
type CacheEntry struct {
	ID         uuid.UUID
	Query      string
	Response   string
	Embedding  Vector
	Score      float32 // similarity score — populated only on retrieval
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// LLMRequest is the provider-agnostic representation of a completion request.
type LLMRequest struct {
	Model    string
	Messages []Message
	// Raw holds the original, unparsed request body for passthrough adapters.
	Raw []byte
}

// LLMResponse is the provider-agnostic representation of a completion response.
type LLMResponse struct {
	Content string
	// Raw holds the original response body for transparent proxying.
	Raw []byte
}

// Message is a single turn in a conversation.
type Message struct {
	Role    string
	Content string
}

// EmbedderPort is the port for turning text into a dense vector.
type EmbedderPort interface {
	Embed(ctx context.Context, text string) (Vector, error)
}

// VectorStorePort is the port for semantic persistence.
type VectorStorePort interface {
	// Search returns up to limit entries whose embeddings are nearest to query,
	// filtered to those with score >= threshold.
	Search(ctx context.Context, query Vector, limit uint64, threshold float64) ([]CacheEntry, error)
	// Upsert stores or replaces an entry indexed by its ID.
	Upsert(ctx context.Context, entry CacheEntry) error
	// EnsureCollection guarantees the backing collection/index exists with the
	// given vector dimension.
	EnsureCollection(ctx context.Context, dimension uint64) error
}

// LLMPort is the port for obtaining completions from an upstream model.
type LLMPort interface {
	Complete(ctx context.Context, req LLMRequest) (LLMResponse, error)
}
