#!/usr/bin/env python3
"""
meshclaw MCP Server - Unified AI interface for infrastructure management

Provides MCP tools for:
- meshdb: Full-text search across servers
- vault: Encrypted secrets management
- vssh: Remote execution and file transfer
- wire: VPN mesh management
- mpop: Server dashboard

Run: meshclaw-mcp
Configure in ~/.claude/settings.json:
{
  "mcpServers": {
    "meshclaw": { "command": "meshclaw-mcp" }
  }
}
"""

import json
import os
import sys
import socket
import time
import sqlite3
import hashlib
import platform
from concurrent.futures import ThreadPoolExecutor, as_completed
from typing import Any, Optional, Dict, List

VERSION = "1.1.0"

# === Configuration ===
CONFIG_PATH = os.path.expanduser("~/.mpop/config.json")
MESHDB_PATH = os.path.expanduser("~/.meshdb/meshdb.db")
VAULT_PATH = os.path.expanduser("~/.sv-vault")
VSSH_PORT = 48291

def load_config() -> dict:
    """Load config from ~/.mpop/config.json"""
    for path in [os.path.expanduser("~/.meshdb/config.json"), CONFIG_PATH]:
        try:
            with open(path) as f:
                return json.load(f)
        except:
            continue
    return {}

def get_servers() -> dict:
    return load_config().get("servers", {})

def get_vssh_secret() -> str:
    return load_config().get("vssh_secret", os.environ.get("VSSH_SECRET", ""))

def get_local_hostname() -> str:
    hostname = socket.gethostname().lower()
    config = load_config()
    for srv_name, srv_cfg in config.get("servers", {}).items():
        if srv_cfg.get("local"):
            return srv_name
    return hostname

# === MCP Protocol ===
def send_message(msg: dict):
    sys.stdout.write(json.dumps(msg) + "\n")
    sys.stdout.flush()

def read_message() -> Optional[dict]:
    line = sys.stdin.readline()
    if not line:
        return None
    return json.loads(line.strip())

# === vssh Protocol ===
def vssh_connect(ip: str, timeout: int = 5) -> socket.socket:
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.settimeout(timeout)
    sock.connect((ip, VSSH_PORT))
    return sock

def vssh_exec_cmd(ip: str, cmd: str, timeout: int = 30) -> str:
    try:
        sock = vssh_connect(ip, timeout)
        secret = get_vssh_secret()
        sock.sendall(f"SSH:{secret}:{cmd}\n".encode())

        resp = sock.recv(64)
        if not resp.startswith(b'OK'):
            return f"Error: {resp.decode()}"

        data = b''
        while True:
            chunk = sock.recv(8192)
            if not chunk or b'__END__' in chunk:
                data += chunk.replace(b'__END__', b'')
                break
            data += chunk

        sock.close()
        return data.decode('utf-8', errors='replace').strip()
    except Exception as e:
        return f"Error: {e}"

# === meshdb Tools ===
def meshdb_search(query: str, limit: int = 20, doc_type: str = None) -> dict:
    """Full-text search in local meshdb"""
    if not os.path.exists(MESHDB_PATH):
        return {"error": "meshdb not found", "count": 0, "results": []}

    t0 = time.time()
    conn = sqlite3.connect(MESHDB_PATH)
    conn.row_factory = sqlite3.Row

    try:
        sql = """
            SELECT d.filepath, d.filename, d.doc_type, d.file_size,
                   bm25(documents_fts, 10.0, 5.0, 1.0) as rank,
                   snippet(documents_fts, 1, '>>>', '<<<', '...', 40) as snippet
            FROM documents_fts fts
            JOIN documents d ON d.id = fts.rowid
            WHERE documents_fts MATCH ?
            ORDER BY rank LIMIT ?
        """
        rows = conn.execute(sql, [query, limit]).fetchall()
        results = [{
            "filepath": r["filepath"], "filename": r["filename"],
            "doc_type": r["doc_type"], "rank": round(r["rank"], 4),
            "snippet": r["snippet"] or ""
        } for r in rows]
    except sqlite3.OperationalError:
        rows = conn.execute(
            "SELECT filepath, filename, doc_type, file_size, substr(content, 1, 200) as snippet "
            "FROM documents WHERE content LIKE ? OR filename LIKE ? LIMIT ?",
            [f"%{query}%", f"%{query}%", limit]
        ).fetchall()
        results = [{"filepath": r["filepath"], "filename": r["filename"],
                   "doc_type": r["doc_type"], "snippet": r["snippet"]} for r in rows]

    elapsed = (time.time() - t0) * 1000
    conn.close()
    return {"count": len(results), "elapsed_ms": round(elapsed, 1), "results": results}

