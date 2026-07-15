# Alekhine's Counter-Gambit — dev entrypoint
# Run `make` or `make help` to list targets.

SHELL := /bin/bash
.DEFAULT_GOAL := help

# Tool versions
BUF_VERSION            := v1.47.2
PROTOC_GEN_GO          := google.golang.org/protobuf/cmd/protoc-gen-go@v1.35.2
PROTOC_GEN_GO_GRPC     := google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
GOOSE                  := github.com/pressly/goose/v3/cmd/goose@v3.22.1
GRPCURL                := github.com/fullstorydev/grpcurl/cmd/grpcurl@v1.9.2
GOLANGCI_LINT          := github.com/golangci/golangci-lint/cmd/golangci-lint@v1.62.2

# Local defaults (override via env)
ACG_POSTGRES_DSN ?= postgres://acg:acg@localhost:5433/acg?sslmode=disable
export ACG_POSTGRES_DSN

GOBIN := $(shell go env GOPATH)/bin

##@ General

.PHONY: help
help: ## Print this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage: make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Setup

.PHONY: tools
tools: ## Install dev tools (buf, protoc plugins, goose, grpcurl, linter)
	@echo ">> installing dev tools into $(GOBIN)"
	go install $(PROTOC_GEN_GO)
	go install $(PROTOC_GEN_GO_GRPC)
	go install $(GOOSE)
	go install $(GRPCURL)
	go install $(GOLANGCI_LINT)
	@command -v buf >/dev/null 2>&1 || go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	@echo ">> done. Ensure $(GOBIN) is on your PATH."

##@ Protobuf

.PHONY: proto
proto: ## Regenerate protobuf Go stubs from proto/
	buf generate

.PHONY: proto-lint
proto-lint: ## Lint proto definitions
	buf lint

##@ Build & test

.PHONY: build
build: ## Build all Go services into ./bin
	@mkdir -p bin
	go build -o bin/ ./services/... ./cmd/...

.PHONY: test
test: ## Run all Go unit tests
	go test ./...

.PHONY: lint
lint: ## Run gofmt check, go vet, and golangci-lint
	@test -z "$$(gofmt -l . | grep -v /gen/)" || (echo "gofmt needed:"; gofmt -l . | grep -v /gen/; exit 1)
	go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "(golangci-lint not installed; run 'make tools')"

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

##@ Database

.PHONY: migrate
migrate: ## Apply DB migrations (goose up)
	goose -dir migrations postgres "$(ACG_POSTGRES_DSN)" up

.PHONY: migrate-down
migrate-down: ## Roll back the last migration
	goose -dir migrations postgres "$(ACG_POSTGRES_DSN)" down

.PHONY: migrate-status
migrate-status: ## Show migration status
	goose -dir migrations postgres "$(ACG_POSTGRES_DSN)" status

##@ Local stack

.PHONY: up
up: ## Bring up the local stack (postgres, engine-worker, game-service)
	docker compose up -d --build

.PHONY: down
down: ## Tear down the local stack
	docker compose down

.PHONY: logs
logs: ## Tail stack logs
	docker compose logs -f

.PHONY: run-game
run-game: ## Play a game vs the engine via the CLI shim
	go run ./cmd/play

##@ Housekeeping

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin
