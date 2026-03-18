"""MeshClaw Scenarios - Multi-server execution patterns.

Three core scenarios that OpenClaw can't do:

1. ParallelScenario: Same or different tasks on multiple servers simultaneously
   - "Build on d1 AND test on d2 AND deploy on v1, all at once"

2. SequentialScenario: Pipeline across servers, output flows to next
   - "Build on d1, THEN test result on d2, THEN deploy to v1"

3. CollaborativeScenario: Multiple agents share state and coordinate
   - "Agent on d1 scrapes data, agent on g1 processes it, agent on v1 serves it"
   - Agents can read/write shared state and react to each other's progress

Plus advanced patterns:
4. FanOutScenario: One task fans out to many servers
5. MapReduceScenario: Map work across servers, reduce results
6. PipelineScenario: Multi-stage pipeline with branching

On a SINGLE machine with rtlinux containers:
   - Runs the same patterns across local containers
   - Each container acts as a mesh node
   - Same API, same power, one machine
"""

import time
import json
from typing import List, Dict, Any, Optional, Callable
from dataclasses import dataclass, field
from enum import Enum

from meshclaw.task import Task, TaskResult, TaskState


class ScenarioState(Enum):
    PENDING = "pending"
    RUNNING = "running"
    SUCCESS = "success"
    PARTIAL = "partial"   # Some tasks succeeded, some failed
    FAILED = "failed"
    CANCELLED = "cancelled"


@dataclass
class ScenarioResult:
    """Aggregated result of a scenario execution."""
    scenario_name: str
    scenario_type: str
    state: ScenarioState
    results: List[TaskResult]
    duration: float
    servers_used: List[str]
    tasks_total: int
    tasks_succeeded: int
    tasks_failed: int
    summary: str = ""

    @property
    def success(self) -> bool:
        return self.state == ScenarioState.SUCCESS

    def to_dict(self) -> Dict[str, Any]:
        return {
            "scenario": self.scenario_name,
            "type": self.scenario_type,
            "state": self.state.value,
            "duration": round(self.duration, 3),
            "servers": self.servers_used,
            "tasks": f"{self.tasks_succeeded}/{self.tasks_total}",
            "results": [r.to_dict() for r in self.results],
            "summary": self.summary,
        }

    def __repr__(self) -> str:
        return (f"ScenarioResult({self.scenario_name}: {self.state.value}, "
                f"{self.tasks_succeeded}/{self.tasks_total} tasks, "
                f"{self.duration:.2f}s)")


class Scenario:
    """Base scenario class. Subclass to create custom execution patterns."""

    def __init__(self, name: str, tasks: Optional[List[Task]] = None,
                 on_progress: Optional[Callable] = None):
        self.name = name
        self.tasks = tasks or []
        self.state = ScenarioState.PENDING
        self.on_progress = on_progress
        self.results: List[TaskResult] = []
        self.started_at = 0.0
        self.completed_at = 0.0

    @property
    def scenario_type(self) -> str:
        return self.__class__.__name__

    def add_task(self, task: Task) -> "Scenario":
        """Add a task. Returns self for chaining."""
        self.tasks.append(task)
        return self

    def _progress(self, message: str) -> None:
        if self.on_progress:
            try:
                self.on_progress(message)
            except Exception:
                pass

    def _build_result(self) -> ScenarioResult:
        succeeded = sum(1 for r in self.results if r.success)
        failed = len(self.results) - succeeded
        servers = list(set(r.server for r in self.results if r.server))
        duration = self.completed_at - self.started_at

        if failed == 0:
            state = ScenarioState.SUCCESS
        elif succeeded == 0:
            state = ScenarioState.FAILED
        else:
            state = ScenarioState.PARTIAL

        self.state = state
        return ScenarioResult(
            scenario_name=self.name,
            scenario_type=self.scenario_type,
            state=state,
            results=self.results,
            duration=duration,
            servers_used=servers,
            tasks_total=len(self.results),
            tasks_succeeded=succeeded,
            tasks_failed=failed,
        )

    def execute(self, orchestrator: "Orchestrator") -> ScenarioResult:
        """Execute this scenario. Override in subclasses."""
        raise NotImplementedError

    def __repr__(self) -> str:
        return f"{self.scenario_type}('{self.name}', {len(self.tasks)} tasks)"


class ParallelScenario(Scenario):
    """Execute tasks on multiple servers simultaneously.

    Usage:
        scenario = ParallelScenario("health-check", [
            Task("check-d1", command="uptime", server="d1"),
            Task("check-d2", command="uptime", server="d2"),
            Task("check-g1", command="uptime", server="g1"),
        ])
        result = orchestrator.run(scenario)

    Or with a single command on multiple servers:
        scenario = ParallelScenario.broadcast("disk-check", "df -h",
                                               servers=["d1", "d2", "g1", "v1"])
    """

    def __init__(self, name: str, tasks: Optional[List[Task]] = None, *,
                 fail_fast: bool = False, max_concurrent: int = 0,
                 on_progress: Optional[Callable] = None):
        super().__init__(name, tasks, on_progress)
        self.fail_fast = fail_fast      # Stop all if one fails
        self.max_concurrent = max_concurrent  # 0 = unlimited

    @classmethod
    def broadcast(cls, name: str, command: str, servers: List[str],
                  **kwargs) -> "ParallelScenario":
        """Create a parallel scenario that runs the same command on multiple servers."""
        tasks = [Task(f"{name}-{s}", command=command, server=s) for s in servers]
        return cls(name, tasks, **kwargs)

    def execute(self, orchestrator) -> ScenarioResult:
        """Execute all tasks in parallel."""
        import concurrent.futures

        self.started_at = time.time()
        self.state = ScenarioState.RUNNING
        self._progress(f"Starting parallel execution: {len(self.tasks)} tasks")

        max_workers = self.max_concurrent or len(self.tasks)

        with concurrent.futures.ThreadPoolExecutor(max_workers=max_workers) as executor:
            future_to_task = {}
            for task in self.tasks:
                agent = orchestrator.get_agent_for(task.server)
                if agent:
                    future = executor.submit(self._execute_task, agent, task)
                    future_to_task[future] = task
                else:
                    task.mark_skipped(f"No agent for server {task.server}")
                    self.results.append(task.result)

            for future in concurrent.futures.as_completed(future_to_task):
                task = future_to_task[future]
                try:
                    result = future.result()
                    self.results.append(result)
                    self._progress(f"  {task.name} on {task.server}: "
                                   f"{'OK' if result.success else 'FAIL'}")

                    if self.fail_fast and not result.success:
                        executor.shutdown(wait=False, cancel_futures=True)
                        break
                except Exception as e:
                    fail_result = TaskResult(
                        task_id=task.id, task_name=task.name,
                        success=False, error=str(e), server=task.server
                    )
                    self.results.append(fail_result)

        self.completed_at = time.time()
        return self._build_result()

    def _execute_task(self, agent, task: Task) -> TaskResult:
        task.mark_running()
        result_dict = agent.execute(task.command, timeout=task.timeout)
        result = TaskResult(
            task_id=task.id, task_name=task.name,
            success=result_dict["success"],
            output=result_dict["stdout"],
            error=result_dict["stderr"],
            exit_code=result_dict["exit_code"],
            server=result_dict["server"],
            agent=result_dict["agent"],
            duration=result_dict["duration"],
        )
        if result.success:
            task.mark_success(result)
        else:
            task.mark_failed(result)
        return result