def meshdb_find(query: str, limit: int = 20) -> dict:
    """Find files by name"""
    if not os.path.exists(MESHDB_PATH):
        return {"error": "meshdb not found", "count": 0, "results": []}

    conn = sqlite3.connect(MESHDB_PATH)
    conn.row_factory = sqlite3.Row
    rows = conn.execute(
        "SELECT filepath, filename, doc_type, file_size FROM documents "
        "WHERE filename LIKE ? ORDER BY file_size LIMIT ?",
        [f"%{query}%", limit]
    ).fetchall()
    conn.close()
    return {"count": len(rows), "results": [dict(r) for r in rows]}

def meshdb_status() -> dict:
    """Get meshdb status"""
    if not os.path.exists(MESHDB_PATH):
        return {"error": "meshdb not found"}

    conn = sqlite3.connect(MESHDB_PATH)
    total = conn.execute("SELECT COUNT(*) FROM documents").fetchone()[0]
    types = {}
    for r in conn.execute("SELECT doc_type, COUNT(*) as cnt FROM documents GROUP BY doc_type"):
        types[r[0]] = r[1]
    conn.close()

    db_size = os.path.getsize(MESHDB_PATH)
    return {
        "hostname": platform.node(),
        "total_documents": total,
        "database_path": MESHDB_PATH,
        "database_size": f"{db_size/1024/1024:.1f}MB",
        "types": types
    }

# === vssh Tools ===
def vssh_status() -> dict:
    """Check vssh connection status"""
    servers = get_servers()
    results = {}

    for name, srv in servers.items():
        ip = srv.get("ip", "")
        if not ip:
            continue
        try:
            start = time.time()
            sock = vssh_connect(ip, 3)
            latency = (time.time() - start) * 1000
            sock.close()
            results[name] = {"status": "online", "ip": ip, "latency_ms": round(latency, 1)}
        except:
            results[name] = {"status": "offline", "ip": ip}

    online = sum(1 for r in results.values() if r["status"] == "online")
    return {"summary": f"{online}/{len(results)} online", "servers": results}

def vssh_exec(server: str, command: str) -> dict:
    """Execute command on remote server"""
    servers = get_servers()
    if server not in servers:
        return {"error": f"Unknown server: {server}"}
    ip = servers[server].get("ip", "")
    if not ip:
        return {"error": f"No IP for server: {server}"}
    output = vssh_exec_cmd(ip, command)
    return {"server": server, "command": command, "output": output}

