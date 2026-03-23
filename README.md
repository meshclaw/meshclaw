# meshclaw — Distributed AI Agent Orchestration

**Run AI agent workflows across your entire infrastructure. Any node. Any runtime. In parallel.**

```bash
pip install meshclaw
```

---

## What meshclaw does

meshclaw turns your servers, containers, and local runtimes into a unified execution surface for AI agents. Build on one node, test on another, deploy on a third — orchestrated as a single workflow.

```bash
# Run a task on a specific server
meshclaw exec "make build" -s worker1

# Run different tasks in parallel across nodes
meshclaw parallel \
  worker1:"make build" \
  worker2:"make test" \
  relay1:"./deploy.sh"

# Chain tasks as a pipeline (each waits for the previous)
meshclaw pipeline \
  worker1:"make build" \
  worker2:"make test" \
  relay1:"./deploy.sh"

# Broadcast the same command to all nodes
meshclaw broadcast "df -h"
```

---

## Key Capabilities

**Parallel execution** — run different tasks across multiple nodes simultaneously. A build on worker1 and a test suite on worker2 finish in the time of the slower one.

**Pipelines** — chain tasks across nodes with dependency ordering. A build must succeed before tests run; tests must pass before deployment.

**Broadcast** — run the same command across all discovered nodes. Useful for fleet-wide operations like updates, health checks, or config pushes.

**Signal-based coordination** — agents wait for signals from other agents before proceeding. Enable complex multi-agent workflows without polling.

**Map-reduce** — distribute data or workloads across nodes and aggregate results. Index a codebase in parallel shards, search in parallel, merge results.

**Single-machine mode** — works on one machine with Docker, LXC, or rtlinux containers as nodes. Same API whether you have one machine or twenty.

---

## Architecture

```
meshclaw (Orchestrator)
   ├── mpop     discovery, scheduling (optional)
   ├── vssh     command execution
   ├── meshdb   state and search (optional)
   ├── network  wire, Tailscale, or any reachable network
   └── runtime  servers, Docker, LXC, rtlinux, localhost
```

Components are independent — meshclaw uses only what's available. No fixed execution chain.

---

## Installation

```bash
pip install meshclaw
```

With full MeshPOP stack integration:

```bash
pip install meshclaw[meshpop]
```

---

## Quick Start

### Discover nodes

```bash
meshclaw discover
```

Finds all nodes on the current wire network (or from config).

### Execute on a specific node

```bash
meshclaw exec "uptime" -s worker1
meshclaw exec "df -h && free -h" -s db1
```

### Parallel execution

```bash
meshclaw parallel \
  worker1:"make build" \
  worker2:"make test"
```

Runs both simultaneously. Reports output from each node as it completes.

### Pipeline

```bash
meshclaw pipeline \
  worker1:"make build" \
  worker2:"make test" \
  relay1:"./deploy.sh"
```

Runs sequentially. If any step fails, the pipeline stops.

### Broadcast

```bash
meshclaw broadcast "systemctl status nginx"
meshclaw broadcast "pip install --upgrade meshpop"
```

Same command on every discovered node, in parallel.

### History

```bash
meshclaw history        # Last 20 commands and their status
meshclaw history 50     # Last 50
meshclaw history exec   # Filter by command type
```

---

## Python API

```python
from meshclaw import Orchestrator, Task

with Orchestrator() as orch:
    orch.discover()

    # Parallel — build and test simultaneously
    orch.parallel("build-and-test", [
        Task("build",  command="make build",  server="worker1"),
        Task("test",   command="make test",   server="worker2"),
    ])

    # Pipeline — sequential with dependency
    orch.pipeline("release", [
        {"server": "worker1", "command": "make build"},
        {"server": "worker2", "command": "make test"},
        {"server": "relay1",  "command": "./deploy.sh"},
    ])

    # Broadcast — same command everywhere
    orch.broadcast("systemctl reload nginx")

    # Signal-based coordination
    orch.signal("build-complete")
    orch.wait_for("build-complete", server="worker2")
```

---

## Single-Machine Mode

meshclaw works without a network. Local runtimes are treated as nodes:

- Docker containers
- LXC containers
- rtlinux instances
- the host machine itself

```bash
meshclaw exec "python train.py" -s docker:trainer1
meshclaw parallel \
  docker:trainer1:"python train.py --shard 0" \
  docker:trainer2:"python train.py --shard 1" \
  docker:trainer3:"python train.py --shard 2"
```

Same API as distributed mode. Useful for local multi-container workflows before moving to production.

---

## CLI Reference

```bash
meshclaw discover                     # Find all available nodes
meshclaw exec "<cmd>" -s <node>       # Run on a specific node
meshclaw parallel <node>:<cmd> ...    # Run different cmds in parallel
meshclaw pipeline <node>:<cmd> ...    # Run cmds sequentially
meshclaw broadcast "<cmd>"            # Same cmd on all nodes
meshclaw history [count] [filter]     # Command history
meshclaw version                      # Show version
```

---

## MCP Integration

```bash
pip install meshclaw
```

Add to Claude config (`~/.claude/settings.json`):

```json
{
  "mcpServers": {
    "meshclaw": { "command": "meshclaw-mcp" }
  }
}
```

### What the AI can do

> "Run the build on worker1 and tests on worker2 in parallel"
> "Deploy to all production nodes"
> "What's the history of deployments this week?"
> "Run a health check broadcast across the fleet"
> "Set up a build-test-deploy pipeline for this project"

---

## Design Principles

**Composable** — use only the components you have. meshclaw works with vssh alone, or with the full mpop stack.

**Decoupled** — components don't depend on each other. Swap out the transport, the scheduler, or the runtime independently.

**Environment-agnostic** — same API on one machine or across twenty servers.

**Agent-first** — designed for AI-driven workflows. Every operation is observable and reportable.

---

## Links

- Fleet orchestration: [github.com/meshpop/mpop](https://github.com/meshpop/mpop)
- Transport: [github.com/meshpop/vssh](https://github.com/meshpop/vssh)
- Mesh VPN: [github.com/meshpop/wire](https://github.com/meshpop/wire)
- PyPI: [pypi.org/project/meshclaw](https://pypi.org/project/meshclaw)

## License

MIT — [MeshPOP](https://github.com/meshpop)
