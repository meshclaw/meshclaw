"""MeshClaw - Distributed AI Agent Orchestration across Mesh Networks.

Unlike single-machine agent frameworks, MeshClaw distributes AI agents
across multiple servers in a mesh network, enabling parallel execution,
sequential pipelines, and collaborative multi-agent workflows.

Built on top of MeshPOP infrastructure (mpop/vssh/wire/meshdb/vault).
"""

__version__ = "0.3.0"

from meshclaw.agent import Agent, AgentState
from meshclaw.task import Task, TaskResult, TaskState
from meshclaw.orchestrator import Orchestrator
from meshclaw.scenario import Scenario, ParallelScenario, SequentialScenario, CollaborativeScenario
from meshclaw.brain import Brain, BrainResult, LLMConfig

__all__ = [
    "Agent", "AgentState",
    "Task", "TaskResult", "TaskState",
    "Orchestrator",
    "Scenario", "ParallelScenario", "SequentialScenario", "CollaborativeScenario",
    "Brain", "BrainResult", "LLMConfig",
]