def vssh_upload(ip: str, local_path: str, remote_path: str) -> dict:
    """Upload file via vssh"""
    try:
        if not os.path.exists(local_path):
            return {"error": f"File not found: {local_path}"}

        size = os.path.getsize(local_path)
        with open(local_path, 'rb') as f:
            data = f.read()
        md5 = hashlib.md5(data).hexdigest()

        sock = vssh_connect(ip, max(60, size // (1024*1024) + 30))
        secret = get_vssh_secret()

        sock.sendall(f"PUT:{secret}:{remote_path}:{size}:{md5}\n".encode())
        resp = sock.recv(64)
        if not resp.startswith(b'OK'):
            return {"error": resp.decode().strip()}

        start = time.time()
        sock.sendall(data)
        sock.settimeout(60)
        resp = sock.recv(64)
        elapsed = time.time() - start
        sock.close()

        if b'OK' in resp:
            speed = size / elapsed / 1024 / 1024 if elapsed > 0 else 0
            return {"status": "success", "size": size, "speed_mbps": round(speed, 2)}
        return {"error": resp.decode().strip()}
    except Exception as e:
        return {"error": str(e)}

def vssh_download(ip: str, remote_path: str, local_path: str) -> dict:
    """Download file via vssh"""
    try:
        sock = vssh_connect(ip, 30)
        secret = get_vssh_secret()

        sock.sendall(f"GET:{secret}:{remote_path}\n".encode())
        resp = sock.recv(256)
        if not resp.startswith(b'OK:'):
            return {"error": resp.decode()}

        parts = resp.decode().split(':')
        size = int(parts[1].strip())

        os.makedirs(os.path.dirname(os.path.abspath(local_path)), exist_ok=True)
        start = time.time()
        received = 0
        with open(local_path, 'wb') as f:
            while received < size:
                chunk = sock.recv(min(1024*1024, size - received))
                if not chunk:
                    break
                f.write(chunk)
                received += len(chunk)

        elapsed = time.time() - start
        sock.close()

        if received < size:
            return {"error": f"Incomplete: {received}/{size}"}

        speed = size / elapsed / 1024 / 1024 if elapsed > 0 else 0
        return {"status": "success", "size": size, "speed_mbps": round(speed, 2)}
    except Exception as e:
        return {"error": str(e)}

# === mpop Tools ===
def mpop_status() -> dict:
    """Get server status dashboard"""
    servers = get_servers()
    results = {}

    def check_server(name: str, srv: dict) -> tuple:
        ip = srv.get("ip", "")
        if not ip:
            return name, {"status": "no_ip"}

        try:
            start = time.time()
            sock = vssh_connect(ip, 3)
            latency = (time.time() - start) * 1000
            sock.close()

            # Get basic info
            uptime = vssh_exec_cmd(ip, "uptime -p 2>/dev/null || uptime | awk '{print $3,$4}'", 5)
            load = vssh_exec_cmd(ip, "cat /proc/loadavg 2>/dev/null | awk '{print $1}'", 5)

            return name, {
                "status": "online",
                "ip": ip,
                "latency_ms": round(latency, 1),
                "uptime": uptime[:50] if uptime and not uptime.startswith("Error") else "",
                "load": load.split()[0] if load and not load.startswith("Error") else ""
            }
        except:
            return name, {"status": "offline", "ip": ip}

    with ThreadPoolExecutor(max_workers=min(len(servers), 10)) as executor:
        futures = [executor.submit(check_server, name, srv) for name, srv in servers.items()]
        for future in as_completed(futures, timeout=15):
            try:
                name, result = future.result()
                results[name] = result
            except:
                pass

    online = sum(1 for r in results.values() if r.get("status") == "online")
    return {"summary": f"{online}/{len(results)} online", "servers": results}

def mpop_exec(targets: str, command: str) -> dict:
    """Execute command on multiple servers"""
    servers = get_servers()

    if targets.lower() == "all":
        target_list = list(servers.keys())
    else:
        target_list = [t.strip() for t in targets.split(",")]

    results = {}

    def run_on(name: str) -> tuple:
        if name not in servers:
            return name, {"error": f"Unknown server: {name}"}
        ip = servers[name].get("ip", "")
        if not ip:
            return name, {"error": "No IP configured"}
        output = vssh_exec_cmd(ip, command, 30)
        return name, {"output": output}

    with ThreadPoolExecutor(max_workers=min(len(target_list), 10)) as executor:
        futures = [executor.submit(run_on, name) for name in target_list]
        for future in as_completed(futures, timeout=60):
            try:
                name, result = future.result()
                results[name] = result
            except Exception as e:
                pass

    return {"command": command, "targets": target_list, "results": results}

# === MCP Tool Definitions ===
TOOLS = [
    # meshdb tools
    {
        "name": "meshdb_search",
        "description": "Full-text search across indexed files. BM25 ranked with snippets. Use for finding code, docs, configs by content.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "Search query (FTS5: AND/OR/NOT, \"phrase\", prefix*)"},
                "limit": {"type": "number", "description": "Max results (default: 20)"},
                "type": {"type": "string", "description": "Filter by type: code, docs, config, log"}
            },
            "required": ["query"]
        }
    },
    {
        "name": "meshdb_find",
        "description": "Find files by filename pattern.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "Filename pattern (substring match)"},
                "limit": {"type": "number", "description": "Max results (default: 20)"}
            },
            "required": ["query"]
        }
    },
    {
        "name": "meshdb_status",
        "description": "Get meshdb index status: document count, types, size.",
        "inputSchema": {"type": "object", "properties": {}}
    },

    # vssh tools
    {
        "name": "vssh_status",
        "description": "Check vssh connection status to all servers. Shows online/offline and latency.",
        "inputSchema": {"type": "object", "properties": {}}
    },
    {
        "name": "vssh_exec",
        "description": "Execute command on remote server via vssh.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "server": {"type": "string", "description": "Server name (e.g., v1, g1)"},
                "command": {"type": "string", "description": "Command to execute"}
            },
            "required": ["server", "command"]
        }
    },
    {
        "name": "vssh_put",
        "description": "Upload file to server via vssh. High-speed transfer.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "server": {"type": "string", "description": "Server name"},
                "local_path": {"type": "string", "description": "Local file path"},
                "remote_path": {"type": "string", "description": "Remote destination"}
            },
            "required": ["server", "local_path", "remote_path"]
        }
    },
    {
        "name": "vssh_get",
        "description": "Download file from server via vssh.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "server": {"type": "string", "description": "Server name"},
                "remote_path": {"type": "string", "description": "Remote file path"},
                "local_path": {"type": "string", "description": "Local destination"}
            },
            "required": ["server", "remote_path", "local_path"]
        }
    },

    # mpop tools
    {
        "name": "mpop_status",
        "description": "Server dashboard: shows all servers with status, latency, uptime, load.",
        "inputSchema": {"type": "object", "properties": {}}
    },
    {
        "name": "mpop_exec",
        "description": "Execute command on multiple servers. Use 'all' for all servers.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "targets": {"type": "string", "description": "Server list (comma-separated) or 'all'"},
                "command": {"type": "string", "description": "Command to execute"}
            },
            "required": ["targets", "command"]
        }
    },
]

