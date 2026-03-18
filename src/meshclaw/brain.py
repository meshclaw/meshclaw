"""MeshClaw Brain - Autonomous agent loop with task decomposition.

This is what makes MeshClaw equivalent to (and beyond) OpenClaw.
OpenClaw runs one brain on one machine.
MeshClaw runs many brains across a mesh — and they coordinate.

The loop:
  1. Receive goal (natural language)
  2. Ask LLM to decompose into steps + tool calls
  3. Execute tools (locally, on remote servers, or via MCP)
  4. Feed results back to LLM
  5. Repeat until goal is achieved or max steps reached

Supports:
  - Any LLM backend (OpenAI, Anthropic, local/ollama)
  - Distributed execution via MeshClaw orchestrator
  - Memory persistence via MeshDB
  - Tool plugins (messenger, email, calendar, shell, etc.)
"""

import json
import time
import os
import subprocess
import sys
from typing import Optional, Callable
from dataclasses import dataclass, field


# ---- LLM Backend ----

@dataclass
class LLMConfig:
    """LLM backend configuration."""
    provider: str = "ollama"          # ollama (default), openai, anthropic, custom
    model: str = "qwen2.5:14b"       # model name
    api_key: str = ""                 # API key (or env var name)
    base_url: str = ""                # custom endpoint (for ollama, vllm, etc.)
    temperature: float = 0.2
    max_tokens: int = 4096

    @classmethod
    def from_env(cls) -> "LLMConfig":
        """Auto-detect LLM config from environment.

        Priority: MESHCLAW_ENGINE env > local Ollama > Anthropic > OpenAI

        Env vars:
          MESHCLAW_ENGINE  = ollama|anthropic|openai  (explicit override)
          MESHCLAW_MODEL   = model name override
          OLLAMA_BASE_URL  = http://host:11434 (remote ollama)
          ANTHROPIC_API_KEY, OPENAI_API_KEY
        """
        explicit = os.environ.get("MESHCLAW_ENGINE", "").lower()
        model_override = os.environ.get("MESHCLAW_MODEL", "")
        ollama_url = os.environ.get("OLLAMA_BASE_URL", "http://localhost:11434")

        if explicit == "anthropic" or (not explicit and os.environ.get("MESHCLAW_ENGINE") == "anthropic"):
            api_key = os.environ.get("ANTHROPIC_API_KEY", "")
            return cls(provider="anthropic",
                       model=model_override or "claude-sonnet-4-20250514",
                       api_key=api_key)
        elif explicit == "openai":
            api_key = os.environ.get("OPENAI_API_KEY", "")
            return cls(provider="openai",
                       model=model_override or "gpt-4o-mini",
                       api_key=api_key)
        elif _ollama_available(base_url=ollama_url):
            # Default: local Ollama (free, no API key)
            return cls(provider="ollama",
                       model=model_override or os.environ.get("OLLAMA_MODEL", "qwen2.5:14b"),
                       base_url=ollama_url)
        elif os.environ.get("ANTHROPIC_API_KEY"):
            return cls(provider="anthropic",
                       model=model_override or "claude-sonnet-4-20250514",
                       api_key=os.environ["ANTHROPIC_API_KEY"])
        elif os.environ.get("OPENAI_API_KEY"):
            return cls(provider="openai",
                       model=model_override or "gpt-4o-mini",
                       api_key=os.environ["OPENAI_API_KEY"])
        else:
            raise RuntimeError(
                "No LLM backend found.\n"
                "  Option 1 (free): Install Ollama -> https://ollama.com\n"
                "  Option 2: Set ANTHROPIC_API_KEY or OPENAI_API_KEY\n"
                "  Option 3: Set MESHCLAW_ENGINE=ollama with OLLAMA_BASE_URL"
            )


def _ollama_available(base_url: str = "http://localhost:11434") -> bool:
    """Check if ollama is running locally."""
    try:
        import urllib.request
        r = urllib.request.urlopen("http://localhost:11434/api/tags", timeout=2)
        return r.status == 200
    except Exception:
        return False


