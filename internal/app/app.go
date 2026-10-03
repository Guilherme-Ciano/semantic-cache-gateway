package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Guilherme-Ciano/semantic-cache-gateway/internal/adapters/httpserver"
	"github.com/Guilherme-Ciano/semantic-cache-gateway/internal/domain"
	"github.com/Guilherme-Ciano/semantic-cache-gateway/internal/domain/cache"
	"github.com/Guilherme-Ciano/semantic-cache-gateway/pkg/config"
	"github.com/Guilherme-Ciano/semantic-cache-gateway/pkg/telemetry"
)

type App struct {
	server          *http.Server
	svc             *cache.Service
	shutdownTimeout time.Duration
	log             *slog.Logger
}

func New(
	cfg *config.Config,
	embedder domain.EmbedderPort,
	llm domain.LLMPort,
	store domain.VectorStorePort,
	metrics *telemetry.Metrics,
	log *slog.Logger,
) (*App, error) {
	svc := cache.NewService(
		embedder,
		store,
		llm,
		metrics,
		cfg.Cache.SimilarityThreshold,
		cfg.Cache.TTL,
		cfg.Cache.MaxCandidates,
		log,
	)

	hCfg := httpserver.Config{
		RequestTimeout: cfg.Server.WriteTimeout,
		RateLimitRPS:   cfg.Server.RateLimitRPS,
		RateLimitBurst: cfg.Server.RateLimitBurst,
	}

	handler := httpserver.New(svc, metrics, hCfg, log)

	srv := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout + 5*time.Second,
	}

	return &App{
		server:          srv,
		svc:             svc,
		shutdownTimeout: cfg.Server.ShutdownTimeout,
		log:             log,
	}, nil
}

// Run starts the HTTP server and performs graceful shutdown on context cancellation.
func (a *App) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		a.log.Info("server listening", slog.String("addr", a.server.Addr))
		if err := a.server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	a.log.Info("signal received, starting graceful shutdown")

	shutCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
	defer cancel()

	if err := a.server.Shutdown(shutCtx); err != nil {
		a.log.Error("http shutdown error", slog.String("error", err.Error()))
	}

	a.log.Info("draining pending cache writes")
	a.svc.Drain()
	a.log.Info("shutdown complete")

	return nil
}
