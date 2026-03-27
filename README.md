# Meshclaw

A unified infrastructure toolkit for distributed systems. Includes VPN mesh networking, secure remote shell, full-text search, encrypted secrets management, and AI workers.

## Features

- **wire** - P2P VPN mesh with NAT traversal
- **vssh** - Secure shell over mesh or SSH fallback
- **mpop** - Server dashboard and multi-host command execution
- **meshdb** - Full-text search across distributed files (SQLite FTS5)
- **vault** - Encrypted secrets with Shamir's Secret Sharing
- **meshclaw** - AI worker runtime (Claude/OpenAI)

## Installation

### pip install (Recommended)

```bash
pip install meshclaw
```

This installs Python wrappers that auto-download Go binaries on first run.

### curl install

```bash
curl -sL https://raw.githubusercontent.com/meshclaw/meshclaw/main/install.sh | bash
```

### Build from source

```bash
git clone https://github.com/meshclaw/meshclaw.git
cd meshclaw
go build ./cmd/...
```

## Quick Start

```bash
# Initialize configuration
mpop init

# Show server dashboard
mpop

# Start VPN mesh
wire register
wire daemon

# Connect to remote server
vssh exec server1 "hostname"
```

---

## mpop - Server Dashboard

Monitor and manage multiple servers from your terminal.

```bash
mpop                      # Show dashboard with server status
mpop exec all "uptime"    # Run command on all servers
mpop exec web1,db1 "df -h"  # Run on specific servers
mpop peers                # List VPN mesh peers
mpop info server1         # Show server details
```

### Configuration

Edit `~/.mpop/config.json`:

```json
{
  "servers": {
    "web1": {"ip": "10.0.1.10", "user": "deploy", "role": "Web"},
    "db1": {"ip": "10.0.1.20", "user": "deploy", "role": "Database"}
  },
  "connection": {
    "vpn": "wire",
    "ssh_method": "vssh"
  }
}
```

---

## wire - VPN Mesh

Create a secure P2P mesh network across servers. Works behind NAT with automatic relay fallback.

```bash
wire register             # Register node with coordinator
wire daemon               # Start VPN daemon
wire status               # Show connection status
wire peers                # List connected peers
wire add-peer <pubkey>    # Manually add peer
```

### How it works

1. Each node generates a WireGuard keypair
2. Nodes register with a coordinator server
3. Direct P2P connections when possible
4. Automatic relay through coordinator when behind strict NAT

---

## vssh - Secure Shell

Execute commands on remote servers. Automatically uses wire mesh, falls back to Tailscale or SSH.

```bash
vssh exec server1 "ls -la"        # Run single command
vssh exec server1                 # Interactive shell
vssh cp file.txt server1:/tmp/    # Copy file to remote
vssh cp server1:/var/log/app.log ./  # Copy from remote
vssh server                       # Start vssh server daemon
```

### Connection Priority

1. **wire** - Uses mesh VPN if available
2. **tailscale** - Falls back to Tailscale if installed
3. **ssh** - Standard SSH as last resort

---

## meshdb - Full-Text Search

Index and search files across local and remote systems. Uses SQLite FTS5 with BM25 ranking.

```bash
# Index files
meshdb index ~/projects           # Index a directory
meshdb index-full                 # Index entire home (smart skip)
meshdb index server1:/home/user   # Index remote directory via SSH

# Search
meshdb search "function async"    # Full-text search
meshdb search "error" --type log  # Filter by type (code/docs/config/log)
meshdb search "api" --all         # Search across all servers
meshdb search "deploy" --smart    # LLM-expanded query (requires Ollama)

# Other commands
meshdb find "config.json"         # Find by filename
meshdb read config.yaml           # Read file content
meshdb status                     # Show index statistics
meshdb doctor                     # Check configuration
```

### File Types

| Type | Extensions |
|------|------------|
| code | .py, .go, .js, .ts, .rs, .java, .c, .cpp, ... |
| docs | .md, .txt, .rst, .org |
| config | .json, .yaml, .toml, .ini, .env |
| log | .log |

### Distributed Search

