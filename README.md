# MeshClaw

**Distributed AI agent orchestration across your infrastructure.**

MeshClaw turns your servers, containers, and local runtimes into a single coordinated execution surface for AI agents.

Build on one node, test on another, deploy on a third — all orchestrated by a unified workflow.

---

## What MeshClaw Does

MeshClaw enables AI agents to run and coordinate work across:

* multiple servers
* mesh networks
* local runtimes
* single-machine environments

It works the same way on one machine as it does across a distributed system.

---

## Core Capabilities

* **Run anywhere** — execute on specific nodes, all nodes, or local runtimes
* **Parallel execution** — run different tasks across multiple nodes simultaneously
* **Pipelines** — chain tasks across nodes (build → test → deploy)
* **Broadcast** — run the same command across the entire infrastructure
* **Collaborative agents** — coordinate tasks using signals
* **Map-reduce** — distribute and aggregate workloads across nodes
* **Agent mobility** — shift execution between nodes when needed

---

## Single-Machine Mode

MeshClaw works without a network.

Local runtimes are treated as nodes, including:

* Docker containers
* LXC containers
* rtlinux instances
* the host machine

This enables distributed-style execution on a single machine.

Same API. Same behavior.

---

## Installation

```bash
pip install meshclaw
```

With MeshPOP integration:

```bash
pip install meshclaw[meshpop]
```

---

## Quick Start

### CLI

```bash
meshclaw discover

meshclaw exec "uptime" -s worker1

meshclaw broadcast "df -h"

meshclaw parallel \
  worker1:"make build" \
  worker2:"make test" \
  relay1:"./deploy.sh"

meshclaw pipeline \
  worker1:"make build" \
  worker2:"make test" \
  relay1:"./deploy.sh"
```

---

### Python API

```python
from meshclaw import Orchestrator, Task

with Orchestrator() as orch:
    orch.discover()

    orch.parallel("build-and-test", [
        Task("build", command="make build", server="worker1"),
        Task("test", command="make test", server="worker2"),
    ])

    orch.pipeline("deploy", [
        {"server": "worker1", "command": "make build"},
        {"server": "worker2", "command": "make test"},
        {"server": "relay1", "command": "./deploy.sh"},
    ])
```

---

## Architecture

MeshClaw orchestrates execution using modular infrastructure components:

```
MeshClaw (Orchestrator)
   ├─ mpop     (scheduling, optional)
   ├─ vssh     (execution)
   ├─ meshdb   (state/search, optional)
   ├─ vault    (secrets, optional)
   ├─ network  (wire, tailscale, etc.)
   └─ runtime  (rtlinux, docker, lxc, host)
```

Components are independent and only used when needed.
There is no fixed execution chain.

---

## Design Principles

* **Composable** — use only what you need
* **Decoupled** — components do not depend on each other
* **Environment-agnostic** — works locally or across servers
* **Agent-first** — built for AI-driven workflows

---

## MCP Integration

```json
{
  "mcpServers": {
    "meshclaw": {
      "command": "meshclaw-mcp"
    }
  }
}
```

---

## Summary

**MeshClaw turns your infrastructure into a unified execution surface for AI agents.**
