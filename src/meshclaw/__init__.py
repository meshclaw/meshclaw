"""
meshclaw — AI Worker Runtime
============================

Run AI assistants anywhere. No cloud dependency, no Docker required.

Each assistant is a lightweight Python process that:
- Listens on a Unix socket for messages
- Calls an LLM (Claude, GPT, Codex, Ollama...)
- Executes bash commands via tool-calling
- Runs scheduled background tasks
- Serves a mobile-friendly web chat UI

Quick start::

    pip install meshclaw
    export ANTHROPIC_API_KEY=sk-ant-...

    meshclaw init my-bot            # scaffold from template
    meshclaw start my-bot           # run in background
    meshclaw chat my-bot            # talk to it
    meshclaw webchat --worker my-bot  # browser/mobile UI

Built-in templates:
    assistant      — General-purpose assistant with bash access
    system-monitor — Server health monitor with alerts
    news           — Hourly news digest via TTS
    research       — Web research + summarization
    email          — Email monitor and responder
    code-reviewer  — Git diff reviewer
    mac-assistant  — macOS: Calendar, Mail, AppleScript

Links:
    - GitHub: https://github.com/meshpop/meshclaw
    - PyPI:   https://pypi.org/project/meshclaw/
"""

__version__ = "0.5.5"
__author__ = "MeshPOP"
__license__ = "Apache-2.0"

import os
import sys
import json
import base64
import subprocess
from pathlib import Path
from meshclaw import template as _template


# ── Config ──────────────────────────────────────────────────────────
def _default_meshclaw_dir() -> str:
    """Return package-bundled dir (for pip users) or ~/meshpoplinux (for dev)."""
    pkg_dir = os.path.dirname(os.path.abspath(__file__))
    if os.path.isdir(os.path.join(pkg_dir, "scripts")):
        return pkg_dir
    return os.path.expanduser("~/meshpoplinux")

RTLINUX_DIR = os.environ.get("RTLINUX_DIR", _default_meshclaw_dir())
BUILD_METHODS = ["buildroot", "alpine", "scratch", "nodocker"]
DEFAULT_METHOD = "buildroot"

MESHPOP_PACKAGES = ["meshpop", "vssh", "meshpop-wire", "meshpop-db", "sv-vault"]


def _resolve_node_ip(node: str) -> str:
    """Resolve node name to wire_ip via ~/.mpop/config.json, fallback to node name."""
    try:
        import json
        cfg_path = os.path.expanduser("~/.mpop/config.json")
        if os.path.exists(cfg_path):
            with open(cfg_path) as f:
                cfg = json.load(f)
            servers = cfg.get("servers", {})
            if node in servers:
                srv = servers[node]
                # Prefer wire_ip (VPN), then ip, then original node name
                return srv.get("wire_ip") or srv.get("ip") or node
    except Exception:
        pass
    return node


def _vssh_exec(node: str, cmd: str) -> str:
    """Execute command on remote node via vssh Python module.
    Resolves node name to VPN IP via ~/.mpop/config.json for reliable routing.
    """
    ip = _resolve_node_ip(node)
    try:
        import vssh
        import io
        old = sys.stdout
        sys.stdout = buf = io.StringIO()
        vssh.ssh(ip, cmd)
        sys.stdout = old
        return buf.getvalue().strip()
    except ImportError:
        # Fallback to CLI
        r = subprocess.run(
            ["vssh", "exec", ip, cmd],
            capture_output=True, text=True, timeout=30
        )
        return r.stdout.strip()


def _vssh_put(local_path: str, node: str, remote_path: str) -> bool:
    """Upload file to remote node via vssh.
    Resolves node name to VPN IP via ~/.mpop/config.json.
    """
    ip = _resolve_node_ip(node)
    try:
        import vssh
        vssh.put(local_path, ip, remote_path)
        return True
    except ImportError:
        r = subprocess.run(
            ["vssh", "put", local_path, f"{ip}:{remote_path}"],
            capture_output=True, text=True, timeout=120
        )
        return r.returncode == 0


# ── Build ───────────────────────────────────────────────────────────
def build(method: str = DEFAULT_METHOD, output_dir: str = None) -> dict:
    """Build meshpoplinux runtime image.

    Args:
        method: Build method (buildroot, alpine, scratch)
        output_dir: Output directory for artifacts

    Returns:
        dict with build results
    """
    if method not in BUILD_METHODS:
        return {"error": f"Unknown method: {method}. Use: {BUILD_METHODS}"}

    output_dir = output_dir or os.path.join(RTLINUX_DIR, "output")
    os.makedirs(output_dir, exist_ok=True)

    scripts = {
        "buildroot": "scripts/build-buildroot.sh",
        "alpine":    "scripts/build-docker.sh",
        "scratch":   "scripts/build-from-scratch.sh",
        "nodocker":  "scripts/build-from-scratch.sh",  # same as scratch, no Docker needed
    }

    script = os.path.join(RTLINUX_DIR, scripts[method])
    if not os.path.exists(script):
        return {"error": f"Build script not found: {script}"}

    r = subprocess.run(
        ["bash", script, output_dir],
        capture_output=True, text=True, timeout=3600
    )

    rootfs = os.path.join(output_dir, "meshpoplinux-rootfs.tar.gz")
    if os.path.exists(rootfs):
        size = os.path.getsize(rootfs)
        return {
            "status": "ok",
            "method": method,
            "artifact": rootfs,
            "size_bytes": size,
            "size_human": f"{size / 1024 / 1024:.1f}MB",
        }
    return {"error": "Build failed — rootfs not produced", "output": r.stderr[-500:]}


# ── Deploy ──────────────────────────────────────────────────────────
def deploy(node: str, image: str = None, install_stack: bool = True) -> dict:
    """Deploy runtime image to a node.

    Args:
        node: Target node name (node1, worker2, etc.)
        image: Path to rootfs image (default: output/meshpoplinux-rootfs.tar.gz)
        install_stack: Whether to install MeshPOP packages

    Returns:
        dict with deployment results
    """
    image = image or os.path.join(RTLINUX_DIR, "output", "meshpoplinux-rootfs.tar.gz")
    if not os.path.exists(image):
        return {"error": f"Image not found: {image}. Run: meshclaw build"}

    results = {"node": node, "steps": []}

    # Upload
    ok = _vssh_put(image, node, "/tmp/meshpoplinux-rootfs.tar.gz")
    results["steps"].append({"upload": "ok" if ok else "failed"})

    # Extract
    out = _vssh_exec(node, "mkdir -p /opt/meshpoplinux && tar xzf /tmp/meshpoplinux-rootfs.tar.gz -C /opt/meshpoplinux 2>&1 | tail -3")
    results["steps"].append({"extract": out or "ok"})

    # Install MeshPOP stack
    if install_stack:
        for pkg in MESHPOP_PACKAGES:
            out = _vssh_exec(node, f"pip3 install --break-system-packages --upgrade {pkg} 2>&1 | tail -1")
            results["steps"].append({pkg: out})

    # Cleanup
    _vssh_exec(node, "rm -f /tmp/meshpoplinux-rootfs.tar.gz")

    results["status"] = "ok"
    return results


