package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/guilhermebr/semantic-cache-gateway/internal/adapters/httpserver"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain/cache"
	"github.com/guilhermebr/semantic-cache-gateway/pkg/config"
	"go.uber.org/zap"
)

// App owns the HTTP server and all wired dependencies.
type App struct {
	server          *http.Server
	shutdownTimeout time.Duration
	log             *zap.Logger
}

// New constructs the full application graph given the provided ports.
func New(
	cfg *config.Config,
	embedder domain.EmbedderPort,
	llm domain.LLMPort,
	store domain.VectorStorePort,
	log *zap.Logger,
) (*App, error) {
	svc := cache.NewService(
		embedder,
		store,
		llm,
		cfg.Cache.SimilarityThreshold,
		cfg.Cache.TTL,
		cfg.Cache.MaxCandidates,
		log,
	)

	handler := httpserver.New(svc, log)

	srv := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	return &App{
		server:          srv,
		shutdownTimeout: cfg.Server.ShutdownTimeout,
		log:             log,
	}, nil
}

// Run starts the HTTP server and blocks until ctx is cancelled, after which
// it performs a graceful shutdown bounded by the configured timeout.
func (a *App) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		a.log.Info("server listening", zap.String("addr", a.server.Addr))
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

	a.log.Info("shutting down gracefully")
	shutCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
	defer cancel()

	return a.server.Shutdown(shutCtx)
}
