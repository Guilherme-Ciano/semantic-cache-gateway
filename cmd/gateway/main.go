package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/guilhermebr/semantic-cache-gateway/internal/adapters/breaker"
	"github.com/guilhermebr/semantic-cache-gateway/internal/adapters/embedder/openaiembedder"
	"github.com/guilhermebr/semantic-cache-gateway/internal/adapters/llm/openaillm"
	"github.com/guilhermebr/semantic-cache-gateway/internal/adapters/llm/passthrough"
	"github.com/guilhermebr/semantic-cache-gateway/internal/adapters/vectordb/qdrant"
	"github.com/guilhermebr/semantic-cache-gateway/internal/adapters/vectordb/redisstore"
	"github.com/guilhermebr/semantic-cache-gateway/internal/app"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	"github.com/guilhermebr/semantic-cache-gateway/pkg/config"
	"github.com/guilhermebr/semantic-cache-gateway/pkg/logger"
	"github.com/guilhermebr/semantic-cache-gateway/pkg/telemetry"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// version is stamped at build time via -ldflags.
var version = "dev"

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var cfgFile string

var rootCmd = &cobra.Command{
	Use:          "gateway",
	Short:        "Semantic Cache Gateway for RAG — OpenAI-compatible semantic caching proxy",
	Version:      version,
	SilenceUsage: true,
	RunE:         runGateway,
}

func init() {
	cobra.OnInitialize(initViper)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ./config.yaml)")

	rootCmd.Flags().String("addr", "", "HTTP listen address (overrides config)")
	rootCmd.Flags().String("log-level", "", "log level: debug|info|warn|error (overrides config)")
	rootCmd.Flags().Float64("threshold", 0, "similarity threshold for cache hits (overrides config)")

	_ = viper.BindPFlag("server.addr", rootCmd.Flags().Lookup("addr"))
	_ = viper.BindPFlag("telemetry.log_level", rootCmd.Flags().Lookup("log-level"))
	_ = viper.BindPFlag("cache.similarity_threshold", rootCmd.Flags().Lookup("threshold"))
}

func initViper() {
	viper.SetEnvPrefix("SCG")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.AddConfigPath(".")
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	setViperDefaults(config.Defaults())

	if err := viper.ReadInConfig(); err == nil {
		// config file found and loaded — no log yet, logger is built from config
		fmt.Fprintf(os.Stderr, "using config file: %s\n", viper.ConfigFileUsed())
	}
}

func runGateway(_ *cobra.Command, _ []string) error {
	var cfg config.Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return fmt.Errorf("unmarshalling config: %w", err)
	}
	if err := config.Validate(&cfg); err != nil {
		return err
	}

	log := logger.New(cfg.Telemetry.LogLevel)
	metrics := telemetry.New()

	embedder := openaiembedder.New(cfg.Embedder.APIKey, cfg.Embedder.Model, cfg.Embedder.BaseURL)

	rawStore, closeStore, err := buildVectorStore(&cfg, log)
	if err != nil {
		return fmt.Errorf("initialising vector store: %w", err)
	}
	if closeStore != nil {
		defer closeStore()
	}

	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := rawStore.EnsureCollection(initCtx, cfg.VectorDB.VectorDimension); err != nil {
		initCancel()
		return fmt.Errorf("ensuring vector collection: %w", err)
	}
	initCancel()

	store := breaker.NewStore(rawStore, breakerStoreConfig(cfg.Breaker.VectorDB), metrics, log)
	llmClient := breaker.NewLLM(buildLLM(&cfg), breakerLLMConfig(cfg.Breaker.LLM), metrics, log)

	application, err := app.New(&cfg, embedder, llmClient, store, metrics, log)
	if err != nil {
		return fmt.Errorf("building application: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return application.Run(ctx)
}

// ── Adapters ──────────────────────────────────────────────────────────────

func buildVectorStore(cfg *config.Config, log *slog.Logger) (domain.VectorStorePort, func(), error) {
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
				log.Warn("closing redis store", slog.String("error", err.Error()))
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

// ── Breaker config adapters ───────────────────────────────────────────────

func breakerLLMConfig(c config.ComponentBreakerConfig) breaker.LLMConfig {
	iv, to := c.Interval, c.Timeout
	return breaker.LLMConfig{
		MaxRequests:      c.MaxRequests,
		Interval:         func() time.Duration { return iv },
		Timeout:          func() time.Duration { return to },
		FailureThreshold: c.FailureThreshold,
	}
}

func breakerStoreConfig(c config.ComponentBreakerConfig) breaker.StoreConfig {
	iv, to := c.Interval, c.Timeout
	return breaker.StoreConfig{
		MaxRequests:      c.MaxRequests,
		Interval:         func() time.Duration { return iv },
		Timeout:          func() time.Duration { return to },
		FailureThreshold: c.FailureThreshold,
	}
}

// ── Viper defaults ────────────────────────────────────────────────────────

func setViperDefaults(d *config.Config) {
	viper.SetDefault("server.addr", d.Server.Addr)
	viper.SetDefault("server.read_timeout", d.Server.ReadTimeout)
	viper.SetDefault("server.write_timeout", d.Server.WriteTimeout)
	viper.SetDefault("server.shutdown_timeout", d.Server.ShutdownTimeout)
	viper.SetDefault("server.rate_limit_rps", d.Server.RateLimitRPS)
	viper.SetDefault("server.rate_limit_burst", d.Server.RateLimitBurst)

	viper.SetDefault("cache.similarity_threshold", d.Cache.SimilarityThreshold)
	viper.SetDefault("cache.ttl", d.Cache.TTL)
	viper.SetDefault("cache.max_candidates", d.Cache.MaxCandidates)

	viper.SetDefault("embedder.provider", "openai")
	viper.SetDefault("embedder.model", "text-embedding-3-small")

	viper.SetDefault("llm.provider", "openai")
	viper.SetDefault("llm.model", "gpt-4o-mini")

	viper.SetDefault("vector_db.provider", d.VectorDB.Provider)
	viper.SetDefault("vector_db.qdrant_host", d.VectorDB.QdrantHost)
	viper.SetDefault("vector_db.qdrant_port", d.VectorDB.QdrantPort)
	viper.SetDefault("vector_db.qdrant_collection", d.VectorDB.QdrantCollection)
	viper.SetDefault("vector_db.redis_addr", d.VectorDB.RedisAddr)
	viper.SetDefault("vector_db.redis_index", d.VectorDB.RedisIndex)
	viper.SetDefault("vector_db.vector_dimension", d.VectorDB.VectorDimension)

	viper.SetDefault("circuit_breaker.llm.max_requests", d.Breaker.LLM.MaxRequests)
	viper.SetDefault("circuit_breaker.llm.interval", d.Breaker.LLM.Interval)
	viper.SetDefault("circuit_breaker.llm.timeout", d.Breaker.LLM.Timeout)
	viper.SetDefault("circuit_breaker.llm.failure_threshold", d.Breaker.LLM.FailureThreshold)
	viper.SetDefault("circuit_breaker.vector_db.max_requests", d.Breaker.VectorDB.MaxRequests)
	viper.SetDefault("circuit_breaker.vector_db.interval", d.Breaker.VectorDB.Interval)
	viper.SetDefault("circuit_breaker.vector_db.timeout", d.Breaker.VectorDB.Timeout)
	viper.SetDefault("circuit_breaker.vector_db.failure_threshold", d.Breaker.VectorDB.FailureThreshold)

	viper.SetDefault("telemetry.log_level", d.Telemetry.LogLevel)
	viper.SetDefault("telemetry.metrics_addr", d.Telemetry.MetricsAddr)
}


