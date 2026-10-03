package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Cache     CacheConfig     `yaml:"cache"`
	Embedder  EmbedderConfig  `yaml:"embedder"`
	LLM       LLMConfig       `yaml:"llm"`
	VectorDB  VectorDBConfig  `yaml:"vector_db"`
	Telemetry TelemetryConfig `yaml:"telemetry"`
}

type ServerConfig struct {
	Addr            string        `yaml:"addr"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	// RateLimitRPS is the sustained request rate per IP (token bucket).
	RateLimitRPS   float64 `yaml:"rate_limit_rps"`
	// RateLimitBurst is the maximum burst size above RateLimitRPS.
	RateLimitBurst int     `yaml:"rate_limit_burst"`
}

type CacheConfig struct {
	// SimilarityThreshold is the minimum cosine similarity [0,1] for a cache hit.
	SimilarityThreshold float64       `yaml:"similarity_threshold"`
	TTL                 time.Duration `yaml:"ttl"`
	MaxCandidates       uint64        `yaml:"max_candidates"`
}

type EmbedderConfig struct {
	Provider string `yaml:"provider"` // "openai"
	Model    string `yaml:"model"`
	APIKey   string `yaml:"api_key"`
	BaseURL  string `yaml:"base_url"`
}

type LLMConfig struct {
	Provider    string `yaml:"provider"` // "openai" | "passthrough"
	Model       string `yaml:"model"`
	APIKey      string `yaml:"api_key"`
	BaseURL     string `yaml:"base_url"`
	UpstreamURL string `yaml:"upstream_url"`
}

type VectorDBConfig struct {
	Provider         string `yaml:"provider"` // "qdrant" | "redis"
	QdrantHost       string `yaml:"qdrant_host"`
	QdrantPort       int    `yaml:"qdrant_port"`
	QdrantCollection string `yaml:"qdrant_collection"`
	QdrantAPIKey     string `yaml:"qdrant_api_key"`
	RedisAddr        string `yaml:"redis_addr"`
	RedisPassword    string `yaml:"redis_password"`
	RedisDB          int    `yaml:"redis_db"`
	RedisIndex       string `yaml:"redis_index"`
	VectorDimension  uint64 `yaml:"vector_dimension"`
}

type TelemetryConfig struct {
	LogLevel    string `yaml:"log_level"`
	MetricsAddr string `yaml:"metrics_addr"`
}

// Load reads configuration from an optional YAML file and overlays env vars.
func Load(path string) (*Config, error) {
	cfg := defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file: %w", err)
		}
	}

	overlayEnv(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Addr:            ":8080",
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    60 * time.Second,
			ShutdownTimeout: 15 * time.Second,
			RateLimitRPS:    10,
			RateLimitBurst:  20,
		},
		Cache: CacheConfig{
			SimilarityThreshold: 0.92,
			TTL:                 24 * time.Hour,
			MaxCandidates:       5,
		},
		VectorDB: VectorDBConfig{
			QdrantHost:       "localhost",
			QdrantPort:       6334,
			QdrantCollection: "rag_cache",
			VectorDimension:  1536,
			RedisAddr:        "localhost:6379",
			RedisIndex:       "rag_cache_idx",
		},
		Telemetry: TelemetryConfig{
			LogLevel:    "info",
			MetricsAddr: ":9090",
		},
	}
}

func overlayEnv(cfg *Config) {
	setStr := func(dst *string, key string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	setFloat := func(dst *float64, key string) {
		if v := os.Getenv(key); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				*dst = f
			}
		}
	}
	setInt := func(dst *int, key string) {
		if v := os.Getenv(key); v != "" {
			if i, err := strconv.Atoi(v); err == nil {
				*dst = i
			}
		}
	}
	setDuration := func(dst *time.Duration, key string) {
		if v := os.Getenv(key); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				*dst = d
			}
		}
	}

	setStr(&cfg.Server.Addr, "SERVER_ADDR")
	setDuration(&cfg.Server.ReadTimeout, "SERVER_READ_TIMEOUT")
	setDuration(&cfg.Server.WriteTimeout, "SERVER_WRITE_TIMEOUT")
	setDuration(&cfg.Server.ShutdownTimeout, "SERVER_SHUTDOWN_TIMEOUT")
	setFloat(&cfg.Server.RateLimitRPS, "RATE_LIMIT_RPS")
	setInt(&cfg.Server.RateLimitBurst, "RATE_LIMIT_BURST")

	setFloat(&cfg.Cache.SimilarityThreshold, "CACHE_SIMILARITY_THRESHOLD")
	setDuration(&cfg.Cache.TTL, "CACHE_TTL")

	setStr(&cfg.Embedder.Provider, "EMBEDDER_PROVIDER")
	setStr(&cfg.Embedder.Model, "EMBEDDER_MODEL")
	setStr(&cfg.Embedder.APIKey, "EMBEDDER_API_KEY")
	setStr(&cfg.Embedder.BaseURL, "EMBEDDER_BASE_URL")

	setStr(&cfg.LLM.Provider, "LLM_PROVIDER")
	setStr(&cfg.LLM.Model, "LLM_MODEL")
	setStr(&cfg.LLM.APIKey, "LLM_API_KEY")
	setStr(&cfg.LLM.BaseURL, "LLM_BASE_URL")
	setStr(&cfg.LLM.UpstreamURL, "LLM_UPSTREAM_URL")

	setStr(&cfg.VectorDB.Provider, "VECTORDB_PROVIDER")
	setStr(&cfg.VectorDB.QdrantHost, "QDRANT_HOST")
	setStr(&cfg.VectorDB.QdrantCollection, "QDRANT_COLLECTION")
	setStr(&cfg.VectorDB.QdrantAPIKey, "QDRANT_API_KEY")
	setStr(&cfg.VectorDB.RedisAddr, "REDIS_ADDR")
	setStr(&cfg.VectorDB.RedisPassword, "REDIS_PASSWORD")

	setStr(&cfg.Telemetry.LogLevel, "LOG_LEVEL")
	setStr(&cfg.Telemetry.MetricsAddr, "METRICS_ADDR")
}

func validate(cfg *Config) error {
	var errs []string

	if cfg.Cache.SimilarityThreshold <= 0 || cfg.Cache.SimilarityThreshold > 1 {
		errs = append(errs, "cache.similarity_threshold must be in (0, 1]")
	}
	if cfg.VectorDB.VectorDimension == 0 {
		errs = append(errs, "vector_db.vector_dimension must be > 0")
	}
	if cfg.Server.RateLimitRPS <= 0 {
		errs = append(errs, "server.rate_limit_rps must be > 0")
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(errs, "; "))
	}
	return nil
}