# === MCP Handler ===
def handle_tool_call(name: str, arguments: dict) -> str:
    if name == "meshdb_search":
        result = meshdb_search(
            arguments.get("query", ""),
            int(arguments.get("limit", 20)),
            arguments.get("type")
        )
    elif name == "meshdb_find":
        result = meshdb_find(arguments.get("query", ""), int(arguments.get("limit", 20)))
    elif name == "meshdb_status":
        result = meshdb_status()
    elif name == "vssh_status":
        result = vssh_status()
    elif name == "vssh_exec":
        result = vssh_exec(arguments.get("server", ""), arguments.get("command", ""))
    elif name == "vssh_put":
        servers = get_servers()
        server = arguments.get("server", "")
        if server not in servers:
            result = {"error": f"Unknown server: {server}"}
        else:
            ip = servers[server].get("ip", "")
            result = vssh_upload(ip, arguments.get("local_path", ""), arguments.get("remote_path", ""))
    elif name == "vssh_get":
        servers = get_servers()
        server = arguments.get("server", "")
        if server not in servers:
            result = {"error": f"Unknown server: {server}"}
        else:
            ip = servers[server].get("ip", "")
            result = vssh_download(ip, arguments.get("remote_path", ""), arguments.get("local_path", ""))
    elif name == "mpop_status":
        result = mpop_status()
    elif name == "mpop_exec":
        result = mpop_exec(arguments.get("targets", ""), arguments.get("command", ""))
    else:
        result = {"error": f"Unknown tool: {name}"}

    return json.dumps(result, ensure_ascii=False)

def main():
    """MCP server main loop"""
    while True:
        msg = read_message()
        if msg is None:
            break

        method = msg.get("method", "")
        msg_id = msg.get("id")

        if method == "initialize":
            send_message({
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {
                    "protocolVersion": "2024-11-05",
                    "capabilities": {"tools": {}},
                    "serverInfo": {"name": "meshclaw", "version": VERSION}
                }
            })

        elif method == "notifications/initialized":
            pass

        elif method == "tools/list":
            send_message({
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {"tools": TOOLS}
            })

        elif method == "tools/call":
            params = msg.get("params", {})
            name = params.get("name", "")
            arguments = params.get("arguments", {})

            try:
                result_text = handle_tool_call(name, arguments)
                send_message({
                    "jsonrpc": "2.0",
                    "id": msg_id,
                    "result": {"content": [{"type": "text", "text": result_text}]}
                })
            except Exception as e:
                send_message({
                    "jsonrpc": "2.0",
                    "id": msg_id,
                    "result": {"content": [{"type": "text", "text": json.dumps({"error": str(e)})}]}
                })

        elif method == "ping":
            send_message({"jsonrpc": "2.0", "id": msg_id, "result": {}})

        else:
            if msg_id is not None:
                send_message({
                    "jsonrpc": "2.0",
                    "id": msg_id,
                    "error": {"code": -32601, "message": f"Unknown method: {method}"}
                })

if __name__ == "__main__":
    main()
