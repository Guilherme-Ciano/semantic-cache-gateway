// Package openaiembedder implements domain.EmbedderPort using the OpenAI
// Embeddings API (or any compatible endpoint).
package openaiembedder

import (
	"context"
	"fmt"

	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	oai "github.com/sashabaranov/go-openai"
)

// Embedder wraps the OpenAI client for text embedding.
type Embedder struct {
	client *oai.Client
	model  oai.EmbeddingModel
}

// New returns an Embedder targeting the given model.
// Set baseURL to a non-empty string to redirect to a compatible local endpoint.
func New(apiKey, model, baseURL string) *Embedder {
	cfg := oai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}

	return &Embedder{
		client: oai.NewClientWithConfig(cfg),
		model:  oai.EmbeddingModel(model),
	}
}

// Embed returns the vector representation of text.
func (e *Embedder) Embed(ctx context.Context, text string) (domain.Vector, error) {
	resp, err := e.client.CreateEmbeddings(ctx, oai.EmbeddingRequestStrings{
		Input: []string{text},
		Model: e.model,
	})
	if err != nil {
		return nil, fmt.Errorf("openai embeddings: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("openai returned empty embedding data")
	}

	raw := resp.Data[0].Embedding
	vec := make(domain.Vector, len(raw))
	for i, v := range raw {
		vec[i] = float32(v)
	}
	return vec, nil
}
