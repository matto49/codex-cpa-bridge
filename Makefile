GO ?= go
GOFMT ?= gofmt
BINARY ?= bin/bridge-go
REMOTE_BINARY ?= bin/bridge-go-linux-amd64
TARGET_TRIPLE ?= $(shell rustc -vV | sed -n 's/^host: //p')
DEVBOX_MANIFEST ?= $(HOME)/.config/codex-cpa-bridge/bridge.toml
SIDECAR = ui/src-tauri/binaries/bridge-go-$(TARGET_TRIPLE)

.PHONY: build remote-binary sidecar test fmt doctor-devbox status-devbox

build:
	$(GO) build -o $(BINARY) ./cmd/bridge

remote-binary:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o $(REMOTE_BINARY) ./cmd/bridge

sidecar:
	mkdir -p ui/src-tauri/binaries
	$(GO) build -o $(SIDECAR) ./cmd/bridge

fmt:
	$(GOFMT) -w cmd internal

test:
	$(GO) test ./...

doctor-devbox: build
	./$(BINARY) --manifest $(DEVBOX_MANIFEST) doctor

status-devbox: build
	./$(BINARY) --manifest $(DEVBOX_MANIFEST) status
