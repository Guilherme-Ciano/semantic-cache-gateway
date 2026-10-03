package redisstore

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Guilherme-Ciano/semantic-cache-gateway/internal/domain"
)

const entryPrefix = "scg:entry:"

type storedEntry struct {
	ID        string    `json:"id"`
	Query     string    `json:"query"`
	Response  string    `json:"response"`
	Embedding []float32 `json:"embedding"`
	ExpiresAt time.Time `json:"expires_at"`
}

type candidate struct {
	entry domain.CacheEntry
	score float32
}

type Store struct {
	client    *redis.Client
	indexName string
	dimension uint64
}

func New(addr, password, indexName string, db int, dimension uint64) (*Store, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(pingCtx).Err(); err != nil {
		return nil, fmt.Errorf("connecting to redis at %s: %w", addr, err)
	}

	return &Store{client: rdb, indexName: indexName, dimension: dimension}, nil
}

func (s *Store) EnsureCollection(_ context.Context, _ uint64) error { return nil }

func (s *Store) Upsert(ctx context.Context, entry domain.CacheEntry) error {
	se := storedEntry{
		ID:        entry.ID.String(),
		Query:     entry.Query,
		Response:  entry.Response,
		Embedding: entry.Embedding,
		ExpiresAt: entry.ExpiresAt,
	}

	data, err := json.Marshal(se)
	if err != nil {
		return fmt.Errorf("marshalling entry: %w", err)
	}

	ttl := time.Until(entry.ExpiresAt)
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	return s.client.Set(ctx, entryPrefix+se.ID, data, ttl).Err()
}

func (s *Store) Search(ctx context.Context, query domain.Vector, limit uint64, threshold float64) ([]domain.CacheEntry, error) {
	var cursor uint64
	var hits []candidate

	for {
		keys, next, err := s.client.Scan(ctx, cursor, entryPrefix+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("redis scan: %w", err)
		}

		if len(keys) > 0 {
			vals, err := s.client.MGet(ctx, keys...).Result()
			if err != nil {
				return nil, fmt.Errorf("redis mget: %w", err)
			}

			for _, v := range vals {
				if v == nil {
					continue
				}
				raw, ok := v.(string)
				if !ok {
					continue
				}
				var se storedEntry
				if err := json.Unmarshal([]byte(raw), &se); err != nil {
					continue
				}
				if time.Now().After(se.ExpiresAt) {
					continue
				}

				score := cosineSimilarity(query, se.Embedding)
				if float64(score) < threshold {
					continue
				}

				id, _ := uuid.Parse(se.ID)
				hits = append(hits, candidate{
					score: score,
					entry: domain.CacheEntry{
						ID:        id,
						Query:     se.Query,
						Response:  se.Response,
						Embedding: se.Embedding,
						Score:     score,
						ExpiresAt: se.ExpiresAt,
					},
				})
			}
		}

		cursor = next
		if cursor == 0 {
			break
		}
	}

	sortCandidates(hits)

	if uint64(len(hits)) > limit {
		hits = hits[:limit]
	}

	entries := make([]domain.CacheEntry, len(hits))
	for i, h := range hits {
		entries[i] = h.entry
	}
	return entries, nil
}

func (s *Store) Close() error {
	return s.client.Close()
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		av, bv := float64(a[i]), float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}

func sortCandidates(hits []candidate) {
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].score > hits[j-1].score; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
}
