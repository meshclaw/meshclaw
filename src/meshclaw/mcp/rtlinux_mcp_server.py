#!/usr/bin/env python3
"""
meshclaw MCP Server — Runtime Linux node management via MCP protocol.

MCP Tools:
    meshclaw_build     — Build runtime image (buildroot/alpine/scratch)
    meshclaw_deploy    — Deploy image to a node
    meshclaw_verify    — Verify node health across all 5 layers
    meshclaw_fleet     — Fleet status overview
    meshclaw_fix       — Auto-fix a degraded node
    meshclaw_cicd      — Full CI/CD loop (build→deploy→verify→fix)

Usage:
    python meshclaw_mcp_server.py              # stdio mode
    python meshclaw_mcp_server.py --port 8098  # HTTP mode
"""

import json
import sys
import os

import meshclaw


# ── MCP Protocol ────────────────────────────────────────────────────

TOOLS = [
    {
        "name": "meshclaw_build",
        "description": "Build meshpoplinux runtime image. Methods: buildroot (recommended, from source), alpine (Docker-based), scratch (BusyBox binary).",
        "inputSchema": {
            "type": "object",
            "properties": {
                "method": {
                    "type": "string",
                    "enum": ["buildroot", "alpine", "scratch"],
                    "description": "Build method. buildroot=source compile (20-60min), alpine=Docker (~2min), scratch=BusyBox (~1min)",
                    "default": "buildroot"
                },
                "output_dir": {
                    "type": "string",
                    "description": "Output directory (default: ~/meshpoplinux/output)"
                }
            }
        }
    },
    {
        "name": "meshclaw_deploy",
        "description": "Deploy runtime image to a node. Uploads rootfs, extracts, installs MeshPOP stack.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "node": {
                    "type": "string",
                    "description": "Target node name (node1, worker2, relay4, etc.)"
                },
                "image": {
                    "type": "string",
                    "description": "Path to rootfs image (default: output/meshpoplinux-rootfs.tar.gz)"
                },
                "install_stack": {
                    "type": "boolean",
                    "description": "Install MeshPOP packages after deploy (default: true)",
                    "default": True
                }
            },
            "required": ["node"]
        }
    },
    {
        "name": "meshclaw_verify",
        "description": "Verify node health across all 5 MeshPOP layers (Wire, vssh, mpop, MeshDB, Vault). Returns status: healthy/degraded/critical.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "node": {
                    "type": "string",
                    "description": "Node to verify (or omit for fleet-wide)"
                },
                "nodes": {
                    "type": "string",
                    "description": "Space-separated node list for fleet verify",
                    "default": "node1 node2 node3 worker1 worker2"
                }
            }
        }
    },
    {
        "name": "meshclaw_fleet",
        "description": "Fleet status overview — shows all nodes with reachability, Python version, MeshPOP version, WireGuard status, disk usage.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "nodes": {
                    "type": "string",
                    "description": "Space-separated node list",
                    "default": "node1 node2 node3 worker1 worker2"
                }
            }
        }
    },
    {
        "name": "meshclaw_fix",
        "description": "Auto-fix a degraded node: install Python, MeshPOP packages, restart WireGuard, clean temp files.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "node": {
                    "type": "string",
                    "description": "Node to fix"
                }
            },
            "required": ["node"]
        }
    },
    {
        "name": "meshclaw_cicd",
        "description": "Full CI/CD loop: build → deploy → verify → fix. Runs all phases automatically.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "nodes": {
                    "type": "string",
                    "description": "Space-separated node list",
                    "default": "node1 node2 node3 worker1 worker2"
                },
                "method": {
                    "type": "string",
                    "enum": ["buildroot", "alpine", "scratch"],
                    "default": "buildroot"
                },
                "skip_build": {
                    "type": "boolean",
                    "description": "Skip build if rootfs exists",
                    "default": True
                }
            }
        }
    },
    {
        "name": "meshclaw_templates",
        "description": "List or search available assistant templates (built-in + community).",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {
                    "type": "string",
                    "description": "Search term (optional — omit to list all)"
                }
            }
        }
    },
    {
        "name": "meshclaw_build_template",
        "description": "Build a self-contained AI assistant image from a template. The built image auto-connects to Wire mesh and responds to mpop messages.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "template": {
                    "type": "string",
                    "description": "Built-in template name (e.g. email, news, code-reviewer, research, system-monitor) or path to custom template.yaml"
                },
                "method": {
                    "type": "string",
                    "enum": ["docker", "nodocker"],
                    "description": "Build method. docker=recommended (macOS + Linux), nodocker=Linux only no Docker",
                    "default": "docker"
                },
                "output_dir": {
                    "type": "string",
                    "description": "Where to write the image (default: ~/meshpoplinux/output)"
                }
            },
            "required": ["template"]
        }
    },
    {
        "name": "meshclaw_create",
        "description": (
            "AI-powered assistant creation from natural language. "
            "Describe what you want the assistant to do — this tool generates the template, "
            "builds the image, and runs it. The assistant then joins the Wire mesh and responds "
            "to mpop messages. This is the primary tool for creating custom AI assistants."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "description": {
                    "type": "string",
                    "description": (
                        "Natural language description of what the assistant should do. "
                        "E.g. 'Monitor my inbox and send a daily digest' or "
                        "'Watch Hacker News and alert me about AI news every 6 hours'"
                    )
                },
                "name": {
                    "type": "string",
                    "description": "Assistant name (auto-generated if omitted)"
                },
                "model": {
                    "type": "string",
                    "enum": ["claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5"],
                    "default": "claude-sonnet-4-6"
                },
                "run_now": {
                    "type": "boolean",
                    "description": "Build and run immediately after creating (default: true)",
                    "default": True
                }
            },
            "required": ["description"]
        }
    },
    {
        "name": "meshclaw_new",
        "description": "Scaffold a new assistant directory from a built-in template. Creates a template.yaml ready to customize.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "template": {
                    "type": "string",
                    "description": "Built-in template: email, news, code-reviewer, research, system-monitor"
                },
                "name": {
                    "type": "string",
                    "description": "Custom name for the assistant"
                },
                "output_dir": {
                    "type": "string",
                    "description": "Where to create the assistant directory"
                }
            },
            "required": ["template"]
        }
    },
    {
        "name": "meshclaw_up",
        "description": "Build and run an assistant in one step. Like docker-compose up — builds image then starts container.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "path": {
                    "type": "string",
                    "description": "Path to directory with template.yaml, or path to template.yaml directly"
                },
                "env": {
                    "type": "object",
                    "description": "Environment variables to inject (e.g. ANTHROPIC_API_KEY)"
                }
            },
            "required": ["path"]
        }
    },
    {
        "name": "meshclaw_down",
        "description": "Stop and remove a running assistant container.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "name": {"type": "string", "description": "Assistant name to stop"}
            },
            "required": ["name"]
        }
    },
    {
        "name": "meshclaw_ps",
        "description": "List all running meshclaw assistant containers.",
        "inputSchema": {"type": "object", "properties": {}}
    },
    {
        "name": "meshclaw_logs",
        "description": "Get logs from a running assistant container.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "name": {"type": "string", "description": "Assistant name"},
                "lines": {"type": "integer", "default": 50}
            },
            "required": ["name"]
        }
    },
    {
        "name": "meshclaw_run",
        "description": "Run a built assistant image locally via Docker. The assistant starts listening for mpop messages.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "name": {
                    "type": "string",
                    "description": "Assistant name (e.g. email-assistant)"
                },
                "env": {
                    "type": "object",
                    "description": "Environment variables to pass (e.g. ANTHROPIC_API_KEY)"
                },
                "detach": {
                    "type": "boolean",
                    "description": "Run in background (default: true)",
                    "default": True
                }
            },
            "required": ["name"]
        }
    },
]


