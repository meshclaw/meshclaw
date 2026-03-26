#!/bin/bash
# Meshclaw installer - curl -sL https://raw.githubusercontent.com/meshclaw/meshclaw/main/install.sh | bash
set -e

VERSION="${MESHCLAW_VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BINARIES="wire vssh mpop meshclaw meshdb vault"

# Detect OS and architecture
detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "$ARCH" in
        x86_64|amd64)
            ARCH="amd64"
            ;;
        aarch64|arm64)
            ARCH="arm64"
            ;;
        *)
            echo "Unsupported architecture: $ARCH"
            exit 1
            ;;
    esac

    case "$OS" in
        linux|darwin)
            ;;
        *)
            echo "Unsupported OS: $OS"
            exit 1
            ;;
    esac

    PLATFORM="${OS}_${ARCH}"
}

# Get latest release version from GitHub
get_version() {
    if [ "$VERSION" = "latest" ]; then
        VERSION=$(curl -sL "https://api.github.com/repos/meshclaw/meshclaw/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
        if [ -z "$VERSION" ]; then
            VERSION="v1.0.0"
        fi
    fi
}

# Download binaries
download_binaries() {
    echo "Downloading meshclaw $VERSION for $PLATFORM..."

    TMPDIR=$(mktemp -d)
    trap "rm -rf $TMPDIR" EXIT

    BASE_URL="https://github.com/meshclaw/meshclaw/releases/download/$VERSION"

    for bin in $BINARIES; do
        echo "  Downloading $bin..."
        curl -sL "$BASE_URL/${bin}_${PLATFORM}" -o "$TMPDIR/$bin"
        chmod +x "$TMPDIR/$bin"
    done

    echo "Installing to $INSTALL_DIR..."
    for bin in $BINARIES; do
        sudo mv "$TMPDIR/$bin" "$INSTALL_DIR/$bin"
    done
}

# Build from source (fallback)
build_from_source() {
    echo "Building from source..."

    if ! command -v go &> /dev/null; then
        echo "Go is not installed. Please install Go 1.21+ first."
        exit 1
    fi

    TMPDIR=$(mktemp -d)
    trap "rm -rf $TMPDIR" EXIT

    git clone --depth 1 https://github.com/meshclaw/meshclaw.git "$TMPDIR/meshclaw"
    cd "$TMPDIR/meshclaw"

    make build

    echo "Installing to $INSTALL_DIR..."
    for bin in $BINARIES; do
        sudo cp "bin/$bin" "$INSTALL_DIR/$bin"
    done
}

# Setup systemd services (Linux only)
setup_services() {
    if [ "$OS" != "linux" ]; then
        return
    fi

    echo "Setting up systemd services..."

    # wire service
    sudo tee /etc/systemd/system/wire.service > /dev/null << 'EOF'
[Unit]
Description=Wire VPN Mesh
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/wire daemon
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    # vssh service
    sudo tee /etc/systemd/system/vssh.service > /dev/null << 'EOF'
[Unit]
Description=VSSH Secure Shell Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/vssh server
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    sudo systemctl daemon-reload
    echo "Services installed. Enable with: sudo systemctl enable --now wire vssh"
}

# Main
main() {
    echo ""
    echo "  meshclaw installer"
    echo ""

    detect_platform
    get_version

    # Try downloading binaries first
    if ! download_binaries 2>/dev/null; then
        echo "Binary download failed, building from source..."
        build_from_source
    fi

    setup_services

    echo ""
    echo "Installation complete!"
    echo ""
    echo "  Installed: $BINARIES"
    echo "  Location:  $INSTALL_DIR"
    echo ""
    echo "  Quick start:"
    echo "    mpop init           # Initialize configuration"
    echo "    mpop                 # Show dashboard"
    echo "    wire register       # Join VPN mesh"
    echo ""
}

main "$@"
