"""
meshclaw webchat — mobile-friendly chat UI for meshclaw workers.

Runs a local web server that bridges HTTP ↔ worker Unix socket.
Access from phone on same WiFi, via Wire VPN, or via ngrok tunnel.

Usage:
    python3 webchat.py                          # chat with local mac-assistant
    python3 webchat.py --worker test-worker     # local worker
    python3 webchat.py --host d1                # worker on remote server (via vssh)
    python3 webchat.py --port 8080
    python3 webchat.py --ngrok                  # expose publicly via ngrok tunnel
"""

import argparse
import base64
import json
import os
import socket
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlparse

# ── Config ───────────────────────────────────────────────────────────

DEFAULT_WORKER = os.environ.get("RTLINUX_WORKER", "mac-assistant")
DEFAULT_HOST   = os.environ.get("RTLINUX_HOST", "")   # empty = local
DEFAULT_PORT   = int(os.environ.get("WEBCHAT_PORT", "8080"))

# ── Worker communication ─────────────────────────────────────────────

def ask_local_worker(worker_name: str, message: str, timeout: int = 90) -> str:
    """Send message to local worker via Unix socket."""
    sock_path = f"/tmp/meshclaw-{worker_name}.sock"
    if not os.path.exists(sock_path):
        return f"Worker '{worker_name}' not running. Start with: meshclaw mac-up {worker_name}"
    try:
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(timeout)
        s.connect(sock_path)
        s.sendall(message.encode() + bytes([10]))
        data = b""
        while True:
            chunk = s.recv(4096)
            if not chunk or bytes([4]) in chunk:
                data += chunk.replace(bytes([4]), b"")
                break
            data += chunk
        s.close()
        return data.decode(errors="replace").strip()
    except Exception as e:
        return f"Error: {e}"


def ask_remote_worker(worker_name: str, server_ip: str, message: str, timeout: int = 90) -> str:
    """Send message to worker on remote server via vssh + docker exec."""
    msg_b64 = base64.b64encode(message.encode()).decode()
    lines = [
        "import base64,socket",
        f"msg=base64.b64decode('{msg_b64}')",
        "s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)",
        f"s.settimeout({timeout})",
        f"s.connect('/tmp/meshclaw-{worker_name}.sock')",
        "s.sendall(msg+bytes([10]))",
        "d=b''",
        "while True:",
        "    c=s.recv(4096)",
        "    if not c or bytes([4]) in c:",
        "        d+=c.replace(bytes([4]),b'')",
        "        break",
        "    d+=c",
        "print(d.decode(errors='replace'))",
    ]
    script = "\n".join(lines) + "\n"
    script_b64 = base64.b64encode(script.encode()).decode()
    cmd = (
        f"docker exec {worker_name} python3 -c "
        f"\"import base64; exec(base64.b64decode('{script_b64}').decode())\""
    )
    try:
        import vssh
        buf = []
        import io, contextlib
        with contextlib.redirect_stdout(io.StringIO()) as f:
            vssh.ssh(server_ip, cmd)
        return f.getvalue().strip() or ask_via_subprocess(server_ip, cmd, timeout)
    except Exception:
        return ask_via_subprocess(server_ip, cmd, timeout)


def ask_via_subprocess(server_ip: str, cmd: str, timeout: int) -> str:
    try:
        vssh_secret = open(os.path.expanduser("~/.vssh/secret")).read().strip()
        vssh_port = 9222
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(timeout)
        s.connect((server_ip, vssh_port))
        s.sendall(f"SSH:{vssh_secret}:{cmd}\n".encode())
        resp = s.recv(4096)
        if not resp.startswith(b"OK"):
            return "vssh error"
        ok_end = resp.find(b"\n")
        data = resp[ok_end + 1:] if ok_end >= 0 else b""
        s.settimeout(timeout)
        while True:
            try:
                chunk = s.recv(8192)
                if not chunk:
                    break
                data += chunk
            except socket.timeout:
                break
        s.close()
        return data.decode(errors="replace").strip()
    except Exception as e:
        return f"Error: {e}"


# ── HTML ─────────────────────────────────────────────────────────────

