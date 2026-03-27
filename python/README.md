# meshclaw

Distributed infrastructure toolkit - VPN mesh, secure shell, AI workers.

## Installation

```bash
pip install meshclaw
```

On first run, the Go binaries will be automatically downloaded.

## Components

| Command | Description |
|---------|-------------|
| `meshclaw` | AI worker runtime |
| `mpop` | Server dashboard CLI |
| `wire` | VPN mesh networking |
| `vssh` | Secure shell over mesh |
| `meshdb` | Local distributed database |
| `vault` | Secrets management |

## Quick Start

```bash
# Initialize configuration
mpop init

# Show server dashboard
mpop

# Start AI worker
meshclaw init my-bot
meshclaw start my-bot
meshclaw chat my-bot
```

## Alternative: curl install

```bash
curl -sL https://raw.githubusercontent.com/meshclaw/meshclaw/main/install.sh | bash
```

## Documentation

See [GitHub](https://github.com/meshclaw/meshclaw) for full documentation.

## License

MIT
