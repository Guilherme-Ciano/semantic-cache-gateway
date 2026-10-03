package config

import (
	"fmt"
	"strings"
	"time"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"         mapstructure:"server"`
	Cache     CacheConfig     `yaml:"cache"          mapstructure:"cache"`
	Embedder  EmbedderConfig  `yaml:"embedder"       mapstructure:"embedder"`
	LLM       LLMConfig       `yaml:"llm"            mapstructure:"llm"`
	VectorDB  VectorDBConfig  `yaml:"vector_db"      mapstructure:"vector_db"`
	Breaker   BreakerConfig   `yaml:"circuit_breaker" mapstructure:"circuit_breaker"`
	Telemetry TelemetryConfig `yaml:"telemetry"      mapstructure:"telemetry"`
}

type ServerConfig struct {
	Addr            string        `yaml:"addr"              mapstructure:"addr"`
	ReadTimeout     time.Duration `yaml:"read_timeout"      mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"     mapstructure:"write_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"  mapstructure:"shutdown_timeout"`
	RateLimitRPS    float64       `yaml:"rate_limit_rps"    mapstructure:"rate_limit_rps"`
	RateLimitBurst  int           `yaml:"rate_limit_burst"  mapstructure:"rate_limit_burst"`
}

type CacheConfig struct {
	SimilarityThreshold float64       `yaml:"similarity_threshold" mapstructure:"similarity_threshold"` // cosine similarity in (0, 1]
	TTL                 time.Duration `yaml:"ttl"                  mapstructure:"ttl"`
	MaxCandidates       uint64        `yaml:"max_candidates"       mapstructure:"max_candidates"`
}

type EmbedderConfig struct {
	Provider string `yaml:"provider" mapstructure:"provider"`
	Model    string `yaml:"model"    mapstructure:"model"`
	APIKey   string `yaml:"api_key"  mapstructure:"api_key"`
	BaseURL  string `yaml:"base_url" mapstructure:"base_url"`
}

type LLMConfig struct {
	Provider    string `yaml:"provider"     mapstructure:"provider"`
	Model       string `yaml:"model"        mapstructure:"model"`
	APIKey      string `yaml:"api_key"      mapstructure:"api_key"`
	BaseURL     string `yaml:"base_url"     mapstructure:"base_url"`
	UpstreamURL string `yaml:"upstream_url" mapstructure:"upstream_url"`
}

type VectorDBConfig struct {
	Provider         string `yaml:"provider"          mapstructure:"provider"`
	QdrantHost       string `yaml:"qdrant_host"       mapstructure:"qdrant_host"`
	QdrantPort       int    `yaml:"qdrant_port"       mapstructure:"qdrant_port"`
	QdrantCollection string `yaml:"qdrant_collection" mapstructure:"qdrant_collection"`
	QdrantAPIKey     string `yaml:"qdrant_api_key"    mapstructure:"qdrant_api_key"`
	RedisAddr        string `yaml:"redis_addr"        mapstructure:"redis_addr"`
	RedisPassword    string `yaml:"redis_password"    mapstructure:"redis_password"`
	RedisDB          int    `yaml:"redis_db"          mapstructure:"redis_db"`
	RedisIndex       string `yaml:"redis_index"       mapstructure:"redis_index"`
	VectorDimension  uint64 `yaml:"vector_dimension"  mapstructure:"vector_dimension"`
}

type BreakerConfig struct {
	LLM      ComponentBreakerConfig `yaml:"llm"       mapstructure:"llm"`
	VectorDB ComponentBreakerConfig `yaml:"vector_db" mapstructure:"vector_db"`
}

type ComponentBreakerConfig struct {
	MaxRequests      uint32        `yaml:"max_requests"      mapstructure:"max_requests"`
	Interval         time.Duration `yaml:"interval"          mapstructure:"interval"`
	Timeout          time.Duration `yaml:"timeout"           mapstructure:"timeout"`
	FailureThreshold uint32        `yaml:"failure_threshold" mapstructure:"failure_threshold"`
}

type TelemetryConfig struct {
	LogLevel    string `yaml:"log_level"    mapstructure:"log_level"`
	MetricsAddr string `yaml:"metrics_addr" mapstructure:"metrics_addr"`
}

func Defaults() *Config {
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
		Breaker: BreakerConfig{
			LLM: ComponentBreakerConfig{
				MaxRequests:      3,
				Interval:         60 * time.Second,
				Timeout:          30 * time.Second,
				FailureThreshold: 5,
			},
			VectorDB: ComponentBreakerConfig{
				MaxRequests:      2,
				Interval:         30 * time.Second,
				Timeout:          10 * time.Second,
				FailureThreshold: 3,
			},
		},
		Telemetry: TelemetryConfig{
			LogLevel:    "info",
			MetricsAddr: ":9090",
		},
	}
}

func Validate(cfg *Config) error {
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
