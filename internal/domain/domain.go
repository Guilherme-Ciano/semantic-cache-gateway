// Package domain defines the core business entities and port interfaces
// for the Semantic Cache Gateway. No infrastructure dependencies belong here.
package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrCircuitOpen is returned by breaker-wrapped adapters when the circuit is
// open. Handlers inspect this sentinel to return 503 without logging it as an
// unexpected error.
var ErrCircuitOpen = errors.New("circuit open")

// MetricsRecorder abstracts metric instrumentation so the domain layer never
// imports an observability library directly.
type MetricsRecorder interface {
	RecordCacheHit()
	RecordCacheMiss()
	RecordLLMDuration(d time.Duration)
	RecordStoreError()
	RecordEmbedDuration(d time.Duration)
	RecordBreakerTrip(component string)
}

// Vector is a typed alias for a dense float32 embedding.
type Vector []float32

// CacheEntry represents a stored question-answer pair bound to its embedding.
type CacheEntry struct {
	ID        uuid.UUID
	Query     string
	Response  string
	Embedding Vector
	Score     float32 // similarity score — populated only on retrieval
	CreatedAt time.Time
	ExpiresAt time.Time
}

// ToolDefinition describes a callable tool following the MCP/OpenAI tools schema.
// Populated when the upstream LLM supports function calling or MCP tool use.
type ToolDefinition struct {
	Name        string
	Description string
	// Parameters is a JSON Schema object describing the tool's input.
	Parameters map[string]any
}

// ToolCall represents a single tool invocation emitted by the LLM.
type ToolCall struct {
	ID        string
	ToolName  string
	Arguments map[string]any
}

// LLMRequest is the provider-agnostic representation of a completion request.
type LLMRequest struct {
	Model    string
	Messages []Message
	// Tools is non-nil when the caller supports MCP-style tool use.
	Tools []ToolDefinition
	// Raw holds the original, unparsed request body for passthrough adapters.
	Raw []byte
}

// LLMResponse is the provider-agnostic completion response.
type LLMResponse struct {
	Content string
	// ToolCalls is populated when the LLM requests one or more tool invocations.
	ToolCalls []ToolCall
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
	// Search returns up to limit entries with similarity >= threshold.
	Search(ctx context.Context, query Vector, limit uint64, threshold float64) ([]CacheEntry, error)
	// Upsert stores or replaces an entry indexed by its ID.
	Upsert(ctx context.Context, entry CacheEntry) error
	// EnsureCollection guarantees the backing collection/index exists.
	EnsureCollection(ctx context.Context, dimension uint64) error
}

// LLMPort is the port for obtaining completions from an upstream model.
type LLMPort interface {
	Complete(ctx context.Context, req LLMRequest) (LLMResponse, error)
}

// MCPToolPort is a forward-looking interface for MCP tool invocation.
// No adapter currently implements this; it marks the intended extension point
// for a future MCP-over-HTTP or MCP-over-stdio transport.
type MCPToolPort interface {
	Call(ctx context.Context, name string, args map[string]any) (map[string]any, error)
	ListTools(ctx context.Context) ([]ToolDefinition, error)
}