# ── Verify ──────────────────────────────────────────────────────────
def verify(node: str) -> dict:
    """Verify a node's health across all 5 MeshPOP layers.

    Returns:
        dict with check results per layer
    """
    checks = {"node": node, "layers": {}}

    # L0: Reachable?
    ping = _vssh_exec(node, "echo pong")
    checks["layers"]["L0_reachable"] = ping == "pong"
    if not checks["layers"]["L0_reachable"]:
        checks["status"] = "unreachable"
        return checks

    # System info
    checks["hostname"] = _vssh_exec(node, "hostname")
    checks["kernel"] = _vssh_exec(node, "uname -r")
    checks["python"] = _vssh_exec(node, "python3 --version 2>&1")

    # L1: Wire
    wg = _vssh_exec(node, "ip -4 addr show wg0 2>/dev/null | grep -oP 'inet \\K[0-9./]+'")
    checks["layers"]["L1_wire"] = bool(wg)
    checks["wire_ip"] = wg

    # L2: vssh
    checks["layers"]["L2_vssh"] = True  # Already reachable

    # L3: mpop packages
    pkg_ok = 0
    for pkg in MESHPOP_PACKAGES:
        ver = _vssh_exec(node, f"python3 -c \"import importlib.metadata; print(importlib.metadata.version('{pkg}'))\" 2>/dev/null")
        checks[f"pkg_{pkg}"] = ver if ver else None
        if ver:
            pkg_ok += 1
    checks["layers"]["L3_mpop"] = pkg_ok >= 3

    # L4: MeshDB
    mdb = _vssh_exec(node, "python3 -c 'import meshpop_db; print(\"ok\")' 2>&1")
    checks["layers"]["L4_meshdb"] = "ok" in mdb

    # L5: Vault
    vlt = _vssh_exec(node, "python3 -c 'import sv_vault; print(\"ok\")' 2>&1")
    checks["layers"]["L5_vault"] = "ok" in vlt

    # Disk
    disk = _vssh_exec(node, "df / | tail -1 | tr -s ' ' | cut -d' ' -f5 | tr -d '%'")
    try:
        checks["disk_pct"] = int(disk)
    except:
        checks["disk_pct"] = None

    # Overall
    layer_ok = sum(1 for v in checks["layers"].values() if v)
    checks["status"] = "healthy" if layer_ok >= 4 else "degraded" if layer_ok >= 2 else "critical"
    checks["layers_ok"] = f"{layer_ok}/{len(checks['layers'])}"

    return checks


def verify_fleet(nodes: list) -> dict:
    """Verify all nodes in the fleet."""
    results = {}
    for node in nodes:
        results[node] = verify(node)

    healthy = sum(1 for r in results.values() if r.get("status") == "healthy")
    return {
        "fleet": results,
        "summary": f"{healthy}/{len(nodes)} healthy",
    }




# ── Templates ────────────────────────────────────────────────────────

def build_from_template(
    config_path: str,
    output_dir: str = None,
    method: str = "docker",
) -> dict:
    """Build a self-contained assistant image from a template.yaml.

    Args:
        config_path: Path to template.yaml (or built-in name like "email")
        output_dir:  Where to write the image (default: ~/meshpoplinux/output)
        method:      "docker" (recommended) or "nodocker" (Linux only)

    Returns:
        dict with build result

    Example::

        meshclaw build --config email           # built-in template
        meshclaw build --config my_bot.yaml     # custom template
    """
    # Resolve template path
    if not config_path.endswith((".yaml", ".yml", ".json")):
        # Try built-in name
        found = _template.find_builtin(config_path)
        if found:
            config_path = found
        else:
            return {"error": f"Template not found: '{config_path}'. Run: meshclaw templates list"}

    # Load + validate
    try:
        tmpl = _template.load(config_path)
    except Exception as e:
        return {"error": f"Template error: {e}"}

    output_dir = output_dir or os.path.join(RTLINUX_DIR, "output")
    os.makedirs(output_dir, exist_ok=True)

    if method == "docker":
        return _build_docker(tmpl, output_dir, config_path)
    elif method == "nodocker":
        return _build_nodocker(tmpl, output_dir, config_path)
    else:
        return {"error": f"Unknown method: {method}. Use: docker, nodocker"}


def _build_docker(tmpl, output_dir: str, config_path: str) -> dict:
    """Build Docker image from template."""
    import tempfile, shutil, textwrap

    # Check Docker is available
    r = subprocess.run(["docker", "info"], capture_output=True, timeout=10)
    if r.returncode != 0:
        return {"error": "Docker not running. Start Docker Desktop or use --method nodocker"}

    # Extra pip packages
    all_packages = list(MESHPOP_PACKAGES) + tmpl.packages
    pip_line = " ".join(all_packages) if all_packages else ""

    # Runtime source
    runtime_src = os.path.join(os.path.dirname(__file__), "runtime.py")

    with tempfile.TemporaryDirectory() as build_dir:
        # Write template config
        with open(os.path.join(build_dir, "template.json"), "w") as f:
            f.write(tmpl.to_json())

        # Copy runtime
        shutil.copy(runtime_src, os.path.join(build_dir, "runtime.py"))

        # Copy extra files from template dir
        tmpl_dir = os.path.dirname(config_path)
        for dest, src in tmpl.files.items():
            src_path = os.path.join(tmpl_dir, src)
            if os.path.exists(src_path):
                shutil.copy(src_path, os.path.join(build_dir, os.path.basename(dest)))

        # Generate Dockerfile
        # Includes wireguard-tools for Wire mesh peer registration
        # Full MeshPOP stack: meshpop + vssh + meshpop-wire + template packages
        dockerfile = textwrap.dedent(f"""
            FROM python:3.11-slim-bookworm
            RUN apt-get update -qq && apt-get install -y -qq \\
                curl iproute2 procps wireguard-tools iptables kmod && \\
                rm -rf /var/lib/apt/lists/*
            RUN pip install --no-cache-dir --break-system-packages \\
                {pip_line}
            RUN mkdir -p /opt/meshclaw /etc/wire /root/.vssh
            COPY template.json /opt/meshclaw/template.json
            COPY runtime.py /opt/meshclaw/runtime.py
            WORKDIR /opt/meshclaw
            ENV RTLINUX_CONFIG=/opt/meshclaw/template.json
            ENV RTLINUX_LOG=/opt/meshclaw/assistant.log
            CMD ["python3", "/opt/meshclaw/runtime.py"]
            LABEL org.meshpop.meshclaw.name="{tmpl.name}" \\
                  org.meshpop.meshclaw.version="{tmpl.version}" \\
                  org.meshpop.meshclaw.model="{tmpl.model}"
        """).strip()

        with open(os.path.join(build_dir, "Dockerfile"), "w") as f:
            f.write(dockerfile + "\n")

        # Build Docker image
        tag = f"meshclaw-{tmpl.name}:{tmpl.version}"
        r = subprocess.run(
            ["docker", "build", "-t", tag, build_dir],
            capture_output=True, text=True, timeout=600,
        )
        if r.returncode != 0:
            return {"error": "Docker build failed", "output": r.stderr[-1000:]}

        # Save image as tar
        image_name = f"meshclaw-{tmpl.name}-{tmpl.version}.tar"
        image_path = os.path.join(output_dir, image_name)
        r2 = subprocess.run(
            ["docker", "save", "-o", image_path, tag],
            capture_output=True, text=True, timeout=120,
        )
        if r2.returncode != 0:
            return {"error": "docker save failed", "output": r2.stderr}

        size = os.path.getsize(image_path)
        return {
            "status": "ok",
            "name": tmpl.name,
            "tag": tag,
            "image": image_path,
            "size_human": f"{size / 1024 / 1024:.0f}MB",
            "run": (
                f"docker run -d --name {tmpl.name} "
                f"--cap-add NET_ADMIN --device /dev/net/tun "
                f"-e ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY "
                f"-e WIRE_RELAY_URL=$WIRE_RELAY_URL "
                f"-e VSSH_SECRET=$VSSH_SECRET "
                f"-e NODE_NAME={tmpl.name} {tag}"
            ),
        }


def _build_nodocker(tmpl, output_dir: str, config_path: str) -> dict:
    """Build nodocker (BusyBox + Python) image from template."""
    # Use the existing nodocker script logic
    script = os.path.join(RTLINUX_DIR, "scripts", "build-from-template.sh")
    if not os.path.exists(script):
        return {"error": f"Build script not found: {script}. Install meshclaw scripts."}

    config_json = os.path.join(output_dir, f"{tmpl.name}-template.json")
    with open(config_json, "w") as f:
        f.write(tmpl.to_json())

    r = subprocess.run(
        ["bash", script, config_json, output_dir],
        capture_output=True, text=True, timeout=1800,
    )
    rootfs = os.path.join(output_dir, f"meshclaw-{tmpl.name}-rootfs.tar.gz")
    if os.path.exists(rootfs):
        size = os.path.getsize(rootfs)
        return {
            "status": "ok",
            "name": tmpl.name,
            "rootfs": rootfs,
            "size_human": f"{size / 1024 / 1024:.1f}MB",
        }
    return {"error": "Build failed", "output": r.stderr[-500:]}


