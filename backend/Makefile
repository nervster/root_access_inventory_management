# Shortcuts for common backend tasks. Run from backend/. `make` alone lists them.

MIGRATIONS := internal/db/migrations
# Same default as internal/platform/config; override with DATABASE_URL=... make <target>
DATABASE_URL ?= postgres://nms:nms_dev@localhost:5433/nms?sslmode=disable

.DEFAULT_GOAL := help
.PHONY: help run test generate migration migrate-status migrate-down

help: ## List the available commands
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  make %-16s %s\n", $$1, $$2}'

run: ## Start the API on :8080 (applies pending migrations first)
	go run ./cmd/api

test: ## Run all tests (needs `docker compose up -d`)
	go test -race ./...

generate: ## Regenerate internal/db/dbgen from the SQL in internal/db/queries
	go tool sqlc generate

migration: ## Create the next migration file: make migration name=create_plants
	@test -n "$(name)" || (echo "usage: make migration name=create_plants" && exit 1)
	go tool goose -dir $(MIGRATIONS) -s create $(name) sql

migrate-status: ## Show which migrations have run on the local database
	go tool goose -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" status

migrate-down: ## Roll back the most recent migration on the local database
	go tool goose -dir $(MIGRATIONS) postgres "$(DATABASE_URL)" down