class SequentialScenario(Scenario):
    """Execute tasks in sequence across servers. Output chains to next task.

    Usage:
        scenario = SequentialScenario("build-test-deploy", [
            Task("build", command="make build", server="d1"),
            Task("test", command="make test", server="d2"),
            Task("deploy", command="./deploy.sh", server="v1"),
        ])
        result = orchestrator.run(scenario)

    The output of each task is available to the next via {PREV_OUTPUT} placeholder.
    """

    def __init__(self, name: str, tasks: Optional[List[Task]] = None, *,
                 stop_on_failure: bool = True,
                 on_progress: Optional[Callable] = None):
        super().__init__(name, tasks, on_progress)
        self.stop_on_failure = stop_on_failure

    def execute(self, orchestrator) -> ScenarioResult:
        """Execute tasks one by one, chaining outputs."""
        self.started_at = time.time()
        self.state = ScenarioState.RUNNING
        self._progress(f"Starting sequential pipeline: {len(self.tasks)} stages")

        prev_output = ""
        for i, task in enumerate(self.tasks):
            self._progress(f"  Stage {i+1}/{len(self.tasks)}: {task.name} on {task.server}")

            agent = orchestrator.get_agent_for(task.server)
            if not agent:
                task.mark_skipped(f"No agent for server {task.server}")
                self.results.append(task.result)
                if self.stop_on_failure:
                    self._skip_remaining(i + 1)
                    break
                continue

            # Inject previous output into command
            command = task.command.replace("{PREV_OUTPUT}", prev_output.strip())
            if task.python:
                command = task.python.replace("{PREV_OUTPUT}", prev_output.strip())

            task.mark_running()
            if task.python:
                result_dict = agent.execute_python(command, timeout=task.timeout)
            else:
                result_dict = agent.execute(command, timeout=task.timeout)

            result = TaskResult(
                task_id=task.id, task_name=task.name,
                success=result_dict["success"],
                output=result_dict["stdout"],
                error=result_dict["stderr"],
                exit_code=result_dict["exit_code"],
                server=result_dict["server"],
                agent=result_dict["agent"],
                duration=result_dict["duration"],
            )
            self.results.append(result)

            if result.success:
                task.mark_success(result)
                prev_output = result.output
                self._progress(f"    -> OK ({result.duration:.2f}s)")
            else:
                task.mark_failed(result)
                self._progress(f"    -> FAIL: {result.error[:80]}")
                if self.stop_on_failure:
                    self._skip_remaining(i + 1)
                    break

        self.completed_at = time.time()
        return self._build_result()

    def _skip_remaining(self, from_index: int) -> None:
        """Skip all remaining tasks after a failure."""
        for task in self.tasks[from_index:]:
            task.mark_skipped("Previous stage failed")
            self.results.append(task.result)


class CollaborativeScenario(Scenario):
    """Multiple agents work together with shared state.

    This is the most powerful pattern - agents on different servers
    communicate via shared state, reacting to each other's progress.

    Usage:
        scenario = CollaborativeScenario("data-pipeline")
        scenario.add_agent_task("scraper", server="d1",
            command="curl -s https://api.example.com/data > /tmp/data.json",
            publishes=["data_ready"])
        scenario.add_agent_task("processor", server="g1",
            command="python3 process.py",
            waits_for=["data_ready"],
            publishes=["processed"])
        scenario.add_agent_task("server", server="v1",
            command="python3 serve.py",
            waits_for=["processed"])
        result = orchestrator.run(scenario)
    """

    def __init__(self, name: str, *,
                 timeout: int = 600,
                 on_progress: Optional[Callable] = None):
        super().__init__(name, [], on_progress)
        self.timeout = timeout
        self.agent_tasks: List[Dict[str, Any]] = []
        self.shared_state: Dict[str, Any] = {}
        self._signals: Dict[str, bool] = {}

    def add_agent_task(self, name: str, server: str, command: str = "",
                       python: str = "",
                       waits_for: Optional[List[str]] = None,
                       publishes: Optional[List[str]] = None,
                       **kwargs) -> "CollaborativeScenario":
        """Add a collaborative task that can wait for and publish signals."""
        task = Task(name=name, command=command, python=python, server=server, **kwargs)
        self.tasks.append(task)
        self.agent_tasks.append({
            "task": task,
            "waits_for": waits_for or [],
            "publishes": publishes or [],
        })
        return self

    def signal(self, name: str) -> None:
        """Set a signal (called by task completion handlers)."""
        self._signals[name] = True

    def wait_signal(self, name: str, timeout: float = 60) -> bool:
        """Wait for a signal with timeout."""
        start = time.time()
        while time.time() - start < timeout:
            if self._signals.get(name):
                return True
            time.sleep(0.5)
        return False

    def execute(self, orchestrator) -> ScenarioResult:
        """Execute collaborative workflow with signal-based coordination."""
        import concurrent.futures

        self.started_at = time.time()
        self.state = ScenarioState.RUNNING
        self._progress(f"Starting collaborative scenario: {len(self.agent_tasks)} agents")

        def run_agent_task(at: Dict[str, Any]) -> TaskResult:
            task = at["task"]
            waits = at["waits_for"]
            pubs = at["publishes"]

            # Wait for dependencies
            for sig in waits:
                self._progress(f"  {task.name}: waiting for signal '{sig}'")
                if not self.wait_signal(sig, timeout=self.timeout):
                    task.mark_skipped(f"Timeout waiting for signal '{sig}'")
                    return task.result

            # Execute
            agent = orchestrator.get_agent_for(task.server)
            if not agent:
                task.mark_skipped(f"No agent for {task.server}")
                return task.result

            self._progress(f"  {task.name}: executing on {task.server}")
            task.mark_running()

            if task.python:
                result_dict = agent.execute_python(task.python, timeout=task.timeout)
            else:
                result_dict = agent.execute(task.command, timeout=task.timeout)

            result = TaskResult(
                task_id=task.id, task_name=task.name,
                success=result_dict["success"],
                output=result_dict["stdout"],
                error=result_dict["stderr"],
                exit_code=result_dict["exit_code"],
                server=result_dict["server"],
                agent=result_dict["agent"],
                duration=result_dict["duration"],
            )

            if result.success:
                task.mark_success(result)
                # Publish signals
                for sig in pubs:
                    self.signal(sig)
                    self._progress(f"  {task.name}: published signal '{sig}'")
            else:
                task.mark_failed(result)

            return result

        with concurrent.futures.ThreadPoolExecutor(
                max_workers=len(self.agent_tasks)) as executor:
            futures = [executor.submit(run_agent_task, at) for at in self.agent_tasks]
            for future in concurrent.futures.as_completed(futures):
                try:
                    result = future.result()
                    self.results.append(result)
                except Exception as e:
                    self.results.append(TaskResult(
                        task_id="error", task_name="unknown",
                        success=False, error=str(e)
                    ))

        self.completed_at = time.time()
        return self._build_result()


