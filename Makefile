.DEFAULT_GOAL := help

ifneq (,$(wildcard .env))
include .env
export
endif

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: help up stop down build run worker migrate test itest test-integration lint tidy generate secrets loadtest

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

test-integration: ## run tests against the local database
	SPACE_TEST_DATABASE_URL="postgres://space:space@localhost:5432/space?sslmode=disable" \
		go test -race -count=1 ./... -run Integration -v

itest: ## run every test, integration included
	SPACE_TEST_DATABASE_URL="postgres://space:space@localhost:5432/space?sslmode=disable" \
		go test -race -count=1 ./...

worker:
	go run ./cmd/worker/

loadtest:
	k6 run -e API=$(API) -e TOKEN=$(TOKEN) -e SPACE=$(SPACE) loadtest/booking.js
