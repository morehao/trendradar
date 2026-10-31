GO      ?= go
BIN     := bin/trendradar
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/morehao/trendradar/internal/ipc.Version=$(VERSION) -X github.com/morehao/trendradar/internal/ipc.Commit=$(COMMIT)

.PHONY: all build test test-eval lint snapshot clean

all: build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/trendradar

test:
	$(GO) vet ./... && $(GO) test ./...

# Live-model evaluation; needs .env or TRENDRADAR_EVAL_* env vars.
test-eval:
	$(GO) test -tags eval ./internal/summarize -run TestEvalSummaries -v

lint:
	golangci-lint run

snapshot:
	goreleaser release --snapshot --clean


clean:
	rm -rf bin dist
