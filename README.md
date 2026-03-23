# meshclaw

[![PyPI](https://img.shields.io/pypi/v/meshclaw)](https://pypi.org/project/meshclaw/)
[![Python](https://img.shields.io/pypi/pyversions/meshclaw)](https://pypi.org/project/meshclaw/)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Run AI agent workflows across your infrastructure. Build → test → deploy, in one command.**

```bash
pip install meshclaw
```

---

## What meshclaw does

```bash
# Parallel — build and test simultaneously on different servers
meshclaw parallel worker1:"make build" worker2:"make test"

# Pipeline — sequential, stops on first failure
meshclaw pipeline worker1:"make build" worker2:"make test" relay1:"./deploy.sh"

# Broadcast — same command on every node
meshclaw broadcast "pip install --upgrade meshpop"
```

meshclaw turns your servers, containers, and local runtimes into a unified execution surface. One command to run distributed workflows.

---

## Key features

**Parallel execution** — different tasks on different nodes simultaneously. A build on worker1 and tests on worker2 finish in the time of the slower one.

**Pipelines** — chain tasks with dependency ordering. Build must succeed before tests run; tests must pass before deploy.

**Broadcast** — same command across all discovered nodes. Fleet-wide updates, health checks, config pushes.

**Signal coordination** — agents wait for signals from other agents before proceeding, enabling complex multi-step workflows.

**Single-machine mode** — works locally with Docker, LXC, or rtlinux containers as nodes. Same API whether you have one machine or twenty.

---

## Quick Start

### Discover nodes

```bash
meshclaw discover
```

Finds all nodes on the wire network (or from config).

### Run commands

```bash
# Single node
meshclaw exec "uptime" -s worker1

# Parallel
meshclaw parallel worker1:"make build" worker2:"make test"

# Pipeline (stops on failure)
meshclaw pipeline worker1:"make build" worker2:"make test" relay1:"./deploy.sh"

# Broadcast to all nodes
meshclaw broadcast "systemctl status nginx"

# History
meshclaw history
```

---

## Python API

```python
from meshclaw import Orchestrator, Task

with Orchestrator() as orch:
    orch.discover()

    # Parallel
    orch.parallel("build-and-test", [
        Task("build", command="make build", server="worker1"),
        Task("test",  command="make test",  server="worker2"),
    ])

    # Pipeline
    orch.pipeline("release", [
        {"server": "worker1", "command": "make build"},
        {"server": "worker2", "command": "make test"},
        {"server": "relay1",  "command": "./deploy.sh"},
    ])

    # Broadcast
    orch.broadcast("systemctl reload nginx")
```

---

## Single-Machine Mode

Works without a network — local runtimes treated as nodes:

```bash
meshclaw parallel \
  docker:trainer1:"python train.py --shard 0" \
  docker:trainer2:"python train.py --shard 1" \
  docker:trainer3:"python train.py --shard 2"
```

Supports Docker containers, LXC containers, rtlinux instances, and the host machine.

---

## MCP Integration

```json
{
  "mcpServers": {
    "meshclaw": { "command": "meshclaw-mcp" }
  }
}
```

> "Run the build on worker1 and tests on worker2 in parallel"
> "Deploy to all production nodes"
> "What's the history of deployments this week?"
> "Run a health check broadcast across the fleet"

---

## CLI Reference

```bash
meshclaw discover                     # Find available nodes
meshclaw exec "<cmd>" -s <node>       # Run on specific node
meshclaw parallel <node>:<cmd> ...    # Parallel execution
meshclaw pipeline <node>:<cmd> ...    # Sequential pipeline
meshclaw broadcast "<cmd>"            # All nodes
meshclaw history [count] [filter]     # Command history
meshclaw version                      # Show version
```

---

## Architecture

meshclaw uses only what's available — components are optional:

```
meshclaw (Orchestrator)
   ├── vssh      command execution
   ├── wire      node discovery (optional)
   ├── mpop      scheduling (optional)
   └── meshdb    state and search (optional)
```

---

## MeshPOP Stack

```
mpop      Fleet orchestration
vssh      Authenticated transport
wire      Encrypted mesh VPN
meshclaw  Agent workflows  ← this
meshdb    Distributed search
```

---

## License

MIT — [MeshPOP](https://github.com/meshpop)