def templates_list(include_registry: bool = False) -> dict:
    """List available templates."""
    builtin = _template.list_builtin()
    result = {"builtin": builtin}
    if include_registry:
        result["community"] = _template.fetch_registry()
    return result


def templates_search(query: str) -> list:
    """Search templates by name or description."""
    query = query.lower()
    all_templates = _template.list_builtin() + _template.fetch_registry()
    return [
        t for t in all_templates
        if query in t.get("name", "").lower()
        or query in t.get("description", "").lower()
    ]




# ── Assistant lifecycle ───────────────────────────────────────────────

def new_assistant(template_name: str, output_dir: str = None, name: str = None) -> dict:
    """Scaffold a new assistant directory from a built-in template.

    Creates a directory with template.yaml ready to customize.

    Args:
        template_name: Built-in template (email, news, code-reviewer, research, system-monitor)
        output_dir:    Where to create the assistant dir (default: current directory)
        name:          Custom name override

    Returns:
        dict with path to created directory
    """
    from meshclaw import template as _tmpl

    tmpl_path = _tmpl.find_builtin(template_name)
    if not tmpl_path:
        available = [t["name"] for t in _tmpl.list_builtin()]
        return {"error": f"Template '{template_name}' not found. Available: {available}"}

    tmpl = _tmpl.load(tmpl_path)
    assistant_name = name or tmpl.name
    out = output_dir or os.getcwd()
    dest_dir = os.path.join(out, assistant_name)

    if os.path.exists(dest_dir):
        return {"error": f"Directory already exists: {dest_dir}"}

    import shutil, textwrap
    os.makedirs(dest_dir)

    # Copy template.yaml with name override
    with open(tmpl_path) as f:
        tmpl_text = f.read()

    if name and name != tmpl.name:
        tmpl_text = tmpl_text.replace(f"name: {tmpl.name}", f"name: {name}", 1)

    with open(os.path.join(dest_dir, "template.yaml"), "w") as f:
        f.write(tmpl_text)

    # Write README
    env_lines = "\n".join(f"  export {e}=..." for e in tmpl.env) or "  # (none required)"
    readme = textwrap.dedent(f"""
        # {assistant_name}

        Built from template: `{template_name}`

        ## Setup

        Edit `template.yaml` to customize your assistant, then set required env vars:

        ```bash
        {env_lines}
        ```

        ## Run

        ```bash
        meshclaw start {dest_dir}
        ```

        ## Talk to it

        ```bash
        # Interactive chat
        meshclaw chat {assistant_name}

        # One-shot question
        meshclaw ask {assistant_name} "your request here"

        # Web/mobile UI
        meshclaw webchat --worker {assistant_name}
        ```

        ## Stop

        ```bash
        meshclaw stop {assistant_name}
        ```
    """).strip()

    with open(os.path.join(dest_dir, "README.md"), "w") as f:
        f.write(readme + "\n")

    return {
        "status": "ok",
        "name": assistant_name,
        "path": dest_dir,
        "next": f"Edit {dest_dir}/template.yaml then run: meshclaw up {dest_dir}",
    }


def up(config_path: str, env: dict = None, detach: bool = True) -> dict:
    """Build and run an assistant in one step (docker-compose style).

    Args:
        config_path: Path to directory containing template.yaml, or template.yaml directly
        env:         Environment variables dict (e.g. {"ANTHROPIC_API_KEY": "sk-..."})
        detach:      Run in background (default: True)

    Returns:
        dict with container status
    """
    # Resolve config path
    if os.path.isdir(config_path):
        config_path = os.path.join(config_path, "template.yaml")

    if not os.path.exists(config_path):
        return {"error": f"Not found: {config_path}"}

    # 1. Build
    build_result = build_from_template(config_path, method="docker")
    if "error" in build_result:
        return build_result

    name = build_result["name"]
    tag  = build_result["tag"]

    # 2. Stop existing container if any
    subprocess.run(["docker", "rm", "-f", name],
                   capture_output=True, timeout=10)

    # 3. Run
    env = env or {}
    env_flags = []
    for k, v in env.items():
        env_flags += ["-e", f"{k}={v}"]
    # Pass through MeshPOP stack env vars from host
    for key in ["ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OLLAMA_URL",
                "WIRE_RELAY_URL", "VSSH_SECRET"]:
        if key not in env and os.environ.get(key):
            env_flags += ["-e", f"{key}={os.environ[key]}"]
    # NODE_NAME defaults to container name
    if "NODE_NAME" not in env:
        env_flags += ["-e", f"NODE_NAME={name}"]

    run_cmd = ["docker", "run", "--name", name]
    if detach:
        run_cmd.append("-d")
    run_cmd += ["--restart", "unless-stopped"]
    # NET_ADMIN + /dev/net/tun required for WireGuard inside container
    run_cmd += ["--cap-add", "NET_ADMIN", "--device", "/dev/net/tun"]
    run_cmd += ["--label", f"org.meshpop.meshclaw.name={name}"]
    run_cmd += env_flags
    run_cmd.append(tag)

    r = subprocess.run(run_cmd, capture_output=True, text=True, timeout=30)
    if r.returncode != 0:
        return {"error": r.stderr.strip(), "cmd": " ".join(run_cmd)}

    return {
        "status": "running",
        "name": name,
        "tag": tag,
        "container_id": r.stdout.strip()[:12],
        "talk": f"mpop ask {name} \"your request here\"",
        "logs": f"meshclaw logs {name}",
    }


def down(name: str) -> dict:
    """Stop and remove a running assistant container."""
    r = subprocess.run(
        ["docker", "rm", "-f", name],
        capture_output=True, text=True, timeout=15,
    )
    if r.returncode == 0:
        return {"status": "stopped", "name": name}
    return {"error": r.stderr.strip(), "name": name}


# ── Mac native workers (launchd) ──────────────────────────────────────