def llm_call(config: LLMConfig, messages: list, tools: list = None) -> dict:
    """Call LLM with messages and optional tool definitions.

    Returns: {"content": str, "tool_calls": [{"name": str, "arguments": dict}]}
    """
    if config.provider == "openai":
        return _call_openai(config, messages, tools)
    elif config.provider == "anthropic":
        return _call_anthropic(config, messages, tools)
    elif config.provider == "ollama":
        return _call_ollama(config, messages, tools)
    elif config.provider == "custom":
        return _call_openai(config, messages, tools)  # OpenAI-compatible
    else:
        raise ValueError(f"Unknown LLM provider: {config.provider}")


def _call_openai(config: LLMConfig, messages: list, tools: list = None) -> dict:
    """OpenAI / OpenAI-compatible API call."""
    import urllib.request
    url = config.base_url or "https://api.openai.com/v1/chat/completions"
    if not url.endswith("/chat/completions"):
        url = url.rstrip("/") + "/v1/chat/completions"

    body = {
        "model": config.model,
        "messages": messages,
        "temperature": config.temperature,
        "max_tokens": config.max_tokens,
    }
    if tools:
        body["tools"] = [{"type": "function", "function": t} for t in tools]
        body["tool_choice"] = "auto"

    data = json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, headers={
        "Content-Type": "application/json",
        "Authorization": f"Bearer {config.api_key}",
    })

    with urllib.request.urlopen(req, timeout=120) as resp:
        result = json.loads(resp.read())

    msg = result["choices"][0]["message"]
    tool_calls = []
    if msg.get("tool_calls"):
        for tc in msg["tool_calls"]:
            tool_calls.append({
                "id": tc["id"],
                "name": tc["function"]["name"],
                "arguments": json.loads(tc["function"]["arguments"]),
            })

    return {"content": msg.get("content", ""), "tool_calls": tool_calls}


def _call_anthropic(config: LLMConfig, messages: list, tools: list = None) -> dict:
    """Anthropic Claude API call."""
    import urllib.request

    # Convert OpenAI-style messages to Anthropic format
    system = ""
    anthropic_msgs = []
    for m in messages:
        if m["role"] == "system":
            system = m["content"]
        elif m["role"] == "tool":
            anthropic_msgs.append({
                "role": "user",
                "content": [{"type": "tool_result",
                             "tool_use_id": m.get("tool_call_id", ""),
                             "content": m["content"]}]
            })
        else:
            anthropic_msgs.append({"role": m["role"], "content": m["content"]})

    body = {
        "model": config.model,
        "max_tokens": config.max_tokens,
        "messages": anthropic_msgs,
    }
    if system:
        body["system"] = system
    if tools:
        body["tools"] = [{"name": t["name"], "description": t.get("description", ""),
                          "input_schema": t.get("parameters", {})} for t in tools]

    data = json.dumps(body).encode()
    req = urllib.request.Request(
        "https://api.anthropic.com/v1/messages", data=data, headers={
            "Content-Type": "application/json",
            "x-api-key": config.api_key,
            "anthropic-version": "2023-06-01",
        })

    with urllib.request.urlopen(req, timeout=120) as resp:
        result = json.loads(resp.read())

    content = ""
    tool_calls = []
    for block in result.get("content", []):
        if block["type"] == "text":
            content += block["text"]
        elif block["type"] == "tool_use":
            tool_calls.append({
                "id": block["id"],
                "name": block["name"],
                "arguments": block["input"],
            })

    return {"content": content, "tool_calls": tool_calls}


