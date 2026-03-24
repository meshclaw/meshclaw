"""
meshclaw Template System
=======================

YAML-based assistant definition — one file describes a complete AI assistant.
Users define their assistant in template.yaml, then:

    meshclaw build --template email        # built-in
    meshclaw build --config my_bot.yaml   # custom

The template is baked into a self-contained image that boots directly into
the assistant loop, auto-connects to the Wire mesh, and responds to mpop messages.
"""

import os
import re
import json
from dataclasses import dataclass, field
from typing import List, Optional, Dict, Any

try:
    import yaml
    _HAS_YAML = True
except ImportError:
    _HAS_YAML = False

__all__ = ["AssistantTemplate", "load", "list_builtin", "fetch_registry"]

# Where built-in templates live inside the package
_BUILTIN_DIR = os.path.normpath(
    os.path.join(os.path.dirname(__file__), "..", "..", "templates")
)

REGISTRY_URL = (
    "https://raw.githubusercontent.com/meshpop/meshclaw-templates/main/registry.json"
)

VALID_MODELS = [
    "claude-opus-4-6",
    "claude-sonnet-4-6",
    "claude-haiku-4-5",
    "gpt-4o",
    "gpt-4o-mini",
    "ollama/llama3",
    "ollama/mistral",
]

INTERVAL_RE = re.compile(r"(\d+)\s*([mhd])", re.IGNORECASE)


@dataclass
class AssistantTemplate:
    """Parsed and validated assistant template."""

    # Identity
    name: str
    description: str = ""
    version: str = "1.0.0"
    author: str = ""

    # LLM
    model: str = "claude-sonnet-4-6"
    system_prompt: str = "You are a helpful assistant."
    max_tokens: int = 2048

    # Capabilities
    tools: List[str] = field(default_factory=list)
    mcp_servers: List[str] = field(default_factory=list)
    packages: List[str] = field(default_factory=list)  # extra pip packages

    # Behavior
    schedule: Optional[str] = None        # "every 30m"
    schedule_task: Optional[str] = None   # LLM prompt to run on schedule
    schedule_script: Optional[str] = None # raw bash command — no LLM call
    on_message: str = "respond helpfully" # instruction for mpop messages

    # Notifications (optional) — send results after schedule runs
    # notify:
    #   platform: telegram          # telegram | slack | discord | webhook
    #   token: "..."                # telegram bot token (or use env var)
    #   chat_id: "..."              # telegram chat_id
    #   webhook_url: "..."          # slack / discord webhook URL
    notify: Optional[Dict[str, str]] = None

    # Runtime
    env: List[str] = field(default_factory=list)  # required env var names
    files: Dict[str, str] = field(default_factory=dict)  # dest → local src

    # Raw parsed data
    raw: Dict[str, Any] = field(default_factory=dict)

    def interval_seconds(self) -> int:
        """Parse schedule string → seconds. Returns 0 if not set."""
        if not self.schedule:
            return 0
        m = INTERVAL_RE.search(self.schedule)
        if not m:
            return 0
        n, unit = int(m.group(1)), m.group(2).lower()
        return n * {"m": 60, "h": 3600, "d": 86400}[unit]

    def to_json(self) -> str:
        """Serialize to JSON for embedding in built image."""
        return json.dumps({
            "name": self.name,
            "description": self.description,
            "version": self.version,
            "author": self.author,
            "model": self.model,
            "system_prompt": self.system_prompt,
            "max_tokens": self.max_tokens,
            "tools": self.tools,
            "mcp_servers": self.mcp_servers,
            "packages": self.packages,
            "schedule": self.schedule,
            "schedule_task": self.schedule_task,
            "schedule_script": self.schedule_script,
            "on_message": self.on_message,
            "notify": self.notify,
            "env": self.env,
        }, indent=2)


def load(path: str) -> "AssistantTemplate":
    """Load and validate a template.yaml (or template.json) file."""
    path = os.path.expanduser(path)

    with open(path) as f:
        raw_text = f.read()

    if path.endswith(".json"):
        data = json.loads(raw_text)
    elif _HAS_YAML:
        data = yaml.safe_load(raw_text)
    else:
        raise RuntimeError(
            "PyYAML not installed. Run: pip install pyyaml"
        )

    if not data or not data.get("name"):
        raise ValueError(f"template must have a 'name' field: {path}")

    # system_prompt can reference a file
    sp = data.get("system_prompt", "You are a helpful assistant.")
    if isinstance(sp, str) and sp.startswith("file:"):
        sp_path = os.path.join(os.path.dirname(path), sp[5:].strip())
        with open(sp_path) as f:
            sp = f.read().strip()

    return AssistantTemplate(
        name=data["name"],
        description=data.get("description", ""),
        version=str(data.get("version", "1.0.0")),
        author=data.get("author", ""),
        model=data.get("model", "claude-sonnet-4-6"),
        system_prompt=sp,
        max_tokens=int(data.get("max_tokens", 2048)),
        tools=data.get("tools", []),
        mcp_servers=data.get("mcp_servers", []),
        packages=data.get("packages", []),
        schedule=data.get("schedule"),
        schedule_task=data.get("schedule_task"),
        schedule_script=data.get("schedule_script"),
        on_message=data.get("on_message", "respond helpfully"),
        notify=data.get("notify"),
        env=data.get("env", []),
        files=data.get("files", {}),
        raw=data,
    )


def list_builtin() -> List[Dict[str, str]]:
    """List built-in templates shipped with meshclaw."""
    result = []
    if not os.path.isdir(_BUILTIN_DIR):
        return result
    for name in sorted(os.listdir(_BUILTIN_DIR)):
        yaml_path = os.path.join(_BUILTIN_DIR, name, "template.yaml")
        if not os.path.exists(yaml_path):
            continue
        try:
            t = load(yaml_path)
            result.append({
                "name": t.name,
                "description": t.description,
                "version": t.version,
                "path": yaml_path,
                "source": "builtin",
            })
        except Exception:
            pass
    return result


def fetch_registry() -> List[Dict[str, Any]]:
    """Fetch community template registry from GitHub."""
    try:
        import urllib.request
        with urllib.request.urlopen(REGISTRY_URL, timeout=10) as r:
            return json.loads(r.read())
    except Exception:
        return []


def find_builtin(name: str) -> Optional[str]:
    """Return path to built-in template.yaml by name, or None."""
    # Direct name match
    direct = os.path.join(_BUILTIN_DIR, name, "template.yaml")
    if os.path.exists(direct):
        return direct
    # Fuzzy search
    if os.path.isdir(_BUILTIN_DIR):
        for entry in os.listdir(_BUILTIN_DIR):
            if name.lower() in entry.lower():
                p = os.path.join(_BUILTIN_DIR, entry, "template.yaml")
                if os.path.exists(p):
                    return p
    return None