def mac_up(config_path: str, env: dict = None) -> dict:
    """Install and launch an meshclaw worker as a native macOS background daemon.

    No Docker required. Uses launchd (~/Library/LaunchAgents) so the worker
    survives reboots and runs even when no terminal is open.

    The worker gets its own Wire VPN IP, runs a vssh server, listens on a
    Unix socket, and appears in `mpop servers` just like a server-side worker.

    Mac-specific advantage: the worker can call AppleScript, read Calendar,
    Mail, Finder, Reminders, etc. — anything the host Mac can do.

    Args:
        config_path: Path to directory with template.yaml, or template.yaml directly
        env:         Extra environment variables dict

    Returns:
        dict with launchd status

    Example::

        import meshclaw
        meshclaw.mac_up("~/my-worker/template.yaml",
                       env={"ANTHROPIC_API_KEY": "sk-ant-...",
                            "WIRE_RELAY_URL": "http://relay:8790",
                            "VSSH_SECRET": "mysecret"})
    """
    import shutil, textwrap

    # Resolve config
    config_path = os.path.expanduser(config_path)
    if os.path.isdir(config_path):
        config_path = os.path.join(config_path, "template.yaml")
    if not os.path.exists(config_path):
        return {"error": f"Not found: {config_path}"}

    from meshclaw import template as _tmpl
    tmpl = _tmpl.load(config_path)
    name = tmpl.name

    # Worker home dir
    worker_dir = os.path.expanduser(f"~/.meshclaw/{name}")
    os.makedirs(worker_dir, exist_ok=True)

    # 1. Copy runtime.py
    runtime_src = os.path.join(os.path.dirname(__file__), "runtime.py")
    runtime_dst = os.path.join(worker_dir, "runtime.py")
    shutil.copy(runtime_src, runtime_dst)

    # 2. Copy template config
    import json as _json
    config_dst = os.path.join(worker_dir, "template.json")
    with open(config_dst, "w") as f:
        f.write(tmpl.to_json())

    # 3. Install packages natively
    all_packages = list(MESHPOP_PACKAGES) + tmpl.packages
    if all_packages:
        r = subprocess.run(
            [sys.executable, "-m", "pip", "install", "--quiet",
             "--break-system-packages"] + all_packages,
            capture_output=True, text=True, timeout=300,
        )
        if r.returncode != 0:
            return {"error": f"pip install failed: {r.stderr[-500:]}", "packages": all_packages}

    # 4. Collect env vars
    env = env or {}
    env_merged = {}
    # Pull from os.environ if not provided
    for key in ["ANTHROPIC_API_KEY", "OPENAI_API_KEY", "WIRE_RELAY_URL", "VSSH_SECRET",
                "OLLAMA_URL"] + list(tmpl.env):
        val = env.get(key) or os.environ.get(key, "")
        if val:
            env_merged[key] = val
    env_merged.update(env)
    env_merged["NODE_NAME"] = name
    env_merged["RTLINUX_CONFIG"] = config_dst
    env_merged["RTLINUX_LOG"] = os.path.join(worker_dir, "assistant.log")
    env_merged["RTLINUX_MODE"] = "mac"   # tells runtime: reuse existing VPN IP

    # 5. Generate launchd plist
    plist_label = f"meshpop.meshclaw.{name}"
    plist_path = os.path.expanduser(f"~/Library/LaunchAgents/{plist_label}.plist")
    os.makedirs(os.path.expanduser("~/Library/LaunchAgents"), exist_ok=True)

    env_xml = "\n".join(
        f"        <key>{k}</key>\n        <string>{v}</string>"
        for k, v in env_merged.items()
    )

    plist_content = textwrap.dedent(f"""
        <?xml version="1.0" encoding="UTF-8"?>
        <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
          "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
        <plist version="1.0">
        <dict>
            <key>Label</key>
            <string>{plist_label}</string>
            <key>ProgramArguments</key>
            <array>
                <string>{sys.executable}</string>
                <string>{runtime_dst}</string>
            </array>
            <key>EnvironmentVariables</key>
            <dict>
        {env_xml}
            </dict>
            <key>RunAtLoad</key>
            <true/>
            <key>KeepAlive</key>
            <true/>
            <key>StandardOutPath</key>
            <string>{worker_dir}/stdout.log</string>
            <key>StandardErrorPath</key>
            <string>{worker_dir}/stderr.log</string>
            <key>WorkingDirectory</key>
            <string>{worker_dir}</string>
        </dict>
        </plist>
    """).strip()

    with open(plist_path, "w") as f:
        f.write(plist_content + "\n")

    # 6. Unload existing if running, then load
    subprocess.run(["launchctl", "unload", plist_path],
                   capture_output=True, timeout=10)
    r = subprocess.run(["launchctl", "load", plist_path],
                       capture_output=True, text=True, timeout=15)
    if r.returncode != 0:
        return {"error": f"launchctl load failed: {r.stderr.strip()}", "plist": plist_path}

    return {
        "status":   "running",
        "name":     name,
        "mode":     "native-mac",
        "plist":    plist_path,
        "dir":      worker_dir,
        "log":      env_merged["RTLINUX_LOG"],
        "talk":     f"mpop worker ask {name} \"your request here\"",
        "logs_cmd": f"tail -f {env_merged['RTLINUX_LOG']}",
        "stop_cmd": f"launchctl unload {plist_path}",
    }


def mac_down(name: str) -> dict:
    """Stop a native macOS meshclaw worker daemon."""
    plist_label = f"meshpop.meshclaw.{name}"
    plist_path = os.path.expanduser(f"~/Library/LaunchAgents/{plist_label}.plist")

    if not os.path.exists(plist_path):
        return {"error": f"No plist found for '{name}': {plist_path}"}

    r = subprocess.run(["launchctl", "unload", plist_path],
                       capture_output=True, text=True, timeout=15)
    if r.returncode == 0:
        return {"status": "stopped", "name": name, "plist": plist_path}
    return {"error": r.stderr.strip(), "name": name}


def mac_ps() -> dict:
    """List running native macOS meshclaw workers."""
    r = subprocess.run(
        ["launchctl", "list"],
        capture_output=True, text=True, timeout=10,
    )
    workers = []
    for line in r.stdout.splitlines():
        if "meshpop.meshclaw." in line:
            parts = line.split("\t")
            label = parts[2] if len(parts) > 2 else line.strip()
            pid   = parts[0].strip() if len(parts) > 0 else "-"
            name  = label.replace("meshpop.meshclaw.", "")
            workers.append({
                "name":   name,
                "pid":    pid,
                "label":  label,
                "status": "running" if pid != "-" else "stopped",
            })
    return {"workers": workers, "count": len(workers)}


# ── Remote worker deployment (SSH-based) ─────────────────────────────

def remote_up(host: str, config_path: str, env: dict = None,
              ssh_user: str = "root", ssh_port: int = 22,
              ssh_key: str = None) -> dict:
    """Deploy and start an meshclaw worker on a remote server via SSH.

    Installs meshclaw on the remote server if not present, uploads the
    template config, and starts the worker as a background process.

    Args:
        host:        Remote server hostname or IP
        config_path: Path to template.yaml (local) or built-in template name
        env:         Environment variables to set on the remote worker
        ssh_user:    SSH username (default: root)
        ssh_port:    SSH port (default: 22)
        ssh_key:     Path to SSH private key (default: uses ssh-agent)

    Returns:
        dict with remote worker status

    Example::

        import meshclaw
        # Deploy a system monitor to any VPS
        meshclaw.remote_up("my-vps.example.com",
                          "system-monitor",
                          env={"OLLAMA_URL": "http://localhost:11434"})
        # Use Ollama on the remote server — no API key needed
        meshclaw.remote_up("192.168.1.100",
                          "assistant",
                          env={"ANTHROPIC_API_KEY": "sk-ant-..."})
    """
    import shutil, tempfile

    # Resolve config
    config_path = os.path.expanduser(str(config_path))
    if not os.path.exists(config_path) and not config_path.endswith((".yaml", ".yml", ".json")):
        from meshclaw import template as _tmpl
        found = _tmpl.find_builtin(config_path)
        if found:
            config_path = found
        else:
            available = [t["name"] for t in _tmpl.list_builtin()]
            return {"error": f"Template '{config_path}' not found. Available: {available}"}

    if os.path.isdir(config_path):
        for ext in ("template.yaml", "template.yml", "template.json"):
            candidate = os.path.join(config_path, ext)
            if os.path.exists(candidate):
                config_path = candidate
                break

    if not os.path.exists(config_path):
        return {"error": f"Not found: {config_path}"}

    from meshclaw import template as _tmpl
    tmpl = _tmpl.load(config_path)
    name = tmpl.name

    # SSH helpers
    ssh_opts = ["-o", "StrictHostKeyChecking=no",
                "-o", "ConnectTimeout=15",
                "-p", str(ssh_port)]
    if ssh_key:
        ssh_opts += ["-i", ssh_key]
    ssh_target = f"{ssh_user}@{host}"

    def _ssh(cmd: str) -> tuple:
        r = subprocess.run(
            ["ssh"] + ssh_opts + [ssh_target, cmd],
            capture_output=True, text=True, timeout=120,
        )
        return r.returncode, r.stdout.strip(), r.stderr.strip()

    def _scp(local: str, remote: str) -> bool:
        r = subprocess.run(
            ["scp"] + [o for o in ssh_opts if o != "-p"] +
            ["-P", str(ssh_port)] + [local, f"{ssh_target}:{remote}"],
            capture_output=True, text=True, timeout=60,
        )
        return r.returncode == 0

    # 1. Test SSH
    rc, out, err = _ssh("echo ok")
    if rc != 0 or out != "ok":
        return {"error": f"SSH failed: {err or 'connection refused'}",
                "host": host, "user": ssh_user}

    # 2. Install meshclaw + packages
    install_pkgs = ["meshclaw"] + tmpl.packages
    rc, out, err = _ssh(
        f"pip3 install --break-system-packages --quiet --upgrade {' '.join(install_pkgs)} 2>&1 | tail -3"
    )
    if rc != 0:
        return {"error": f"pip install failed: {err}", "host": host}

    # 3. Upload runtime.py + template.json
    runtime_src = os.path.join(os.path.dirname(__file__), "runtime.py")
    remote_dir = f"/opt/meshclaw/{name}"
    _ssh(f"mkdir -p {remote_dir}")
    _scp(runtime_src, f"{remote_dir}/runtime.py")

    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        f.write(tmpl.to_json())
        tmp_json = f.name
    _scp(tmp_json, f"{remote_dir}/template.json")
    os.unlink(tmp_json)

    # 4. Build env exports
    env = env or {}
    env_str = ""
    for key in list(tmpl.env) + list(env.keys()):
        val = env.get(key) or os.environ.get(key, "")
        if val:
            env_str += f"export {key}='{val}'; "
    env_str += (f"export RTLINUX_CONFIG={remote_dir}/template.json; "
                f"export RTLINUX_LOG={remote_dir}/assistant.log; "
                f"export NODE_NAME={name}; ")

    # 5. Kill existing, remove old socket
    sock_path = f"/tmp/meshclaw-{name}.sock"
    _ssh(f"pkill -f '{remote_dir}/runtime.py' 2>/dev/null; rm -f {sock_path}; sleep 0.3")

    # 6. Start background worker
    rc, pid, err = _ssh(
        f"nohup bash -c '{env_str} python3 {remote_dir}/runtime.py'"
        f" > {remote_dir}/stdout.log 2>&1 & echo $!"
    )
    if rc != 0:
        return {"error": f"Failed to start: {err}", "host": host}

    # 7. Wait for socket
    import time
    for _ in range(15):
        rc2, out2, _ = _ssh(f"test -S {sock_path} && echo ready || echo waiting")
        if out2 == "ready":
            break
        time.sleep(1)

    return {
        "status":  "running" if out2 == "ready" else "starting",
        "name":    name,
        "host":    host,
        "pid":     pid.strip(),
        "socket":  f"{ssh_target}:{sock_path}",
        "log":     f"{remote_dir}/assistant.log",
        "stop":    f"meshclaw remote-down {host} {name}",
    }


