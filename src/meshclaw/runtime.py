"""
meshclaw Runtime — Full mesh node + AI assistant loop.

Runs INSIDE the Docker container on startup:
  1. Register as a Wire peer → get own VPN IP (10.99.x.x)
  2. Start vssh server       → respond to remote commands
  3. Start AI assistant loop → answer mpop worker ask messages
  4. Start scheduled tasks   → run background jobs on interval

Required environment variables:
  WIRE_RELAY_URL    Wire relay URL   e.g. http://78.141.x.x:8787
  NODE_NAME         Worker name      e.g. "test-worker"
  VSSH_SECRET       vssh shared secret
  ANTHROPIC_API_KEY Anthropic API key (for Claude models)

Optional:
  WIRE_LISTEN_PORT  WireGuard listen port (default: 51820)
  RTLINUX_CONFIG    Template config path (default: /opt/meshclaw/template.json)
  RTLINUX_LOG       Log file path       (default: /opt/meshclaw/assistant.log)
"""

import json
import os
import sys
import time
import socket
import threading
import subprocess
import re
import hashlib
from pathlib import Path

CONFIG_PATH  = os.environ.get("RTLINUX_CONFIG", "/opt/meshclaw/template.json")
LOG_PATH     = os.environ.get("RTLINUX_LOG",    "/opt/meshclaw/assistant.log")
WORK_DIR     = os.path.dirname(LOG_PATH)   # e.g. /opt/meshclaw or ~/.meshclaw/mac-assistant
SOCK_PATH    = "/tmp/meshclaw-{name}.sock"

_INTERVAL_RE = re.compile(r"(\d+)\s*([mhd])", re.IGNORECASE)

# ── Logging ──────────────────────────────────────────────────────────

def log(msg: str):
    ts = time.strftime("%Y-%m-%d %H:%M:%S")
    line = f"[{ts}] {msg}"
    print(line, flush=True)
    try:
        os.makedirs(os.path.dirname(LOG_PATH), exist_ok=True)
        with open(LOG_PATH, "a") as f:
            f.write(line + "\n")
    except Exception:
        pass


# ── LLM call ─────────────────────────────────────────────────────────

# Patterns that require user confirmation before running.
# Returns a NEEDS_CONFIRM sentinel instead of executing.
_DANGEROUS_PATTERNS = [
    r"\brm\s+-[rf]",          # rm -rf, rm -r, rm -f
    r"\brm\b.*\*",            # rm with glob
    r"\bdd\b",                # dd (disk write)
    r"\bmkfs\b",              # format filesystem
    r"\bfdisk\b",             # partition editor
    r"\bdiskutil\s+erase",    # macOS disk erase
    r"\bformat\b",            # format
    r"\bshred\b",             # secure delete
    r">\s*/dev/",             # redirect to device
    r"\bpkill\b|\bkillall\b", # kill processes by name
    r"\blaunchctl\s+remove",  # remove launchd service
    r"\bcrontab\s+-r",        # delete crontab
    r"\bchmod\s+777",         # world-writable
    r"\bsudo\s+rm",           # sudo rm
]

import re as _re