def _call_ollama(config: LLMConfig, messages: list, tools: list = None) -> dict:
    """Ollama local API call."""
    import urllib.request
    url = f"{config.base_url}/api/chat"

    body = {
        "model": config.model,
        "messages": messages,
        "stream": False,
        "options": {"temperature": config.temperature},
    }
    if tools:
        body["tools"] = [{"type": "function", "function": t} for t in tools]

    data = json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, headers={"Content-Type": "application/json"})

    with urllib.request.urlopen(req, timeout=300) as resp:
        result = json.loads(resp.read())

    msg = result.get("message", {})
    tool_calls = []
    if msg.get("tool_calls"):
        for tc in msg["tool_calls"]:
            tool_calls.append({
                "id": tc.get("id", f"call_{int(time.time())}"),
                "name": tc["function"]["name"],
                "arguments": tc["function"]["arguments"]
                    if isinstance(tc["function"]["arguments"], dict)
                    else json.loads(tc["function"]["arguments"]),
            })

    return {"content": msg.get("content", ""), "tool_calls": tool_calls}


# ---- Tools Registry ----

# Built-in tools that every MeshClaw brain has access to
BUILTIN_TOOLS = [
    {
        "name": "shell",
        "description": "Execute a shell command on a specific server or locally. "
                       "Use for system tasks: check status, install packages, restart services, etc.",
        "parameters": {
            "type": "object",
            "properties": {
                "command": {"type": "string", "description": "Shell command to execute"},
                "server": {"type": "string",
                           "description": "Target server name (empty = local)"},
            },
            "required": ["command"],
        }
    },
    {
        "name": "read_file",
        "description": "Read contents of a file on a server or locally.",
        "parameters": {
            "type": "object",
            "properties": {
                "path": {"type": "string", "description": "File path to read"},
                "server": {"type": "string", "description": "Server name (empty = local)"},
            },
            "required": ["path"],
        }
    },
    {
        "name": "write_file",
        "description": "Write content to a file on a server or locally.",
        "parameters": {
            "type": "object",
            "properties": {
                "path": {"type": "string", "description": "File path to write"},
                "content": {"type": "string", "description": "File content"},
                "server": {"type": "string", "description": "Server name (empty = local)"},
            },
            "required": ["path", "content"],
        }
    },
    {
        "name": "memory_store",
        "description": "Store information in persistent memory (survives across sessions). "
                       "Use for saving important context, decisions, preferences.",
        "parameters": {
            "type": "object",
            "properties": {
                "key": {"type": "string", "description": "Memory key"},
                "value": {"type": "string", "description": "Value to remember"},
            },
            "required": ["key", "value"],
        }
    },
    {
        "name": "memory_recall",
        "description": "Recall information from persistent memory.",
        "parameters": {
            "type": "object",
            "properties": {
                "key": {"type": "string", "description": "Memory key (or 'all' for everything)"},
            },
            "required": ["key"],
        }
    },
    {
        "name": "broadcast",
        "description": "Run same command on ALL servers in the mesh simultaneously.",
        "parameters": {
            "type": "object",
            "properties": {
                "command": {"type": "string", "description": "Command to broadcast"},
            },
            "required": ["command"],
        }
    },
    {
        "name": "think",
        "description": "Think step by step about a problem before acting. "
                       "Use when you need to plan, reason, or make a decision. "
                       "Output your reasoning — no action is taken.",
        "parameters": {
            "type": "object",
            "properties": {
                "thought": {"type": "string", "description": "Your reasoning / plan"},
            },
            "required": ["thought"],
        }
    },
    {
        "name": "done",
        "description": "Signal that the goal has been achieved. Include a summary of what was done.",
        "parameters": {
            "type": "object",
            "properties": {
                "summary": {"type": "string", "description": "Summary of what was accomplished"},
            },
            "required": ["summary"],
        }
    },
]


# ---- Tool Execution ----