def handle_tool(name: str, arguments: dict) -> str:
    """Handle MCP tool calls."""

    if name == "meshclaw_build":
        method = arguments.get("method", "buildroot")
        output = arguments.get("output_dir")
        result = meshclaw.build(method=method, output_dir=output)
        return json.dumps(result, indent=2)

    elif name == "meshclaw_deploy":
        node = arguments["node"]
        image = arguments.get("image")
        install = arguments.get("install_stack", True)
        result = meshclaw.deploy(node, image=image, install_stack=install)
        return json.dumps(result, indent=2)

    elif name == "meshclaw_verify":
        node = arguments.get("node")
        if node:
            result = meshclaw.verify(node)
        else:
            nodes_str = arguments.get("nodes", "")
            nodes = nodes_str.split() if nodes_str else []
            result = meshclaw.verify_fleet(nodes)
        return json.dumps(result, indent=2)

    elif name == "meshclaw_fleet":
        nodes_str = arguments.get("nodes", "")
        nodes = nodes_str.split() if nodes_str else []
        result = meshclaw.verify_fleet(nodes)
        return json.dumps(result, indent=2)

    elif name == "meshclaw_fix":
        node = arguments["node"]
        # Fix sequence: python → packages → wireguard → cleanup
        steps = []

        # 1. Ensure Python
        out = meshclaw._vssh_exec(node, "which python3 || (apt-get update -qq && apt-get install -y -qq python3 python3-pip) 2>&1 | tail -3")
        steps.append({"python": out or "ok"})

        # 2. Install packages
        for pkg in meshclaw.MESHPOP_PACKAGES:
            out = meshclaw._vssh_exec(node, f"pip3 install --break-system-packages --upgrade {pkg} 2>&1 | tail -1")
            steps.append({pkg: out})

        # 3. WireGuard
        wg = meshclaw._vssh_exec(node, "ip addr show wg0 2>/dev/null | grep inet")
        if not wg:
            out = meshclaw._vssh_exec(node, "wire connect 2>&1 | tail -3")
            steps.append({"wireguard": f"started: {out}"})
        else:
            steps.append({"wireguard": "already active"})

        # 4. Cleanup
        meshclaw._vssh_exec(node, "rm -f /tmp/meshpoplinux-rootfs.tar.gz")
        steps.append({"cleanup": "ok"})

        # 5. Re-verify
        verify_result = meshclaw.verify(node)

        return json.dumps({"node": node, "fix_steps": steps, "verify": verify_result}, indent=2)

    elif name == "meshclaw_cicd":
        nodes_str = arguments.get("nodes", "")
        nodes = nodes_str.split() if nodes_str else []
        method = arguments.get("method", "buildroot")
        skip_build = arguments.get("skip_build", True)
        results = {"phases": {}}

        # Phase 1: Build
        rootfs = os.path.join(meshclaw.RTLINUX_DIR, "output", "meshpoplinux-rootfs.tar.gz")
        if skip_build and os.path.exists(rootfs):
            results["phases"]["build"] = "skipped (rootfs exists)"
        else:
            results["phases"]["build"] = meshclaw.build(method=method)

        # Phase 2: Deploy
        deploy_results = {}
        for node in nodes:
            deploy_results[node] = meshclaw.deploy(node)
        results["phases"]["deploy"] = deploy_results

        # Phase 3: Verify
        fleet = meshclaw.verify_fleet(nodes)
        results["phases"]["verify"] = fleet

        # Phase 4: Fix degraded
        fixed = {}
        for node, status in fleet.get("fleet", {}).items():
            if status.get("status") != "healthy":
                # Auto-fix
                fixed[node] = handle_tool("meshclaw_fix", {"node": node})
        results["phases"]["fix"] = fixed if fixed else "all healthy"

        return json.dumps(results, indent=2)

    elif name == "meshclaw_templates":
        query = arguments.get("query", "")
        if query:
            results = meshclaw.templates_search(query)
            if results:
                return json.dumps(results, indent=2)
            return json.dumps({"message": f"No templates found for: {query}"})
        result = meshclaw.templates_list()
        return json.dumps(result, indent=2)

    elif name == "meshclaw_build_template":
        template = arguments["template"]
        method = arguments.get("method", "docker")
        output_dir = arguments.get("output_dir")
        result = meshclaw.build_from_template(
            config_path=template,
            output_dir=output_dir,
            method=method,
        )
        return json.dumps(result, indent=2)

    elif name == "meshclaw_run":
        name_arg = arguments["name"]
        env_vars = arguments.get("env", {})
        detach = arguments.get("detach", True)
        env_flags = " ".join(f"-e {k}={v}" for k, v in env_vars.items())
        d_flag = "-d" if detach else ""
        tag = f"meshclaw-{name_arg}"
        cmd = f"docker run {d_flag} --name {name_arg} {env_flags} {tag}"
        import subprocess as _sp
        r = _sp.run(cmd, shell=True, capture_output=True, text=True, timeout=30)
        if r.returncode == 0:
            return json.dumps({"status": "running", "container": name_arg, "cmd": cmd})
        return json.dumps({"error": r.stderr.strip(), "cmd": cmd})


    elif name == "meshclaw_create":
        description = arguments["description"]
        asst_name = arguments.get("name")
        model = arguments.get("model", "claude-sonnet-4-6")
        run_now = arguments.get("run_now", True)
        result = meshclaw.create_assistant(description, name=asst_name, model=model)
        if "error" in result or not run_now:
            return json.dumps(result, indent=2)
        up_result = meshclaw.up(os.path.dirname(result["path"]))
        result["run"] = up_result
        return json.dumps(result, indent=2)

    elif name == "meshclaw_new":
        result = meshclaw.new_assistant(
            arguments["template"],
            output_dir=arguments.get("output_dir"),
            name=arguments.get("name"),
        )
        return json.dumps(result, indent=2)

    elif name == "meshclaw_up":
        result = meshclaw.up(arguments["path"], env=arguments.get("env", {}))
        return json.dumps(result, indent=2)

    elif name == "meshclaw_down":
        result = meshclaw.down(arguments["name"])
        return json.dumps(result, indent=2)

    elif name == "meshclaw_ps":
        result = meshclaw.ps()
        return json.dumps(result, indent=2)

    elif name == "meshclaw_logs":
        return meshclaw.logs(arguments["name"], lines=arguments.get("lines", 50))

    return json.dumps({"error": f"Unknown tool: {name}"})


