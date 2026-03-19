#!/usr/bin/env python3
"""MeshClaw MCP Server - AI-native distributed agent orchestration.

Exposes MeshClaw capabilities to AI assistants via MCP protocol.
This is what makes MeshClaw fundamentally different from OpenClaw:
- AI can orchestrate tasks across MULTIPLE SERVERS simultaneously
- AI can build pipelines that span the entire mesh network
- AI can run collaborative workflows where agents coordinate via signals

Tools:
    meshclaw_discover  - Find mesh nodes (servers or containers)
    meshclaw_exec      - Execute command on specific server
    meshclaw_broadcast - Run command on all servers
    meshclaw_parallel  - Run different tasks on different servers simultaneously
    meshclaw_pipeline  - Sequential execution across servers (output chains)
    meshclaw_collab    - Collaborative multi-agent scenario with signals
    meshclaw_status    - Get orchestrator and agent status
    meshclaw_migrate   - Move an agent to a different server
"""

import sys
import json
import time
import threading

from meshclaw.agent import Agent
from meshclaw.task import Task, TaskResult
from meshclaw.orchestrator import Orchestrator
from meshclaw.scenario import (
    ParallelScenario, SequentialScenario, CollaborativeScenario,
    FanOutScenario, MapReduceScenario
)

# Global orchestrator (persists across MCP calls)
_orchestrator = None
_orch_lock = threading.Lock()


def get_orchestrator() -> Orchestrator:
    """Get or create the global orchestrator."""
    global _orchestrator
    with _orch_lock:
        if _orchestrator is None:
            _orchestrator = Orchestrator(mode="auto", use_meshpop=True)
            _orchestrator.discover()
        return _orchestrator


# ---- MCP Protocol ----

TOOLS = [
    {
        "name": "meshclaw_discover",
        "description": "Discover mesh nodes (servers in mesh network or local containers). "
                       "Returns list of available servers that agents can run on. "
                       "Auto-detects mesh mode (multi-server via mpop/wire) or "
                       "local mode (Docker/LXC/rtlinux containers on single machine).",
        "inputSchema": {
            "type": "object",
            "properties": {
                "mode": {
                    "type": "string",
                    "enum": ["auto", "mesh", "local"],
                    "description": "Discovery mode. auto=detect, mesh=multi-server, local=containers"
                }
            }
        }
    },
    {
        "name": "meshclaw_exec",
        "description": "Execute a command on a specific mesh server. "
                       "Uses vssh/mpop for remote execution, direct for local. "
                       "Returns stdout, stderr, exit code, duration.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "command": {"type": "string", "description": "Shell command to execute"},
                "server": {"type": "string", "description": "Target server name"},
                "timeout": {"type": "integer", "description": "Timeout in seconds (default 300)"}
            },
            "required": ["command", "server"]
        }
    },
    {
        "name": "meshclaw_broadcast",
        "description": "Run the same command on ALL mesh servers simultaneously. "
                       "Perfect for health checks, updates, status collection. "
                       "Returns aggregated results from all servers.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "command": {"type": "string", "description": "Command to run on all servers"},
                "servers": {
                    "type": "array", "items": {"type": "string"},
                    "description": "Specific servers (empty = all discovered)"
                }
            },
            "required": ["command"]
        }
    },
    {
        "name": "meshclaw_parallel",
        "description": "Run DIFFERENT tasks on DIFFERENT servers simultaneously. "
                       "This is the key differentiator - true distributed parallel execution. "
                       "Example: build on d1, test on d2, deploy on v1, all at once.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "name": {"type": "string", "description": "Scenario name"},
                "tasks": {
                    "type": "array",
                    "items": {
                        "type": "object",
                        "properties": {
                            "name": {"type": "string"},
                            "command": {"type": "string"},
                            "server": {"type": "string"}
                        },
                        "required": ["command", "server"]
                    },
                    "description": "List of {name, command, server} to run in parallel"
                }
            },
            "required": ["tasks"]
        }
    },
    {
        "name": "meshclaw_pipeline",
        "description": "Run tasks SEQUENTIALLY across servers. Output of each stage "
                       "flows to the next via {PREV_OUTPUT} placeholder. "
                       "Example: build on d1 -> test on d2 -> deploy on v1.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "name": {"type": "string", "description": "Pipeline name"},
                "stages": {
                    "type": "array",
                    "items": {
                        "type": "object",
                        "properties": {
                            "command": {"type": "string"},
                            "server": {"type": "string"}
                        },
                        "required": ["command", "server"]
                    },
                    "description": "Ordered stages [{command, server}, ...]"
                }
            },
            "required": ["stages"]
        }
    },
    {
        "name": "meshclaw_collab",
        "description": "Collaborative multi-agent scenario. Agents on different servers "
                       "coordinate via signals. One agent can wait for another to finish "
                       "before starting. Enables complex distributed workflows.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "name": {"type": "string", "description": "Scenario name"},
                "agents": {
                    "type": "array",
                    "items": {
                        "type": "object",
                        "properties": {
                            "name": {"type": "string", "description": "Agent task name"},
                            "server": {"type": "string"},
                            "command": {"type": "string"},
                            "waits_for": {
                                "type": "array", "items": {"type": "string"},
                                "description": "Signal names to wait for before starting"
                            },
                            "publishes": {
                                "type": "array", "items": {"type": "string"},
                                "description": "Signal names to publish on completion"
                            }
                        },
                        "required": ["name", "server", "command"]
                    }
                },
                "timeout": {"type": "integer", "description": "Overall timeout (default 600)"}
            },
            "required": ["agents"]
        }
    },
    {
        "name": "meshclaw_mapreduce",
        "description": "Map-Reduce across mesh. Run a command on multiple servers (map), "
                       "then aggregate results on one server (reduce). "
                       "Example: count errors on all servers, sum on m1.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "name": {"type": "string"},
                "map_command": {"type": "string", "description": "Command to run on map servers"},
                "map_servers": {"type": "array", "items": {"type": "string"}},
                "reduce_command": {"type": "string", "description": "Command to aggregate results"},
                "reduce_server": {"type": "string", "description": "Server for reduce phase"}
            },
            "required": ["map_command", "map_servers", "reduce_command", "reduce_server"]
        }
    },
    {
        "name": "meshclaw_status",
        "description": "Get orchestrator status: agents, servers, mode, history.",
        "inputSchema": {
            "type": "object",
            "properties": {}
        }
    },
    {
        "name": "meshclaw_migrate",
        "description": "Migrate an agent from one server to another, preserving state. "
                       "This is impossible in OpenClaw - agents are bound to one machine.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "agent_server": {"type": "string", "description": "Current server of the agent"},
                "target_server": {"type": "string", "description": "Server to migrate to"}
            },
            "required": ["agent_server", "target_server"]
        }
    },
]


def handle_tool_call(name: str, arguments: dict) -> str:
    """Handle an MCP tool call."""
    orch = get_orchestrator()

    if name == "meshclaw_discover":
        mode = arguments.get("mode", "auto")
        old_mode = orch.mode
        if mode != "auto":
            orch.mode = mode
        servers = orch.discover()
        return json.dumps({
            "mode": orch.mode,
            "servers": servers,
            "count": len(servers),
            "agents": {s: orch.agents[s].info() for s in servers if s in orch.agents}
        }, indent=2)

    elif name == "meshclaw_exec":
        server = arguments["server"]
        command = arguments["command"]
        timeout = arguments.get("timeout", 300)

        if server not in orch.agents:
            orch.add_agent(f"agent-{server}", server=server)

        result = orch.exec(command, server=server)
        return json.dumps(result, indent=2)

    elif name == "meshclaw_broadcast":
        command = arguments["command"]
        servers = arguments.get("servers", []) or None

        if servers:
            for s in servers:
                if s not in orch.agents:
                    orch.add_agent(f"agent-{s}", server=s)

        result = orch.broadcast("broadcast", command, servers=servers)
        return _format_scenario_result(result)

    elif name == "meshclaw_parallel":
        task_name = arguments.get("name", "parallel")
        tasks = []
        for t in arguments["tasks"]:
            server = t["server"]
            if server not in orch.agents:
                orch.add_agent(f"agent-{server}", server=server)
            tasks.append(Task(
                name=t.get("name", f"task-{server}"),
                command=t["command"],
                server=server
            ))

        result = orch.parallel(task_name, tasks)
        return _format_scenario_result(result)

    elif name == "meshclaw_pipeline":
        pipe_name = arguments.get("name", "pipeline")
        stages = []
        for s in arguments["stages"]:
            if s["server"] not in orch.agents:
                orch.add_agent(f"agent-{s['server']}", server=s["server"])
            stages.append({"server": s["server"], "command": s["command"]})

        result = orch.pipeline(pipe_name, stages)
        return _format_scenario_result(result)

    elif name == "meshclaw_collab":
        collab_name = arguments.get("name", "collaborative")
        timeout = arguments.get("timeout", 600)
        scenario = CollaborativeScenario(collab_name, timeout=timeout)

        for a in arguments["agents"]:
            if a["server"] not in orch.agents:
                orch.add_agent(f"agent-{a['server']}", server=a["server"])
            scenario.add_agent_task(
                name=a["name"],
                server=a["server"],
                command=a["command"],
                waits_for=a.get("waits_for", []),
                publishes=a.get("publishes", []),
            )

        result = orch.run(scenario)
        return _format_scenario_result(result)

    elif name == "meshclaw_mapreduce":
        mr_name = arguments.get("name", "mapreduce")
        for s in arguments["map_servers"] + [arguments["reduce_server"]]:
            if s not in orch.agents:
                orch.add_agent(f"agent-{s}", server=s)

        scenario = MapReduceScenario(
            mr_name,
            map_command=arguments["map_command"],
            map_servers=arguments["map_servers"],
            reduce_command=arguments["reduce_command"],
            reduce_server=arguments["reduce_server"],
        )
        result = orch.run(scenario)
        return _format_scenario_result(result)

    elif name == "meshclaw_status":
        status = orch.status()
        status["summary"] = orch.summary()
        return json.dumps(status, indent=2, default=str)

    elif name == "meshclaw_migrate":
        agent_server = arguments["agent_server"]
        target = arguments["target_server"]

        agent = orch.get_agent_for(agent_server)
        if not agent:
            return f"Error: No agent on server {agent_server}"

        success = agent.migrate(target)
        if success:
            # Update orchestrator mapping
            with orch._lock:
                orch.agents.pop(agent_server, None)
                orch.agents[target] = agent
            return f"Agent '{agent.name}' migrated from {agent_server} to {target}"
        else:
            return f"Migration failed: could not reach {target}"

    return f"Unknown tool: {name}"


def _format_scenario_result(result) -> str:
    """Format scenario result for MCP output."""
    output = {
        "scenario": result.scenario_name,
        "type": result.scenario_type,
        "state": result.state.value,
        "duration": round(result.duration, 3),
        "tasks": f"{result.tasks_succeeded}/{result.tasks_total}",
        "servers": result.servers_used,
        "results": []
    }
    for r in result.results:
        entry = {
            "task": r.task_name,
            "server": r.server,
            "success": r.success,
            "duration": round(r.duration, 3),
        }
        if r.output:
            entry["output"] = r.output.strip()[:2000]
        if r.error and not r.success:
            entry["error"] = r.error.strip()[:500]
        output["results"].append(entry)
    return json.dumps(output, indent=2)


# ---- MCP Protocol Handling (stdio JSON-RPC) ----

def send_message(msg):
    sys.stdout.write(json.dumps(msg) + "\n")
    sys.stdout.flush()


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            continue

        method = msg.get("method", "")
        msg_id = msg.get("id")
        params = msg.get("params", {})

        if method == "initialize":
            send_message({
                "jsonrpc": "2.0", "id": msg_id,
                "result": {
                    "protocolVersion": "2024-11-05",
                    "capabilities": {"tools": {"listChanged": False}},
                    "serverInfo": {
                        "name": "meshclaw",
                        "version": "0.2.0",
                        "description": "Distributed AI agent orchestration across mesh networks"
                    }
                }
            })

        elif method == "notifications/initialized":
            pass

        elif method == "tools/list":
            send_message({
                "jsonrpc": "2.0", "id": msg_id,
                "result": {"tools": TOOLS}
            })

        elif method == "tools/call":
            tool_name = params.get("name", "")
            arguments = params.get("arguments", {})
            try:
                result = handle_tool_call(tool_name, arguments)
                send_message({
                    "jsonrpc": "2.0", "id": msg_id,
                    "result": {
                        "content": [{"type": "text", "text": result}]
                    }
                })
            except Exception as e:
                send_message({
                    "jsonrpc": "2.0", "id": msg_id,
                    "result": {
                        "content": [{"type": "text", "text": f"Error: {e}"}],
                        "isError": True
                    }
                })

        elif method == "ping":
            send_message({"jsonrpc": "2.0", "id": msg_id, "result": {}})



def handle_tool(name: str, arguments: dict) -> str:
    """Unified MCP compatibility wrapper."""
    return handle_tool_call(name, arguments)

if __name__ == "__main__":
    main()
