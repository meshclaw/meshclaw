"""MeshClaw Agent - Distributed AI agent that runs on mesh network nodes.

Each agent binds to a server in the mesh and can execute tasks locally.
Agents communicate through the orchestrator and can share state via meshdb.

Key difference from OpenClaw:
- OpenClaw agents run on ONE machine
- MeshClaw agents run on ANY node in the mesh network
- Agents can migrate, replicate, and collaborate across servers
"""

import os
import time
import json
import uuid
import subprocess
import threading
from enum import Enum
from typing import Optional, Dict, Any, List, Callable
from dataclasses import dataclass, field


class AgentState(Enum):
    """Agent lifecycle states."""
    IDLE = "idle"
    RUNNING = "running"
    WAITING = "waiting"      # Waiting for dependency
    MIGRATING = "migrating"  # Moving to another node
    ERROR = "error"
    STOPPED = "stopped"


class AgentCapability(Enum):
    """What this agent can do."""
    SHELL = "shell"           # Execute shell commands
    PYTHON = "python"         # Run Python code
    FILE_IO = "file_io"       # Read/write files
    NETWORK = "network"       # Network operations
    GPU = "gpu"               # GPU compute
    BUILD = "build"           # Build/compile
    DEPLOY = "deploy"         # Deployment
    MONITOR = "monitor"       # System monitoring
    DATABASE = "database"     # Database operations


@dataclass
class AgentConfig:
    """Agent configuration."""
    name: str = ""
    server: str = ""                          # Target mesh server
    capabilities: List[str] = field(default_factory=lambda: ["shell", "python", "file_io"])
    max_concurrent_tasks: int = 5
    timeout: int = 300                        # Default task timeout (seconds)
    working_dir: str = "/tmp/meshclaw"
    env: Dict[str, str] = field(default_factory=dict)
    soul: str = ""                            # Agent personality/instructions (like SOUL.md)
    retry_count: int = 2
    heartbeat_interval: int = 30


