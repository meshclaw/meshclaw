.PHONY: all build clean test install linux linux-arm64 darwin

VERSION := 1.0.0
LDFLAGS := -s -w
BINARIES := wire vssh mpop meshclaw meshdb vault

# Default target
all: build

# Build all binaries
build:
	go build -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

# Build individual binaries
wire:
	go build -ldflags "$(LDFLAGS)" -o bin/wire ./cmd/wire

vssh:
	go build -ldflags "$(LDFLAGS)" -o bin/vssh ./cmd/vssh

mpop:
	go build -ldflags "$(LDFLAGS)" -o bin/mpop ./cmd/mpop

meshclaw:
	go build -ldflags "$(LDFLAGS)" -o bin/meshclaw ./cmd/meshclaw

meshdb:
	go build -ldflags "$(LDFLAGS)" -o bin/meshdb ./cmd/meshdb

vault:
	go build -ldflags "$(LDFLAGS)" -o bin/vault ./cmd/vault

# Cross-compile for Linux amd64
linux:
	@for bin in $(BINARIES); do \
		GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$${bin}_linux_amd64 ./cmd/$$bin; \
	done

# Cross-compile for Linux arm64
linux-arm64:
	@for bin in $(BINARIES); do \
		GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/$${bin}_linux_arm64 ./cmd/$$bin; \
	done

# Build for macOS
darwin:
	@for bin in $(BINARIES); do \
		GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$${bin}_darwin_amd64 ./cmd/$$bin; \
		GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/$${bin}_darwin_arm64 ./cmd/$$bin; \
	done

# Install to /usr/local/bin
install: build
	@for bin in $(BINARIES); do \
		cp bin/$$bin /usr/local/bin/; \
	done

# Clean build artifacts
clean:
	rm -rf bin/*

# Run tests
test:
	go test ./...

# Format code
fmt:
	go fmt ./...

# Vet code
vet:
	go vet ./...

# Release - build for all platforms
release: clean
	mkdir -p bin
	$(MAKE) linux
	$(MAKE) linux-arm64
	$(MAKE) darwin
	@echo "Built binaries:"
	@ls -la bin/