def list_local_workers() -> list[str]:
    """Return names of locally running workers (by scanning /tmp/meshclaw-*.sock)."""
    import glob
    socks = glob.glob("/tmp/meshclaw-*.sock")
    names = [os.path.basename(s).replace("meshclaw-", "").replace(".sock", "") for s in socks]
    return sorted(names)


def build_html(worker_name: str, host: str) -> str:
    target = f"{worker_name}@{host}" if host else worker_name
    return f"""<!DOCTYPE html>
<html lang="ko">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1">
<title>{target}</title>
<style>
  * {{ box-sizing: border-box; margin: 0; padding: 0; }}
  body {{
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
    background: #1a1a2e;
    color: #e0e0e0;
    height: 100dvh;
    display: flex;
    flex-direction: column;
  }}
  header {{
    background: #16213e;
    padding: 12px 16px;
    display: flex;
    align-items: center;
    gap: 10px;
    border-bottom: 1px solid #0f3460;
    flex-shrink: 0;
  }}
  .dot {{
    width: 10px; height: 10px;
    border-radius: 50%;
    background: #4ade80;
    box-shadow: 0 0 6px #4ade80;
  }}
  header h1 {{
    font-size: 15px;
    font-weight: 600;
    color: #e2e8f0;
  }}
  header span {{
    font-size: 12px;
    color: #64748b;
    margin-left: auto;
  }}
  #chat {{
    flex: 1;
    overflow-y: auto;
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 12px;
    scroll-behavior: smooth;
  }}
  .msg {{
    max-width: 85%;
    padding: 10px 14px;
    border-radius: 16px;
    font-size: 14px;
    line-height: 1.5;
    word-break: break-word;
    white-space: pre-wrap;
  }}
  .msg.user {{
    background: #0f3460;
    align-self: flex-end;
    border-bottom-right-radius: 4px;
    color: #e2e8f0;
  }}
  .msg.worker {{
    background: #16213e;
    border: 1px solid #1e3a5f;
    align-self: flex-start;
    border-bottom-left-radius: 4px;
    color: #cbd5e1;
  }}
  .msg.worker code {{
    background: #0d1b2a;
    padding: 2px 6px;
    border-radius: 4px;
    font-family: 'SF Mono', monospace;
    font-size: 12px;
    color: #7dd3fc;
  }}
  .msg.worker pre {{
    background: #0d1b2a;
    padding: 10px;
    border-radius: 8px;
    overflow-x: auto;
    font-size: 12px;
    color: #94a3b8;
    margin: 6px 0;
    font-family: 'SF Mono', monospace;
  }}
  .thinking {{
    color: #475569;
    font-size: 13px;
    align-self: flex-start;
    display: flex;
    align-items: center;
    gap: 6px;
  }}
  .dots span {{
    display: inline-block;
    width: 6px; height: 6px;
    border-radius: 50%;
    background: #475569;
    animation: bounce 1.2s infinite;
  }}
  .dots span:nth-child(2) {{ animation-delay: 0.2s; }}
  .dots span:nth-child(3) {{ animation-delay: 0.4s; }}
  @keyframes bounce {{
    0%, 80%, 100% {{ transform: scale(0.8); opacity: 0.4; }}
    40% {{ transform: scale(1.2); opacity: 1; }}
  }}
  #inputbar {{
    display: flex;
    padding: 10px 12px;
    gap: 8px;
    background: #16213e;
    border-top: 1px solid #0f3460;
    flex-shrink: 0;
  }}
  #msg {{
    flex: 1;
    background: #1e3a5f;
    border: 1px solid #2d5a8e;
    border-radius: 20px;
    padding: 10px 16px;
    color: #e2e8f0;
    font-size: 15px;
    outline: none;
    resize: none;
    max-height: 120px;
    overflow-y: auto;
    line-height: 1.4;
  }}
  #msg::placeholder {{ color: #475569; }}
  #send {{
    width: 42px; height: 42px;
    border-radius: 50%;
    background: #3b82f6;
    border: none;
    color: white;
    font-size: 18px;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    transition: background 0.2s;
  }}
  #send:hover {{ background: #2563eb; }}
  #send:disabled {{ background: #1e3a5f; cursor: default; }}
</style>
</head>
<body>
<header>
  <div class="dot" id="statusdot"></div>
  <h1>🦞</h1>
  <select id="workersel" onchange="switchWorker(this.value)" style="
    background:#0f3460; color:#e2e8f0; border:1px solid #1e4a8a;
    border-radius:6px; padding:4px 8px; font-size:13px; cursor:pointer;
  ">
    <option value="{worker_name}">{worker_name}</option>
  </select>
  <span id="statustext" style="margin-left:auto;font-size:12px;color:#64748b;">연결됨</span>
</header>
<div id="chat"></div>
<div id="inputbar">
  <textarea id="msg" placeholder="메시지 입력..." rows="1" autofocus></textarea>
  <button id="send" onclick="sendMsg()">↑</button>
</div>
<script>
const chat = document.getElementById('chat');
const input = document.getElementById('msg');
const btn = document.getElementById('send');
const sel = document.getElementById('workersel');
let currentWorker = '{worker_name}';

// 실행 중인 워커 목록 가져오기
async function loadWorkers() {{
  try {{
    const r = await fetch('/workers');
    const data = await r.json();
    const workers = data.workers || [];
    sel.innerHTML = '';
    workers.forEach(w => {{
      const o = document.createElement('option');
      o.value = w; o.text = w;
      if (w === currentWorker) o.selected = true;
      sel.appendChild(o);
    }});
    if (!workers.includes(currentWorker) && workers.length) {{
      currentWorker = workers[0];
      sel.value = currentWorker;
    }}
  }} catch(e) {{}}
}}

function switchWorker(name) {{
  currentWorker = name;
  chat.innerHTML = '';
  addMsg(`워커 전환: ${{name}}`, 'worker');
}}

function addMsg(text, role) {{
  const d = document.createElement('div');
  d.className = 'msg ' + role;
  text = text
    .replace(/```([\\s\\S]*?)```/g, '<pre>$1</pre>')
    .replace(/`([^`]+)`/g, '<code>$1</code>');
  d.innerHTML = text;
  chat.appendChild(d);
  chat.scrollTop = chat.scrollHeight;
  return d;
}}

function addThinking() {{
  const d = document.createElement('div');
  d.className = 'thinking';
  d.innerHTML = '<div class="dots"><span></span><span></span><span></span></div>';
  chat.appendChild(d);
  chat.scrollTop = chat.scrollHeight;
  return d;
}}

async function sendMsg() {{
  const text = input.value.trim();
  if (!text) return;
  input.value = '';
  input.style.height = 'auto';
  btn.disabled = true;

  addMsg(text, 'user');
  const thinking = addThinking();

  try {{
    const res = await fetch('/ask', {{
      method: 'POST',
      headers: {{'Content-Type': 'application/json'}},
      body: JSON.stringify({{message: text, worker: currentWorker}})
    }});
    const data = await res.json();
    thinking.remove();
    addMsg(data.response || data.error || '(응답 없음)', 'worker');
  }} catch(e) {{
    thinking.remove();
    addMsg('연결 오류: ' + e.message, 'worker');
  }}
  btn.disabled = false;
  input.focus();
}}

input.addEventListener('keydown', e => {{
  if (e.key === 'Enter' && !e.shiftKey) {{
    e.preventDefault();
    sendMsg();
  }}
}});

input.addEventListener('input', () => {{
  input.style.height = 'auto';
  input.style.height = Math.min(input.scrollHeight, 120) + 'px';
}});

loadWorkers();
addMsg('안녕하세요! 무엇을 도와드릴까요?', 'worker');
</script>
</body>
</html>"""