class ToolExecutor:
    """Executes tool calls. Extensible with plugins."""

    def __init__(self, orchestrator=None):
        self.orchestrator = orchestrator
        self.memory = {}   # In-memory store, can be backed by MeshDB
        self.plugins = {}  # name -> callable

        # Try to load MeshDB for persistent memory
        self._meshdb = None
        try:
            from meshclaw.orchestrator import Orchestrator
            if orchestrator:
                self._load_meshdb_memory()
        except Exception:
            pass

    def register_plugin(self, name: str, description: str, parameters: dict,
                        handler: Callable):
        """Register a custom tool plugin."""
        self.plugins[name] = {
            "definition": {
                "name": name,
                "description": description,
                "parameters": parameters,
            },
            "handler": handler,
        }

    def get_all_tools(self) -> list:
        """Get all available tool definitions (builtin + plugins)."""
        tools = list(BUILTIN_TOOLS)
        for p in self.plugins.values():
            tools.append(p["definition"])
        return tools

    def execute(self, name: str, arguments: dict) -> str:
        """Execute a tool call, return result as string."""

        # Check plugins first
        if name in self.plugins:
            try:
                result = self.plugins[name]["handler"](**arguments)
                return str(result)
            except Exception as e:
                return f"Plugin error: {e}"

        # Built-in tools
        if name == "shell":
            return self._exec_shell(arguments)
        elif name == "read_file":
            return self._exec_read_file(arguments)
        elif name == "write_file":
            return self._exec_write_file(arguments)
        elif name == "memory_store":
            return self._exec_memory_store(arguments)
        elif name == "memory_recall":
            return self._exec_memory_recall(arguments)
        elif name == "broadcast":
            return self._exec_broadcast(arguments)
        elif name == "think":
            return f"[Thought recorded: {arguments.get('thought', '')}]"
        elif name == "done":
            return f"[DONE: {arguments.get('summary', '')}]"
        else:
            return f"Unknown tool: {name}"

    def _exec_shell(self, args: dict) -> str:
        server = args.get("server", "")
        command = args["command"]

        if server and self.orchestrator:
            # Distributed execution via MeshClaw
            try:
                result = self.orchestrator.exec(command, server=server)
                if isinstance(result, dict):
                    return result.get("stdout", "") or result.get("output", str(result))
                return str(result)
            except Exception as e:
                return f"Remote exec error on {server}: {e}"
        else:
            # Local execution
            try:
                proc = subprocess.run(
                    command, shell=True, capture_output=True, text=True, timeout=60
                )
                output = proc.stdout
                if proc.returncode != 0 and proc.stderr:
                    output += f"\n[stderr]: {proc.stderr}"
                return output or "(no output)"
            except subprocess.TimeoutExpired:
                return "[Error: command timed out after 60s]"
            except Exception as e:
                return f"[Error: {e}]"

    def _exec_read_file(self, args: dict) -> str:
        server = args.get("server", "")
        path = args["path"]

        if server and self.orchestrator:
            result = self.orchestrator.exec(f"cat {path}", server=server)
            if isinstance(result, dict):
                return result.get("stdout", str(result))
            return str(result)
        else:
            try:
                with open(path, "r") as f:
                    return f.read()
            except Exception as e:
                return f"[Error reading {path}: {e}]"

    def _exec_write_file(self, args: dict) -> str:
        server = args.get("server", "")
        path = args["path"]
        content = args["content"]

        if server and self.orchestrator:
            # Escape content for remote write
            escaped = content.replace("'", "'\\''")
            result = self.orchestrator.exec(
                f"cat > {path} << 'MESHCLAW_EOF'\n{content}\nMESHCLAW_EOF",
                server=server
            )
            return f"Written to {server}:{path}"
        else:
            try:
                with open(path, "w") as f:
                    f.write(content)
                return f"Written to {path}"
            except Exception as e:
                return f"[Error writing {path}: {e}]"

    def _exec_memory_store(self, args: dict) -> str:
        key = args["key"]
        value = args["value"]
        self.memory[key] = value

        # Persist to MeshDB if available
        if self._meshdb:
            try:
                self._meshdb_store(key, value)
            except Exception:
                pass

        return f"Stored: {key}"

    def _exec_memory_recall(self, args: dict) -> str:
        key = args["key"]
        if key == "all":
            if not self.memory:
                return "(no memories stored)"
            return json.dumps(self.memory, indent=2, ensure_ascii=False)
        value = self.memory.get(key)
        if value is None:
            return f"(no memory for key '{key}')"
        return value

    def _exec_broadcast(self, args: dict) -> str:
        command = args["command"]
        if self.orchestrator:
            try:
                result = self.orchestrator.broadcast("brain-broadcast", command)
                # Format results
                lines = []
                for r in result.results:
                    status = "OK" if r.success else "FAIL"
                    output = (r.output or "").strip()[:200]
                    lines.append(f"[{r.server}] {status}: {output}")
                return "\n".join(lines)
            except Exception as e:
                return f"Broadcast error: {e}"
        return "[No orchestrator — broadcast requires mesh connection]"

    def _load_meshdb_memory(self):
        """Load persistent memory from MeshDB."""
        try:
            # Try to import and use meshdb
            proc = subprocess.run(
                ["meshdb", "find", "meshclaw_memory"],
                capture_output=True, text=True, timeout=5
            )
            if proc.returncode == 0 and proc.stdout.strip():
                data = json.loads(proc.stdout)
                if isinstance(data, dict):
                    self.memory.update(data)
                    self._meshdb = True
        except Exception:
            pass

    def _meshdb_store(self, key: str, value: str):
        """Persist a memory entry to MeshDB."""
        try:
            subprocess.run(
                ["meshdb", "read", "meshclaw_memory", "--set",
                 json.dumps({key: value})],
                capture_output=True, timeout=5
            )
        except Exception:
            pass


