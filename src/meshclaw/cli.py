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
    meshclaw brain "check all servers"      # AI agent - give it a goal
    meshclaw chat                           # Interactive AI agent chat
    meshclaw telegram --token BOT_TOKEN     # Telegram bot
    meshclaw slack --token TOKEN -c CHANNEL # Slack bot
    meshclaw discord --token TOKEN          # Discord bot
    meshclaw webhook --port 8199            # Webhook server
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


def cmd_brain(args):
    """Run AI brain with a goal."""
    from meshclaw.brain import Brain, LLMConfig

    llm_config = LLMConfig.from_env()
    if args.model:
        llm_config.model = args.model

    orch = Orchestrator(mode=args.mode)
    orch.discover()

    brain = Brain(
        llm_config=llm_config,
        orchestrator=orch,
        max_steps=args.max_steps,
        verbose=args.verbose,
    )
    result = brain.run(args.goal)
    return 0 if result.success else 1


def cmd_chat(args):
    """Interactive AI agent chat."""
    from meshclaw.brain import Brain, LLMConfig

    llm_config = LLMConfig.from_env()
    if args.model:
        llm_config.model = args.model

    orch = Orchestrator(mode=args.mode)
    try:
        orch.discover()
    except Exception:
        pass

    brain = Brain(
        llm_config=llm_config,
        orchestrator=orch,
        verbose=args.verbose,
    )

    print(f"MeshClaw Brain v{__version__} — interactive mode")
    print(f"LLM: {llm_config.provider}/{llm_config.model}")
    if orch.server_names:
        print(f"Mesh: {', '.join(orch.server_names)}")
    print("Type your goal, or 'quit' to exit.\n")

    while True:
        try:
            goal = input("You: ").strip()
        except (EOFError, KeyboardInterrupt):
            print("\nBye.")
            break

        if not goal or goal.lower() in ("quit", "exit", "q"):
            print("Bye.")
            break

        brain.run(goal)
        print()


def _make_brain(args):
    """Create Brain instance for messenger commands."""
    from meshclaw.brain import Brain, LLMConfig
    llm_config = LLMConfig.from_env()
    if hasattr(args, 'model') and args.model:
        llm_config.model = args.model

    orch = Orchestrator(mode=args.mode)
    try:
        orch.discover()
    except Exception:
        pass

    return Brain(
        llm_config=llm_config,
        orchestrator=orch,
        approval_mode=not getattr(args, 'no_approval', False),
        verbose=getattr(args, 'verbose', False),
    )


def cmd_telegram(args):
    """Run Telegram bot."""
    from meshclaw.messenger import TelegramAdapter
    token = args.token or os.environ.get("TELEGRAM_BOT_TOKEN", "")
    if not token:
        print("Error: --token or TELEGRAM_BOT_TOKEN required", file=sys.stderr)
        return 1

    brain = _make_brain(args)
    allowed = args.users.split(",") if args.users else None
    bot = TelegramAdapter(
        token=token,
        brain=brain,
        allowed_users=allowed,
        approval_mode=not args.no_approval,
    )
    bot.run()


def cmd_slack(args):
    """Run Slack bot."""
    from meshclaw.messenger import SlackAdapter
    token = args.token or os.environ.get("SLACK_BOT_TOKEN", "")
    if not token:
        print("Error: --token or SLACK_BOT_TOKEN required", file=sys.stderr)
        return 1

    brain = _make_brain(args)
    bot = SlackAdapter(
        token=token,
        channel=args.channel,
        brain=brain,
        approval_mode=not args.no_approval,
    )
    bot.run()


def cmd_discord(args):
    """Run Discord bot."""
    from meshclaw.messenger import DiscordAdapter
    token = args.token or os.environ.get("DISCORD_BOT_TOKEN", "")
    if not token:
        print("Error: --token or DISCORD_BOT_TOKEN required", file=sys.stderr)
        return 1

    brain = _make_brain(args)
    bot = DiscordAdapter(
        token=token,
        brain=brain,
        approval_mode=not args.no_approval,
    )
    bot.run()


