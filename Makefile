.PHONY: run run-docker test test-coverage build lint tidy docker-up docker-down clean help

BINARY     := bin/gateway
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -s -w -X main.version=$(VERSION)
GOFLAGS    := CGO_ENABLED=0

# ── Development ────────────────────────────────────────────────────────────

## run: start the gateway locally using config.example.yaml
run:
	go run ./cmd/gateway --config config.example.yaml

## run-docker: start the full stack via Docker Compose
run-docker: docker-up

# ── Testing ────────────────────────────────────────────────────────────────

## test: run all tests with race detector
test:
	go test -race -count=1 -timeout=120s ./...

## test-coverage: run tests and open HTML coverage report
test-coverage:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "coverage report: coverage.html"

## bench: run benchmarks
bench:
	go test -bench=. -benchmem ./...

# ── Build ──────────────────────────────────────────────────────────────────

## build: compile a static binary to bin/gateway
build:
	$(GOFLAGS) go build \
		-ldflags="$(LDFLAGS)" \
		-trimpath \
		-o $(BINARY) \
		./cmd/gateway

## build-docker: build the production Docker image
build-docker:
	docker build \
		--build-arg VERSION=$(VERSION) \
		-t semantic-cache-gateway:$(VERSION) \
		-t semantic-cache-gateway:latest \
		.

# ── Code Quality ───────────────────────────────────────────────────────────

## lint: run golangci-lint
lint:
	golangci-lint run --timeout=5m

## fmt: format all Go source files
fmt:
	gofmt -w -s .
	goimports -w .

## vet: run go vet
vet:
	go vet ./...

## tidy: tidy and verify go modules
tidy:
	go mod tidy
	go mod verify

# ── Infrastructure ─────────────────────────────────────────────────────────

## docker-up: start Qdrant and gateway via Docker Compose
docker-up:
	docker compose up --build -d

## docker-down: stop and remove containers
docker-down:
	docker compose down

## docker-logs: follow gateway container logs
docker-logs:
	docker compose logs -f gateway

# ── Utilities ──────────────────────────────────────────────────────────────

## clean: remove build artifacts and coverage reports
clean:
	rm -rf bin/ coverage.out coverage.html

## help: print this help message
help:
	@echo "Usage: make <target>"
	@echo ""
	@grep -E '^## ' Makefile | sed 's/## /  /' | column -t -s ':'
