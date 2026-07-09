.PHONY: build test lint coverage sqlc check release-artifacts

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

release-artifacts:
	@test -n "$(VERSION)" || (echo "usage: make release-artifacts VERSION=vX.Y.Z" >&2; exit 2)
	@helper="$${MAC_RELEASE_HELPER:-$$HOME/Projects/agent-scripts/skills/release-mac-app/scripts/mac-release}"; \
	"$$helper" codesign-run -- ./scripts/package-wacrawl-release.sh "$(VERSION)"
