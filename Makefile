MODULE  := github.com/young1ll/keelage
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN     := bin

.PHONY: all build test lint schema check-generated tidy clean

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