class FanOutScenario(Scenario):
    """Run one command across all (or selected) mesh servers.

    Usage:
        scenario = FanOutScenario("update-all", "apt update && apt upgrade -y")
        result = orchestrator.run(scenario)  # Runs on ALL mesh nodes
    """

    def __init__(self, name: str, command: str, *,
                 servers: Optional[List[str]] = None,
                 on_progress: Optional[Callable] = None):
        super().__init__(name, [], on_progress)
        self.command = command
        self.target_servers = servers  # None = all

    def execute(self, orchestrator) -> ScenarioResult:
        servers = self.target_servers or list(orchestrator.agents.keys())
        parallel = ParallelScenario.broadcast(self.name, self.command, servers,
                                               on_progress=self.on_progress)
        return parallel.execute(orchestrator)


class MapReduceScenario(Scenario):
    """Map work across servers, then reduce results on one server.

    Usage:
        scenario = MapReduceScenario(
            "log-analysis",
            map_command="grep ERROR /var/log/syslog | wc -l",
            map_servers=["d1", "d2", "g1", "v1"],
            reduce_command="python3 -c 'import sys; print(sum(int(l) for l in sys.stdin))'",
            reduce_server="m1"
        )
    """

    def __init__(self, name: str, *,
                 map_command: str,
                 map_servers: List[str],
                 reduce_command: str,
                 reduce_server: str,
                 on_progress: Optional[Callable] = None):
        super().__init__(name, [], on_progress)
        self.map_command = map_command
        self.map_servers = map_servers
        self.reduce_command = reduce_command
        self.reduce_server = reduce_server

    def execute(self, orchestrator) -> ScenarioResult:
        self.started_at = time.time()
        self.state = ScenarioState.RUNNING

        # Map phase
        self._progress(f"MAP phase: {self.map_command} on {len(self.map_servers)} servers")
        map_scenario = ParallelScenario.broadcast(
            f"{self.name}-map", self.map_command, self.map_servers)
        map_result = map_scenario.execute(orchestrator)
        self.results.extend(map_result.results)

        if not map_result.success and map_result.state == ScenarioState.FAILED:
            self.completed_at = time.time()
            return self._build_result()

        # Collect map outputs
        map_outputs = "\n".join(r.output.strip() for r in map_result.results if r.success)

        # Reduce phase
        self._progress(f"REDUCE phase: on {self.reduce_server}")
        reduce_agent = orchestrator.get_agent_for(self.reduce_server)
        if reduce_agent:
            # Substitute {PREV_OUTPUT} placeholder, then pipe map outputs into reduce command
            reduce_command = self.reduce_command.replace("{PREV_OUTPUT}", map_outputs.strip())
            reduce_cmd = f"echo '{map_outputs}' | {reduce_command}"
            result_dict = reduce_agent.execute(reduce_cmd)
            reduce_result = TaskResult(
                task_id="reduce", task_name=f"{self.name}-reduce",
                success=result_dict["success"],
                output=result_dict["stdout"],
                error=result_dict["stderr"],
                exit_code=result_dict["exit_code"],
                server=result_dict["server"],
                agent=result_dict["agent"],
                duration=result_dict["duration"],
            )
            self.results.append(reduce_result)

        self.completed_at = time.time()
        return self._build_result()
