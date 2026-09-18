# experiment-go — developer entrypoints.
# Run `make` or `make help` to see everything available, including make/*.mk.

APP_NAME    := experiment-go
MAIN_PKG    := ./cmd/api
BIN_DIR     := bin
BIN         := $(BIN_DIR)/api
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)
COVER_FILE  := coverage.out

DOCKER_IMAGE ?= $(APP_NAME)
DOCKER_TAG   ?= $(VERSION)
COMPOSE      := docker compose

GOLANGCI     := $(BIN_DIR)/golangci-lint
GOLANGCI_PKG := github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

# Local overrides live in .env (copy .env.example). Exported so `make run`,
# docker compose and the migrate targets all see the same values.
-include .env
export

POSTGRES_USER     ?= experiment
POSTGRES_PASSWORD ?= wIzPdGJ340MziIHNN5upglMh4vTy54MF
POSTGRES_DB       ?= experiment
POSTGRES_PORT     ?= 5432
HTTP_PORT         ?= 8080

# From the host the database is on localhost; from inside a container it is either
# host.docker.internal (published port) or "db" (attached to the compose network).
LOCAL_DB_URL    := postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable
DOCKER_DB_URL   := postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@host.docker.internal:$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable
COMPOSE_NETWORK ?= $(APP_NAME)_default
# Migrations are embedded in the binary: `api migrate ...` is the whole tool.
MIGRATE      := DATABASE_URL="$(LOCAL_DB_URL)" go run $(MAIN_PKG) migrate
# Same commands through the built image instead of the host toolchain:
MIGRATE_DOCKER := $(COMPOSE) run --rm api migrate

BASE_URL ?= http://localhost:$(HTTP_PORT)

.DEFAULT_GOAL := help
SHELL := /bin/bash

## ---------------------------------------------------------------- meta

.PHONY: help
help: ## Show this help
	@echo "$(APP_NAME) $(VERSION)"
	@echo
	@grep -hE '^[a-zA-Z0-9_.-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "} {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: init
init: ## First-time setup: create .env and download dependencies
	@test -f .env || { cp .env.example .env; echo "created .env"; }
	@$(MAKE) deps

## ---------------------------------------------------------------- dependencies

.PHONY: deps
deps: ## Download module dependencies
	go mod download

.PHONY: tidy
tidy: ## Add missing and remove unused modules
	go mod tidy

.PHONY: verify
verify: ## Verify dependencies against go.sum
	go mod verify

.PHONY: upgrade
upgrade: ## Upgrade all direct dependencies to their latest minor/patch
	go get -u ./...
	go mod tidy

## ---------------------------------------------------------------- quality

.PHONY: fmt
fmt: ## Format the code
	go fmt ./...

.PHONY: fmt-check
fmt-check: ## Fail if any file is not gofmt-formatted
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "not formatted:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Run go vet
	go vet ./...

# Installs golangci-lint into bin/ on first use (not listed in help).
$(GOLANGCI):
	GOBIN=$(CURDIR)/$(BIN_DIR) go install $(GOLANGCI_PKG)

.PHONY: lint
lint: $(GOLANGCI) ## Run golangci-lint
	$(GOLANGCI) run

.PHONY: lint-fix
lint-fix: $(GOLANGCI) ## Run golangci-lint with autofixes
	$(GOLANGCI) run --fix

.PHONY: test
test: ## Run tests
	go test ./...

.PHONY: test-race
test-race: ## Run tests with the race detector
	go test -race -count=1 ./...

.PHONY: cover
cover: ## Run tests and write an HTML coverage report
	go test -coverprofile=$(COVER_FILE) -covermode=atomic ./...
	go tool cover -func=$(COVER_FILE) | tail -1
	go tool cover -html=$(COVER_FILE) -o coverage.html
	@echo "report: coverage.html"

.PHONY: check
check: fmt-check vet lint test ## Everything CI runs

## ---------------------------------------------------------------- build & run

.PHONY: build
build: ## Build the binary into bin/
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) $(MAIN_PKG)
	@echo "built $(BIN) ($(VERSION))"

.PHONY: run
run: ## Run the API on the host against the compose database
	DATABASE_URL="$(LOCAL_DB_URL)" MIGRATE_ON_START=true LOG_FORMAT=text LOG_LEVEL=debug go run $(MAIN_PKG)

.PHONY: clean
clean: ## Remove build and coverage artifacts
	rm -rf $(BIN_DIR) $(COVER_FILE) coverage.html

## ---------------------------------------------------------------- docker

.PHONY: docker-build
docker-build: ## Build the production image
	docker build --build-arg VERSION=$(VERSION) -t $(DOCKER_IMAGE):$(DOCKER_TAG) -t $(DOCKER_IMAGE):latest .

.PHONY: docker-run
docker-run: ## Run the image against a Postgres on the host (make up db first)
	docker run --rm --name $(APP_NAME) -p $(HTTP_PORT):8080 \
		-e DATABASE_URL="$(DOCKER_DB_URL)" \
		-e MIGRATE_ON_START=true \
		-e APP_ENV=development \
		--add-host=host.docker.internal:host-gateway \
		$(DOCKER_IMAGE):$(DOCKER_TAG)