# ---- Brain (Agent Loop) ----

@dataclass
class BrainResult:
    """Result of a brain run."""
    goal: str
    steps: int
    success: bool
    summary: str
    history: list = field(default_factory=list)
    duration: float = 0.0


SYSTEM_PROMPT = """You are MeshClaw Brain — a distributed AI agent that runs across a mesh network of servers.

You can:
- Execute shell commands on any server in the mesh (or locally)
- Read and write files across servers
- Store and recall persistent memory
- Broadcast commands to all servers simultaneously
- Think step by step before acting

You are NOT a single-machine agent. You have access to multiple servers connected via encrypted VPN mesh. Use this power — distribute work, check multiple servers, coordinate tasks.

Rules:
- Think before acting on complex tasks
- Use 'shell' with a server name for remote execution
- Use 'broadcast' to check all servers at once
- Store important findings in memory for future sessions
- Call 'done' when the goal is achieved
- Be concise in your responses
- If a tool fails, try a different approach
- Never run dangerous commands without thinking first
"""


class Brain:
    """The MeshClaw Brain — autonomous agent loop.

    Usage:
        brain = Brain()
        result = brain.run("Check disk space on all servers and alert if any > 80%")
    """

    # Tools that require approval before execution
    DANGEROUS_TOOLS = {"shell", "write_file", "broadcast"}

    def __init__(self, llm_config: LLMConfig = None, orchestrator=None,
                 max_steps: int = 20, verbose: bool = True,
                 approval_mode: bool = False):
        self.llm_config = llm_config or LLMConfig.from_env()
        self.executor = ToolExecutor(orchestrator=orchestrator)
        self.max_steps = max_steps
        self.verbose = verbose
        self.approval_mode = approval_mode
        self.on_step = None  # Optional callback: (step_num, action, result) -> None
        self.approval_callback = None  # (step, tool_name, args) -> bool

    def register_tool(self, name: str, description: str, parameters: dict,
                      handler: Callable):
        """Register a custom tool for the brain to use."""
        self.executor.register_plugin(name, description, parameters, handler)

    def run(self, goal: str, context: str = "") -> BrainResult:
        """Run the agent loop until goal is achieved or max steps reached."""
        start = time.time()
        tools = self.executor.get_all_tools()

        messages = [
            {"role": "system", "content": SYSTEM_PROMPT},
            {"role": "user", "content": self._format_goal(goal, context)},
        ]

        history = []
        result = BrainResult(goal=goal, steps=0, success=False, summary="")

        for step in range(1, self.max_steps + 1):
            result.steps = step

            if self.verbose:
                print(f"\n--- Step {step}/{self.max_steps} ---")

            # Ask LLM
            try:
                response = llm_call(self.llm_config, messages, tools)
            except Exception as e:
                if self.verbose:
                    print(f"[LLM Error]: {e}")
                result.summary = f"LLM call failed: {e}"
                break

            # Process response text
            if response["content"]:
                if self.verbose:
                    print(f"[Brain]: {response['content'][:500]}")

            # No tool calls = brain is done talking
            if not response["tool_calls"]:
                if response["content"]:
                    messages.append({"role": "assistant", "content": response["content"]})
                    result.summary = response["content"]
                    result.success = True
                break

            # Process tool calls — add assistant message
            if self.llm_config.provider == "ollama":
                # Ollama: pass tool_calls in native format (no "type" wrapper)
                messages.append({
                    "role": "assistant",
                    "content": response.get("content") or "",
                    "tool_calls": [{
                        "id": tc["id"],
                        "function": {
                            "name": tc["name"],
                            "arguments": tc["arguments"],
                        }
                    } for tc in response["tool_calls"]]
                })
            elif self.llm_config.provider in ("openai", "custom"):
                # OpenAI format: with "type": "function" wrapper
                messages.append({
                    "role": "assistant",
                    "content": response.get("content") or "",
                    "tool_calls": [{
                        "id": tc["id"],
                        "type": "function",
                        "function": {
                            "name": tc["name"],
                            "arguments": json.dumps(tc["arguments"]),
                        }
                    } for tc in response["tool_calls"]]
                })
            elif self.llm_config.provider == "anthropic":
                # Anthropic: assistant message with tool_use blocks
                content_blocks = []
                if response["content"]:
                    content_blocks.append({"type": "text", "text": response["content"]})
                for tc in response["tool_calls"]:
                    content_blocks.append({
                        "type": "tool_use",
                        "id": tc["id"],
                        "name": tc["name"],
                        "input": tc["arguments"],
                    })
                messages.append({"role": "assistant", "content": content_blocks})

            for tc in response["tool_calls"]:
                tool_name = tc["name"]
                tool_args = tc["arguments"]

                if self.verbose:
                    args_str = json.dumps(tool_args, ensure_ascii=False)[:200]
                    print(f"[Tool]: {tool_name}({args_str})")

                # Check for 'done'
                if tool_name == "done":
                    result.success = True
                    result.summary = tool_args.get("summary", "Goal completed")
                    history.append({
                        "step": step,
                        "tool": tool_name,
                        "args": tool_args,
                        "result": result.summary,
                    })
                    if self.on_step:
                        self.on_step(step, f"done", result.summary)
                    result.history = history
                    result.duration = time.time() - start
                    if self.verbose:
                        print(f"\n=== DONE in {step} steps ({result.duration:.1f}s) ===")
                        print(f"Summary: {result.summary}")
                    return result

                # Approval check for dangerous tools
                if self.approval_mode and tool_name in self.DANGEROUS_TOOLS:
                    if self.approval_callback:
                        approved = self.approval_callback(step, tool_name, tool_args)
                        if not approved:
                            tool_result = f"[Skipped: user rejected {tool_name}]"
                            if self.verbose:
                                print(f"[Rejected]: {tool_name}")
                            history.append({
                                "step": step,
                                "tool": tool_name,
                                "args": tool_args,
                                "result": tool_result,
                            })
                            if self.llm_config.provider in ("openai", "ollama", "custom"):
                                messages.append({
                                    "role": "tool",
                                    "tool_call_id": tc["id"],
                                    "content": tool_result,
                                })
                            else:
                                messages.append({
                                    "role": "tool",
                                    "tool_call_id": tc.get("id", ""),
                                    "content": tool_result,
                                })
                            continue
                    elif self.verbose:
                        # CLI approval (interactive)
                        args_str = json.dumps(tool_args, ensure_ascii=False)[:200]
                        answer = input(
                            f"⚠️  Approve {tool_name}({args_str})? [y/N]: "
                        ).strip().lower()
                        if answer not in ("y", "yes", "ㅇ", "ㅇㅇ"):
                            tool_result = f"[Skipped: user rejected {tool_name}]"
                            history.append({
                                "step": step,
                                "tool": tool_name,
                                "args": tool_args,
                                "result": tool_result,
                            })
                            if self.llm_config.provider in ("openai", "ollama", "custom"):
                                messages.append({
                                    "role": "tool",
                                    "tool_call_id": tc["id"],
                                    "content": tool_result,
                                })
                            else:
                                messages.append({
                                    "role": "tool",
                                    "tool_call_id": tc.get("id", ""),
                                    "content": tool_result,
                                })
                            continue

                # Execute tool
                tool_result = self.executor.execute(tool_name, tool_args)

                if self.verbose:
                    print(f"[Result]: {tool_result[:300]}")

                history.append({
                    "step": step,
                    "tool": tool_name,
                    "args": tool_args,
                    "result": tool_result[:1000],
                })

                if self.on_step:
                    self.on_step(step, f"{tool_name}({tool_args})", tool_result)

                # Add tool result to messages
                if self.llm_config.provider in ("openai", "ollama", "custom"):
                    messages.append({
                        "role": "tool",
                        "tool_call_id": tc["id"],
                        "content": tool_result[:4000],
                    })
                else:
                    # Anthropic format
                    messages.append({
                        "role": "tool",
                        "tool_call_id": tc.get("id", ""),
                        "content": tool_result[:4000],
                    })

        # Max steps reached
        if not result.success:
            result.summary = f"Reached max steps ({self.max_steps}) without completing goal"

        result.history = history
        result.duration = time.time() - start

        if self.verbose:
            status = "COMPLETE" if result.success else "INCOMPLETE"
            print(f"\n=== {status} in {result.steps} steps ({result.duration:.1f}s) ===")
            print(f"Summary: {result.summary}")

        return result

    def _format_goal(self, goal: str, context: str = "") -> str:
        """Format the goal with optional context."""
        parts = [f"Goal: {goal}"]

        if context:
            parts.append(f"\nContext: {context}")

        # Add memory context if available
        if self.executor.memory:
            mem_str = json.dumps(self.executor.memory, ensure_ascii=False, indent=2)
            if len(mem_str) < 2000:
                parts.append(f"\nPersistent memory:\n{mem_str}")

        # Add server info if orchestrator is available
        if self.executor.orchestrator:
            try:
                servers = self.executor.orchestrator.discover()
                if servers:
                    parts.append(f"\nAvailable servers: {', '.join(servers)}")
            except Exception:
                pass

        parts.append("\nProceed step by step. Use tools to accomplish this goal.")
        return "\n".join(parts)


# ---- Convenience functions ----

def run(goal: str, **kwargs) -> BrainResult:
    """Quick-run: create brain and execute goal.

    Usage:
        from meshclaw.brain import run
        result = run("Check all servers are healthy")
    """
    brain = Brain(**kwargs)
    return brain.run(goal)


def chat(verbose: bool = True):
    """Interactive chat loop with the brain.

    Usage:
        from meshclaw.brain import chat
        chat()
    """
    brain = Brain(verbose=verbose)
    print("MeshClaw Brain — interactive mode")
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


if __name__ == "__main__":
    if len(sys.argv) > 1:
        goal = " ".join(sys.argv[1:])
        run(goal)
    else:
        chat()
