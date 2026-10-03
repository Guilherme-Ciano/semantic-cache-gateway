// Package qdrant implements domain.VectorStorePort backed by Qdrant.
package qdrant

import (
	"context"
	"fmt"
	"time"

	pb "github.com/qdrant/go-client/qdrant"
	"github.com/guilhermebr/semantic-cache-gateway/internal/domain"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const payloadKeyQuery    = "query"
const payloadKeyResponse = "response"
const payloadKeyExpires  = "expires_at"

// Store is a Qdrant-backed vector store.
type Store struct {
	client     pb.PointsClient
	collections pb.CollectionsClient
	collection string
	dimension  uint64
}

// New dials the Qdrant gRPC endpoint and returns a ready Store.
func New(host string, port int, collection string, dimension uint64, apiKey string) (*Store, error) {
	addr := fmt.Sprintf("%s:%d", host, port)

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	if apiKey != "" {
		dialOpts = append(dialOpts, grpc.WithUnaryInterceptor(apiKeyInterceptor(apiKey)))
	}

	conn, err := grpc.NewClient(addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dialing qdrant at %s: %w", addr, err)
	}

	return &Store{
		client:      pb.NewPointsClient(conn),
		collections: pb.NewCollectionsClient(conn),
		collection:  collection,
		dimension:   dimension,
	}, nil
}

// EnsureCollection creates the collection if it doesn't already exist.
func (s *Store) EnsureCollection(ctx context.Context, dimension uint64) error {
	_, err := s.collections.Get(ctx, &pb.GetCollectionInfoRequest{CollectionName: s.collection})
	if err == nil {
		return nil
	}

	_, err = s.collections.Create(ctx, &pb.CreateCollection{
		CollectionName: s.collection,
		VectorsConfig: &pb.VectorsConfig{
			Config: &pb.VectorsConfig_Params{
				Params: &pb.VectorParams{
					Size:     dimension,
					Distance: pb.Distance_Cosine,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("creating qdrant collection %q: %w", s.collection, err)
	}
	return nil
}

// Search retrieves the nearest neighbours above threshold.
func (s *Store) Search(ctx context.Context, query domain.Vector, limit uint64, threshold float64) ([]domain.CacheEntry, error) {
	score := float32(threshold)
	resp, err := s.client.Search(ctx, &pb.SearchPoints{
		CollectionName: s.collection,
		Vector:         query,
		Limit:          limit,
		ScoreThreshold: &score,
		WithPayload:    &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: true}},
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant search: %w", err)
	}

	entries := make([]domain.CacheEntry, 0, len(resp.Result))
	for _, r := range resp.Result {
		entry, err := pointToEntry(r)
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Upsert inserts or replaces a cache entry in Qdrant.
func (s *Store) Upsert(ctx context.Context, entry domain.CacheEntry) error {
	idStr := entry.ID.String()
	point := &pb.PointStruct{
		Id:      &pb.PointId{PointIdOptions: &pb.PointId_Uuid{Uuid: idStr}},
		Vectors: &pb.Vectors{VectorsOptions: &pb.Vectors_Vector{Vector: &pb.Vector{Data: entry.Embedding}}},
		Payload: map[string]*pb.Value{
			payloadKeyQuery:    {Kind: &pb.Value_StringValue{StringValue: entry.Query}},
			payloadKeyResponse: {Kind: &pb.Value_StringValue{StringValue: entry.Response}},
			payloadKeyExpires:  {Kind: &pb.Value_StringValue{StringValue: entry.ExpiresAt.Format(time.RFC3339)}},
		},
	}

	wait := true
	_, err := s.client.Upsert(ctx, &pb.UpsertPoints{
		CollectionName: s.collection,
		Wait:           &wait,
		Points:         []*pb.PointStruct{point},
	})
	if err != nil {
		return fmt.Errorf("qdrant upsert: %w", err)
	}
	return nil
}

func pointToEntry(r *pb.ScoredPoint) (domain.CacheEntry, error) {
	idStr := r.Id.GetUuid()
	id, err := uuid.Parse(idStr)
	if err != nil {
		return domain.CacheEntry{}, fmt.Errorf("invalid point uuid %q: %w", idStr, err)
	}

	payload := r.Payload
	query    := payload[payloadKeyQuery].GetStringValue()
	response := payload[payloadKeyResponse].GetStringValue()

	var expiresAt time.Time
	if exp := payload[payloadKeyExpires].GetStringValue(); exp != "" {
		expiresAt, _ = time.Parse(time.RFC3339, exp)
	}

	return domain.CacheEntry{
		ID:        id,
		Query:     query,
		Response:  response,
		Score:     r.Score,
		ExpiresAt: expiresAt,
	}, nil
}

func apiKeyInterceptor(key string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "api-key", key)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
