package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain/cache"
	"github.com/guilhermebr/semantic-cache-gateway/pkg/telemetry"
)

const (
	headerCacheStatus = "X-Cache"
	headerEntryID     = "X-Cache-Entry-Id"
	headerScore       = "X-Cache-Score"
)

// Config groups HTTP-layer tunables injected at construction time.
type Config struct {
	RequestTimeout time.Duration
	RateLimitRPS   float64
	RateLimitBurst int
}

// Handler holds the HTTP layer dependencies.
type Handler struct {
	svc *cache.Service
	log *slog.Logger
}

// New returns an http.Handler with all routes and middlewares mounted.
func New(svc *cache.Service, metrics *telemetry.Metrics, cfg Config, log *slog.Logger) http.Handler {
	h := &Handler{svc: svc, log: log}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(loggingMiddleware(log))
	r.Use(middleware.Recoverer)
	r.Use(RateLimiterMiddleware(cfg.RateLimitRPS, cfg.RateLimitBurst))
	r.Use(TimeoutMiddleware(cfg.RequestTimeout))

	r.Get("/healthz", h.healthz)
	r.Handle("/metrics", telemetry.Handler())
	r.Post("/v1/chat/completions", h.chatCompletions)

	return r
}

func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// chatCompletions is the primary gateway endpoint compatible with the OpenAI
// Chat Completions API. Any client targeting /v1/chat/completions can use
// this gateway as a drop-in replacement by changing only the base URL.
func (h *Handler) chatCompletions(w http.ResponseWriter, r *http.Request) {
	var req openAIChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid request body: "+err.Error()))
		return
	}

	resp, result, err := h.svc.Handle(r.Context(), toDomainRequest(req))
	if err != nil {
		if errors.Is(err, domain.ErrCircuitOpen) {
			h.log.WarnContext(r.Context(), "LLM circuit open, returning 503")
			writeJSON(w, http.StatusServiceUnavailable, errBody("service temporarily unavailable — upstream LLM circuit open"))
			return
		}
		h.log.ErrorContext(r.Context(), "service error", slog.String("error", err.Error()))
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
