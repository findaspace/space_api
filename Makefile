.DEFAULT_GOAL := help

ifneq (,$(wildcard .env))
include .env
export
endif

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: help up down build run migrate test lint tidy generate

help:
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "%-10s %s\n", $$1, $$2}'

up: ## start local dependencies
	docker compose up -d --wait

stop:
	docker compose stop

down: ## stop and wipe local dependencies
	docker compose down -v

build: ## compile both binaries into bin/
	go build -trimpath -ldflags="$(LDFLAGS)" -o bin/ ./cmd/...

run: ## run the api
	go run ./cmd/api

migrate: ## apply pending migrations
	go run ./cmd/migrate

test: ## run tests with the race detector
	go test -race -count=1 ./...

lint:
	go vet ./...
	golangci-lint run

tidy:
	go mod tidy

generate: ## regenerate sqlc output
	sqlc generate