# ── HTTP Handler ──────────────────────────────────────────────────────

class ChatHandler(BaseHTTPRequestHandler):
    worker_name = DEFAULT_WORKER
    server_host = DEFAULT_HOST

    def log_message(self, fmt, *args):
        pass  # suppress default logging

    def do_GET(self):
        if self.path == "/" or self.path == "/index.html":
            html = build_html(self.worker_name, self.server_host).encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(html)))
            self.end_headers()
            self.wfile.write(html)
        elif self.path == "/ping":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"pong")
        elif self.path == "/workers":
            workers = list_local_workers()
            body = json.dumps({"workers": workers}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_response(404)
            self.end_headers()

    def do_POST(self):
        if self.path == "/ask":
            length = int(self.headers.get("Content-Length", 0))
            body = self.rfile.read(length)
            try:
                data = json.loads(body)
                message = data.get("message", "").strip()
                if not message:
                    raise ValueError("empty message")
                # Allow per-request worker override from UI dropdown
                worker = data.get("worker", self.worker_name) or self.worker_name

                if self.server_host:
                    response = ask_remote_worker(worker, self.server_host, message)
                else:
                    response = ask_local_worker(worker, message)

                result = json.dumps({"response": response}).encode()
            except Exception as e:
                result = json.dumps({"error": str(e)}).encode()

            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(result)))
            self.send_header("Access-Control-Allow-Origin", "*")
            self.end_headers()
            self.wfile.write(result)
        else:
            self.send_response(404)
            self.end_headers()


