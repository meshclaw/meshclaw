# Meshclaw

Distributed infrastructure toolkit with mesh networking, secure shell, and AI worker runtime.

## Components

| Binary | Description |
|--------|-------------|
| `wire` | VPN mesh networking daemon |
| `vssh` | Secure shell over mesh network |
| `mpop` | Server status dashboard & CLI |
| `meshclaw` | AI worker runtime |
| `meshdb` | Local distributed database |
| `vault` | Secrets management |

## Installation

### One-line install

```bash
curl -sL https://raw.githubusercontent.com/meshclaw/meshclaw/main/install.sh | bash
```

### Build from source

```bash
git clone https://github.com/meshclaw/meshclaw.git
cd meshclaw
make build
sudo make install
```

## Quick Start

```bash
# Initialize mpop configuration
mpop init

# View server dashboard
mpop

# Register with VPN mesh
wire register

# Start services (Linux)
sudo systemctl enable --now wire vssh
```

## mpop - Dashboard CLI

Monitor infrastructure from the command line.

```bash
mpop                    # Show dashboard
mpop exec all uptime    # Run on all servers
mpop peers              # List VPN peers
mpop info node1         # Server details
```

## wire - VPN Mesh

P2P VPN mesh with automatic NAT traversal and relay support.

```bash
wire register           # Register with coordinator
wire daemon             # Run as daemon
wire status             # Show connection status
wire peers              # List peers
```

## vssh - Secure Shell

Execute commands on remote servers over the mesh network.

```bash
vssh exec node1 uptime          # Run command
vssh server                     # Start vssh server
vssh cp localfile node1:/path   # Copy file
```

## meshclaw - AI Worker

Distributed AI worker runtime for running LLM tasks.

```bash
meshclaw init assistant         # Create worker from template
meshclaw start assistant        # Start worker
meshclaw ask assistant "query"  # One-shot query
meshclaw chat assistant         # Interactive chat
meshclaw ps                     # List workers
meshclaw templates              # List templates
```

### Templates

- `assistant` - General purpose with bash tools
- `system-monitor` - CPU/memory/disk monitoring
- `code-reviewer` - Git diff review
- `research` - Web research and summary
- `devops` - Infrastructure automation

## Configuration

Configuration in `~/.mpop/config.json`:

```json
{
  "servers": {
    "node1": {"ip": "10.98.x.x", "user": "root"},
    "node2": {"ip": "10.98.x.x", "user": "root"}
  },
  "connection": {
    "vpn": "wire",
    "ssh_method": "vssh"
  }
}
```

## Environment Variables

| Variable | Description |
|----------|-------------|
| `WIRE_SERVER_URL` | Wire coordinator URL |
| `VSSH_SECRET` | vssh authentication secret |
| `ANTHROPIC_API_KEY` | Claude API key for meshclaw |

## License

MIT