class Agent:
    """A distributed AI agent bound to a mesh network node.

    Usage:
        agent = Agent("builder", server="d1")
        agent.start()
        result = agent.execute("uname -a")
        agent.stop()

    With MeshPOP integration:
        agent = Agent("builder", server="d1", use_meshpop=True)
        # Automatically uses vssh for remote execution
        # Automatically registers in meshdb for discovery
        # Automatically uses wire for secure transport
    """

    def __init__(self, name: str = "", server: str = "", *,
                 config: Optional[AgentConfig] = None,
                 use_meshpop: bool = True):
        self.id = str(uuid.uuid4())[:8]
        self.config = config or AgentConfig()
        self.config.name = name or self.config.name or f"agent-{self.id}"
        self.config.server = server or self.config.server or self._detect_server()
        self.state = AgentState.IDLE
        self.use_meshpop = use_meshpop
        self.task_history: List[Dict[str, Any]] = []
        self.shared_state: Dict[str, Any] = {}
        self._lock = threading.Lock()
        self._heartbeat_thread: Optional[threading.Thread] = None
        self._running = False
        self._callbacks: Dict[str, List[Callable]] = {}
        self.created_at = time.time()
        self.last_active = time.time()

    @property
    def name(self) -> str:
        return self.config.name

    @property
    def server(self) -> str:
        return self.config.server

    @property
    def is_local(self) -> bool:
        """Check if this agent runs on the local machine."""
        hostname = os.uname().nodename.split('.')[0].lower()
        return self.server.lower() in (hostname, 'localhost', '127.0.0.1', '')

    @property
    def is_alive(self) -> bool:
        return self._running and self.state not in (AgentState.ERROR, AgentState.STOPPED)

    def _detect_server(self) -> str:
        """Auto-detect current server name."""
        try:
            return os.uname().nodename.split('.')[0]
        except Exception:
            return "localhost"

    def start(self) -> "Agent":
        """Start the agent. Returns self for chaining."""
        self._running = True
        self.state = AgentState.IDLE
        self.last_active = time.time()

        # Ensure working directory exists
        if self.is_local:
            os.makedirs(self.config.working_dir, exist_ok=True)
        else:
            self._remote_exec(f"mkdir -p {self.config.working_dir}")

        # Register in meshdb if available
        if self.use_meshpop:
            self._register()

        # Start heartbeat
        self._heartbeat_thread = threading.Thread(target=self._heartbeat_loop, daemon=True)
        self._heartbeat_thread.start()

        self._emit("started", {"agent": self.name, "server": self.server})
        return self

    def stop(self) -> None:
        """Stop the agent."""
        self._running = False
        self.state = AgentState.STOPPED
        if self.use_meshpop:
            self._deregister()
        self._emit("stopped", {"agent": self.name})

    def execute(self, command: str, *, timeout: Optional[int] = None,
                env: Optional[Dict[str, str]] = None,
                working_dir: Optional[str] = None) -> Dict[str, Any]:
        """Execute a command on this agent's server.

        Returns:
            {
                "success": bool,
                "stdout": str,
                "stderr": str,
                "exit_code": int,
                "duration": float,
                "server": str,
                "agent": str
            }
        """
        if not self._running:
            return {"success": False, "stderr": "Agent not running", "exit_code": -1,
                    "stdout": "", "duration": 0, "server": self.server, "agent": self.name}

        with self._lock:
            self.state = AgentState.RUNNING
            self.last_active = time.time()

        timeout = timeout or self.config.timeout
        cwd = working_dir or self.config.working_dir
        merged_env = {**os.environ, **self.config.env, **(env or {})}

        start_time = time.time()
        try:
            if self.is_local:
                result = self._local_exec(command, timeout=timeout, env=merged_env, cwd=cwd)
            else:
                result = self._remote_exec(command, timeout=timeout)
            duration = time.time() - start_time

            output = {
                "success": result["exit_code"] == 0,
                "stdout": result["stdout"],
                "stderr": result["stderr"],
                "exit_code": result["exit_code"],
                "duration": round(duration, 3),
                "server": self.server,
                "agent": self.name,
            }

            self.task_history.append({
                "command": command,
                "result": output,
                "timestamp": time.time(),
            })

            with self._lock:
                self.state = AgentState.IDLE

            self._emit("executed", output)
            return output

        except subprocess.TimeoutExpired:
            with self._lock:
                self.state = AgentState.ERROR
            return {"success": False, "stdout": "", "stderr": f"Timeout after {timeout}s",
                    "exit_code": -1, "duration": timeout, "server": self.server, "agent": self.name}
        except Exception as e:
            with self._lock:
                self.state = AgentState.ERROR
            return {"success": False, "stdout": "", "stderr": str(e),
                    "exit_code": -1, "duration": time.time() - start_time,
                    "server": self.server, "agent": self.name}

    def execute_python(self, code: str, *, timeout: Optional[int] = None) -> Dict[str, Any]:
        """Execute Python code on this agent's server."""
        # Escape for shell
        escaped = code.replace("'", "'\\''")
        return self.execute(f"python3 -c '{escaped}'", timeout=timeout)

    def read_file(self, path: str) -> Dict[str, Any]:
        """Read a file from this agent's server."""
        if self.is_local:
            try:
                with open(path) as f:
                    return {"success": True, "content": f.read(), "server": self.server}
            except Exception as e:
                return {"success": False, "content": "", "error": str(e), "server": self.server}
        else:
            result = self._remote_exec(f"cat {path}")
            return {"success": result["exit_code"] == 0,
                    "content": result["stdout"],
                    "error": result["stderr"],
                    "server": self.server}

    def write_file(self, path: str, content: str) -> Dict[str, Any]:
        """Write a file to this agent's server."""
        if self.is_local:
            try:
                os.makedirs(os.path.dirname(path) or '.', exist_ok=True)
                with open(path, 'w') as f:
                    f.write(content)
                return {"success": True, "path": path, "server": self.server}
            except Exception as e:
                return {"success": False, "error": str(e), "server": self.server}
        else:
            # Use vssh put if available, fallback to echo
            escaped = content.replace("'", "'\\''")
            result = self._remote_exec(f"mkdir -p $(dirname {path}) && cat > {path} << 'MESHCLAW_EOF'\n{content}\nMESHCLAW_EOF")
            return {"success": result["exit_code"] == 0,
                    "path": path, "error": result["stderr"], "server": self.server}

    def get_state(self, key: str, default: Any = None) -> Any:
        """Get shared state value."""
        return self.shared_state.get(key, default)

    def set_state(self, key: str, value: Any) -> None:
        """Set shared state value (broadcast to orchestrator)."""
        self.shared_state[key] = value
        self._emit("state_changed", {"key": key, "value": value, "agent": self.name})

    def migrate(self, target_server: str) -> bool:
        """Migrate this agent to a different server in the mesh.

        This is what OpenClaw CAN'T do. An agent can move between servers
        while preserving its state and task history.
        """
        if target_server == self.server:
            return True

        with self._lock:
            old_server = self.server
            self.state = AgentState.MIGRATING

        # Test connectivity to target
        test = self._exec_on(target_server, "echo meshclaw_ping")
        if not test.get("success"):
            with self._lock:
                self.state = AgentState.ERROR
            return False

        # Prepare working directory on target
        self._exec_on(target_server, f"mkdir -p {self.config.working_dir}")

        # Transfer state
        state_json = json.dumps({
            "id": self.id,
            "name": self.name,
            "shared_state": self.shared_state,
            "task_history_count": len(self.task_history),
        })
        self._exec_on(target_server,
                       f"echo '{state_json}' > {self.config.working_dir}/.agent_state.json")

        # Update server binding
        with self._lock:
            self.config.server = target_server
            self.state = AgentState.IDLE

        if self.use_meshpop:
            self._register()

        self._emit("migrated", {"from": old_server, "to": target_server, "agent": self.name})
        return True

    def on(self, event: str, callback: Callable) -> None:
        """Register an event callback."""
        self._callbacks.setdefault(event, []).append(callback)

    def info(self) -> Dict[str, Any]:
        """Get agent info summary."""
        return {
            "id": self.id,
            "name": self.name,
            "server": self.server,
            "state": self.state.value,
            "is_local": self.is_local,
            "capabilities": self.config.capabilities,
            "tasks_completed": len(self.task_history),
            "uptime": round(time.time() - self.created_at, 1),
            "last_active": round(time.time() - self.last_active, 1),
        }

    def _emit(self, event: str, data: Dict[str, Any]) -> None:
        """Emit an event to registered callbacks."""
        for cb in self._callbacks.get(event, []):
            try:
                cb(data)
            except Exception:
                pass

    def _local_exec(self, command: str, *, timeout: int = 300,
                    env: Optional[Dict] = None, cwd: Optional[str] = None) -> Dict[str, Any]:
        """Execute command locally."""
        proc = subprocess.run(
            command, shell=True, capture_output=True, text=True,
            timeout=timeout, env=env, cwd=cwd
        )
        return {"stdout": proc.stdout, "stderr": proc.stderr, "exit_code": proc.returncode}

    def _remote_exec(self, command: str, *, timeout: int = 300) -> Dict[str, Any]:
        """Execute command on remote server via MeshPOP stack."""
        # Try vssh first (fastest), then mpop exec, then ssh
        for method in [self._exec_vssh, self._exec_mpop, self._exec_ssh]:
            try:
                result = method(command, timeout=timeout)
                if result is not None:
                    return result
            except Exception:
                continue

        return {"stdout": "", "stderr": f"Cannot reach server {self.server}", "exit_code": -1}

    def _exec_on(self, server: str, command: str) -> Dict[str, Any]:
        """Execute command on a specific server."""
        old_server = self.config.server
        self.config.server = server
        result = self._remote_exec(command)
        self.config.server = old_server
        return {"success": result["exit_code"] == 0, **result}

    def _exec_vssh(self, command: str, timeout: int = 300) -> Optional[Dict[str, Any]]:
        """Execute via vssh (MeshPOP fast transfer protocol)."""
        try:
            proc = subprocess.run(
                ["vssh", "exec", self.server, "--", command],
                capture_output=True, text=True, timeout=timeout
            )
            return {"stdout": proc.stdout, "stderr": proc.stderr, "exit_code": proc.returncode}
        except FileNotFoundError:
            return None

    def _exec_mpop(self, command: str, timeout: int = 300) -> Optional[Dict[str, Any]]:
        """Execute via mpop exec."""
        try:
            proc = subprocess.run(
                ["mpop", "exec", "-s", self.server, command],
                capture_output=True, text=True, timeout=timeout
            )
            return {"stdout": proc.stdout, "stderr": proc.stderr, "exit_code": proc.returncode}
        except FileNotFoundError:
            return None

    def _exec_ssh(self, command: str, timeout: int = 300) -> Optional[Dict[str, Any]]:
        """Execute via plain SSH (fallback)."""
        try:
            proc = subprocess.run(
                ["ssh", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=no",
                 self.server, command],
                capture_output=True, text=True, timeout=timeout
            )
            return {"stdout": proc.stdout, "stderr": proc.stderr, "exit_code": proc.returncode}
        except FileNotFoundError:
            return None

    def _register(self) -> None:
        """Register agent in meshdb for discovery."""
        try:
            subprocess.run(
                ["meshdb", "write", f"meshclaw:agent:{self.name}",
                 json.dumps(self.info())],
                capture_output=True, timeout=5
            )
        except Exception:
            pass  # meshdb not available, that's fine

    def _deregister(self) -> None:
        """Remove agent from meshdb."""
        try:
            subprocess.run(
                ["meshdb", "delete", f"meshclaw:agent:{self.name}"],
                capture_output=True, timeout=5
            )
        except Exception:
            pass

    def _heartbeat_loop(self) -> None:
        """Send periodic heartbeats."""
        while self._running:
            time.sleep(self.config.heartbeat_interval)
            if self._running and self.use_meshpop:
                self._register()

    def __repr__(self) -> str:
        return f"Agent('{self.name}', server='{self.server}', state={self.state.value})"

    def __enter__(self) -> "Agent":
        return self.start()

    def __exit__(self, *args) -> None:
        self.stop()