def remote_down(host: str, name: str, ssh_user: str = "root",
                ssh_port: int = 22, ssh_key: str = None) -> dict:
    """Stop a remote meshclaw worker."""
    ssh_opts = ["-o", "StrictHostKeyChecking=no", "-P", str(ssh_port)]
    if ssh_key:
        ssh_opts += ["-i", ssh_key]
    r = subprocess.run(
        ["ssh"] + ["-o", "StrictHostKeyChecking=no", "-p", str(ssh_port)] +
        (["-i", ssh_key] if ssh_key else []) +
        [f"{ssh_user}@{host}",
         f"pkill -f '/opt/meshclaw/{name}/runtime.py'; rm -f /tmp/meshclaw-{name}.sock && echo stopped"],
        capture_output=True, text=True, timeout=30,
    )
    if "stopped" in r.stdout:
        return {"status": "stopped", "name": name, "host": host}
    return {"status": "unknown", "host": host, "output": (r.stdout + r.stderr)[:200]}


# ── Standalone (cross-platform, no Docker) ──────────────────────────

def worker_up(config_path: str, env: dict = None, foreground: bool = False) -> dict:
    """Start an meshclaw worker as a native background process.

    Works on macOS, Linux, or any system with Python 3.8+.
    No Docker, no Wire VPN, no MeshPOP required.

    The worker listens on a Unix socket at /tmp/meshclaw-<name>.sock.
    Talk to it via:
        meshclaw chat <name>
        meshclaw webchat --worker <name>

    Args:
        config_path:  Path to directory with template.yaml, or template.yaml directly.
                      Can also be a built-in template name (e.g. "assistant", "system-monitor").
        env:          Extra environment variables dict (e.g. {"ANTHROPIC_API_KEY": "sk-..."})
        foreground:   If True, run in foreground (blocks). Default: background.

    Returns:
        dict with worker status, socket path, log path

    Example::

        import meshclaw
        meshclaw.worker_up("assistant", env={"ANTHROPIC_API_KEY": "sk-ant-..."})
        meshclaw.worker_up("~/my-bot/template.yaml")
    """
    import shutil

    # Resolve config — support built-in template name
    config_path = os.path.expanduser(str(config_path))
    if not os.path.exists(config_path) and not config_path.endswith((".yaml", ".yml", ".json")):
        # Try built-in template
        from meshclaw import template as _tmpl
        found = _tmpl.find_builtin(config_path)
        if found:
            config_path = found
        else:
            available = [t["name"] for t in _tmpl.list_builtin()]
            return {"error": f"Template '{config_path}' not found. Available: {available}"}

    if os.path.isdir(config_path):
        for ext in ("template.yaml", "template.yml", "template.json"):
            candidate = os.path.join(config_path, ext)
            if os.path.exists(candidate):
                config_path = candidate
                break

    if not os.path.exists(config_path):
        return {"error": f"Not found: {config_path}"}

    from meshclaw import template as _tmpl
    tmpl = _tmpl.load(config_path)
    name = tmpl.name

    # Worker home dir
    worker_dir = os.path.expanduser(f"~/.meshclaw/{name}")
    os.makedirs(worker_dir, exist_ok=True)

    # Copy runtime.py
    runtime_src = os.path.join(os.path.dirname(__file__), "runtime.py")
    runtime_dst = os.path.join(worker_dir, "runtime.py")
    shutil.copy(runtime_src, runtime_dst)

    # Write template config
    config_dst = os.path.join(worker_dir, "template.json")
    with open(config_dst, "w") as f:
        f.write(tmpl.to_json())

    # Install worker packages
    worker_packages = [p for p in tmpl.packages if p not in ("anthropic",)] + ["anthropic"]
    # De-duplicate
    seen = set()
    uniq_packages = [p for p in worker_packages if not (p in seen or seen.add(p))]
    if uniq_packages:
        r = subprocess.run(
            [sys.executable, "-m", "pip", "install", "--quiet",
             "--break-system-packages"] + uniq_packages,
            capture_output=True, text=True, timeout=300,
        )
        if r.returncode != 0:
            return {"error": f"pip install failed: {r.stderr[-400:]}"}

    # Build env
    env = env or {}
    worker_env = dict(os.environ)
    # Pull API keys from host env if not provided
    for key in ["ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OLLAMA_URL",
                "CURSOR_API_KEY"] + list(tmpl.env):
        if key in env:
            worker_env[key] = env[key]
        elif key in os.environ:
            worker_env[key] = os.environ[key]
    worker_env.update(env)
    worker_env["RTLINUX_CONFIG"] = config_dst
    log_path = os.path.join(worker_dir, "assistant.log")
    worker_env["RTLINUX_LOG"] = log_path
    worker_env["NODE_NAME"] = name

    pid_path = os.path.join(worker_dir, "worker.pid")
    sock_path = f"/tmp/meshclaw-{name}.sock"

    if foreground:
        # Run directly (blocks)
        os.execve(sys.executable, [sys.executable, runtime_dst], worker_env)
        return {}  # unreachable

    # Kill any existing instance
    if os.path.exists(pid_path):
        try:
            with open(pid_path) as f:
                old_pid = int(f.read().strip())
            import signal
            os.kill(old_pid, signal.SIGTERM)
            import time; time.sleep(0.5)
        except Exception:
            pass
        try:
            os.remove(pid_path)
        except Exception:
            pass
    if os.path.exists(sock_path):
        try:
            os.remove(sock_path)
        except Exception:
            pass

    # Start background process
    stdout_log = open(os.path.join(worker_dir, "stdout.log"), "a")
    proc = subprocess.Popen(
        [sys.executable, "-u", runtime_dst],
        env=worker_env,
        stdout=stdout_log,
        stderr=subprocess.STDOUT,
        start_new_session=True,  # detach from terminal
    )

    # Write PID file
    with open(pid_path, "w") as f:
        f.write(str(proc.pid))

    # Wait briefly for socket to appear
    import time
    for _ in range(20):
        if os.path.exists(sock_path):
            break
        time.sleep(0.3)

    running = os.path.exists(sock_path)
    return {
        "status":   "running" if running else "starting",
        "name":     name,
        "pid":      proc.pid,
        "socket":   sock_path,
        "log":      log_path,
        "dir":      worker_dir,
        "chat":     f"meshclaw chat {name}",
        "webchat":  f"meshclaw webchat --worker {name}",
        "stop":     f"meshclaw stop {name}",
    }


