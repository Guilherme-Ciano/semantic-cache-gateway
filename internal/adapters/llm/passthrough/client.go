package passthrough

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Guilherme-Ciano/semantic-cache-gateway/internal/domain"
)

type Client struct {
	upstream   string
	apiKey     string
	httpClient *http.Client
}

func New(upstreamURL, apiKey string) *Client {
	return &Client{
		upstream: upstreamURL,
		apiKey:   apiKey,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *Client) Complete(ctx context.Context, req domain.LLMRequest) (domain.LLMResponse, error) {
	body := req.Raw
	if len(body) == 0 {
		var err error
		body, err = marshalRequest(req)
		if err != nil {
			return domain.LLMResponse{}, err
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.upstream, bytes.NewReader(body))
	if err != nil {
		return domain.LLMResponse{}, fmt.Errorf("building upstream request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return domain.LLMResponse{}, fmt.Errorf("upstream request: %w", err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return domain.LLMResponse{}, fmt.Errorf("reading upstream response: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		return domain.LLMResponse{}, fmt.Errorf("upstream returned %d: %s", httpResp.StatusCode, raw)
	}

	content := extractContent(raw)
	return domain.LLMResponse{Content: content, Raw: raw}, nil
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func extractContent(body []byte) string {
	var r openAIResponse
	if err := json.Unmarshal(body, &r); err != nil || len(r.Choices) == 0 {
		return string(body)
	}
	return r.Choices[0].Message.Content
}

func marshalRequest(req domain.LLMRequest) ([]byte, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type payload struct {
		Model    string `json:"model"`
		Messages []msg  `json:"messages"`
	}

	msgs := make([]msg, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = msg{Role: m.Role, Content: m.Content}
	}

	data, err := json.Marshal(payload{Model: req.Model, Messages: msgs})
	if err != nil {
		return nil, fmt.Errorf("serialising LLM request: %w", err)
	}
	return data, nil
}

