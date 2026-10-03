package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrCircuitOpen = errors.New("circuit open")

type MetricsRecorder interface {
	RecordCacheHit()
	RecordCacheMiss()
	RecordLLMDuration(d time.Duration)
	RecordStoreError()
	RecordEmbedDuration(d time.Duration)
	RecordBreakerTrip(component string)
}

type Vector []float32

type CacheEntry struct {
	ID        uuid.UUID
	Query     string
	Response  string
	Embedding Vector
	Score     float32
	CreatedAt time.Time
	ExpiresAt time.Time
}

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type ToolCall struct {
	ID        string
	ToolName  string
	Arguments map[string]any
}

type LLMRequest struct {
	Model    string
	Messages []Message
	Tools    []ToolDefinition
	Raw      []byte
}

type LLMResponse struct {
	Content   string
	ToolCalls []ToolCall
	Raw       []byte
}

type Message struct {
	Role    string
	Content string
}

type EmbedderPort interface {
	Embed(ctx context.Context, text string) (Vector, error)
}

type VectorStorePort interface {
	Search(ctx context.Context, query Vector, limit uint64, threshold float64) ([]CacheEntry, error)
	Upsert(ctx context.Context, entry CacheEntry) error
	EnsureCollection(ctx context.Context, dimension uint64) error
}

type LLMPort interface {
	Complete(ctx context.Context, req LLMRequest) (LLMResponse, error)
}

type MCPToolPort interface {
	Call(ctx context.Context, name string, args map[string]any) (map[string]any, error)
	ListTools(ctx context.Context) ([]ToolDefinition, error)
}