def worker_down(name: str) -> dict:
    """Stop a running meshclaw worker (cross-platform)."""
    import signal

    worker_dir = os.path.expanduser(f"~/.meshclaw/{name}")
    pid_path = os.path.join(worker_dir, "worker.pid")
    sock_path = f"/tmp/meshclaw-{name}.sock"

    if not os.path.exists(pid_path):
        # Try mac launchd as fallback
        return mac_down(name)

    try:
        with open(pid_path) as f:
            pid = int(f.read().strip())
        os.kill(pid, signal.SIGTERM)
        import time; time.sleep(0.5)
        try:
            os.kill(pid, 0)
            os.kill(pid, signal.SIGKILL)  # force if still alive
        except ProcessLookupError:
            pass
        os.remove(pid_path)
        if os.path.exists(sock_path):
            os.remove(sock_path)
        return {"status": "stopped", "name": name}
    except ProcessLookupError:
        for path in (pid_path, sock_path):
            try:
                os.remove(path)
            except Exception:
                pass
        return {"status": "stopped", "name": name, "note": "process already gone"}
    except Exception as e:
        return {"error": str(e), "name": name}


def worker_ps() -> dict:
    """List all running meshclaw workers by scanning Unix sockets."""
    import glob
    workers = []
    for sock in sorted(glob.glob("/tmp/meshclaw-*.sock")):
        name = sock.replace("/tmp/meshclaw-", "").replace(".sock", "")
        worker_dir = os.path.expanduser(f"~/.meshclaw/{name}")
        pid_path = os.path.join(worker_dir, "worker.pid")
        pid = None
        if os.path.exists(pid_path):
            try:
                with open(pid_path) as f:
                    pid = int(f.read().strip())
                # Verify process is alive
                import signal
                os.kill(pid, 0)
            except Exception:
                pid = None

        # Load template info if available
        config_path = os.path.join(worker_dir, "template.json")
        model = "?"
        description = ""
        if os.path.exists(config_path):
            try:
                with open(config_path) as f:
                    cfg = json.load(f)
                model = cfg.get("model", "?")
                description = cfg.get("description", "")
            except Exception:
                pass

        workers.append({
            "name":        name,
            "pid":         pid,
            "socket":      sock,
            "model":       model,
            "description": description,
            "status":      "running" if pid else "socket-only",
        })
    return {"workers": workers, "count": len(workers)}


def ask_worker(name: str, message: str, timeout: int = 60) -> str:
    """Send a message to a running worker and return the response.

    Args:
        name:    Worker name (matches /tmp/meshclaw-<name>.sock)
        message: Message to send
        timeout: Seconds to wait for response (default 60)

    Returns:
        str response from worker

    Example::

        import meshclaw
        reply = meshclaw.ask_worker("my-bot", "what is the weather in Seoul?")
        print(reply)
    """
    sock_path = f"/tmp/meshclaw-{name}.sock"
    if not os.path.exists(sock_path):
        # Try finding workers and suggest
        import glob
        running = [s.replace("/tmp/meshclaw-", "").replace(".sock", "")
                   for s in glob.glob("/tmp/meshclaw-*.sock")]
        if running:
            return f"Worker '{name}' not running. Running workers: {running}"
        return f"No workers running. Start one with: meshclaw start {name}"

    import socket as _socket
    try:
        payload = json.dumps({"message": message}) + "\n"
        with _socket.socket(_socket.AF_UNIX, _socket.SOCK_STREAM) as s:
            s.settimeout(timeout)
            s.connect(sock_path)
            s.sendall(payload.encode())
            chunks = []
            while True:
                data = s.recv(65536)
                if not data:
                    break
                chunks.append(data)
                # Check if response is complete (ends with newline after JSON)
                combined = b"".join(chunks)
                try:
                    resp = json.loads(combined.decode())
                    return resp.get("response", resp.get("error", str(resp)))
                except json.JSONDecodeError:
                    continue
            combined = b"".join(chunks)
            try:
                resp = json.loads(combined.decode())
                return resp.get("response", str(resp))
            except Exception:
                return combined.decode().strip()
    except Exception as e:
        return f"Error talking to worker '{name}': {e}"


def chat_worker(name: str):
    """Interactive chat session with a running worker.

    Opens a REPL-style chat with the named worker.
    Type 'exit' or press Ctrl+C to quit.

    Args:
        name: Worker name
    """
    sock_path = f"/tmp/meshclaw-{name}.sock"
    if not os.path.exists(sock_path):
        import glob
        running = [s.replace("/tmp/meshclaw-", "").replace(".sock", "")
                   for s in glob.glob("/tmp/meshclaw-*.sock")]
        if running:
            print(f"Worker '{name}' not found. Running: {', '.join(running)}")
        else:
            print(f"No workers running. Start one:\n  meshclaw start {name}")
        return

    print(f"💬 meshclaw chat — {name}  (Ctrl+C to exit)\n")
    try:
        while True:
            try:
                msg = input("You: ").strip()
            except EOFError:
                break
            if not msg:
                continue
            if msg.lower() in ("exit", "quit", "bye", "/exit"):
                print("Bye!")
                break
            response = ask_worker(name, msg)
            print(f"\n{name}: {response}\n")
    except KeyboardInterrupt:
        print("\nBye!")


def ps() -> dict:
    """List running meshclaw assistant containers."""
    r = subprocess.run(
        ["docker", "ps", "--filter", "label=org.meshpop.meshclaw.name",
         "--format", "{{.Names}}\t{{.Status}}\t{{.Image}}"],
        capture_output=True, text=True, timeout=10,
    )
    if r.returncode != 0:
        return {"error": r.stderr.strip()}

    assistants = []
    for line in r.stdout.strip().splitlines():
        if not line:
            continue
        parts = line.split("\t")
        assistants.append({
            "name":   parts[0] if len(parts) > 0 else "?",
            "status": parts[1] if len(parts) > 1 else "?",
            "image":  parts[2] if len(parts) > 2 else "?",
        })
    return {"assistants": assistants, "count": len(assistants)}


def logs(name: str, lines: int = 50) -> str:
    """Get logs from a running assistant container."""
    r = subprocess.run(
        ["docker", "logs", "--tail", str(lines), name],
        capture_output=True, text=True, timeout=10,
    )
    return (r.stdout + r.stderr).strip() or "(no logs)"


