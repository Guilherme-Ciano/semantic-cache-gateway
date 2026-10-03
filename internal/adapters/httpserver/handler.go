package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain/cache"
	"go.uber.org/zap"
)

const (
	headerCacheStatus = "X-Cache"
	headerEntryID     = "X-Cache-Entry-Id"
	headerScore       = "X-Cache-Score"
)

// Handler holds the HTTP layer dependencies.
type Handler struct {
	svc *cache.Service
	log *zap.Logger
}

// New returns an http.Handler with all routes mounted.
func New(svc *cache.Service, log *zap.Logger) http.Handler {
	h := &Handler{svc: svc, log: log}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(loggingMiddleware(log))
	r.Use(middleware.Recoverer)

	r.Get("/healthz", h.healthz)
	r.Post("/v1/chat/completions", h.chatCompletions)

	return r
}

func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// chatCompletions is the primary gateway endpoint. It accepts an OpenAI-
// compatible chat request, queries the semantic cache, and either returns a
// cached response or proxies the call to the configured upstream LLM.
func (h *Handler) chatCompletions(w http.ResponseWriter, r *http.Request) {
	var req openAIChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid request body: "+err.Error()))
		return
	}

	domainReq := toDomainRequest(req)

	resp, result, err := h.svc.Handle(r.Context(), domainReq)
	if err != nil {
		h.log.Error("service error", zap.Error(err))
		writeJSON(w, http.StatusBadGateway, errBody("upstream error: "+err.Error()))
		return
	}

	cacheStatus := "MISS"
	if result.IsHit {
		cacheStatus = "HIT"
	}

	w.Header().Set(headerCacheStatus, cacheStatus)
	w.Header().Set(headerEntryID, result.Entry.ID.String())
	if result.IsHit {
		w.Header().Set(headerScore, strconv.FormatFloat(float64(result.Entry.Score), 'f', 4, 32))
	}

	writeJSON(w, http.StatusOK, toOpenAIResponse(resp, req.Model))
}

// ── OpenAI wire types ──────────────────────────────────────────────────────

type openAIChatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
}

type choice struct {
	Index   int     `json:"index"`
	Message message `json:"message"`
	Reason  string  `json:"finish_reason"`
}

func toDomainRequest(req openAIChatRequest) domain.LLMRequest {
	msgs := make([]domain.Message, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = domain.Message{Role: m.Role, Content: m.Content}
	}
	return domain.LLMRequest{Model: req.Model, Messages: msgs}
}

func toOpenAIResponse(resp domain.LLMResponse, model string) openAIResponse {
	return openAIResponse{
		ID:      fmt.Sprintf("scg-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []choice{
			{
				Index:   0,
				Message: message{Role: "assistant", Content: resp.Content},
				Reason:  "stop",
			},
		},
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func errBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}
