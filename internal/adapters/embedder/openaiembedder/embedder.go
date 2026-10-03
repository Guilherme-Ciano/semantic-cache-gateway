package openaiembedder

import (
	"context"
	"fmt"

	oai "github.com/sashabaranov/go-openai"

	"github.com/Guilherme-Ciano/semantic-cache-gateway/internal/domain"
)

type Embedder struct {
	client *oai.Client
	model  oai.EmbeddingModel
}

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
