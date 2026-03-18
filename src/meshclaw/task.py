"""MeshClaw Task - Units of work distributed across the mesh.

A Task is a discrete piece of work that can be:
- Executed on a specific server
- Part of a parallel batch (run simultaneously on multiple servers)
- Part of a sequential pipeline (output of one feeds into next)
- Part of a collaborative workflow (multiple agents share state)

Tasks can depend on other tasks, enabling complex DAG workflows
that span the entire mesh network.
"""

import time
import json
import uuid
from enum import Enum
from typing import Optional, Dict, Any, List, Callable
from dataclasses import dataclass, field


class TaskState(Enum):
    """Task lifecycle states."""
    PENDING = "pending"
    QUEUED = "queued"
    RUNNING = "running"
    SUCCESS = "success"
    FAILED = "failed"
    SKIPPED = "skipped"
    CANCELLED = "cancelled"
    RETRYING = "retrying"


class TaskType(Enum):
    """What kind of work this task does."""
    SHELL = "shell"       # Shell command
    PYTHON = "python"     # Python code
    FILE_READ = "read"    # Read a file
    FILE_WRITE = "write"  # Write a file
    TRANSFER = "transfer" # Transfer file between servers
    PROBE = "probe"       # Health check / status check
    CUSTOM = "custom"     # User-defined function


@dataclass
class TaskResult:
    """Result of a task execution."""
    task_id: str
    task_name: str
    success: bool
    output: str = ""
    error: str = ""
    exit_code: int = 0
    server: str = ""
    agent: str = ""
    duration: float = 0.0
    data: Dict[str, Any] = field(default_factory=dict)
    timestamp: float = field(default_factory=time.time)

    def to_dict(self) -> Dict[str, Any]:
        return {
            "task_id": self.task_id,
            "task_name": self.task_name,
            "success": self.success,
            "output": self.output,
            "error": self.error,
            "exit_code": self.exit_code,
            "server": self.server,
            "agent": self.agent,
            "duration": self.duration,
            "data": self.data,
            "timestamp": self.timestamp,
        }

    def __bool__(self) -> bool:
        return self.success

    def __repr__(self) -> str:
        status = "OK" if self.success else "FAIL"
        return f"TaskResult({self.task_name}: {status} on {self.server} in {self.duration:.2f}s)"


