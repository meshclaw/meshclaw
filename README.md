# MeshClaw

Distributed AI agent orchestration across mesh networks.

While existing agent frameworks (OpenClaw, etc.) run on a single machine, MeshClaw distributes AI agents across your entire server infrastructure. Build on d1, test on d2, deploy on v1 — all orchestrated by AI.

**Works on a single PC too** — MeshClaw treats local containers (Docker, LXC, rtlinux) as mesh nodes, giving you distributed agent power on one machine.

## What MeshClaw Does That Others Can't

| Feature | OpenClaw | MeshClaw |
|---------|----------|----------|
| Single-machine agent | Yes | Yes |
| Multi-server execution | No | **Yes** |
| Agent migration between servers | No | **Yes** |
| Parallel tasks across servers | No | **Yes** |
| Sequential pipelines across servers | No | **Yes** |
| Collaborative multi-agent with signals | No | **Yes** |
| Map-Reduce across infrastructure | No | **Yes** |
| Container-as-node on single machine | No | **Yes** |
| MeshPOP infrastructure integration | No | **Yes** |

## Install

```bash
pip install meshclaw
```

With MeshPOP integration:
```bash
pip install meshclaw[meshpop]
```

## Quick Start

### CLI

```bash
# Discover mesh nodes
meshclaw discover

# Execute on a specific server
meshclaw exec "uptime" -s d1

# Broadcast to all servers
meshclaw broadcast "df -h"

# Parallel tasks on different servers
meshclaw parallel d1:"make build" d2:"make test" v1:"./deploy.sh"

# Sequential pipeline
meshclaw pipeline d1:"make build" d2:"make test" v1:"./deploy.sh"
```

### Python API

```python
from meshclaw import Orchestrator, Task
from meshclaw.scenario import ParallelScenario, SequentialScenario, CollaborativeScenario

# Auto-discover mesh
with Orchestrator() as orch:
    orch.discover()

    # Parallel: different tasks on different servers
    result = orch.parallel("build-and-test", [
        Task("build", command="make build", server="d1"),
        Task("test", command="make test", server="d2"),
        Task("lint", command="make lint", server="g1"),
    ])

    # Sequential pipeline: output chains
    result = orch.pipeline("deploy", [
        {"server": "d1", "command": "make build"},
        {"server": "d2", "command": "make test"},
        {"server": "v1", "command": "./deploy.sh"},
    ])

    # Broadcast: same command on all servers
    result = orch.broadcast("health", "uptime")
```

### Collaborative Workflow

```python
from meshclaw import Orchestrator
from meshclaw.scenario import CollaborativeScenario

with Orchestrator() as orch:
    orch.discover()

    collab = CollaborativeScenario("data-pipeline")
    collab.add_agent_task("scraper", server="d1",
        command="curl -s https://api.example.com/data > /tmp/data.json",
        publishes=["data_ready"])
    collab.add_agent_task("processor", server="g1",
        command="python3 /opt/process.py /tmp/data.json",
        waits_for=["data_ready"],
        publishes=["processed"])
    collab.add_agent_task("server", server="v1",
        command="cp /tmp/output.json /var/www/api/",
        waits_for=["processed"])

    result = orch.run(collab)
```

### Agent Migration

```python
from meshclaw import Agent

agent = Agent("worker", server="d1")
agent.start()
agent.execute("heavy-computation.sh")

# Server d1 is overloaded? Move to d2
agent.migrate("d2")
agent.execute("continue-work.sh")  # Now runs on d2
```

### Map-Reduce

```python
from meshclaw import Orchestrator
from meshclaw.scenario import MapReduceScenario

with Orchestrator() as orch:
    orch.discover()

    scenario = MapReduceScenario(
        "error-count",
        map_command="grep -c ERROR /var/log/syslog",
        map_servers=["d1", "d2", "g1", "v1"],
        reduce_command="python3 -c 'import sys; print(sum(int(l) for l in sys.stdin))'",
        reduce_server="m1"
    )
    result = orch.run(scenario)
    print(f"Total errors: {result.results[-1].output}")
```

## MCP Server

MeshClaw includes an MCP server for AI assistant integration:

```json
{
  "mcpServers": {
    "meshclaw": {
      "command": "python3",
      "args": ["/path/to/meshclaw-mcp-server.py"]
    }
  }
}
```

### MCP Tools

| Tool | Description |
|------|-------------|
| `meshclaw_discover` | Find mesh nodes |
| `meshclaw_exec` | Execute on specific server |
| `meshclaw_broadcast` | Run on all servers |
| `meshclaw_parallel` | Different tasks, different servers, simultaneously |
| `meshclaw_pipeline` | Sequential cross-server pipeline |
| `meshclaw_collab` | Collaborative multi-agent with signals |
| `meshclaw_mapreduce` | Map-Reduce across mesh |
| `meshclaw_status` | Orchestrator status |
| `meshclaw_migrate` | Move agent between servers |

## Single-Machine Mode

No mesh network? No problem. MeshClaw works with local containers:

```python
# Auto-detects Docker/LXC/rtlinux containers
orch = Orchestrator(mode="local")
orch.discover()  # Finds containers as nodes

# Same powerful API, one machine
result = orch.parallel("local-work", [
    Task("build", command="make", server="container-1"),
    Task("test", command="pytest", server="container-2"),
])
```

## Architecture

```
                    Orchestrator
                    /    |     \
              Agent-d1  Agent-d2  Agent-v1
              (build)   (test)    (deploy)
                |         |         |
            [vssh/ssh] [vssh/ssh] [vssh/ssh]
                |         |         |
              Server-d1 Server-d2 Server-v1
```

Built on MeshPOP: mpop (management), vssh (execution), wire (networking), meshdb (state), vault (secrets).

## License

MIT

## Links

- [MeshPOP](https://github.com/meshpop)
- [mpop.dev](https://mpop.dev)
- [PyPI](https://pypi.org/project/meshclaw/)
