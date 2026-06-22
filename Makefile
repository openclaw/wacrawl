.PHONY: build test lint coverage sqlc check

COVERAGE_THRESHOLD ?= 85.0

build:
	mkdir -p bin
	go build -o bin/wacrawl ./cmd/wacrawl

test:
	go test ./...

lint:
	golangci-lint run ./...

coverage:
	./scripts/coverage.sh $(COVERAGE_THRESHOLD)

sqlc:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

check: lint coverage build