.PHONY: docker-run-net
docker-run-net: ## Run the image inside the compose network (db reachable as "db")
	docker run --rm --name $(APP_NAME) -p $(HTTP_PORT):8080 \
		--network $(COMPOSE_NETWORK) \
		--env-file .env \
		-e POSTGRES_HOST=db \
		-e POSTGRES_PORT=5432 \
		-e HTTP_PORT=8080 \
		-e MIGRATE_ON_START=true \
		$(DOCKER_IMAGE):$(DOCKER_TAG)

.PHONY: docker-shell
docker-shell: ## Open a shell in the image without starting the server
	docker run --rm -it --entrypoint sh $(DOCKER_IMAGE):$(DOCKER_TAG)

.PHONY: docker-images
docker-images: ## List the tags built from this project
	docker images $(DOCKER_IMAGE)

.PHONY: docker-push
docker-push: ## Push the image (set DOCKER_IMAGE to your registry path)
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)

## ---------------------------------------------------------------- compose stack

.PHONY: up
up: ## Start the full stack (db + migrations + api) in the background
	$(COMPOSE) up --build -d
	@echo "api on $(BASE_URL)"

.PHONY: up-db
up-db: ## Start only Postgres (for `make run` or `make docker-run`)
	$(COMPOSE) up -d db

.PHONY: up-fg
up-fg: ## Start the full stack in the foreground
	$(COMPOSE) up --build

.PHONY: down
down: ## Stop the stack
	$(COMPOSE) down

.PHONY: down-hard
down-hard: ## Stop the stack and delete the database volume
	$(COMPOSE) down -v

.PHONY: restart
restart: ## Rebuild and restart just the api service
	$(COMPOSE) up --build -d api

.PHONY: logs
logs: ## Tail api logs
	$(COMPOSE) logs -f api

.PHONY: ps
ps: ## Show stack status
	$(COMPOSE) ps

.PHONY: sh
sh: ## Shell into the running api container
	$(COMPOSE) exec api sh

.PHONY: psql
psql: ## Open a psql session on the compose database
	$(COMPOSE) exec db psql -U $(POSTGRES_USER) -d $(POSTGRES_DB)

## ---------------------------------------------------------------- migrations

.PHONY: migrate-up
migrate-up: ## Apply all pending migrations
	$(MIGRATE) up

.PHONY: migrate-down
migrate-down: ## Roll back the last migration (make migrate-down n=2 | n=all)
	$(MIGRATE) down $(or $(n),1)

.PHONY: migrate-version
migrate-version: ## Show the current schema version
	$(MIGRATE) version

.PHONY: migrate-force
migrate-force: ## Clear a dirty state: make migrate-force version=1
	@test -n "$(version)" || { echo "usage: make migrate-force version=1"; exit 1; }
	$(MIGRATE) force $(version)

.PHONY: migrate-docker
migrate-docker: ## Run migrations through the container: make migrate-docker cmd="up"
	$(MIGRATE_DOCKER) $(or $(cmd),up)

.PHONY: migrate-new
migrate-new: ## Create a migration pair: make migrate-new name=add_users
	@test -n "$(name)" || { echo "usage: make migrate-new name=add_users"; exit 1; }
	@last=$$(ls migrations/*.up.sql 2>/dev/null | sed -E 's|.*/0*([0-9]+)_.*|\1|' | sort -n | tail -1); \
	next=$$(printf "%06d" $$(( $${last:-0} + 1 ))); \
	touch "migrations/$${next}_$(name).up.sql" "migrations/$${next}_$(name).down.sql"; \
	echo "created migrations/$${next}_$(name).{up,down}.sql"

## ---------------------------------------------------------------- smoke test

.PHONY: smoke
smoke: ## Hit the running API with a few requests
	@set -e; \
	echo "GET /healthz";        curl -fsS $(BASE_URL)/healthz; echo; \
	echo "GET /readyz";         curl -fsS $(BASE_URL)/readyz; echo; \
	echo "POST /api/v1/tasks";  curl -fsS -X POST $(BASE_URL)/api/v1/tasks \
		-H 'Content-Type: application/json' -d '{"title":"first task"}'; echo; \
	echo "GET /api/v1/tasks";   curl -fsS $(BASE_URL)/api/v1/tasks; echo

## ------------------------------------------------------------------ load tests
## Cloud-neutral k6 runs against BASE_URL. In-cluster runs live in make/k8s.mk.

K6 ?= k6

.PHONY: loadtest-smoke
loadtest-smoke: ## k6 smoke test against BASE_URL
	$(K6) run -e BASE_URL=$(BASE_URL) loadtest/k6/smoke.js

.PHONY: loadtest-load
loadtest-load: ## k6 steady load: make loadtest-load RATE=100 DURATION=3m
	$(K6) run -e BASE_URL=$(BASE_URL) -e RATE=$(or $(RATE),50) -e DURATION=$(or $(DURATION),2m) \
		loadtest/k6/load.js

.PHONY: loadtest-stress
loadtest-stress: ## k6 ramping stress test (drives the HPA)
	$(K6) run -e BASE_URL=$(BASE_URL) -e BURN_MS=$(or $(BURN_MS),50) \
		-e PEAK_RATE=$(or $(PEAK_RATE),150) loadtest/k6/stress.js

## ---------------------------------------------------------------------- clouds
## Google Cloud targets: make/gcp.mk. AWS targets: make/aws.mk.
## kubectl targets for either cluster: make/k8s.mk, switched with CLOUD=gcp|aws.

include make/gcp.mk
include make/aws.mk
include make/k8s.mk