def cmd_webhook(args):
    """Run webhook server."""
    from meshclaw.messenger import WebhookAdapter
    brain = _make_brain(args)
    server = WebhookAdapter(
        host=args.host,
        port=args.port,
        brain=brain,
        approval_mode=not args.no_approval,
    )
    server.run()


def cmd_whatsapp(args):
    """Run WhatsApp bot (via webhook — requires Twilio or WhatsApp Business API)."""
    from meshclaw.messenger import WebhookAdapter
    brain = _make_brain(args)

    print("🌲 MeshClaw WhatsApp Bot")
    print("   WhatsApp uses webhook mode.")
    print(f"   Set your WhatsApp Business API webhook to: http://YOUR_IP:{args.port}/")
    print("   Supports: Twilio WhatsApp, Meta WhatsApp Business API")

    server = WebhookAdapter(
        host=args.host,
        port=args.port,
        brain=brain,
        approval_mode=not args.no_approval,
    )
    server.run()


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

    # brain
    sub = subparsers.add_parser("brain", help="AI agent - give it a goal in natural language")
    sub.add_argument("goal", help="Goal for the AI agent")
    sub.add_argument("--model", default="", help="LLM model override")
    sub.add_argument("--max-steps", type=int, default=20, help="Max reasoning steps")
    sub.set_defaults(func=cmd_brain)

    # chat
    sub = subparsers.add_parser("chat", help="Interactive AI agent chat")
    sub.add_argument("--model", default="", help="LLM model override")
    sub.set_defaults(func=cmd_chat)

    # telegram
    sub = subparsers.add_parser("telegram", help="Run Telegram bot")
    sub.add_argument("--token", "-t", default="", help="Bot token (or TELEGRAM_BOT_TOKEN env)")
    sub.add_argument("--model", default="", help="LLM model override")
    sub.add_argument("--users", default="", help="Allowed user IDs (comma-separated)")
    sub.add_argument("--no-approval", action="store_true", help="Skip approval for actions")
    sub.set_defaults(func=cmd_telegram)

    # slack
    sub = subparsers.add_parser("slack", help="Run Slack bot")
    sub.add_argument("--token", "-t", default="", help="Bot token (or SLACK_BOT_TOKEN env)")
    sub.add_argument("--channel", "-c", default="", help="Channel to monitor")
    sub.add_argument("--model", default="", help="LLM model override")
    sub.add_argument("--no-approval", action="store_true", help="Skip approval for actions")
    sub.set_defaults(func=cmd_slack)

    # discord
    sub = subparsers.add_parser("discord", help="Run Discord bot")
    sub.add_argument("--token", "-t", default="", help="Bot token (or DISCORD_BOT_TOKEN env)")
    sub.add_argument("--model", default="", help="LLM model override")
    sub.add_argument("--no-approval", action="store_true", help="Skip approval for actions")
    sub.set_defaults(func=cmd_discord)

    # webhook
    sub = subparsers.add_parser("webhook", help="Run generic webhook server")
    sub.add_argument("--host", default="0.0.0.0", help="Bind host")
    sub.add_argument("--port", "-p", type=int, default=8199, help="Port (default: 8199)")
    sub.add_argument("--model", default="", help="LLM model override")
    sub.add_argument("--no-approval", action="store_true", help="Skip approval for actions")
    sub.set_defaults(func=cmd_webhook)

    # whatsapp
    sub = subparsers.add_parser("whatsapp", help="Run WhatsApp bot (webhook mode)")
    sub.add_argument("--host", default="0.0.0.0", help="Bind host")
    sub.add_argument("--port", "-p", type=int, default=8199, help="Port (default: 8199)")
    sub.add_argument("--model", default="", help="LLM model override")
    sub.add_argument("--no-approval", action="store_true", help="Skip approval for actions")
    sub.set_defaults(func=cmd_whatsapp)

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
