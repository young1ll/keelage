MODULE  := github.com/young1ll/keelage
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN     := bin

.PHONY: all build test lint schema check-generated tidy clean dogfood-record dogfood-verify wasm pg-start pg-stop test-pg

all: lint test build

build:
	@mkdir -p $(BIN)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/keelage ./cmd/keelage
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/keelage-server ./cmd/keelage-server

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

# Go 구조체 → JSON Schema. spec/schema는 생성물이며 손으로 고치지 않는다.
schema:
	go run ./tools/schemagen -out spec/schema

# CI: 생성물이 커밋본과 다르면 실패.
check-generated: schema
	git diff --exit-code -- spec/schema

tidy:
	go mod tidy

clean:
	rm -rf $(BIN)

# dogfood: 이 리포의 닻을 개인 원장에 기록하고 검증한다 (docs/metrics/stale-false-positives.md).
DOGFOOD_ANCHORS := code://internal/core/envelope.go code://internal/core/scope.go code://internal/app/pipeline.go code://internal/adapter/sqlite/ledger.go code://internal/adapter/treesitter/queries/typescript.scm
dogfood-record: build
	$(BIN)/keelage anchor add $(DOGFOOD_ANCHORS)
dogfood-verify: build
	$(BIN)/keelage verify --changed --fail-on none

# tree-sitter 런타임+문법을 wasm 하나로 (internal/adapter/treesitter/VERSION 참조).
wasm:
	./scripts/build-treesitter-wasm.sh

# 서버 원장 테스트: 일회용 Postgres 16 (scripts/pg-test.sh). KEELAGE_TEST_PG 없으면 해당 테스트는 skip.
pg-start:
	./scripts/pg-test.sh start
pg-stop:
	./scripts/pg-test.sh stop
test-pg:
	KEELAGE_TEST_PG="$$(./scripts/pg-test.sh start)" go test -race -count=1 ./internal/adapter/postgres/... ./cmd/keelage-server/...; ./scripts/pg-test.sh stop
