"""MeshClaw CLI - Command-line interface for distributed agent orchestration.

Usage:
    meshclaw discover                       # Find mesh nodes
    meshclaw status                         # Show orchestrator status
    meshclaw exec "uptime" -s d1            # Run command on server
    meshclaw broadcast "uptime"             # Run on all servers
    meshclaw parallel -s d1 "make" -s d2 "test"  # Parallel tasks
    meshclaw pipeline d1:"build" d2:"test" v1:"deploy"  # Sequential
    meshclaw agents                         # List agents
    meshclaw run scenario.json              # Run scenario from file
"""

import sys
import os
import json
import time
import argparse
from typing import List, Optional

from meshclaw import __version__
from meshclaw.agent import Agent
from meshclaw.task import Task
from meshclaw.orchestrator import Orchestrator
from meshclaw.scenario import (
    ParallelScenario, SequentialScenario, CollaborativeScenario,
    FanOutScenario, MapReduceScenario
)


def print_result(result, verbose=False):
    """Pretty-print a scenario result."""
    state_icons = {
        "success": "\u2713",   # checkmark
        "failed": "\u2717",    # x mark
        "partial": "~",
    }
    icon = state_icons.get(result.state.value, "?")

    print(f"\n{icon} {result.scenario_name} ({result.scenario_type})")
    print(f"  State: {result.state.value}")
    print(f"  Duration: {result.duration:.2f}s")
    print(f"  Tasks: {result.tasks_succeeded}/{result.tasks_total} succeeded")
    print(f"  Servers: {', '.join(result.servers_used)}")

    if verbose or not result.success:
        print(f"\n  Results:")
        for r in result.results:
            icon = "\u2713" if r.success else "\u2717"
            print(f"    {icon} {r.task_name} on {r.server} ({r.duration:.2f}s)")
            if r.output and verbose:
                for line in r.output.strip().split('\n')[:5]:
                    print(f"      | {line}")
            if r.error and not r.success:
                print(f"      ! {r.error[:120]}")


def cmd_discover(args):
    """Discover mesh nodes."""
    with Orchestrator(mode=args.mode) as orch:
        servers = orch.discover()
        print(f"Discovered {len(servers)} nodes ({orch.mode} mode):")
        for s in servers:
            agent = orch.get_agent_for(s)
            state = agent.state.value if agent else "?"
            print(f"  {s} [{state}]")


def cmd_status(args):
    """Show orchestrator status."""
    with Orchestrator(mode=args.mode) as orch:
        orch.discover()
        print(orch.summary())


def cmd_exec(args):
    """Execute a command on a server."""
    with Orchestrator(mode=args.mode) as orch:
        if not args.server:
            orch.discover()
            if not orch.server_names:
                print("No servers found. Use -s to specify a server.", file=sys.stderr)
                return 1
            server = orch.server_names[0]
        else:
            server = args.server
            orch.add_agent(f"agent-{server}", server=server)

        result = orch.exec(args.command, server=server)
        if result["stdout"]:
            print(result["stdout"], end="")
        if result["stderr"]:
            print(result["stderr"], end="", file=sys.stderr)
        return result.get("exit_code", 1)


def cmd_broadcast(args):
    """Run command on all servers."""
    with Orchestrator(mode=args.mode) as orch:
        if args.servers:
            for s in args.servers:
                orch.add_agent(f"agent-{s}", server=s)
        else:
            orch.discover()

        if not orch.server_names:
            print("No servers found.", file=sys.stderr)
            return 1

        result = orch.broadcast("broadcast", args.command,
                                servers=args.servers or None)
        print_result(result, verbose=args.verbose)
        return 0 if result.success else 1


def cmd_parallel(args):
    """Run parallel tasks."""
    tasks = []
    for spec in args.tasks:
        if ':' in spec:
            server, command = spec.split(':', 1)
            tasks.append(Task(f"task-{server}", command=command.strip('"\''),
                              server=server))
        else:
            print(f"Invalid task spec: {spec} (use server:command)", file=sys.stderr)
            return 1

    with Orchestrator(mode=args.mode) as orch:
        for task in tasks:
            if task.server not in orch.agents:
                orch.add_agent(f"agent-{task.server}", server=task.server)

        result = orch.parallel("parallel", tasks)
        print_result(result, verbose=args.verbose)
        return 0 if result.success else 1


def cmd_pipeline(args):
    """Run sequential pipeline."""
    stages = []
    for spec in args.stages:
        if ':' in spec:
            server, command = spec.split(':', 1)
            stages.append({"server": server, "command": command.strip('"\'')})
        else:
            print(f"Invalid stage spec: {spec} (use server:command)", file=sys.stderr)
            return 1

    with Orchestrator(mode=args.mode) as orch:
        for stage in stages:
            if stage["server"] not in orch.agents:
                orch.add_agent(f"agent-{stage['server']}", server=stage["server"])

        result = orch.pipeline("pipeline", stages)
        print_result(result, verbose=args.verbose)
        return 0 if result.success else 1


