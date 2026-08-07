SHELL := /bin/bash

# Local (non-docker) runs read .env if present.
-include .env
export

DATABASE_URL ?= postgres://memoria:memoria@localhost:5432/memoria?sslmode=disable
MIGRATIONS_DIR := db/migrations

.PHONY: help up down logs build run test lint sqlc migrate-up migrate-down migrate-status migrate-new tools openapi-validate seed-summertides

help: ## List targets
	@grep -E '^[a-zA-Z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-15s %s\n", $$1, $$2}'

up: ## Start full stack (api + postgres + minio) in Docker
	docker compose up -d --build

down: ## Stop the stack (keeps volumes)
	docker compose down

logs: ## Tail API logs
	docker compose logs -f api

build: ## Compile all packages
	go build ./...

run: ## Run the API locally against compose postgres
	go run ./cmd/api

test: ## Run all tests
	go test ./... -count=1

lint: ## Run golangci-lint
	golangci-lint run

sqlc: ## Regenerate type-safe DB code from db/queries
	sqlc generate

migrate-up: ## Apply pending migrations
	goose -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" up

migrate-down: ## Roll back the last migration
	goose -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" down

migrate-status: ## Show migration status
	goose -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" status

migrate-new: ## Create a migration: make migrate-new name=add_users
	@test -n "$(name)" || (echo "usage: make migrate-new name=add_users" && exit 1)
	goose -dir $(MIGRATIONS_DIR) create $(name) sql

tools: ## Install dev tools (goose, sqlc, golangci-lint)
	go install github.com/pressly/goose/v3/cmd/goose@latest
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

openapi-validate: ## Parse-check openapi.yaml
	@python3 -c "import yaml; yaml.safe_load(open('openapi.yaml')); print('openapi.yaml OK')" 2>/dev/null \
		|| (grep -q '^openapi:' openapi.yaml && grep -q '^paths:' openapi.yaml && echo 'openapi.yaml structure OK (pip install pyyaml for full YAML parse)')

seed-summertides: ## Seed marketing capsule Summertides 2026 (Recap screenshots)
	./scripts/seed_summertides_recap.sh