def create_assistant(description: str, name: str = None, output_dir: str = None,
                     model: str = "claude-sonnet-4-6") -> dict:
    """AI-powered assistant creation from natural language description.

    Generates a complete template.yaml from the user\'s description,
    then builds and runs the assistant automatically.

    Args:
        description: What you want the assistant to do (natural language)
        name:        Assistant name (auto-generated if not provided)
        output_dir:  Where to create files (default: ~/meshpoplinux/assistants)
        model:       LLM model to use

    Returns:
        dict with created template and run status

    Example::

        create_assistant("Monitor my inbox and send me a daily digest at 9am")
        create_assistant("Watch Hacker News and alert me when something is trending about AI")
    """
    import re

    # Derive name from description if not given
    if not name:
        words = re.sub(r"[^a-z0-9 ]", "", description.lower()).split()[:3]
        name = "-".join(words) or "my-assistant"

    out = output_dir or os.path.expanduser("~/meshpoplinux/assistants")
    dest_dir = os.path.join(out, name)
    os.makedirs(dest_dir, exist_ok=True)

    # Infer template fields from description
    schedule = None
    schedule_task = None
    packages = ["anthropic"]
    tools = []

    desc_lower = description.lower()

    # Schedule hints
    if any(x in desc_lower for x in ["every hour", "hourly"]):
        schedule, schedule_task = "every 1h", description
    elif any(x in desc_lower for x in ["every 30", "half hour"]):
        schedule, schedule_task = "every 30m", description
    elif any(x in desc_lower for x in ["daily", "every day", "each day", "9am", "morning"]):
        schedule, schedule_task = "every 24h", description
    elif any(x in desc_lower for x in ["every 15", "15 min"]):
        schedule, schedule_task = "every 15m", description

    # Tool hints
    if any(x in desc_lower for x in ["news", "web", "search", "internet", "hacker", "reddit"]):
        tools.append("web_search")
    if any(x in desc_lower for x in ["file", "meshdb", "index", "find"]):
        tools.append("meshdb_search")

    # Package hints
    if any(x in desc_lower for x in ["email", "inbox", "imap", "gmail"]):
        packages.append("imapclient")
    if any(x in desc_lower for x in ["rss", "feed", "news"]):
        packages.append("feedparser")
    if any(x in desc_lower for x in ["system", "cpu", "memory", "disk", "monitor"]):
        packages.append("psutil")

    # Required env vars
    env_vars = ["ANTHROPIC_API_KEY"]
    if any(x in desc_lower for x in ["email", "inbox", "imap", "gmail"]):
        env_vars += ["IMAP_HOST", "IMAP_USER", "IMAP_PASS"]

    # Build template yaml
    tools_yaml = "\n".join(f"  - {t}" for t in tools) if tools else ""
    pkgs_yaml  = "\n".join(f"  - {p}" for p in packages)
    env_yaml   = "\n".join(f"  - {e}" for e in env_vars)
    sched_yaml = f"schedule: \"every 1h\"\nschedule_task: \"{description}\"" if schedule else ""

    yaml_content = f"""name: {name}
description: "{description[:80]}"
version: "1.0.0"
model: {model}
system_prompt: |
  You are a specialized AI assistant. Your purpose:
  {description}

  Be concise, accurate, and actionable.
tools:
{tools_yaml if tools_yaml else "  []"}
packages:
{pkgs_yaml}
{sched_yaml}
on_message: "{description[:80]}"
env:
{env_yaml}
"""

    tmpl_path = os.path.join(dest_dir, "template.yaml")
    with open(tmpl_path, "w") as f:
        f.write(yaml_content)

    return {
        "status": "created",
        "name": name,
        "path": tmpl_path,
        "template_preview": yaml_content,
        "next_steps": [
            f"Review: cat {tmpl_path}",
            f"Start: meshclaw start {dest_dir}",
            f"Chat: meshclaw chat {name}",
            f"Web UI: meshclaw webchat --worker {name}",
        ],
    }