# ── MCP stdio server ───────────────────────────────────────────────

def run_stdio():
    """Run as MCP stdio server."""
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue

        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            continue

        method = msg.get("method", "")
        msg_id = msg.get("id")
        params = msg.get("params", {})

        if method == "initialize":
            resp = {
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {
                    "protocolVersion": "2024-11-05",
                    "capabilities": {"tools": {"listChanged": False}},
                    "serverInfo": {
                        "name": "meshclaw",
                        "version": meshclaw.__version__,
                    },
                },
            }
        elif method == "notifications/initialized":
            continue
        elif method == "tools/list":
            resp = {
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {"tools": TOOLS},
            }
        elif method == "tools/call":
            name = params.get("name", "")
            arguments = params.get("arguments", {})
            try:
                result_text = handle_tool(name, arguments)
                resp = {
                    "jsonrpc": "2.0",
                    "id": msg_id,
                    "result": {
                        "content": [{"type": "text", "text": result_text}]
                    },
                }
            except Exception as e:
                resp = {
                    "jsonrpc": "2.0",
                    "id": msg_id,
                    "result": {
                        "content": [{"type": "text", "text": f"Error: {e}"}],
                        "isError": True,
                    },
                }
        else:
            resp = {
                "jsonrpc": "2.0",
                "id": msg_id,
                "error": {"code": -32601, "message": f"Unknown method: {method}"},
            }

        sys.stdout.write(json.dumps(resp) + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    if "--port" in sys.argv:
        idx = sys.argv.index("--port")
        port = int(sys.argv[idx + 1]) if idx + 1 < len(sys.argv) else 8098
        print(f"HTTP mode not yet implemented. Use stdio mode.", file=sys.stderr)
        sys.exit(1)
    else:
        run_stdio()
