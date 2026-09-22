GO ?= /data00/home/wangyang.49/.local/bin/go
GOFMT ?= /data00/home/wangyang.49/.local/bin/gofmt
BINARY ?= bin/bridge-go

.PHONY: build test fmt doctor-devbox status-devbox

build:
	$(GO) build -o $(BINARY) ./cmd/bridge

fmt:
	$(GOFMT) -w cmd internal

test: fmt
	$(GO) test ./...

doctor-devbox: build
	./$(BINARY) --manifest examples/devbox.toml doctor

status-devbox: build
	./$(BINARY) --manifest examples/devbox.toml status