# ── CLI ─────────────────────────────────────────────────────────────
def main():
    """CLI entry point."""
    import argparse

    parser = argparse.ArgumentParser(
        prog="meshclaw",
        description="AI Worker Runtime — run AI assistants anywhere",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
examples:
  meshclaw init my-bot              scaffold a new assistant
  meshclaw start my-bot             start the assistant in background
  meshclaw chat my-bot              interactive chat
  meshclaw ask my-bot "hi"          one-shot message
  meshclaw webchat --worker my-bot  web/mobile UI on port 8080
  meshclaw ps                       list running workers
  meshclaw stop my-bot              stop the worker
  meshclaw templates list           show built-in templates
""",
    )
    sub = parser.add_subparsers(dest="cmd")

    # ── STANDALONE (primary) ──────────────────────────────────────────

    # init — scaffold new assistant
    p_init = sub.add_parser("init", help="Scaffold a new assistant from a template")
    p_init.add_argument("name", help="Assistant name (e.g. my-bot)")
    p_init.add_argument("--template", "-t", default="assistant",
                        help="Built-in template to use (default: assistant)")
    p_init.add_argument("--output", "-o", default=None,
                        help="Output directory (default: current dir)")

    # start — run worker natively
    p_start = sub.add_parser("start", help="Start an assistant worker in background")
    p_start.add_argument("name_or_path",
                         help="Worker name, built-in template, or path to template.yaml")
    p_start.add_argument("--foreground", "-f", action="store_true",
                         help="Run in foreground (default: background)")
    p_start.add_argument("--env", "-e", action="append", default=[],
                         metavar="KEY=VALUE", help="Extra env vars (repeatable)")

    # stop — stop worker
    p_stop = sub.add_parser("stop", help="Stop a running worker")
    p_stop.add_argument("name", help="Worker name")

    # restart
    p_restart = sub.add_parser("restart", help="Restart a worker")
    p_restart.add_argument("name_or_path", help="Worker name or path")

    # ps — list workers
    sub.add_parser("ps", help="List running workers")

    # chat — interactive chat
    p_chat = sub.add_parser("chat", help="Interactive chat with a worker")
    p_chat.add_argument("name", help="Worker name")

    # ask — one-shot message
    p_ask = sub.add_parser("ask", help="Send a one-shot message to a worker")
    p_ask.add_argument("name", help="Worker name")
    p_ask.add_argument("message", help="Message to send")
    p_ask.add_argument("--timeout", "-t", type=int, default=60,
                       help="Timeout in seconds (default: 60)")

    # webchat — web UI
    p_webchat = sub.add_parser("webchat", help="Start web/mobile chat UI")
    p_webchat.add_argument("--worker", "-w", default=os.environ.get("RTLINUX_WORKER", ""),
                           help="Worker name (auto-detects if empty)")
    p_webchat.add_argument("--host", default=os.environ.get("RTLINUX_HOST", ""),
                           help="Remote server IP (empty for local)")
    p_webchat.add_argument("--port", "-p", type=int,
                           default=int(os.environ.get("WEBCHAT_PORT", "8080")),
                           help="Port (default: 8080)")
    p_webchat.add_argument("--ngrok", action="store_true",
                           help="Expose publicly via ngrok tunnel")

    # templates
    p_tmpl = sub.add_parser("templates", help="Browse built-in templates")
    tmpl_sub = p_tmpl.add_subparsers(dest="tmpl_cmd")
    tmpl_sub.add_parser("list", help="List built-in templates")
    p_tsearch = tmpl_sub.add_parser("search", help="Search templates")
    p_tsearch.add_argument("query", help="Search term")

    # version
    sub.add_parser("version", help="Show version")

    # ── ADVANCED (Docker / fleet) ─────────────────────────────────────

    # build (base image)
    p_build = sub.add_parser("build", help="[advanced] Build Docker image from template")
    p_build.add_argument("--method", default="docker",
                         choices=["docker", "nodocker", "buildroot", "alpine", "scratch"])
    p_build.add_argument("--output", default=None)
    p_build.add_argument("--config", default=None, metavar="TEMPLATE")

    # deploy (fleet)
    p_deploy = sub.add_parser("deploy", help="[advanced] Deploy to a remote node via vssh")
    p_deploy.add_argument("node")
    p_deploy.add_argument("--image", default=None)
    p_deploy.add_argument("--no-stack", action="store_true")

    # verify (fleet)
    p_verify = sub.add_parser("verify", help="[advanced] Verify node health")
    p_verify.add_argument("node", nargs="?")
    p_verify.add_argument("--all", action="store_true")
    p_verify.add_argument("--nodes", default=os.environ.get("RTLINUX_NODES", ""))

    # status (fleet)
    sub.add_parser("status", help="[advanced] Fleet status")

    # remote-up / remote-down
    p_remup = sub.add_parser("remote-up", help="Deploy a worker to a remote server via SSH")
    p_remup.add_argument("host", help="Remote server hostname or IP")
    p_remup.add_argument("template", help="Built-in template name or path to template.yaml")
    p_remup.add_argument("--user", "-u", default="root", help="SSH user (default: root)")
    p_remup.add_argument("--port", type=int, default=22, help="SSH port (default: 22)")
    p_remup.add_argument("--key", "-i", default=None, help="SSH private key path")
    p_remup.add_argument("--env", "-e", action="append", default=[],
                         metavar="KEY=VALUE", help="Env vars to set on remote (repeatable)")

    p_remdn = sub.add_parser("remote-down", help="Stop a remote worker")
    p_remdn.add_argument("host", help="Remote server hostname or IP")
    p_remdn.add_argument("name", help="Worker name")
    p_remdn.add_argument("--user", "-u", default="root")
    p_remdn.add_argument("--port", type=int, default=22)
    p_remdn.add_argument("--key", "-i", default=None)

    # mac-up / mac-down
    p_macup = sub.add_parser("mac-up", help="[macOS] Install worker as launchd daemon")
    p_macup.add_argument("config_path", help="Path to template.yaml")
    p_macdn = sub.add_parser("mac-down", help="[macOS] Stop launchd worker")
    p_macdn.add_argument("name")
    sub.add_parser("mac-ps", help="[macOS] List launchd workers")

    # ─────────────────────────────────────────────────────────────────
    args = parser.parse_args()

    # ── Standalone commands ───────────────────────────────────────────

    if args.cmd == "init":
        result = new_assistant(args.template, output_dir=args.output, name=args.name)
        if "error" in result:
            print(f"Error: {result['error']}")
            sys.exit(1)
        print(f"✓ Created: {result['path']}")
        print(f"\nNext steps:")
        print(f"  1. Edit {result['path']}/template.yaml")
        print(f"  2. export ANTHROPIC_API_KEY=sk-ant-...")
        print(f"  3. meshclaw start {args.name}")
        print(f"  4. meshclaw chat {args.name}")

    elif args.cmd == "start":
        # Parse --env KEY=VALUE pairs
        extra_env = {}
        for kv in (args.env or []):
            if "=" in kv:
                k, v = kv.split("=", 1)
                extra_env[k] = v
        result = worker_up(args.name_or_path, env=extra_env or None,
                           foreground=args.foreground)
        if "error" in result:
            print(f"Error: {result['error']}")
            sys.exit(1)
        print(f"✓ {result['name']} is {result['status']}  (pid {result.get('pid', '?')})")
        print(f"  socket: {result.get('socket', '?')}")
        print(f"  log:    {result.get('log', '?')}")
        print(f"\nTalk to it:")
        print(f"  meshclaw chat {result['name']}")
        print(f"  meshclaw webchat --worker {result['name']}")

    elif args.cmd == "stop":
        result = worker_down(args.name)
        if "error" in result:
            print(f"Error: {result['error']}")
        else:
            print(f"✓ Stopped: {args.name}")

    elif args.cmd == "restart":
        r1 = worker_down(args.name_or_path.split("/")[-1].replace("template.yaml","").strip("/") or args.name_or_path)
        r2 = worker_up(args.name_or_path)
        if "error" in r2:
            print(f"Error: {r2['error']}")
            sys.exit(1)
        print(f"✓ Restarted: {r2['name']}  (pid {r2.get('pid', '?')})")

    elif args.cmd == "ps":
        result = worker_ps()
        if not result["workers"]:
            print("No workers running.")
            print("Start one with:  meshclaw start <name>")
        else:
            print(f"{'NAME':<20} {'PID':<8} {'MODEL':<30} STATUS")
            print("-" * 72)
            for w in result["workers"]:
                pid = str(w["pid"]) if w["pid"] else "-"
                print(f"{w['name']:<20} {pid:<8} {w['model']:<30} {w['status']}")

    elif args.cmd == "chat":
        chat_worker(args.name)

    elif args.cmd == "ask":
        response = ask_worker(args.name, args.message, timeout=args.timeout)
        print(response)

    elif args.cmd == "webchat":
        import importlib
        webchat = importlib.import_module("meshclaw.webchat")
        wc_argv = ["webchat"]
        if args.worker:
            wc_argv += ["--worker", args.worker]
        if args.host:
            wc_argv += ["--host", args.host]
        if args.port:
            wc_argv += ["--port", str(args.port)]
        if args.ngrok:
            wc_argv += ["--ngrok"]
        sys.argv = wc_argv
        webchat.main()

    elif args.cmd == "templates":
        if not hasattr(args, "tmpl_cmd") or not args.tmpl_cmd:
            p_tmpl.print_help()
        elif args.tmpl_cmd == "list":
            result = templates_list()
            builtin = result.get("builtin", [])
            if builtin:
                print(f"{'NAME':<20} {'MODEL':<30} DESCRIPTION")
                print("-" * 72)
                for t in builtin:
                    print(f"{t['name']:<20} {t.get('model','?'):<30} {t.get('description','')[:40]}")
            else:
                print(json.dumps(result, indent=2))
        elif args.tmpl_cmd == "search":
            result = templates_search(args.query)
            print(json.dumps(result, indent=2))
        else:
            p_tmpl.print_help()

    elif args.cmd == "version":
        print(f"meshclaw {__version__}")

    # ── Advanced / fleet commands ─────────────────────────────────────

    elif args.cmd == "build":
        result = build(method=args.method, output_dir=args.output)
        print(json.dumps(result, indent=2))

    elif args.cmd == "deploy":
        result = deploy(args.node, image=args.image, install_stack=not args.no_stack)
        print(json.dumps(result, indent=2))

    elif args.cmd == "verify":
        if args.all or not args.node:
            nodes = args.nodes.split()
            result = verify_fleet(nodes)
        else:
            result = verify(args.node)
        print(json.dumps(result, indent=2))

    elif args.cmd == "status":
        nodes_env = os.environ.get("RTLINUX_NODES", "")
        nodes = nodes_env.split() if nodes_env else []
        result = verify_fleet(nodes)
        print(json.dumps(result, indent=2))

    elif args.cmd == "remote-up":
        extra_env = {}
        for kv in (args.env or []):
            if "=" in kv:
                k, v = kv.split("=", 1)
                extra_env[k] = v
        result = remote_up(args.host, args.template,
                           env=extra_env or None,
                           ssh_user=args.user,
                           ssh_port=args.port,
                           ssh_key=args.key)
        if "error" in result:
            print(f"Error: {result['error']}")
            sys.exit(1)
        print(f"✓ {result['name']} is {result['status']} on {result['host']}  (pid {result.get('pid', '?')})")
        print(f"  log: {result.get('log', '?')}")
        print(f"  stop: {result.get('stop', '?')}")

    elif args.cmd == "remote-down":
        result = remote_down(args.host, args.name,
                             ssh_user=args.user,
                             ssh_port=args.port,
                             ssh_key=args.key)
        print(json.dumps(result, indent=2))

    elif args.cmd == "mac-up":
        result = mac_up(args.config_path)
        print(json.dumps(result, indent=2))

    elif args.cmd == "mac-down":
        result = mac_down(args.name)
        print(json.dumps(result, indent=2))

    elif args.cmd == "mac-ps":
        result = mac_ps()
        print(json.dumps(result, indent=2))

    else:
        parser.print_help()


# ── Messenger ─────────────────────────────────────────────────────────────────
# Notify via Telegram, Slack, Discord, or Webhook — no mpop required.
#
# Usage:
#   from meshclaw import notify
#   notify("telegram", "Your message", token="...", chat_id="...")
#
#   # Or use adapters directly:
#   from meshclaw.messenger import TelegramAdapter
#   bot = TelegramAdapter(token="...")
#   bot.send(chat_id="12345", text="Worker done!")

try:
    from meshclaw.messenger import (
        TelegramAdapter,
        SlackAdapter,
        DiscordAdapter,
        WebhookAdapter,
        create_adapter,
    )

    def notify(platform: str, message: str, **kwargs) -> bool:
        """Send a notification from any meshclaw worker.

        Args:
            platform: "telegram", "slack", "discord", or "webhook"
            message:  Text to send
            **kwargs: Platform-specific config:
                      telegram → token=str, chat_id=str
                      slack    → webhook_url=str
                      discord  → webhook_url=str
                      webhook  → url=str

        Returns:
            True on success, False on failure
        """
        try:
            if platform == "telegram":
                adapter = TelegramAdapter(token=kwargs["token"])
                adapter.send(chat_id=kwargs["chat_id"], text=message)
            elif platform in ("slack", "discord"):
                adapter = (SlackAdapter if platform == "slack" else DiscordAdapter)(
                    webhook_url=kwargs["webhook_url"]
                )
                adapter.send(chat_id="", text=message)
            elif platform == "webhook":
                adapter = WebhookAdapter(url=kwargs["url"])
                adapter.send(chat_id="", text=message)
            else:
                raise ValueError(f"Unknown platform: {platform}")
            return True
        except Exception as e:
            print(f"[meshclaw.notify] {platform} failed: {e}")
            return False

except ImportError:
    pass


if __name__ == "__main__":
    main()
