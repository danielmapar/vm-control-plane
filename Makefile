# Thin convenience wrapper — every target is runnable without make (see the
# underlying go commands). Recipes assume a POSIX sh (ezwinports make on
# Windows ships one); on native Windows without make, run the go commands
# directly.

GOBIN := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)

.PHONY: build test race stress lint build-all proto tools dev clean

build: ## Default: compile all binaries into bin/.
	go build -o bin/ ./cmd/...

test:
	go test ./...

race: ## Race detector across the whole tree (CI-required).
	go test -race ./...

stress: ## Bounded repeat runs to shake out flaky concurrency (grows with hot packages).
	go test -race -count=3 -timeout=15m ./internal/...

lint:
	"$(GOBIN)/golangci-lint" run

build-all: ## Cross-compile gate: both GOOSes must always build.
	GOOS=linux go build ./... && GOOS=windows go build ./...

proto:
	"$(GOBIN)/buf" generate

tools: ## Pinned toolchain bootstrap (verified working on Windows).
	go install github.com/bufbuild/buf/cmd/buf@v1.72.0
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2

dev: ## Tier-0 stack (embedded Postgres + control-plane + agent).
	@if [ -d scripts/dev ]; then go run ./scripts/dev; else \
		echo "make dev lands with the Tier-0 E2E PR (see docs/implementation-plan.md §9)"; exit 1; fi

clean:
	rm -rf bin
