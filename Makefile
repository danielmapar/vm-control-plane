# Thin convenience wrapper — every target is runnable without make (see the
# underlying go commands). Windows: `winget install ezwinports.make`, or run
# the go commands directly.

GOBIN := $(shell go env GOPATH)/bin

.PHONY: dev test race lint build build-all proto tools clean

dev: ## Run the full Tier-0 stack (embedded Postgres + control-plane + agent)
	go run ./scripts/dev

test:
	go test ./...

race:
	go test -race ./internal/...

lint:
	$(GOBIN)/golangci-lint run

build:
	go build -o bin/ ./cmd/...

build-all: ## Cross-compile gate: both GOOSes must always build.
	GOOS=linux go build ./... && GOOS=windows go build ./...

proto:
	$(GOBIN)/buf generate

tools: ## Pinned toolchain bootstrap (verified working on Windows).
	go install github.com/bufbuild/buf/cmd/buf@v1.72.0
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2

clean:
	rm -rf bin
