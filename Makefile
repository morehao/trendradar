GO      ?= go
BIN     := bin/trendradar
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/morehao/trendradar/internal/ipc.Version=$(VERSION) -X github.com/morehao/trendradar/internal/ipc.Commit=$(COMMIT)

.PHONY: all build test lint snapshot contract-hash clean

all: build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/trendradar

test:
	$(GO) vet ./... && $(GO) test ./...

lint:
	golangci-lint run

snapshot:
	goreleaser release --snapshot --clean

contract-hash:
	$(GO) run ./tools/contracthash -contract contract

clean:
	rm -rf bin dist
