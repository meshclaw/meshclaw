.PHONY: all build clean test install linux linux-arm64 darwin deploy deploy-vps deploy-amd64 deploy-arm64 deploy-vssh deploy-mpop

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

# Build for macOS (with codesign)
darwin:
	@for bin in $(BINARIES); do \
		GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$${bin}_darwin_amd64 ./cmd/$$bin; \
		codesign -s - bin/$${bin}_darwin_amd64 2>/dev/null || true; \
		GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/$${bin}_darwin_arm64 ./cmd/$$bin; \
		codesign -s - bin/$${bin}_darwin_arm64 2>/dev/null || true; \
	done

# Install to /usr/local/bin (with codesign for macOS)
install: build
	@for bin in $(BINARIES); do \
		sudo cp bin/$$bin /usr/local/bin/; \
		sudo codesign -s - /usr/local/bin/$$bin 2>/dev/null || true; \
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

# Server groups
VPS_SERVERS := v1 v2 v3 v4
AMD64_SERVERS := d1 d2 n1 s1 s2
ARM64_SERVERS := g1 g2 g3 g4

# Deploy to all servers (parallel)
deploy: linux linux-arm64
	@echo "Deploying to all servers in parallel..."
	@for srv in $(VPS_SERVERS) $(AMD64_SERVERS); do \
		(vssh put $$srv bin/vssh_linux_amd64 /usr/local/bin/vssh 2>/dev/null && \
		 vssh put $$srv bin/mpop_linux_amd64 /usr/local/bin/mpop 2>/dev/null && \
		 vssh exec $$srv "chmod +x /usr/local/bin/vssh /usr/local/bin/mpop" 2>/dev/null && \
		 echo "$$srv: OK") & \
	done; \
	for srv in $(ARM64_SERVERS); do \
		(vssh put $$srv bin/vssh_linux_arm64 /usr/local/bin/vssh 2>/dev/null && \
		 vssh put $$srv bin/mpop_linux_arm64 /usr/local/bin/mpop 2>/dev/null && \
		 vssh exec $$srv "chmod +x /usr/local/bin/vssh /usr/local/bin/mpop" 2>/dev/null && \
		 echo "$$srv: OK") & \
	done; \
	wait
	@echo "Done"

# Deploy to VPS (amd64) - parallel
deploy-vps: linux
	@for srv in $(VPS_SERVERS); do \
		(vssh put $$srv bin/vssh_linux_amd64 /usr/local/bin/vssh 2>/dev/null && \
		 vssh put $$srv bin/mpop_linux_amd64 /usr/local/bin/mpop 2>/dev/null && \
		 vssh exec $$srv "chmod +x /usr/local/bin/vssh /usr/local/bin/mpop" 2>/dev/null && \
		 echo "$$srv: OK") & \
	done; wait

# Deploy to AMD64 servers - parallel
deploy-amd64: linux
	@for srv in $(AMD64_SERVERS); do \
		(vssh put $$srv bin/vssh_linux_amd64 /usr/local/bin/vssh 2>/dev/null && \
		 vssh put $$srv bin/mpop_linux_amd64 /usr/local/bin/mpop 2>/dev/null && \
		 vssh exec $$srv "chmod +x /usr/local/bin/vssh /usr/local/bin/mpop" 2>/dev/null && \
		 echo "$$srv: OK") & \
	done; wait

# Deploy to ARM64 servers - parallel
deploy-arm64: linux-arm64
	@for srv in $(ARM64_SERVERS); do \
		(vssh put $$srv bin/vssh_linux_arm64 /usr/local/bin/vssh 2>/dev/null && \
		 vssh put $$srv bin/mpop_linux_arm64 /usr/local/bin/mpop 2>/dev/null && \
		 vssh exec $$srv "chmod +x /usr/local/bin/vssh /usr/local/bin/mpop" 2>/dev/null && \
		 echo "$$srv: OK") & \
	done; wait

# Deploy single binary to all servers - parallel
deploy-vssh: linux linux-arm64
	@for srv in $(VPS_SERVERS) $(AMD64_SERVERS); do \
		(vssh put $$srv bin/vssh_linux_amd64 /usr/local/bin/vssh 2>/dev/null && \
		 vssh exec $$srv "chmod +x /usr/local/bin/vssh" 2>/dev/null && echo "$$srv: OK") & \
	done; \
	for srv in $(ARM64_SERVERS); do \
		(vssh put $$srv bin/vssh_linux_arm64 /usr/local/bin/vssh 2>/dev/null && \
		 vssh exec $$srv "chmod +x /usr/local/bin/vssh" 2>/dev/null && echo "$$srv: OK") & \
	done; wait

deploy-mpop: linux linux-arm64
	@for srv in $(VPS_SERVERS) $(AMD64_SERVERS); do \
		(vssh put $$srv bin/mpop_linux_amd64 /usr/local/bin/mpop 2>/dev/null && \
		 vssh exec $$srv "chmod +x /usr/local/bin/mpop" 2>/dev/null && echo "$$srv: OK") & \
	done; \
	for srv in $(ARM64_SERVERS); do \
		(vssh put $$srv bin/mpop_linux_arm64 /usr/local/bin/mpop 2>/dev/null && \
		 vssh exec $$srv "chmod +x /usr/local/bin/mpop" 2>/dev/null && echo "$$srv: OK") & \
	done; wait
