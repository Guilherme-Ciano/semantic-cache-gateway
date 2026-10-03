// Package openaillm implements domain.LLMPort using the OpenAI Chat Completions
// API (or any compatible endpoint such as vLLM, Ollama with OpenAI compat, etc.).
package openaillm

import (
	"context"
	"fmt"

	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	oai "github.com/sashabaranov/go-openai"
)

// Client wraps go-openai to satisfy domain.LLMPort.
type Client struct {
	inner *oai.Client
	model string
}

// New returns a Client pointing at the given endpoint.
func New(apiKey, model, baseURL string) *Client {
	cfg := oai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}

	return &Client{
		inner: oai.NewClientWithConfig(cfg),
		model: model,
	}
}

// Complete sends a chat completion request to the upstream model.
func (c *Client) Complete(ctx context.Context, req domain.LLMRequest) (domain.LLMResponse, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}

	msgs := make([]oai.ChatCompletionMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = oai.ChatCompletionMessage{Role: m.Role, Content: m.Content}
	}

	resp, err := c.inner.CreateChatCompletion(ctx, oai.ChatCompletionRequest{
		Model:    model,
		Messages: msgs,
	})
	if err != nil {
		return domain.LLMResponse{}, fmt.Errorf("openai chat completion: %w", err)
	}

	if len(resp.Choices) == 0 {
		return domain.LLMResponse{}, fmt.Errorf("openai returned no choices")
	}

	content := resp.Choices[0].Message.Content
	return domain.LLMResponse{
		Content: content,
		Raw:     []byte(content),
	}, nil
}
