APP     := stream_monitor
MODULE  := stream_monitor
GOFLAGS :=

# Build output directory
DIST    := dist

# ──────────────────────────────────────────────────────────────────────────────
.PHONY: all build run test vet fmt lint clean linux darwin help

## all: default target — vet, then build
all: vet build

## build: compile for current platform
build:
	@mkdir -p $(DIST)
	go build $(GOFLAGS) -o $(DIST)/$(APP).exe .

## run: build and run
run: build
	./$(DIST)/$(APP).exe

## test: run all tests
test:
	go test ./...

## vet: run go vet on all packages
vet:
	go vet ./...

## fmt: format all Go source files
fmt:
	gofmt -w .

## lint: run staticcheck if available, fall back to go vet
lint:
	@if command -v staticcheck >/dev/null 2>&1; then \
		staticcheck ./...; \
	else \
		echo "staticcheck not found, running go vet instead"; \
		go vet ./...; \
	fi

## linux: cross-compile for Linux amd64
linux:
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o $(DIST)/$(APP)-linux-amd64 .

## darwin: cross-compile for macOS arm64
darwin:
	@mkdir -p $(DIST)
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -o $(DIST)/$(APP)-darwin-arm64 .

## clean: remove build artifacts
clean:
	rm -rf $(DIST)

## help: show this help
help:
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':' 2>/dev/null || sed -n 's/^## //p' $(MAKEFILE_LIST)