Configure remote servers in `~/.meshdb/config.json`:

```json
{
  "servers": {
    "server1": {"ip": "10.0.1.10", "user": "deploy"},
    "server2": {"ip": "10.0.1.20", "user": "deploy"}
  }
}
```

Then search everywhere: `meshdb search "error" --all`

---

## vault - Secrets Management

Securely store secrets with AES-256-GCM encryption and Shamir's Secret Sharing for key recovery.

```bash
# Initialize vault
vault init                        # Create new vault (generates Shamir shares)
vault init -n 5 -k 3              # 5 shares, 3 needed to recover

# Manage secrets
vault add github-token            # Add secret (prompts for value)
vault get github-token            # Retrieve secret
vault list                        # List all secrets
vault search "api"                # Search secrets
vault delete old-secret           # Remove secret

# File encryption
vault encrypt secrets.json        # Encrypt file -> secrets.json.vault
vault decrypt secrets.json.vault  # Decrypt back

# Backup and recovery
vault export -o backup.vault      # Export encrypted backup
vault import backup.vault         # Import from backup
vault rekey                       # Rotate master key

# Shamir key recovery
vault distribute --local-dir ./shares  # Write share files
vault collect --local-dir ./shares     # Recover from shares
```

### Security Features

- **Encryption**: AES-256-GCM
- **Key Derivation**: Argon2id (memory-hard)
- **Key Splitting**: Shamir's Secret Sharing over GF(2^8)
- **Audit Log**: All operations logged

### How Shamir Works

When you run `vault init -n 5 -k 3`:
- 5 key shares are generated
- Any 3 shares can recover the master key
- Distribute shares to different locations/people
- Even if 2 shares are lost, the key is recoverable

---

## meshclaw - AI Worker

Run AI assistants locally with Claude or OpenAI. Supports scheduling, notifications, and web chat.

```bash
# Create and run worker
meshclaw init assistant           # Create from template
meshclaw start assistant          # Start worker daemon
meshclaw stop assistant           # Stop worker

# Interact
meshclaw ask assistant "What is kubernetes?"  # One-shot query
meshclaw chat assistant           # Interactive chat
meshclaw web assistant            # Start web chat UI

# Manage
meshclaw ps                       # List running workers
meshclaw logs assistant           # View worker logs
meshclaw templates                # List available templates
```

### Templates

| Template | Description |
|----------|-------------|
| assistant | General purpose with bash tools |
| system-monitor | CPU, memory, disk monitoring |
| code-reviewer | Review git diffs |
| research | Web research and summarization |
| devops | Infrastructure automation |

### Configuration

Worker config in `~/.meshclaw/workers/<name>/config.json`:

```json
{
  "name": "assistant",
  "model": "claude-sonnet-4-20250514",
  "system_prompt": "You are a helpful assistant.",
  "tools": ["bash", "read_file", "write_file"],
  "schedule": "every 1h",
  "notify": {
    "platform": "telegram",
    "chat_id": "123456"
  }
}
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | Claude API key |
| `OPENAI_API_KEY` | OpenAI API key |

---

## Environment Variables

| Variable | Used By | Description |
|----------|---------|-------------|
| `WIRE_SERVER_URL` | wire | Coordinator server URL |
| `VSSH_SECRET` | vssh | Authentication token |
| `ANTHROPIC_API_KEY` | meshclaw | Claude API key |
| `OPENAI_API_KEY` | meshclaw | OpenAI API key |
| `MESHDB_DB` | meshdb | Custom database path |
| `MESHDB_OLLAMA_URL` | meshdb | Ollama URL for smart search |

---

## Network Fallback

All tools automatically try multiple connection methods:

```
wire (mesh VPN) -> tailscale -> ssh
```

This means commands work even if:
- Wire mesh is down (uses Tailscale)
- No VPN at all (falls back to SSH)
- You're on a laptop without VPN (SSH to public IP)

---

## License

MIT

---

## Links

- [GitHub](https://github.com/meshclaw/meshclaw)
- [Issues](https://github.com/meshclaw/meshclaw/issues)
- [PyPI](https://pypi.org/project/meshclaw/)