# ── Main ──────────────────────────────────────────────────────────────

def start_ngrok_tunnel(port: int) -> str:
    """Start ngrok tunnel and return public URL. Returns empty string on failure."""
    try:
        # Try pyngrok first (pip install pyngrok)
        try:
            from pyngrok import ngrok as _ngrok
            tunnel = _ngrok.connect(port, "http")
            url = tunnel.public_url.replace("http://", "https://")
            return url
        except ImportError:
            pass

        # Fallback: ngrok CLI
        import subprocess, time, json as _json, urllib.request
        proc = subprocess.Popen(
            ["ngrok", "http", str(port), "--log=stdout", "--log-format=json"],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL
        )
        # Wait for tunnel to come up
        for _ in range(20):
            time.sleep(0.5)
            try:
                with urllib.request.urlopen("http://localhost:4040/api/tunnels", timeout=1) as r:
                    data = _json.loads(r.read())
                    tunnels = data.get("tunnels", [])
                    for t in tunnels:
                        if t.get("proto") == "https":
                            return t["public_url"]
                        if t.get("proto") == "http":
                            return t["public_url"]
            except Exception:
                pass
        return ""
    except Exception as e:
        return ""


def get_local_ips():
    ips = []
    try:
        import subprocess as sp
        r = sp.run(["ifconfig"], capture_output=True, text=True)
        import re
        # LAN IPs
        for m in re.finditer(r"inet (192\.168\.\d+\.\d+|10\.\d+\.\d+\.\d+)", r.stdout):
            ips.append(m.group(1))
    except Exception:
        pass
    return ips


def main():
    parser = argparse.ArgumentParser(description="meshclaw webchat — chat with workers from any device")
    parser.add_argument("--worker", "-w", default=DEFAULT_WORKER, help="Worker name (default: mac-assistant)")
    parser.add_argument("--host", default=DEFAULT_HOST, help="Remote server IP (empty for local)")
    parser.add_argument("--port", "-p", type=int, default=DEFAULT_PORT, help="Port (default: 8080)")
    parser.add_argument("--ngrok", action="store_true", help="Expose via ngrok tunnel (access from anywhere)")
    args = parser.parse_args()

    ChatHandler.worker_name = args.worker
    ChatHandler.server_host = args.host

    target = f"{args.worker}@{args.host}" if args.host else args.worker
    server = HTTPServer(("0.0.0.0", args.port), ChatHandler)

    ips = get_local_ips()
    print(f"\n🦞 meshclaw webchat — {target}")
    print(f"   Local:   http://localhost:{args.port}")
    for ip in ips:
        print(f"   Network: http://{ip}:{args.port}")

    if args.ngrok:
        print(f"   Starting ngrok tunnel...")
        ngrok_url = start_ngrok_tunnel(args.port)
        if ngrok_url:
            print(f"   Public:  {ngrok_url}  ← 핸드폰에서 이 URL 접속")
        else:
            print(f"   ngrok: failed. Install with: pip install pyngrok  OR  brew install ngrok")

    print(f"\n   Ctrl+C to stop\n")

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\n stopped.")
        server.shutdown()


if __name__ == "__main__":
    main()