def _run_bash_tool(command: str, timeout: int = 30) -> str:
    """Execute a bash command and return its output (stdout + stderr).

    Dangerous commands return a NEEDS_CONFIRM sentinel instead of executing.
    The caller (tool loop) surfaces this as a confirmation request to the user.
    """
    # Safety check: block dangerous patterns
    cmd_lower = command.strip()
    for pattern in _DANGEROUS_PATTERNS:
        if _re.search(pattern, cmd_lower):
            return (
                f"NEEDS_CONFIRM: This command may be destructive:\n"
                f"  {command}\n"
                f"Tell the user what this command will do and ask them to "
                f"confirm before running it."
            )

    try:
        result = subprocess.run(
            command,
            shell=True,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        output = ""
        if result.stdout:
            output += result.stdout
        if result.stderr:
            output += result.stderr
        if result.returncode != 0 and not output:
            output = f"(exit code {result.returncode})"
        return output.strip() or "(no output)"
    except subprocess.TimeoutExpired:
        return f"(timeout after {timeout}s)"
    except Exception as e:
        return f"(error: {e})"


# Tool definition sent to Claude API
_BASH_TOOL = {
    "name": "bash",
    "description": (
        "Run a bash/shell command on this machine and get the output. "
        "Use this whenever you need to check system state, run programs, "
        "read files, or execute anything. Always prefer running commands "
        "over just describing them."
    ),
    "input_schema": {
        "type": "object",
        "properties": {
            "command": {
                "type": "string",
                "description": "The shell command to execute",
            },
            "timeout": {
                "type": "integer",
                "description": "Timeout in seconds (default 30)",
                "default": 30,
            },
        },
        "required": ["command"],
    },
}


def _collect_mac_context() -> str:
    """Pre-collect real Mac system data to inject into prompts."""
    cmds = {
        "disk": "df -h | grep -E '^/dev/disk'",
        "memory_stats": "vm_stat | grep -E 'Pages (free|active|wired|compressed):'",
        "mem_total": "sysctl -n hw.memsize",
        "cpu_load": "sysctl -n vm.loadavg",
        "os": "sw_vers -productVersion",
        "uptime": "uptime",
    }
    results = []
    for key, cmd in cmds.items():
        try:
            import subprocess as _sp
            out = _sp.run(cmd, shell=True, capture_output=True, text=True, timeout=5).stdout.strip()
            if out:
                results.append(f"[{key}]\n{out}")
        except Exception:
            pass
    return "\n".join(results)


def _preprocess_worker_routing(user_prompt: str, workers: dict | None = None) -> str:
    """@워커명 멘션을 명시적인 bash SSH 명령으로 변환해서 LLM 오해 방지.

    workers: config/template.yaml 의 'workers' 섹션. 없으면 라우팅 안 함.

    template.yaml 설정 예시:
        workers:
          g1:                  # @g1 로 호출
            host: g1           # SSH 호스트 (기본값: 키 이름)
            worker: g1-worker  # meshclaw worker 이름 (기본값: 키-worker)
          gpu2:
            host: 192.168.1.50
            worker: llm-worker

    단축 형식 (host == worker 이름의 앞부분):
        workers:
          g1: g1-worker   # host=g1, worker=g1-worker
          g2: g2-worker
    """
    if not workers:
        return user_prompt

    import re

    # 워커 목록 정규화 → {mention: {"host": str, "worker": str}}
    resolved = {}
    for name, val in workers.items():
        if isinstance(val, str):
            # "g1": "g1-worker" 형식
            resolved[name.lower()] = {"host": name, "worker": val}
        elif isinstance(val, dict):
            host   = val.get("host",   name)
            worker = val.get("worker", f"{name}-worker")
            resolved[name.lower()] = {"host": host, "worker": worker}
        else:
            resolved[name.lower()] = {"host": name, "worker": f"{name}-worker"}

    if not resolved:
        return user_prompt

    # @all / @전체 → 모든 워커에 병렬 호출
    m = re.match(r'^@(all|전체)\s+(.*)', user_prompt, re.IGNORECASE | re.DOTALL)
    if m:
        query = m.group(2).strip().replace('"', '\\"')
        parts = [
            f"ssh {w['host']} '/usr/local/bin/meshclaw ask {w['worker']} \"{query}\" --timeout 90' "
            f"2>&1 | sed 's/^/[{n}] /'"
            for n, w in resolved.items()
        ]
        cmd = "{ " + "; ".join(parts) + "; }"
        return f'Run this bash command and show all output:\n{cmd}'

    # @워커명 → 해당 워커에만 호출
    worker_pattern = "|".join(re.escape(n) for n in resolved)
    m = re.match(rf'^@({worker_pattern})\s+(.*)', user_prompt, re.IGNORECASE | re.DOTALL)
    if m:
        name  = m.group(1).lower()
        query = m.group(2).strip().replace('"', '\\"')
        w = resolved[name]
        return (f'Run this bash command and return its output:\n'
                f"ssh {w['host']} '/usr/local/bin/meshclaw ask {w['worker']} \"{query}\" --timeout 90'")

    return user_prompt


def _call_llm_claude_code(system: str, user_prompt: str, api_key: str,
                           workers: dict | None = None) -> str:
    """Use `claude -p` subprocess with pre-collected real Mac data injected."""
    user_prompt = _preprocess_worker_routing(user_prompt, workers)
    claude_bin = None
    for p in ["/opt/homebrew/bin/claude", "/usr/local/bin/claude"]:
        if os.path.isfile(p):
            claude_bin = p
            break
    if not claude_bin:
        return "Error: claude CLI not found"

    env = {**os.environ,
           "PATH": "/opt/homebrew/bin:/usr/local/bin:" + os.environ.get("PATH", "")}
    if api_key:
        env["ANTHROPIC_API_KEY"] = api_key

    # Pre-collect real system data so model never hallucinates numbers
    mac_ctx = _collect_mac_context()
    ctx_block = (
        f"\n\n[REAL SYSTEM DATA — always use these exact numbers, never make up data]\n{mac_ctx}\n[END SYSTEM DATA]"
        if mac_ctx else ""
    )

    full_prompt = (
        f"[System context: {system}]{ctx_block}\n\nUser: {user_prompt}"
        if system else
        f"{ctx_block}\n\nUser: {user_prompt}"
    )

    try:
        result = subprocess.run(
            [claude_bin, "-p", full_prompt, "--dangerously-skip-permissions",
             "--allowedTools", "Bash,WebFetch,WebSearch",
             "--output-format", "text"],
            capture_output=True, text=True, timeout=120, env=env,
        )
        out = result.stdout.strip()
        if result.returncode != 0 and not out:
            out = result.stderr.strip() or f"(exit {result.returncode})"
        return out or "(no response)"
    except subprocess.TimeoutExpired:
        return "(timeout after 120s)"
    except Exception as e:
        return f"Error (claude-code): {e}"

def _call_llm_codex(system: str, user_prompt: str) -> str:
    """Use `codex exec` (OpenAI Codex CLI) as agent backend.

    Codex CLI handles bash execution natively with full-auto mode.
    Requires codex CLI installed and logged in (`codex login`).
    Uses --output-last-message for clean output without token stats.
    """
    codex_bin = None
    for p in ["/opt/homebrew/bin/codex", "/usr/local/bin/codex"]:
        if os.path.isfile(p):
            codex_bin = p
            break
    if not codex_bin:
        return "Error: codex CLI not found"

    out_file = f"/tmp/codex_out_{os.getpid()}.txt"
    env = {**os.environ,
           "PATH": "/opt/homebrew/bin:/usr/local/bin:" + os.environ.get("PATH", "")}

    full_prompt = f"[System: {system}]\n\n{user_prompt}" if system else user_prompt

    try:
        subprocess.run(
            [codex_bin, "exec", "--full-auto", "--skip-git-repo-check",
             "--output-last-message", out_file, full_prompt],
            capture_output=True, text=True, timeout=120, env=env,
        )
        if os.path.exists(out_file):
            out = open(out_file).read().strip()
            os.unlink(out_file)
            return out or "(no response)"
        return "(no output file)"
    except subprocess.TimeoutExpired:
        return "(timeout after 120s)"
    except Exception as e:
        return f"Error (codex): {e}"


def call_llm(config: dict, user_prompt: str) -> str:
    """Call the configured LLM model, with bash tool-calling loop for Claude."""
    model  = config.get("model", "claude-sonnet-4-6")
    system = config.get("system_prompt", "You are a helpful assistant.")
    max_tokens = config.get("max_tokens", 2048)
    # use_tools: True/missing = enable bash tool, False = disable
    # Guard: [] (empty list from template default) is falsy but should mean "use defaults"
    _tools_cfg = config.get("tools", True)
    use_tools  = _tools_cfg if isinstance(_tools_cfg, bool) else True

    # ── Claude Code agent path (model = "claude-code") ──────────────────
    if model == "claude-code":
        # API key optional — claude CLI authenticates via Claude Code subscription (OAuth)
        api_key = os.environ.get("ANTHROPIC_API_KEY", "")
        workers = config.get("workers") or {}
        return _call_llm_claude_code(system, user_prompt, api_key, workers)

    # ── OpenAI Codex CLI agent path (model = "codex") ────────────────────
    if model == "codex":
        return _call_llm_codex(system, user_prompt)

    if "claude" in model:
        try:
            import anthropic
            api_key = os.environ.get("ANTHROPIC_API_KEY", "")
            if not api_key:
                return "Error: ANTHROPIC_API_KEY not set"
            client = anthropic.Anthropic(api_key=api_key)

            messages = [{"role": "user", "content": user_prompt}]
            tools    = [_BASH_TOOL] if use_tools else []

            # Agentic tool-call loop (max 10 rounds to prevent runaway)
            for _round in range(10):
                kwargs = dict(
                    model=model,
                    max_tokens=max_tokens,
                    system=system,
                    messages=messages,
                )
                if tools:
                    kwargs["tools"] = tools

                resp = client.messages.create(**kwargs)

                # Collect any text from this response
                text_parts = [b.text for b in resp.content if b.type == "text"]

                # Check for tool_use blocks
                tool_uses = [b for b in resp.content if b.type == "tool_use"]

                if not tool_uses:
                    # No more tool calls → done
                    return "\n".join(text_parts) if text_parts else "(no response)"

                # Execute each tool call
                tool_results = []
                for tu in tool_uses:
                    cmd     = tu.input.get("command", "")
                    timeout = int(tu.input.get("timeout", 30))
                    output  = _run_bash_tool(cmd, timeout)
                    tool_results.append({
                        "type": "tool_result",
                        "tool_use_id": tu.id,
                        "content": output,
                    })

                # Append assistant turn + tool results to message history
                messages.append({"role": "assistant", "content": resp.content})
                messages.append({"role": "user",      "content": tool_results})

            # Fallback if loop exhausted
            return "\n".join(text_parts) if text_parts else "(max iterations reached)"

        except Exception as e:
            return f"Error (claude): {e}"

    if "gpt" in model:
        try:
            import openai
            client = openai.OpenAI(api_key=os.environ.get("OPENAI_API_KEY", ""))
            resp = client.chat.completions.create(
                model=model, max_tokens=max_tokens,
                messages=[{"role": "system", "content": system},
                           {"role": "user",   "content": user_prompt}],
            )
            return resp.choices[0].message.content
        except Exception as e:
            return f"Error (openai): {e}"

    # ── Cursor API (model = "cursor/...") — OpenAI-compatible ────────────
    if model.startswith("cursor/"):
        try:
            import openai
            cursor_model = model.split("/", 1)[1]
            api_key = os.environ.get("CURSOR_API_KEY", "")
            if not api_key:
                return "Error: CURSOR_API_KEY not set"
            client = openai.OpenAI(
                api_key=api_key,
                base_url="https://api.cursor.sh/v1",
            )
            messages_list = [{"role": "system", "content": system},
                              {"role": "user",   "content": user_prompt}]
            tools_oai = [{
                "type": "function",
                "function": {
                    "name": "bash",
                    "description": "Run a shell command and return output",
                    "parameters": {
                        "type": "object",
                        "properties": {"command": {"type": "string"}},
                        "required": ["command"],
                    },
                },
            }] if use_tools else []
            for _ in range(10):
                kwargs = dict(model=cursor_model, max_tokens=max_tokens,
                              messages=messages_list)
                if tools_oai:
                    kwargs["tools"] = tools_oai
                    kwargs["tool_choice"] = "auto"
                resp = client.chat.completions.create(**kwargs)
                msg = resp.choices[0].message
                tool_calls = getattr(msg, "tool_calls", None) or []
                if not tool_calls:
                    return msg.content or "(no response)"
                messages_list.append(msg)
                for tc in tool_calls:
                    import json as _json
                    cmd = _json.loads(tc.function.arguments).get("command", "")
                    out = _run_bash_tool(cmd)
                    messages_list.append({
                        "role": "tool",
                        "tool_call_id": tc.id,
                        "content": out,
                    })
            return msg.content or "(max iterations)"
        except Exception as e:
            return f"Error (cursor): {e}"

    if model.startswith("ollama/"):
        # Uses Ollama's OpenAI-compatible API (/v1/chat/completions)
        # Supports tool-calling on models that have it (llama3.1+, qwen2.5, etc.)
        try:
            import openai
            ollama_model = model.split("/", 1)[1]
            ollama_url   = os.environ.get("OLLAMA_URL", "http://localhost:11434")
            client = openai.OpenAI(
                api_key="ollama",  # Ollama doesn't require a real key
                base_url=f"{ollama_url}/v1",
            )
            messages_list = [{"role": "system", "content": system},
                              {"role": "user",   "content": user_prompt}]
            tools_oai = [{
                "type": "function",
                "function": {
                    "name": "bash",
                    "description": "Run a bash/shell command on this machine and return the output. Use this to get real data.",
                    "parameters": {
                        "type": "object",
                        "properties": {
                            "command": {"type": "string", "description": "Shell command to execute"},
                            "timeout": {"type": "integer", "default": 30},
                        },
                        "required": ["command"],
                    },
                },
            }] if use_tools else []

            for _ in range(10):
                kwargs = dict(model=ollama_model, max_tokens=max_tokens,
                              messages=messages_list)
                if tools_oai:
                    kwargs["tools"] = tools_oai
                    kwargs["tool_choice"] = "auto"
                resp = client.chat.completions.create(**kwargs)
                msg = resp.choices[0].message
                tool_calls = getattr(msg, "tool_calls", None) or []
                if not tool_calls:
                    return msg.content or "(no response)"
                messages_list.append(msg)
                for tc in tool_calls:
                    args = json.loads(tc.function.arguments) if isinstance(tc.function.arguments, str) else tc.function.arguments
                    cmd = args.get("command", "")
                    out = _run_bash_tool(cmd, int(args.get("timeout", 30)))
                    messages_list.append({
                        "role": "tool",
                        "tool_call_id": tc.id,
                        "content": out,
                    })
            return msg.content or "(max iterations)"
        except ImportError:
            # Fallback: plain HTTP if openai package not installed
            try:
                import urllib.request
                ollama_model = model.split("/", 1)[1]
                ollama_url   = os.environ.get("OLLAMA_URL", "http://localhost:11434")
                payload = json.dumps({
                    "model": ollama_model,
                    "prompt": f"{system}\n\n{user_prompt}",
                    "stream": False,
                }).encode()
                req = urllib.request.Request(
                    f"{ollama_url}/api/generate", data=payload,
                    headers={"Content-Type": "application/json"},
                )
                with urllib.request.urlopen(req, timeout=120) as r:
                    return json.loads(r.read())["response"]
            except Exception as e2:
                return f"Error (ollama-fallback): {e2}"
        except Exception as e:
            return f"Error (ollama): {e}"

    return f"Error: unsupported model '{model}'"


# ── Schedule ─────────────────────────────────────────────────────────

def _parse_interval(s: str) -> int:
    m = _INTERVAL_RE.search(s)
    if not m:
        return 0
    n, unit = int(m.group(1)), m.group(2).lower()
    return n * {"m": 60, "h": 3600, "d": 86400}[unit]


def run_schedule_loop(config: dict):
    schedule = config.get("schedule", "")
    task     = config.get("schedule_task", "")
    script   = config.get("schedule_script", "")  # raw bash — no LLM call

    if not schedule or (not task and not script):
        return
    interval = _parse_interval(schedule)
    if not interval:
        log(f"Schedule: could not parse '{schedule}'")
        return

    mode = "script" if script else "llm"
    label = (script if script else task)[:60]
    log(f"Schedule: {schedule} ({interval}s) [{mode}] — {label}")

    while True:
        time.sleep(interval)
        out_path = os.path.join(WORK_DIR, "schedule_output.txt")
        ts = time.strftime("%Y-%m-%d %H:%M:%S")
        try:
            if script:
                # ── Script mode: run bash directly, no LLM ────────────────
                log("Running scheduled script...")
                result = _run_bash_tool(script, timeout=120)
                log(f"Script result: {result[:200]}")
                with open(out_path, "a") as f:
                    f.write(f"[{ts}]\n{result}\n\n")
            else:
                # ── LLM mode: pass task description to model ───────────────
                log("Running scheduled task (LLM)...")
                result = call_llm(config, task)
                log(f"Scheduled result: {result[:300]}")
                with open(out_path, "w") as f:
                    f.write(f"[{ts}]\n{result}\n")
            # ── Notify after schedule run ─────────────────────────────────
            notify_cfg = config.get("notify")
            if notify_cfg and result:
                try:
                    from meshclaw import notify as _notify
                    platform = notify_cfg.get("platform", "")
                    _notify(platform, result[:2000], **{
                        k: v for k, v in notify_cfg.items() if k != "platform"
                    })
                    log(f"Notified via {platform}")
                except Exception as ne:
                    log(f"Notify error: {ne}")

        except Exception as e:
            log(f"Schedule error: {e}")


# ── Message server (Unix socket) ──────────────────────────────────────

def handle_connection(conn: socket.socket, config: dict):
    try:
        data = b""
        while True:
            chunk = conn.recv(4096)
            if not chunk:
                break
            data += chunk
            if bytes([4]) in data or bytes([10]) in data:
                break
        msg = data.replace(bytes([4]), b"").decode(errors="replace").strip()
        if not msg:
            conn.close()
            return
        log(f"Message: {msg[:100]}")
        reply = call_llm(config, msg)
        conn.sendall(reply.encode() + bytes([4]))
    except Exception as e:
        log(f"Connection error: {e}")
        try:
            conn.sendall(f"Error: {e}".encode() + bytes([4]))
        except Exception:
            pass
    finally:
        conn.close()


def run_message_server(config: dict):
    sock_path = SOCK_PATH.format(name=config["name"])
    if os.path.exists(sock_path):
        os.unlink(sock_path)
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(sock_path)
    srv.listen(16)
    os.chmod(sock_path, 0o666)
    log(f"Listening on {sock_path}")
    while True:
        try:
            conn, _ = srv.accept()
            threading.Thread(target=handle_connection, args=(conn, config), daemon=True).start()
        except Exception as e:
            log(f"Server error: {e}")
            time.sleep(1)


# ── Wire mesh — full peer registration ───────────────────────────────

def _generate_node_id(node_name: str) -> str:
    """Deterministic node_id from node_name (stable across restarts)."""
    return hashlib.sha256(f"meshclaw:{node_name}".encode()).hexdigest()[:32]


def _generate_vpn_ip(node_id: str) -> str:
    """Derive a 10.99.x.x VPN IP deterministically from node_id."""
    h = hashlib.sha256(node_id.encode()).digest()
    b1 = (h[0] % 200) + 10   # 10–209
    b2 = h[1]                 # 0–255
    return f"10.99.{b1}.{b2}"


def _api_post(relay_url: str, path: str, data: dict, timeout: int = 10) -> dict:
    import urllib.request
    try:
        body = json.dumps(data).encode()
        req  = urllib.request.Request(f"{relay_url}{path}", data=body, method="POST")
        req.add_header("Content-Type", "application/json")
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return json.loads(r.read())
    except Exception as e:
        return {"error": str(e)}


def _detect_existing_vpn_ip() -> str:
    """Detect existing VPN IP from Wire (wg0) or Tailscale (tailscale0/utun).

    Returns IP string like '10.99.x.x' or '100.x.x.x', or empty string.
    Used by Mac native workers that share the host's VPN connection.
    """
    # Strategy 1: WIRE_VPN_IP env var (explicit override)
    explicit = os.environ.get("WIRE_VPN_IP", "")
    if explicit:
        return explicit

    # Strategy 2: wg show (WireGuard) — works on Linux and Mac
    try:
        r = subprocess.run(["wg", "show", "wg0", "allowed-ips"],
                           capture_output=True, text=True, timeout=5)
        # wg0 own IP is better found via ip/ifconfig
    except Exception:
        pass

    # Strategy 3: ip addr / ifconfig for wg0
    try:
        r = subprocess.run(["ip", "addr", "show", "wg0"],
                           capture_output=True, text=True, timeout=5)
        m = re.search(r"inet\s+(10\.\d+\.\d+\.\d+)", r.stdout)
        if m:
            return m.group(1)
    except Exception:
        pass

    # Strategy 4: ifconfig wg0 (macOS)
    try:
        r = subprocess.run(["ifconfig", "wg0"],
                           capture_output=True, text=True, timeout=5)
        m = re.search(r"inet\s+(10\.\d+\.\d+\.\d+)", r.stdout)
        if m:
            return m.group(1)
    except Exception:
        pass

    # Strategy 4b: macOS utun interfaces — WireGuard on Mac uses utun not wg0
    try:
        r = subprocess.run(["ifconfig"], capture_output=True, text=True, timeout=5)
        # Find 10.99.x.x IPs on any utun interface (Wire mesh range)
        m = re.search(r"inet\s+(10\.99\.\d+\.\d+)", r.stdout)
        if m:
            return m.group(1)
    except Exception:
        pass

    # Strategy 5: wire_client get current IP
    try:
        from wire_client import cmd_status
        status = cmd_status()
        ip = status.get("vpn_ip") or status.get("ip", "")
        if ip:
            return ip
    except Exception:
        pass

    # Strategy 6: Tailscale IP (100.x.x.x range)
    try:
        r = subprocess.run(["tailscale", "ip", "--4"],
                           capture_output=True, text=True, timeout=5)
        ip = r.stdout.strip()
        if re.match(r"^100\.", ip):
            return ip
    except Exception:
        pass

    return ""


def connect_wire_as_node(node_name: str) -> str:
    """Register this node as a Wire peer and configure VPN.

    For Mac native workers (RTLINUX_MODE=mac): detects existing Wire/Tailscale
    VPN IP instead of creating a new interface.
    For Docker containers: brings up wg0 as a new peer.

    Returns the assigned VPN IP, or empty string on failure.
    """
    relay_url   = os.environ.get("WIRE_RELAY_URL", "").rstrip("/")
    listen_port = int(os.environ.get("WIRE_LISTEN_PORT", "51820"))
    mac_mode    = os.environ.get("RTLINUX_MODE", "") == "mac"

    # ── Mac mode: reuse existing VPN interface ───────────────────────
    if mac_mode:
        log("Wire: Mac mode — detecting existing VPN IP")
        existing_ip = _detect_existing_vpn_ip()
        if existing_ip:
            log(f"Wire: using existing VPN IP = {existing_ip} ✓")
            return existing_ip
        log("Wire: no existing VPN found — continuing without mesh IP")
        return ""

    if not relay_url:
        log("Wire: WIRE_RELAY_URL not set — skipping mesh join")
        return ""

    log(f"Wire: joining mesh via {relay_url} as '{node_name}'")

    try:
        # Try using wire_client directly (meshpop-wire installed in image)
        from wire_client import cmd_up
        result = cmd_up(name=node_name, server=relay_url, port=listen_port)
        if result.get("ok"):
            vpn_ip = result.get("vpn_ip", "")
            log(f"Wire: joined ✓  VPN IP = {vpn_ip}  peers = {result.get('peers', '?')}")
            return vpn_ip
        else:
            log(f"Wire: cmd_up failed: {result.get('error', '?')} — trying CLI fallback")
    except ImportError:
        log("Wire: wire_client not importable — trying CLI")
    except Exception as e:
        log(f"Wire: cmd_up error: {e} — trying CLI fallback")

    # CLI fallback: write config then run `wire up`
    try:
        node_id = _generate_node_id(node_name)
        vpn_ip  = _generate_vpn_ip(node_id)

        wire_cfg = {
            "server_url": relay_url,
            "node_name":  node_name,
            "node_id":    node_id,
            "vpn_ip":     vpn_ip,
            "listen_port": listen_port,
        }
        os.makedirs("/etc/wire", exist_ok=True)
        with open("/etc/wire/config.json", "w") as f:
            json.dump(wire_cfg, f, indent=2)

        r = subprocess.run(["wire", "up"], capture_output=True, text=True, timeout=20)
        out = (r.stdout + r.stderr).strip()
        if r.returncode == 0:
            log(f"Wire: CLI joined ✓  VPN IP = {vpn_ip}")
            return vpn_ip
        else:
            log(f"Wire: CLI failed: {out[:120]}")

            # Last resort: manual POST /register + wg setup
            pub_key = _get_or_create_wg_keys()
            if pub_key:
                reg = _api_post(relay_url, "/register", {
                    "node_id": node_id, "node_name": node_name,
                    "wg_public_key": pub_key, "vpn_ip": vpn_ip,
                    "port": listen_port, "nat_port": listen_port,
                })
                if not reg.get("error"):
                    log(f"Wire: manual registration OK  VPN IP = {vpn_ip}")
                    return vpn_ip
                log(f"Wire: manual registration failed: {reg.get('error')}")
    except Exception as e:
        log(f"Wire: connection error: {e}")

    return ""


def _get_or_create_wg_keys() -> str:
    """Generate WireGuard keypair if not present, return public key."""
    key_dir = Path("/etc/wire")
    key_dir.mkdir(parents=True, exist_ok=True)
    priv_path = key_dir / "private.key"
    pub_path  = key_dir / "public.key"

    if not priv_path.exists():
        try:
            r = subprocess.run(["wg", "genkey"], capture_output=True, text=True, timeout=5)
            if r.returncode == 0:
                priv_path.write_text(r.stdout.strip())
                r2 = subprocess.run(["wg", "pubkey"], input=r.stdout,
                                    capture_output=True, text=True, timeout=5)
                if r2.returncode == 0:
                    pub_path.write_text(r2.stdout.strip())
        except Exception as e:
            log(f"Wire keygen error: {e}")
            return ""

    return pub_path.read_text().strip() if pub_path.exists() else ""


# ── vssh server ───────────────────────────────────────────────────────

def start_vssh_server():
    """Start vssh server in a background thread.

    Reads VSSH_SECRET env var (falls back to ~/.vssh/secret).
    Workers become reachable via: vssh exec <vpn_ip> <cmd>
    """
    vssh_secret = os.environ.get("VSSH_SECRET", "")

    if vssh_secret:
        # Inject secret so vssh finds it
        vssh_dir = Path.home() / ".vssh"
        vssh_dir.mkdir(exist_ok=True)
        (vssh_dir / "secret").write_text(vssh_secret)
        (vssh_dir / "secret").chmod(0o600)
        log(f"vssh: secret configured")

    try:
        import vssh as _vssh
        t = threading.Thread(target=_vssh.server, daemon=True)
        t.start()
        log("vssh: server started (port 9222)")
    except ImportError:
        log("vssh: not installed — remote command execution unavailable")
    except Exception as e:
        log(f"vssh: failed to start: {e}")


# ── Health + fleet registration ───────────────────────────────────────

def write_health(config: dict, vpn_ip: str = ""):
    """Write health.json — readable by mpop fleet scanner."""
    import platform
    health = {
        "name":       config["name"],
        "version":    config.get("version", "?"),
        "model":      config.get("model", "?"),
        "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
        "python":     platform.python_version(),
        "hostname":   platform.node(),
        "vpn_ip":     vpn_ip,
        "role":       "meshclaw-worker",
        "wire_relay": os.environ.get("WIRE_RELAY_URL", ""),
        "mode":       os.environ.get("RTLINUX_MODE", "docker"),
    }
    os.makedirs(WORK_DIR, exist_ok=True)
    with open(os.path.join(WORK_DIR, "health.json"), "w") as f:
        json.dump(health, f, indent=2)


def register_with_mpop(config: dict, vpn_ip: str):
    """Write mpop-compatible server entry so worker appears in mpop servers."""
    if not vpn_ip:
        return
    node_name = config["name"]
    mpop_dir  = Path.home() / ".mpop"
    mpop_dir.mkdir(exist_ok=True)
    cfg_path  = mpop_dir / "config.json"

    try:
        cfg = json.loads(cfg_path.read_text()) if cfg_path.exists() else {}
        servers = cfg.setdefault("servers", {})
        servers[node_name] = {
            "ip":       vpn_ip,
            "wire_ip":  vpn_ip,
            "role":     "meshclaw-worker",
            "model":    config.get("model", "?"),
        }
        cfg["node_name"] = node_name
        cfg_path.write_text(json.dumps(cfg, indent=2))
        log(f"mpop: registered as '{node_name}' ({vpn_ip})")
    except Exception as e:
        log(f"mpop: registration error: {e}")


# ── Entry point ───────────────────────────────────────────────────────

def main():
    log("=" * 50)
    log("meshclaw runtime starting")

    if not os.path.exists(CONFIG_PATH):
        log(f"Config not found: {CONFIG_PATH}")
        sys.exit(1)

    with open(CONFIG_PATH) as f:
        config = json.load(f)

    # NODE_NAME env var overrides template name (set by docker run -e NODE_NAME=...)
    node_name = os.environ.get("NODE_NAME", config["name"])
    config["name"] = node_name

    log(f"Node:      {node_name}")
    log(f"Model:     {config.get('model', '?')}")
    if config.get("schedule"):
        log(f"Schedule:  {config['schedule']}")

    missing = [k for k in config.get("env", []) if not os.environ.get(k)]
    if missing:
        log(f"Warning: missing env vars: {', '.join(missing)}")

    # ── 1. Join Wire mesh → get VPN IP ──────────────────────────────
    vpn_ip = connect_wire_as_node(node_name)

    # ── 2. Start vssh server ────────────────────────────────────────
    start_vssh_server()

    # ── 3. Write health + register with mpop ────────────────────────
    try:
        write_health(config, vpn_ip)
        register_with_mpop(config, vpn_ip)
    except Exception as e:
        log(f"Health/register error: {e}")

    # ── 4. Start scheduled tasks ─────────────────────────────────────
    if config.get("schedule") and (config.get("schedule_task") or config.get("schedule_script")):
        threading.Thread(target=run_schedule_loop, args=(config,), daemon=True).start()

    log("Ready.")

    # ── 5. Main AI message loop (blocking) ───────────────────────────
    run_message_server(config)


if __name__ == "__main__":
    main()
