SHELL := /usr/bin/env bash

WEB_DIR := web
GO_PACKAGES := ./cmd/... ./internal/...

.PHONY: bootstrap hooks-install format format-check lint static-analysis test test-unit test-component test-integration test-contract test-e2e test-race test-fuzz-smoke test-performance-smoke coverage security-scan build precommit verify verify-ci verify-push run run-web run-daemon auth-token

bootstrap:
	npm ci --prefix $(WEB_DIR)

hooks-install:
	pre-commit install

format:
	gofmt -w cmd internal
	npm run format --prefix $(WEB_DIR)

format-check:
	test -z "$$(gofmt -l cmd internal)"
	npm run format:check --prefix $(WEB_DIR)

lint:
	go vet $(GO_PACKAGES)
	npm run lint --prefix $(WEB_DIR)

static-analysis:
	bash scripts/static-analysis.sh

test: test-unit test-component test-integration

test-unit:
	go test $(GO_PACKAGES)

test-component:
	npm run test:coverage --prefix $(WEB_DIR)

test-integration:
	go test ./internal/api

test-contract:
	go test ./internal/api -run '^TestSessionAPIContract$$'

test-e2e:
	npm run test:e2e --prefix $(WEB_DIR)

test-race:
	bash scripts/test-race.sh

test-fuzz-smoke:
	go test ./internal/config -run='^$$' -fuzz=FuzzLoad -fuzztime=3s
	go test ./internal/privacy -run='^$$' -fuzz=FuzzSanitize -fuzztime=3s
	go test ./internal/normalize/codex -run='^$$' -fuzz=FuzzNormalize -fuzztime=3s

test-performance-smoke:
	bash scripts/performance-smoke.sh

coverage:
	bash scripts/coverage.sh

security-scan:
	bash scripts/security-scan.sh

build:
	go build ./cmd/telemetryiq
	npm run build --prefix $(WEB_DIR)

precommit: format-check lint static-analysis test-unit test-component

verify-ci: format-check lint static-analysis test-unit test-component test-integration test-race test-contract test-fuzz-smoke test-performance-smoke test-e2e coverage build

verify: format-check lint static-analysis test-unit test-component test-integration coverage security-scan build

verify-push: verify test-race test-contract test-fuzz-smoke test-performance-smoke test-e2e

run-daemon:
	go run ./cmd/telemetryiq

auth-token:
	go run ./cmd/telemetryiq auth-token

run-web:
	npm run dev --prefix $(WEB_DIR)

run:
	@echo "Run 'make run-daemon' and 'make run-web' in separate terminals."
