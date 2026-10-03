# Semantic Cache Gateway for RAG

A high-performance semantic caching proxy between RAG applications and LLM APIs. Instead of forwarding every request to an expensive language model, the gateway embeds incoming queries, searches a vector store for semantically equivalent past queries, and returns cached answers when similarity exceeds a configurable threshold.

---

## Data Flow

```
Client Request
      │
      ▼
 ┌─────────────────────────────────────────────────────┐
 │               HTTP Gateway (:8080)                  │
 │  POST /v1/chat/completions  (OpenAI-compatible)     │
 └──────────────────────┬──────────────────────────────┘
                        │
                        ▼
             ┌─────────────────┐
             │  Embed query    │  → EmbedderPort (OpenAI / local)
             └────────┬────────┘
                      │ vector (float32[])
                      ▼
         ┌────────────────────────┐
         │  Vector similarity     │  cosine search, k=5
         │  search in VectorDB    │  → VectorStorePort (Qdrant / Redis)
         └───────────┬────────────┘
                     │
          ┌──────────┴──────────┐
          │                     │
    score ≥ threshold      score < threshold
          │                     │
          ▼                     ▼
    ┌──────────┐        ┌──────────────────┐
    │  Cache   │        │ Forward to LLM   │ → LLMPort (OpenAI / passthrough)
    │  HIT ✓   │        │                  │
    └──────────┘        └────────┬─────────┘
          │                      │
          │              ┌───────▼──────────┐
          │              │ Async upsert     │  embed + store in VectorDB
          │              │ into VectorDB    │
          │              └──────────────────┘
          │                      │
          └──────────┬───────────┘
                     ▼
              Response to Client
              X-Cache: HIT | MISS
              X-Cache-Entry-Id: <uuid>
              X-Cache-Score: 0.9742  (on HIT)
```

---

## Project Layout

```
semantic-cache-gateway/
├── cmd/
│   └── gateway/
│       └── main.go              # composition root, DI wiring
├── internal/
│   ├── app/
│   │   └── app.go               # lifecycle management
│   ├── domain/
│   │   ├── domain.go            # entities + port interfaces (no infra)
│   │   └── cache/
│   │       ├── service.go       # core cache logic
│   │       └── service_test.go
│   └── adapters/
│       ├── httpserver/
│       │   ├── handler.go       # OpenAI-compatible HTTP layer
│       │   └── middleware.go
│       ├── embedder/
│       │   └── openaiembedder/  # EmbedderPort → OpenAI Embeddings API
│       ├── llm/
│       │   ├── openaillm/       # LLMPort → OpenAI Chat Completions
│       │   └── passthrough/     # LLMPort → raw HTTP proxy
│       └── vectordb/
│           ├── qdrant/          # VectorStorePort → Qdrant gRPC
│           └── redisstore/      # VectorStorePort → Redis KV + cosine scan
├── pkg/
│   ├── config/                  # YAML + env configuration
│   └── logger/                  # zap wrapper
├── config.example.yaml
├── docker-compose.yml
├── Dockerfile
└── go.mod
```

---

## Prerequisites

- Docker ≥ 24 and Docker Compose v2
- An OpenAI API key (or any compatible embedding + LLM endpoint)

---

## Setup

```bash
git clone https://github.com/guilhermebr/semantic-cache-gateway
cd semantic-cache-gateway

cp .env.example .env
# Edit .env and set OPENAI_API_KEY=sk-...

docker compose up --build -d
```

Verify the stack is healthy:

```bash
curl http://localhost:8080/healthz
# {"status":"ok"}
```

---

## Configuration

All settings can be provided via a YAML file (`-config` flag) or environment variables. Environment variables always take precedence.

| Env var | Default | Description |
|---|---|---|
| `SERVER_ADDR` | `:8080` | Listen address |
| `CACHE_SIMILARITY_THRESHOLD` | `0.92` | Minimum cosine similarity for a cache hit `(0, 1]` |
| `CACHE_TTL` | `24h` | How long entries live in the vector store |
| `EMBEDDER_PROVIDER` | `openai` | `openai` |
| `EMBEDDER_MODEL` | `text-embedding-3-small` | Any OpenAI embedding model |
| `EMBEDDER_API_KEY` | — | OpenAI key for embeddings |
| `LLM_PROVIDER` | `openai` | `openai` \| `passthrough` |
| `LLM_MODEL` | `gpt-4o-mini` | Upstream model name |
| `LLM_API_KEY` | — | OpenAI key for completions |
| `LLM_UPSTREAM_URL` | — | Used only by `passthrough` provider |
| `VECTORDB_PROVIDER` | `qdrant` | `qdrant` \| `redis` |
| `QDRANT_HOST` | `localhost` | Qdrant hostname |
| `QDRANT_COLLECTION` | `rag_cache` | Collection name |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |

---

## Usage Examples

### First request — cache MISS

The query has never been seen. The gateway forwards to OpenAI, stores the result, and returns it:

```bash
curl -s -i http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {"role": "user", "content": "What is the capital of France?"}
    ]
  }'
```

Expected response headers:

```
HTTP/1.1 200 OK
Content-Type: application/json
X-Cache: MISS
X-Cache-Entry-Id: 3f2504e0-4f89-11d3-9a0c-0305e82c3301
```

```json
{
  "id": "scg-1709123456789",
  "object": "chat.completion",
  "created": 1709123456,
  "model": "gpt-4o-mini",
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": "The capital of France is Paris."},
      "finish_reason": "stop"
    }
  ]
}
```

---

### Second request — cache HIT

A semantically equivalent (but not identical) query now hits the cache:

```bash
curl -s -i http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {"role": "user", "content": "Which city is the capital of France?"}
    ]
  }'
```

Expected response headers:

```
HTTP/1.1 200 OK
Content-Type: application/json
X-Cache: HIT
X-Cache-Entry-Id: 3f2504e0-4f89-11d3-9a0c-0305e82c3301
X-Cache-Score: 0.9742
```

The response body is identical to the first call, returned in microseconds with **zero LLM cost**.

---

## Running Tests

```bash
go test ./...
```

---

## Adding a Custom Adapter

The system is built around three port interfaces in [`internal/domain/domain.go`](internal/domain/domain.go):

| Interface | Implement to add… |
|---|---|
| `EmbedderPort` | A new embedding model (Cohere, Mistral, local) |
| `VectorStorePort` | A new vector database (Pinecone, pgvector, Weaviate) |
| `LLMPort` | A new LLM provider (Anthropic, Mistral, local vLLM) |

Wire the new adapter in `cmd/gateway/main.go` — no other files need to change.

---

## License

MIT
