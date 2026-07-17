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
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "(golangci-lint not installed; run 'make tools')"; \
	fi

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

##@ Kubernetes (kind)

IMAGES := gateway game-service engine-worker analysis-worker session-manager web
KIND_CLUSTER ?= alekhine

.PHONY: k8s-load
k8s-load: ## Build service images and load them into the kind cluster
	docker compose build $(IMAGES)
	@for svc in $(IMAGES); do \
		echo ">> loading alekhinescounter-gambit-$$svc into kind"; \
		kind load docker-image alekhinescounter-gambit-$$svc:latest --name $(KIND_CLUSTER); \
	done

.PHONY: k8s-ingress
k8s-ingress: ## Install the ingress-nginx controller on the kind cluster
	./infra/k8s/ingress-nginx.sh

.PHONY: k8s-deploy
k8s-deploy: k8s-load ## Deploy the platform to the kind cluster via Helm
	helm upgrade --install acg infra/helm/alekhine \
		-f infra/helm/alekhine/values-kind.yaml --wait --timeout 5m
	@echo ">> deployed. gateway on http://localhost:8088 (via the kind port map)"
	@echo ">> for the ingress on http://localhost:8888, run 'make k8s-ingress' once"

.PHONY: k8s-status
k8s-status: ## Show pod status
	kubectl get pods -o wide

##@ Load testing

.PHONY: load
load: ## Run the load-test suite (autocannon + k6); needs `make up` running
	@command -v k6 >/dev/null 2>&1 || { echo "k6 not installed: brew install k6"; exit 1; }
	cd load && npm install --silent
	@echo "\n>> GraphQL read throughput (autocannon)"
	cd load && DURATION=10 CONNECTIONS=60 node autocannon/graphql.js
	@echo "\n>> Full game flow (k6)"
	k6 run -e VUS=15 -e DURATION=20s --no-usage-report load/k6/game_flow.js
	@echo "\n>> WebSocket spectators (k6)"
	k6 run -e VUS=150 -e DURATION=20s --no-usage-report load/k6/spectate.js

.PHONY: chaos
chaos: ## Run the chaos + scalability suite against the kind cluster (see load/chaos)
	cd load && npm install --silent
	./load/chaos/chaos.sh all

##@ Housekeeping

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin
