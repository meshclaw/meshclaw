"""MeshClaw Orchestrator - The brain that distributes work across the mesh.

The Orchestrator:
- Manages a pool of agents across mesh nodes
- Auto-discovers servers via mpop if available
- Routes tasks to the right agent based on server, capabilities, load
- Executes scenarios (parallel, sequential, collaborative, etc.)
- Tracks execution history and provides summaries

Single-machine mode (rtlinux):
- Discovers local containers as mesh nodes
- Each container is treated as a separate server
- Same API, same power, one machine

Multi-server mode (meshpop):
- Discovers servers via mpop/wire
- Uses vssh for fast remote execution
- Stores state in meshdb
"""

import os
import json
import time
import subprocess
import threading
from typing import Optional, Dict, List, Any, Callable

from meshclaw.agent import Agent, AgentConfig, AgentState
from meshclaw.task import Task, TaskResult, TaskState, TaskType
from meshclaw.scenario import (
    Scenario, ScenarioResult, ParallelScenario,
    SequentialScenario, CollaborativeScenario, FanOutScenario
)


class Orchestrator:
    """Distributed agent orchestrator for mesh networks.

    Usage (auto-discover mesh):
        orch = Orchestrator()
        orch.discover()  # Find all mesh nodes
        result = orch.run(ParallelScenario.broadcast("uptime", "uptime",
                          servers=orch.server_names))

    Usage (manual agents):
        orch = Orchestrator()
        orch.add_agent("builder", server="server1")
        orch.add_agent("tester", server="server2")
        orch.add_agent("deployer", server="v1")
        result = orch.run(scenario)

    Usage (single machine with containers):
        orch = Orchestrator(mode="local")
        orch.discover()  # Finds local containers
        result = orch.run(scenario)

    Context manager:
        with Orchestrator() as orch:
            orch.discover()
            result = orch.run(scenario)
    """

    def __init__(self, *, mode: str = "auto", use_meshpop: bool = True,
                 on_event: Optional[Callable] = None):
        """
        Args:
            mode: "auto" (detect), "mesh" (multi-server), "local" (containers)
            use_meshpop: Use mpop/vssh/wire for execution
            on_event: Callback for orchestrator events
        """
        self.agents: Dict[str, Agent] = {}  # server -> agent
        self.mode = mode
        self.use_meshpop = use_meshpop
        self.on_event = on_event
        self.history: List[ScenarioResult] = []
        self._lock = threading.Lock()

        if mode == "auto":
            self.mode = self._detect_mode()

    @property
    def server_names(self) -> List[str]:
        """List of all managed server names."""
        return list(self.agents.keys())

    @property
    def agent_count(self) -> int:
        return len(self.agents)

    @property
    def active_agents(self) -> List[Agent]:
        return [a for a in self.agents.values() if a.is_alive]

    def _detect_mode(self) -> str:
        """Detect if we're on a mesh network or single machine."""
        # Check if mpop is available and has multiple servers
        try:
            result = subprocess.run(
                ["mpop", "servers"], capture_output=True, text=True, timeout=10
            )
            if result.returncode == 0 and result.stdout.strip():
                lines = [l for l in result.stdout.strip().split('\n') if l.strip()]
                if len(lines) > 1:
                    return "mesh"
        except Exception:
            pass

        # Check for local containers (rtlinux or docker)
        try:
            result = subprocess.run(
                ["docker", "ps", "--format", "{{.Names}}"],
                capture_output=True, text=True, timeout=10
            )
            if result.returncode == 0 and result.stdout.strip():
                return "local"
        except Exception:
            pass

        # Check for LXC containers
        try:
            result = subprocess.run(
                ["lxc", "list", "--format", "json"],
                capture_output=True, text=True, timeout=10
            )
            if result.returncode == 0:
                containers = json.loads(result.stdout)
                if containers:
                    return "local"
        except Exception:
            pass

        return "local"

    def discover(self) -> List[str]:
        """Auto-discover mesh nodes and create agents.

        Returns list of discovered server names.
        """
        servers = []

        if self.mode == "mesh":
            servers = self._discover_mesh()
        elif self.mode == "local":
            servers = self._discover_local()

        # Always include localhost
        if not servers:
            servers = [self._local_hostname()]

        for server in servers:
            if server not in self.agents:
                self.add_agent(f"agent-{server}", server=server)

        self._emit("discovered", {"servers": servers, "count": len(servers)})
        return servers

    def _discover_mesh(self) -> List[str]:
        """Discover servers via MeshPOP."""
        servers = []

        # Try mpop servers
        try:
            result = subprocess.run(
                ["mpop", "servers"], capture_output=True, text=True, timeout=10
            )
            if result.returncode == 0:
                for line in result.stdout.strip().split('\n'):
                    name = line.strip().split()[0] if line.strip() else ""
                    if name and not name.startswith('#'):
                        servers.append(name)
        except Exception:
            pass

        # Try wire peers as fallback
        if not servers:
            try:
                result = subprocess.run(
                    ["wire", "peers"], capture_output=True, text=True, timeout=10
                )
                if result.returncode == 0:
                    for line in result.stdout.strip().split('\n'):
                        name = line.strip().split()[0] if line.strip() else ""
                        if name:
                            servers.append(name)
            except Exception:
                pass

        return servers

    def _discover_local(self) -> List[str]:
        """Discover local containers (Docker, LXC, rtlinux)."""
        containers = []

        # Docker
        try:
            result = subprocess.run(
                ["docker", "ps", "--format", "{{.Names}}"],
                capture_output=True, text=True, timeout=10
            )
            if result.returncode == 0:
                for name in result.stdout.strip().split('\n'):
                    if name.strip():
                        containers.append(name.strip())
        except Exception:
            pass

        # LXC
        if not containers:
            try:
                result = subprocess.run(
                    ["lxc", "list", "--format", "json"],
                    capture_output=True, text=True, timeout=10
                )
                if result.returncode == 0:
                    for c in json.loads(result.stdout):
                        if c.get("status") == "Running":
                            containers.append(c["name"])
            except Exception:
                pass

        # Systemd-nspawn (rtlinux containers)
        if not containers:
            try:
                result = subprocess.run(
                    ["machinectl", "list", "--no-legend"],
                    capture_output=True, text=True, timeout=10
                )
                if result.returncode == 0:
                    for line in result.stdout.strip().split('\n'):
                        parts = line.strip().split()
                        if parts:
                            containers.append(parts[0])
            except Exception:
                pass

        return containers

    def _local_hostname(self) -> str:
        try:
            return os.uname().nodename.split('.')[0]
        except Exception:
            return "localhost"

    def add_agent(self, name: str, server: str = "", *,
                  capabilities: Optional[List[str]] = None,
                  config: Optional[AgentConfig] = None) -> Agent:
        """Add and start an agent for a server."""
        agent_config = config or AgentConfig(
            name=name,
            server=server or self._local_hostname(),
            capabilities=capabilities or ["shell", "python", "file_io"],
        )
        agent = Agent(name, server=agent_config.server,
                      config=agent_config, use_meshpop=self.use_meshpop)
        agent.start()

        with self._lock:
            self.agents[agent.server] = agent

        self._emit("agent_added", {"agent": name, "server": agent.server})
        return agent

    def remove_agent(self, server: str) -> None:
        """Stop and remove an agent."""
        with self._lock:
            agent = self.agents.pop(server, None)
        if agent:
            agent.stop()
            self._emit("agent_removed", {"agent": agent.name, "server": server})

    def get_agent_for(self, server: str) -> Optional[Agent]:
        """Get the agent assigned to a server."""
        agent = self.agents.get(server)
        if agent and agent.is_alive:
            return agent
        return None

    def run(self, scenario: Scenario) -> ScenarioResult:
        """Execute a scenario across the mesh.

        This is the main entry point. Pass any Scenario subclass.
        """
        self._emit("scenario_started", {
            "scenario": scenario.name, "type": scenario.scenario_type})

        result = scenario.execute(self)
        self.history.append(result)

        self._emit("scenario_completed", {
            "scenario": scenario.name,
            "state": result.state.value,
            "duration": result.duration,
        })

        return result

    # ---- Convenience methods (no need to construct scenarios manually) ----

    def parallel(self, name: str, tasks: List[Task], **kwargs) -> ScenarioResult:
        """Run tasks in parallel across servers."""
        return self.run(ParallelScenario(name, tasks, **kwargs))

    def sequential(self, name: str, tasks: List[Task], **kwargs) -> ScenarioResult:
        """Run tasks sequentially across servers (pipeline)."""
        return self.run(SequentialScenario(name, tasks, **kwargs))

    def collaborative(self, name: str) -> CollaborativeScenario:
        """Start building a collaborative scenario. Call .execute() when ready."""
        return CollaborativeScenario(name)

    def broadcast(self, name: str, command: str,
                  servers: Optional[List[str]] = None) -> ScenarioResult:
        """Run the same command on all (or selected) servers."""
        targets = servers or self.server_names
        return self.run(ParallelScenario.broadcast(name, command, targets))

    def exec(self, command: str, server: str = "") -> Dict[str, Any]:
        """Quick single-command execution on a server."""
        target = server or self.server_names[0] if self.server_names else ""
        agent = self.get_agent_for(target)
        if agent:
            return agent.execute(command)
        return {"success": False, "stderr": f"No agent for {target}",
                "stdout": "", "exit_code": -1}

    def pipeline(self, name: str, stages: List[Dict[str, str]]) -> ScenarioResult:
        """Quick pipeline builder.

        Usage:
            orch.pipeline("build-deploy", [
                {"server": "server1", "command": "make build"},
                {"server": "server2", "command": "make test"},
                {"server": "v1", "command": "./deploy.sh"},
            ])
        """
        tasks = [
            Task(f"{name}-stage-{i}", command=s["command"], server=s["server"])
            for i, s in enumerate(stages)
        ]
        return self.run(SequentialScenario(name, tasks))

    # ---- Status & Reporting ----

    def status(self) -> Dict[str, Any]:
        """Get orchestrator status summary."""
        return {
            "mode": self.mode,
            "agents": {
                name: agent.info() for name, agent in self.agents.items()
            },
            "active_agents": len(self.active_agents),
            "total_agents": self.agent_count,
            "scenarios_run": len(self.history),
            "last_scenario": self.history[-1].to_dict() if self.history else None,
        }

    def summary(self) -> str:
        """Human-readable status summary."""
        lines = [
            f"MeshClaw Orchestrator ({self.mode} mode)",
            f"  Agents: {len(self.active_agents)}/{self.agent_count} active",
            f"  Servers: {', '.join(self.server_names)}",
            f"  Scenarios run: {len(self.history)}",
        ]
        if self.history:
            last = self.history[-1]
            lines.append(f"  Last: {last.scenario_name} ({last.state.value}, "
                         f"{last.duration:.2f}s)")
        return "\n".join(lines)

    def _emit(self, event: str, data: Dict[str, Any]) -> None:
        if self.on_event:
            try:
                self.on_event(event, data)
            except Exception:
                pass

    def shutdown(self) -> None:
        """Stop all agents and clean up."""
        for server in list(self.agents.keys()):
            self.remove_agent(server)
        self._emit("shutdown", {})

    def __enter__(self) -> "Orchestrator":
        return self

    def __exit__(self, *args) -> None:
        self.shutdown()

    def __repr__(self) -> str:
        return (f"Orchestrator(mode='{self.mode}', "
                f"agents={self.agent_count}, "
                f"servers={self.server_names})")
