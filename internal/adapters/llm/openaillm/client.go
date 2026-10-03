package openaillm

import (
	"context"
	"fmt"

	"github.com/Guilherme-Ciano/semantic-cache-gateway/internal/domain"
	oai "github.com/sashabaranov/go-openai"
)

type Client struct {
	inner *oai.Client
	model string
}

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

