package redis

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Guilherme-Ciano/semantic-cache-gateway/internal/domain"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const (
	keyPrefix = "scg:entry:"

	fieldQuery     = "query"
	fieldResponse  = "response"
	fieldEmbedding = "embedding"
	fieldCreatedAt = "created_at"
	fieldExpiresAt = "expires_at"
	fieldDistance  = "distance"
)

type Store struct {
	client     *goredis.Client
	index      string
	dimension  uint64
	defaultTTL time.Duration
	log        *slog.Logger
}

func New(addr, password, index string, db int, dimension uint64, ttl time.Duration, log *slog.Logger) (*Store, error) {
	c := goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	return &Store{
		client:     c,
		index:      index,
		dimension:  dimension,
		defaultTTL: ttl,
		log:        log,
	}, nil
}

func (s *Store) EnsureCollection(ctx context.Context, dimension uint64) error {
	if dimension > 0 {
		s.dimension = dimension
	}

	_, err := s.client.FTInfo(ctx, s.index).Result()
	if err == nil {
		return nil
	}
	if !isNoIndexErr(err) {
		return fmt.Errorf("ft.info %q: %w", s.index, err)
	}

	dim := int(s.dimension) //nolint:gosec // dimension bounded by config validation
	_, err = s.client.FTCreate(ctx, s.index,
		&goredis.FTCreateOptions{
			OnHash: true,
			Prefix: []interface{}{keyPrefix},
		},
		&goredis.FieldSchema{
			FieldName: fieldQuery,
			FieldType: goredis.SearchFieldTypeText,
			NoIndex:   true,
			NoStem:    true,
		},
		&goredis.FieldSchema{
			FieldName: fieldResponse,
			FieldType: goredis.SearchFieldTypeText,
			NoIndex:   true,
			NoStem:    true,
		},
		&goredis.FieldSchema{
			FieldName: fieldExpiresAt,
			FieldType: goredis.SearchFieldTypeNumeric,
			Sortable:  true,
		},
		&goredis.FieldSchema{
			FieldName: fieldEmbedding,
			FieldType: goredis.SearchFieldTypeVector,
			VectorArgs: &goredis.FTVectorArgs{
				HNSWOptions: &goredis.FTHNSWOptions{
					Type:            "FLOAT32",
					Dim:             dim,
					DistanceMetric:  "COSINE",
					MaxEdgesPerNode: 16,
				},
			},
		},
	).Result()
	if err != nil && !isAlreadyExistsErr(err) {
		return fmt.Errorf("ft.create %q: %w", s.index, err)
	}

	s.log.Info("redis stack index created",
		slog.String("index", s.index),
		slog.Int("dim", dim),
	)
	return nil
}

func (s *Store) Search(ctx context.Context, query domain.Vector, limit uint64, threshold float64) ([]domain.CacheEntry, error) {
	// COSINE distance in [0, 2]: similarity = 1 - distance.
	maxDistance := 1.0 - threshold

	ftQuery := fmt.Sprintf("*=>[KNN %d @%s $vec AS %s]", limit, fieldEmbedding, fieldDistance)

	res, err := s.client.FTSearchWithArgs(ctx, s.index, ftQuery, &goredis.FTSearchOptions{
		Params:         map[string]interface{}{"vec": vectorToBytes(query)},
		Return:         []goredis.FTSearchReturn{{FieldName: fieldDistance}, {FieldName: fieldQuery}, {FieldName: fieldResponse}, {FieldName: fieldExpiresAt}},
		SortBy:         []goredis.FTSearchSortBy{{FieldName: fieldDistance, Asc: true}},
		DialectVersion: 2,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("ft.search: %w", err)
	}

	now := time.Now()
	entries := make([]domain.CacheEntry, 0, len(res.Docs))

	for _, doc := range res.Docs {
		dist, ok := parseFloat(doc.Fields[fieldDistance])
		if !ok || dist > maxDistance {
			continue
		}

		expiresUnix, _ := parseInt(doc.Fields[fieldExpiresAt])
		expiresAt := time.Unix(expiresUnix, 0)
		if expiresAt.Before(now) {
			continue
		}

		id, err := parseEntryID(doc.ID)
		if err != nil {
			s.log.WarnContext(ctx, "unparseable entry key, skipping",
				slog.String("key", doc.ID),
				slog.String("error", err.Error()),
			)
			continue
		}

		entry := domain.CacheEntry{
			ID:        id,
			Query:     stringField(doc.Fields[fieldQuery]),
			Response:  stringField(doc.Fields[fieldResponse]),
			Score:     float32(1.0 - dist),
			ExpiresAt: expiresAt,
		}
		entries = append(entries, entry)

		go s.refreshTTL(doc.ID)
	}

	return entries, nil
}

func (s *Store) Upsert(ctx context.Context, entry domain.CacheEntry) error {
	key := keyPrefix + entry.ID.String()

	pipe := s.client.Pipeline()
	pipe.HSet(ctx, key,
		fieldQuery, entry.Query,
		fieldResponse, entry.Response,
		fieldEmbedding, vectorToBytes(entry.Embedding),
		fieldCreatedAt, entry.CreatedAt.Unix(),
		fieldExpiresAt, entry.ExpiresAt.Unix(),
	)
	pipe.ExpireAt(ctx, key, entry.ExpiresAt)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("hset+expireat %s: %w", key, err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.client.Close()
}

// refreshTTL extends key TTL asynchronously with a detached context.
func (s *Store) refreshTTL(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := s.client.Expire(ctx, key, s.defaultTTL).Err(); err != nil {
		s.log.Warn("ttl refresh failed",
			slog.String("key", key),
			slog.String("error", err.Error()),
		)
	}
}

// vectorToBytes serializes float32 slice to little-endian bytes for RediSearch VECTOR fields.
func vectorToBytes(v domain.Vector) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func bytesToVector(b []byte) domain.Vector {
	if len(b)%4 != 0 {
		return nil
	}
	v := make(domain.Vector, len(b)/4)
	for i := range v {
		bits := binary.LittleEndian.Uint32(b[i*4:])
		v[i] = math.Float32frombits(bits)
	}
	return v
}

func parseFloat(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case string:
		f, err := strconv.ParseFloat(val, 64)
		return f, err == nil
	}
	return 0, false
}

func parseInt(v interface{}) (int64, bool) {
	switch val := v.(type) {
	case int64:
		return val, true
	case float64:
		return int64(val), true
	case string:
		i, err := strconv.ParseInt(val, 10, 64)
		return i, err == nil
	}
	return 0, false
}

func stringField(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func parseEntryID(key string) (uuid.UUID, error) {
	raw := strings.TrimPrefix(key, keyPrefix)
	return uuid.Parse(raw)
}

func isNoIndexErr(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Unknown Index name") ||
		strings.Contains(msg, "no such index") ||
		strings.Contains(msg, "ERR no such index")
}

func isAlreadyExistsErr(err error) bool {
	return strings.Contains(err.Error(), "Index already exists")
}

var _ = bytesToVector