@dataclass
class Task:
    """A unit of work to be executed on a mesh node.

    Usage:
        # Simple shell task
        task = Task("check-disk", command="df -h", server="server1")

        # Python task
        task = Task("analyze", python="print(2+2)", server="server2")

        # Task with dependencies
        build = Task("build", command="make", server="server1")
        test = Task("test", command="make test", server="server3", depends_on=[build])

        # Task with conditions
        deploy = Task("deploy", command="./deploy.sh", server="v1",
                      condition=lambda results: all(r.success for r in results))

        # Task targeting multiple servers
        task = Task("health-check", command="uptime", servers=["d1", "d2", "g1"])
    """

    name: str
    command: str = ""
    python: str = ""
    task_type: TaskType = TaskType.SHELL
    server: str = ""                              # Single target server
    servers: List[str] = field(default_factory=list)  # Multiple target servers
    depends_on: List["Task"] = field(default_factory=list)
    condition: Optional[Callable] = None          # Run only if condition met
    timeout: int = 300
    retry_count: int = 0
    env: Dict[str, str] = field(default_factory=dict)
    working_dir: str = ""
    tags: List[str] = field(default_factory=list)
    metadata: Dict[str, Any] = field(default_factory=dict)

    # File operations
    file_path: str = ""
    file_content: str = ""
    source_server: str = ""     # For transfers
    target_server: str = ""     # For transfers

    # Runtime state (managed by orchestrator)
    id: str = field(default_factory=lambda: str(uuid.uuid4())[:8])
    state: TaskState = TaskState.PENDING
    result: Optional[TaskResult] = None
    attempts: int = 0
    created_at: float = field(default_factory=time.time)
    started_at: float = 0
    completed_at: float = 0

    def __post_init__(self):
        # Auto-detect task type
        if self.python and not self.command:
            self.task_type = TaskType.PYTHON
        elif self.file_content:
            self.task_type = TaskType.FILE_WRITE
        elif self.source_server and self.target_server:
            self.task_type = TaskType.TRANSFER

        # Normalize server targets
        if self.server and not self.servers:
            self.servers = [self.server]
        elif self.servers and not self.server:
            self.server = self.servers[0]

    @property
    def is_multi_server(self) -> bool:
        """Task targets multiple servers."""
        return len(self.servers) > 1

    @property
    def is_complete(self) -> bool:
        return self.state in (TaskState.SUCCESS, TaskState.FAILED,
                              TaskState.SKIPPED, TaskState.CANCELLED)

    @property
    def is_runnable(self) -> bool:
        """Check if all dependencies are satisfied."""
        if self.state != TaskState.PENDING:
            return False
        return all(dep.state == TaskState.SUCCESS for dep in self.depends_on)

    @property
    def dependency_names(self) -> List[str]:
        return [d.name for d in self.depends_on]

    def should_run(self, completed_results: List[TaskResult]) -> bool:
        """Check if this task should run given completed results."""
        if not self.is_runnable:
            return False
        if self.condition:
            try:
                return bool(self.condition(completed_results))
            except Exception:
                return False
        return True

    def mark_running(self) -> None:
        self.state = TaskState.RUNNING
        self.started_at = time.time()
        self.attempts += 1

    def mark_success(self, result: TaskResult) -> None:
        self.state = TaskState.SUCCESS
        self.result = result
        self.completed_at = time.time()

    def mark_failed(self, result: TaskResult) -> None:
        if self.attempts < self.retry_count + 1:
            self.state = TaskState.RETRYING
        else:
            self.state = TaskState.FAILED
        self.result = result
        self.completed_at = time.time()

    def mark_skipped(self, reason: str = "") -> None:
        self.state = TaskState.SKIPPED
        self.result = TaskResult(
            task_id=self.id, task_name=self.name,
            success=False, error=reason or "Skipped",
            server=self.server, agent=""
        )
        self.completed_at = time.time()

    def mark_cancelled(self) -> None:
        self.state = TaskState.CANCELLED
        self.completed_at = time.time()

    def reset(self) -> None:
        """Reset task for retry."""
        self.state = TaskState.PENDING
        self.result = None
        self.started_at = 0
        self.completed_at = 0

    def to_dict(self) -> Dict[str, Any]:
        return {
            "id": self.id,
            "name": self.name,
            "type": self.task_type.value,
            "state": self.state.value,
            "server": self.server,
            "servers": self.servers,
            "depends_on": self.dependency_names,
            "attempts": self.attempts,
            "duration": round(self.completed_at - self.started_at, 3) if self.started_at else 0,
            "result": self.result.to_dict() if self.result else None,
        }

    def __repr__(self) -> str:
        target = ",".join(self.servers) if self.is_multi_server else self.server
        return f"Task('{self.name}', server='{target}', state={self.state.value})"


def shell(name: str, command: str, server: str = "", **kwargs) -> Task:
    """Shorthand to create a shell task."""
    return Task(name=name, command=command, server=server, task_type=TaskType.SHELL, **kwargs)


def py(name: str, code: str, server: str = "", **kwargs) -> Task:
    """Shorthand to create a Python task."""
    return Task(name=name, python=code, server=server, task_type=TaskType.PYTHON, **kwargs)


def probe(name: str, server: str = "", servers: Optional[List[str]] = None, **kwargs) -> Task:
    """Shorthand to create a health-check task."""
    return Task(name=name, command="uptime", server=server,
                servers=servers or [], task_type=TaskType.PROBE, **kwargs)


def transfer(name: str, path: str, source: str, target: str, **kwargs) -> Task:
    """Shorthand to create a file transfer task."""
    return Task(name=name, file_path=path, source_server=source,
                target_server=target, task_type=TaskType.TRANSFER, **kwargs)
