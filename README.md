# Meshclaw

Distributed infrastructure toolkit. VPN mesh, remote shell, full-text search, secrets vault, AI workers.

## Install

```bash
# pip (downloads Go binaries automatically)
pip install meshclaw

# or build from source
git clone https://github.com/meshclaw/meshclaw.git
cd meshclaw && go build ./cmd/...
```

## Tools

### wire - WireGuard Mesh VPN

```bash
# First node (coordinator + relay)
wire install --relay

# Join existing mesh
wire install --join http://coordinator:8790

# VPS/public IP nodes (can relay for NAT clients)
wire up --relay

# NAT clients (auto-select best relay)
wire up

# Status
wire status
wire down
```

**Relay Features:**
- Auto-select relay based on latency
- Sticky relay (keeps using same relay unless it fails)
- Auto-failover on relay failure (15s detection)
- NAT traversal via relay nodes

### vssh - Remote Shell

```bash
vssh node1                      # Interactive shell
vssh exec node1 uname -a        # Run command
vssh put node1 file.txt /tmp/   # Upload
vssh get node1 /etc/hosts ./    # Download
vssh sync node1 large.tar       # Chunked sync (300GB+)
vssh status                     # Show peers
vssh server                     # Start daemon
```

### mpop - Server Dashboard

```bash
mpop                            # Dashboard
mpop exec node1 uptime          # Run on one
mpop exec all df -h             # Run on all
mpop peers                      # VPN peers
mpop servers                    # Server list
```

### meshdb - Full-Text Search

```bash
meshdb index ~/projects         # Index directory
meshdb index node1:/home/user   # Index remote
meshdb search "async function"  # Search
meshdb find config.json         # Find by name
meshdb status                   # Index stats
```

### vault - Secrets Management

```bash
vault init                      # Initialize (Shamir shares)
vault add api-key               # Add secret
vault get api-key               # Get secret
vault list                      # List all
vault encrypt file.json         # Encrypt file
vault decrypt file.json.vault   # Decrypt
```

### meshclaw - AI Workers

```bash
export ANTHROPIC_API_KEY=sk-ant-...
meshclaw init assistant         # Create worker
meshclaw start assistant        # Start daemon
meshclaw chat assistant         # Interactive chat
meshclaw ask assistant "hello"  # One-shot
meshclaw ps                     # List workers
```

## MCP Server (for Claude Code)

```bash
# Install
pip install meshclaw

# Configure ~/.claude/settings.json
{
  "mcpServers": {
    "meshclaw": { "command": "meshclaw-mcp" }
  }
}
```

Tools: `meshdb_search`, `meshdb_find`, `vssh_exec`, `vssh_status`, `mpop_status`, `mpop_exec`

## Environment

| Variable | Description |
|----------|-------------|
| `WIRE_SERVER_URL` | Coordinator URL |
| `VSSH_SECRET` | Auth token |
| `ANTHROPIC_API_KEY` | Claude API |
| `OPENAI_API_KEY` | OpenAI API |

## License

MIT