def cmd_agents(args):
    """List agents."""
    with Orchestrator(mode=args.mode) as orch:
        orch.discover()
        if not orch.agents:
            print("No agents.")
            return

        print(f"{'Name':<20} {'Server':<12} {'State':<10} {'Tasks':<8} {'Uptime'}")
        print("-" * 65)
        for server, agent in orch.agents.items():
            info = agent.info()
            print(f"{info['name']:<20} {info['server']:<12} {info['state']:<10} "
                  f"{info['tasks_completed']:<8} {info['uptime']:.0f}s")


def cmd_run(args):
    """Run scenario from JSON file."""
    try:
        with open(args.file) as f:
            spec = json.load(f)
    except Exception as e:
        print(f"Error reading {args.file}: {e}", file=sys.stderr)
        return 1

    scenario_type = spec.get("type", "parallel")
    name = spec.get("name", os.path.basename(args.file))
    tasks_spec = spec.get("tasks", [])

    tasks = []
    for t in tasks_spec:
        tasks.append(Task(
            name=t.get("name", f"task-{len(tasks)}"),
            command=t.get("command", ""),
            python=t.get("python", ""),
            server=t.get("server", ""),
            timeout=t.get("timeout", 300),
        ))

    with Orchestrator(mode=args.mode) as orch:
        for task in tasks:
            if task.server and task.server not in orch.agents:
                orch.add_agent(f"agent-{task.server}", server=task.server)

        if not orch.agents:
            orch.discover()

        scenario_map = {
            "parallel": ParallelScenario,
            "sequential": SequentialScenario,
        }
        scenario_cls = scenario_map.get(scenario_type, ParallelScenario)
        scenario = scenario_cls(name, tasks)
        result = orch.run(scenario)
        print_result(result, verbose=args.verbose)
        return 0 if result.success else 1


def cmd_version(args):
    """Show version."""
    print(f"meshclaw {__version__}")


def main():
    parser = argparse.ArgumentParser(
        prog="meshclaw",
        description="Distributed AI agent orchestration across mesh networks"
    )
    parser.add_argument("--version", action="version", version=f"meshclaw {__version__}")
    parser.add_argument("--mode", "-m", default="auto",
                        choices=["auto", "mesh", "local"],
                        help="Orchestration mode")
    parser.add_argument("--verbose", "-v", action="store_true",
                        help="Verbose output")

    subparsers = parser.add_subparsers(dest="command")

    # discover
    sub = subparsers.add_parser("discover", help="Discover mesh nodes")
    sub.set_defaults(func=cmd_discover)

    # status
    sub = subparsers.add_parser("status", help="Show orchestrator status")
    sub.set_defaults(func=cmd_status)

    # exec
    sub = subparsers.add_parser("exec", help="Execute command on server")
    sub.add_argument("command", help="Command to execute")
    sub.add_argument("-s", "--server", default="", help="Target server")
    sub.set_defaults(func=cmd_exec)

    # broadcast
    sub = subparsers.add_parser("broadcast", help="Run on all servers")
    sub.add_argument("command", help="Command to broadcast")
    sub.add_argument("-s", "--servers", nargs="*", help="Target servers (default: all)")
    sub.set_defaults(func=cmd_broadcast)

    # parallel
    sub = subparsers.add_parser("parallel", help="Run parallel tasks")
    sub.add_argument("tasks", nargs="+", help="server:command pairs")
    sub.set_defaults(func=cmd_parallel)

    # pipeline
    sub = subparsers.add_parser("pipeline", help="Run sequential pipeline")
    sub.add_argument("stages", nargs="+", help="server:command stages")
    sub.set_defaults(func=cmd_pipeline)

    # agents
    sub = subparsers.add_parser("agents", help="List agents")
    sub.set_defaults(func=cmd_agents)

    # run
    sub = subparsers.add_parser("run", help="Run scenario from JSON file")
    sub.add_argument("file", help="Scenario JSON file")
    sub.set_defaults(func=cmd_run)

    # version
    sub = subparsers.add_parser("version", help="Show version")
    sub.set_defaults(func=cmd_version)

    args = parser.parse_args()
    if not args.command:
        parser.print_help()
        return 0

    try:
        exit_code = args.func(args) or 0
        sys.exit(exit_code)
    except KeyboardInterrupt:
        print("\nInterrupted.")
        sys.exit(130)
    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
