package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/guilhermebr/semantic-cache-gateway/internal/adapters/embedder/openaiembedder"
	"github.com/guilhermebr/semantic-cache-gateway/internal/adapters/llm/openaillm"
	"github.com/guilhermebr/semantic-cache-gateway/internal/adapters/llm/passthrough"
	"github.com/guilhermebr/semantic-cache-gateway/internal/adapters/vectordb/qdrant"
	"github.com/guilhermebr/semantic-cache-gateway/internal/adapters/vectordb/redisstore"
	"github.com/guilhermebr/semantic-cache-gateway/internal/app"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	"github.com/guilhermebr/semantic-cache-gateway/pkg/config"
	"github.com/guilhermebr/semantic-cache-gateway/pkg/logger"
	"go.uber.org/zap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := flag.String("config", "", "path to YAML config file (optional; env vars take precedence)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	log := logger.Must(cfg.Telemetry.LogLevel)
	defer log.Sync() //nolint:errcheck

	embedder, err := buildEmbedder(cfg)
	if err != nil {
		return fmt.Errorf("initialising embedder: %w", err)
	}

	store, closeStore, err := buildVectorStore(cfg, log)
	if err != nil {
		return fmt.Errorf("initialising vector store: %w", err)
	}
	if closeStore != nil {
		defer closeStore()
	}

	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := store.EnsureCollection(initCtx, cfg.VectorDB.VectorDimension); err != nil {
		initCancel()
		return fmt.Errorf("ensuring vector collection: %w", err)
	}
	initCancel()

	llmClient := buildLLM(cfg)

	application, err := app.New(cfg, embedder, llmClient, store, log)
	if err != nil {
		return fmt.Errorf("building application: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return application.Run(ctx)
}

func buildEmbedder(cfg *config.Config) (domain.EmbedderPort, error) {
	switch cfg.Embedder.Provider {
	case "openai", "":
		return openaiembedder.New(cfg.Embedder.APIKey, cfg.Embedder.Model, cfg.Embedder.BaseURL), nil
	default:
		return nil, fmt.Errorf("unknown embedder provider %q", cfg.Embedder.Provider)
	}
}

func buildVectorStore(cfg *config.Config, log *zap.Logger) (domain.VectorStorePort, func(), error) {
	switch cfg.VectorDB.Provider {
	case "qdrant", "":
		s, err := qdrant.New(
			cfg.VectorDB.QdrantHost,
			cfg.VectorDB.QdrantPort,
			cfg.VectorDB.QdrantCollection,
			cfg.VectorDB.VectorDimension,
			cfg.VectorDB.QdrantAPIKey,
		)
		if err != nil {
			return nil, nil, err
		}
		return s, nil, nil

	case "redis":
		s, err := redisstore.New(
			cfg.VectorDB.RedisAddr,
			cfg.VectorDB.RedisPassword,
			cfg.VectorDB.RedisIndex,
			cfg.VectorDB.RedisDB,
			cfg.VectorDB.VectorDimension,
		)
		if err != nil {
			return nil, nil, err
		}
		return s, func() {
			if err := s.Close(); err != nil {
				log.Warn("closing redis store", zap.Error(err))
			}
		}, nil

	default:
		return nil, nil, fmt.Errorf("unknown vector DB provider %q", cfg.VectorDB.Provider)
	}
}

func buildLLM(cfg *config.Config) domain.LLMPort {
	if cfg.LLM.Provider == "passthrough" {
		return passthrough.New(cfg.LLM.UpstreamURL, cfg.LLM.APIKey)
	}
	return openaillm.New(cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.BaseURL)
}
