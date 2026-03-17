"""MeshClaw Messenger — Connect Brain to messaging platforms.

Supported:
  - Telegram Bot
  - Slack Bot
  - Discord Bot
  - Webhook (generic, for any platform)

The Brain runs inside a VPN mesh. Messenger adapters bridge
external messages to the Brain, and send results back.

Usage:
    # Telegram
    meshclaw telegram --token BOT_TOKEN

    # Slack
    meshclaw slack --token xoxb-SLACK_TOKEN

    # Discord
    meshclaw discord --token DISCORD_TOKEN

    # Or from Python:
    from meshclaw.messenger import TelegramAdapter
    bot = TelegramAdapter(token="...", brain=brain)
    bot.run()
"""

import json
import time
import os
import sys
import threading
import traceback
from typing import Optional, Callable, Dict, List
from dataclasses import dataclass, field


# ---- Base Adapter ----

@dataclass
class Message:
    """Incoming message from any platform."""
    text: str
    user_id: str
    user_name: str = ""
    chat_id: str = ""
    platform: str = ""
    reply_to: str = ""       # message ID being replied to
    raw: dict = field(default_factory=dict)


class BaseAdapter:
    """Base class for messenger adapters.

    Subclasses implement:
      - poll() or listen(): receive messages
      - send(chat_id, text): send response
      - run(): main loop
    """

    def __init__(self, brain=None, allowed_users: list = None,
                 approval_mode: bool = True):
        """
        Args:
            brain: Brain instance to process messages
            allowed_users: List of user IDs allowed to use the bot (None = all)
            approval_mode: If True, brain asks for approval before dangerous actions
        """
        self.brain = brain
        self.allowed_users = allowed_users
        self.approval_mode = approval_mode
        self._pending_approvals: Dict[str, dict] = {}  # chat_id -> pending action
        self._conversations: Dict[str, list] = {}  # chat_id -> message history

    def send(self, chat_id: str, text: str):
        """Send a message. Override in subclass."""
        raise NotImplementedError

    def on_message(self, msg: Message):
        """Process an incoming message."""

        # Auth check
        if self.allowed_users and msg.user_id not in self.allowed_users:
            self.send(msg.chat_id, "⛔ Unauthorized. Add your user ID to allowed_users.")
            return

        text = msg.text.strip()

        # Handle approval responses
        if msg.chat_id in self._pending_approvals:
            return self._handle_approval_response(msg)

        # Special commands
        if text.lower() in ("/start", "/help", "help"):
            return self._send_help(msg.chat_id)
        if text.lower() in ("/status", "status"):
            return self._send_status(msg.chat_id)
        if text.lower() in ("/stop", "stop", "cancel"):
            self.send(msg.chat_id, "🛑 Stopped.")
            return

        # Run brain with the goal
        self._run_brain(msg)

    def _run_brain(self, msg: Message):
        """Execute brain with the message as goal."""
        if not self.brain:
            self.send(msg.chat_id, "❌ No brain configured.")
            return

        chat_id = msg.chat_id
        self.send(chat_id, f"🧠 Working on: {msg.text[:100]}...")

        # Set up approval callback if approval mode is on
        if self.approval_mode:
            self.brain.approval_callback = lambda step, action, args: \
                self._request_approval(chat_id, step, action, args)

        # Set up step reporting
        original_on_step = self.brain.on_step

        def report_step(step_num, action, result):
            # Send concise step updates
            action_short = str(action)[:100]
            result_short = str(result)[:200]
            self.send(chat_id, f"📍 Step {step_num}: {action_short}\n→ {result_short}")
            if original_on_step:
                original_on_step(step_num, action, result)

        self.brain.on_step = report_step

        try:
            result = self.brain.run(msg.text)

            # Send final result
            icon = "✅" if result.success else "❌"
            duration = f"{result.duration:.1f}s"
            self.send(chat_id,
                      f"{icon} {result.summary}\n"
                      f"({result.steps} steps, {duration})")
        except Exception as e:
            self.send(chat_id, f"❌ Error: {e}")
        finally:
            self.brain.on_step = original_on_step
            if self.approval_mode:
                self.brain.approval_callback = None

    def _request_approval(self, chat_id: str, step: int, action: str,
                          args: dict) -> bool:
        """Request user approval for a dangerous action.

        Returns True if approved, False if rejected.
        This blocks the brain loop until the user responds.
        """
        args_str = json.dumps(args, ensure_ascii=False)[:300]
        self.send(chat_id,
                  f"⚠️ Step {step} needs approval:\n"
                  f"Action: {action}\n"
                  f"Args: {args_str}\n\n"
                  f"Reply: ✅ yes / ❌ no")

        # Store pending approval and wait
        event = threading.Event()
        self._pending_approvals[chat_id] = {
            "event": event,
            "approved": False,
            "step": step,
            "action": action,
        }

        # Wait for response (timeout 5 minutes)
        approved = event.wait(timeout=300)
        result = self._pending_approvals.pop(chat_id, {})

        if not approved:
            self.send(chat_id, "⏰ Approval timed out. Skipping action.")
            return False

        return result.get("approved", False)

    def _handle_approval_response(self, msg: Message):
        """Handle yes/no response to a pending approval."""
        pending = self._pending_approvals.get(msg.chat_id)
        if not pending:
            return

        text = msg.text.strip().lower()
        if text in ("yes", "y", "ㅇ", "ㅇㅇ", "응", "네", "ok", "확인", "승인", "✅"):
            pending["approved"] = True
            self.send(msg.chat_id, "✅ Approved. Continuing...")
        elif text in ("no", "n", "ㄴ", "아니", "취소", "거부", "❌"):
            pending["approved"] = False
            self.send(msg.chat_id, "❌ Rejected. Skipping action.")
        else:
            self.send(msg.chat_id, "↩️ Reply yes(ㅇㅇ) or no(ㄴ)")
            return

        pending["event"].set()

    def _send_help(self, chat_id: str):
        self.send(chat_id,
                  "🌲 MeshClaw Brain\n\n"
                  "Send any goal in natural language:\n"
                  "  \"Check disk space on all servers\"\n"
                  "  \"Deploy the app to v1\"\n"
                  "  \"g1 서버 상태 확인해줘\"\n\n"
                  "Commands:\n"
                  "  /status — Show mesh status\n"
                  "  /stop — Cancel current task\n"
                  "  /help — This message")

    def _send_status(self, chat_id: str):
        if self.brain and self.brain.executor.orchestrator:
            try:
                servers = self.brain.executor.orchestrator.discover()
                self.send(chat_id,
                          f"🌲 MeshClaw Status\n"
                          f"Servers: {', '.join(servers) if servers else 'none'}\n"
                          f"LLM: {self.brain.llm_config.provider}/{self.brain.llm_config.model}\n"
                          f"Approval mode: {'on' if self.approval_mode else 'off'}")
                return
            except Exception:
                pass
        self.send(chat_id, "🌲 MeshClaw Status\nNo mesh connection.")


# ---- Telegram Adapter ----

class TelegramAdapter(BaseAdapter):
    """Telegram Bot adapter using Bot API (no dependencies, just urllib)."""

    def __init__(self, token: str, **kwargs):
        super().__init__(**kwargs)
        self.token = token
        self.api_base = f"https://api.telegram.org/bot{token}"
        self._offset = 0

    def _api(self, method: str, data: dict = None) -> dict:
        """Call Telegram Bot API."""
        import urllib.request
        url = f"{self.api_base}/{method}"
        if data:
            body = json.dumps(data).encode()
            req = urllib.request.Request(url, data=body,
                                        headers={"Content-Type": "application/json"})
        else:
            req = urllib.request.Request(url)

        with urllib.request.urlopen(req, timeout=60) as resp:
            return json.loads(resp.read())

    def send(self, chat_id: str, text: str):
        """Send message to Telegram chat."""
        # Split long messages (Telegram limit: 4096 chars)
        for i in range(0, len(text), 4000):
            chunk = text[i:i + 4000]
            try:
                self._api("sendMessage", {
                    "chat_id": chat_id,
                    "text": chunk,
                    "parse_mode": "Markdown",
                })
            except Exception:
                # Retry without markdown if parsing fails
                try:
                    self._api("sendMessage", {
                        "chat_id": chat_id,
                        "text": chunk,
                    })
                except Exception as e:
                    print(f"[Telegram send error]: {e}")

    def poll(self) -> List[Message]:
        """Long-poll for new messages."""
        try:
            result = self._api("getUpdates", {
                "offset": self._offset,
                "timeout": 30,
                "allowed_updates": ["message"],
            })
        except Exception as e:
            print(f"[Telegram poll error]: {e}")
            time.sleep(5)
            return []

        messages = []
        for update in result.get("result", []):
            self._offset = update["update_id"] + 1
            msg_data = update.get("message", {})
            text = msg_data.get("text", "")
            if not text:
                continue

            user = msg_data.get("from", {})
            messages.append(Message(
                text=text,
                user_id=str(user.get("id", "")),
                user_name=user.get("username", user.get("first_name", "")),
                chat_id=str(msg_data.get("chat", {}).get("id", "")),
                platform="telegram",
                raw=update,
            ))

        return messages

    def run(self):
        """Main loop: poll for messages and process them."""
        print(f"🌲 MeshClaw Telegram Bot started")
        print(f"   Approval mode: {'on' if self.approval_mode else 'off'}")

        # Get bot info
        try:
            me = self._api("getMe")
            bot_name = me.get("result", {}).get("username", "unknown")
            print(f"   Bot: @{bot_name}")
        except Exception:
            pass

        while True:
            try:
                messages = self.poll()
                for msg in messages:
                    # Process in a thread so we can handle approvals
                    t = threading.Thread(target=self.on_message, args=(msg,),
                                        daemon=True)
                    t.start()
            except KeyboardInterrupt:
                print("\n🛑 Bot stopped.")
                break
            except Exception as e:
                print(f"[Error]: {e}")
                time.sleep(5)


# ---- Slack Adapter ----

class SlackAdapter(BaseAdapter):
    """Slack Bot adapter using Slack Web API (socket mode not needed,
    uses simple polling via conversations.history)."""

    def __init__(self, token: str, channel: str = "", **kwargs):
        super().__init__(**kwargs)
        self.token = token
        self.channel = channel  # Default channel to monitor
        self._last_ts = str(time.time())
        self._bot_user_id = ""

    def _api(self, method: str, data: dict = None) -> dict:
        """Call Slack Web API."""
        import urllib.request
        url = f"https://slack.com/api/{method}"
        body = json.dumps(data or {}).encode()
        req = urllib.request.Request(url, data=body, headers={
            "Content-Type": "application/json; charset=utf-8",
            "Authorization": f"Bearer {self.token}",
        })

        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.loads(resp.read())

    def send(self, chat_id: str, text: str):
        """Send message to Slack channel."""
        channel = chat_id or self.channel
        for i in range(0, len(text), 3900):
            chunk = text[i:i + 3900]
            try:
                self._api("chat.postMessage", {
                    "channel": channel,
                    "text": chunk,
                })
            except Exception as e:
                print(f"[Slack send error]: {e}")

    def poll(self) -> List[Message]:
        """Poll for new messages in channel."""
        if not self.channel:
            return []

        try:
            result = self._api("conversations.history", {
                "channel": self.channel,
                "oldest": self._last_ts,
                "limit": 10,
            })
        except Exception as e:
            print(f"[Slack poll error]: {e}")
            time.sleep(5)
            return []

        messages = []
        for msg_data in result.get("messages", []):
            # Skip bot's own messages
            if msg_data.get("bot_id") or msg_data.get("user") == self._bot_user_id:
                continue

            ts = msg_data.get("ts", "")
            if float(ts) <= float(self._last_ts):
                continue

            self._last_ts = ts
            text = msg_data.get("text", "")
            if not text:
                continue

            messages.append(Message(
                text=text,
                user_id=msg_data.get("user", ""),
                chat_id=self.channel,
                platform="slack",
                raw=msg_data,
            ))

        return messages

    def run(self):
        """Main loop."""
        print(f"🌲 MeshClaw Slack Bot started")
        print(f"   Channel: {self.channel}")

        # Get bot user ID
        try:
            auth = self._api("auth.test")
            self._bot_user_id = auth.get("user_id", "")
            print(f"   Bot: {auth.get('user', 'unknown')}")
        except Exception:
            pass

        while True:
            try:
                messages = self.poll()
                for msg in messages:
                    t = threading.Thread(target=self.on_message, args=(msg,),
                                        daemon=True)
                    t.start()
                time.sleep(2)  # Slack rate limit friendly
            except KeyboardInterrupt:
                print("\n🛑 Bot stopped.")
                break
            except Exception as e:
                print(f"[Error]: {e}")
                time.sleep(5)


# ---- Discord Adapter ----

class DiscordAdapter(BaseAdapter):
    """Discord Bot adapter using Gateway (websocket) + REST API."""

    def __init__(self, token: str, **kwargs):
        super().__init__(**kwargs)
        self.token = token
        self.api_base = "https://discord.com/api/v10"
        self._ws = None
        self._heartbeat_interval = 41250
        self._sequence = None
        self._session_id = None

    def _rest(self, method: str, endpoint: str, data: dict = None) -> dict:
        """Call Discord REST API."""
        import urllib.request
        url = f"{self.api_base}{endpoint}"
        body = json.dumps(data).encode() if data else None
        req = urllib.request.Request(url, data=body, method=method, headers={
            "Content-Type": "application/json",
            "Authorization": f"Bot {self.token}",
        })

        with urllib.request.urlopen(req, timeout=30) as resp:
            if resp.status == 204:
                return {}
            return json.loads(resp.read())

    def send(self, chat_id: str, text: str):
        """Send message to Discord channel."""
        for i in range(0, len(text), 1900):
            chunk = text[i:i + 1900]
            try:
                self._rest("POST", f"/channels/{chat_id}/messages", {
                    "content": chunk,
                })
            except Exception as e:
                print(f"[Discord send error]: {e}")

    def run(self):
        """Main loop using Gateway websocket."""
        print(f"🌲 MeshClaw Discord Bot started")
        print(f"   Approval mode: {'on' if self.approval_mode else 'off'}")

        try:
            import websocket
        except ImportError:
            print("Discord adapter requires 'websocket-client' package.")
            print("Install: pip install websocket-client")
            return

        gateway_url = "wss://gateway.discord.gg/?v=10&encoding=json"

        def on_message(ws, raw):
            data = json.loads(raw)
            op = data.get("op")
            t = data.get("t")
            d = data.get("d")

            if data.get("s"):
                self._sequence = data["s"]

            # Hello — start heartbeat
            if op == 10:
                self._heartbeat_interval = d["heartbeat_interval"]
                # Identify
                ws.send(json.dumps({
                    "op": 2,
                    "d": {
                        "token": self.token,
                        "intents": 512 | 32768,  # GUILD_MESSAGES | MESSAGE_CONTENT
                        "properties": {
                            "os": "linux",
                            "browser": "meshclaw",
                            "device": "meshclaw",
                        }
                    }
                }))

                # Heartbeat thread
                def heartbeat():
                    while True:
                        time.sleep(self._heartbeat_interval / 1000)
                        try:
                            ws.send(json.dumps({"op": 1, "d": self._sequence}))
                        except Exception:
                            break
                threading.Thread(target=heartbeat, daemon=True).start()

            # Ready
            elif t == "READY":
                self._session_id = d.get("session_id")
                user = d.get("user", {})
                print(f"   Bot: {user.get('username', 'unknown')}#{user.get('discriminator', '0')}")

            # Message
            elif t == "MESSAGE_CREATE":
                # Skip own messages
                if d.get("author", {}).get("bot"):
                    return

                msg = Message(
                    text=d.get("content", ""),
                    user_id=d.get("author", {}).get("id", ""),
                    user_name=d.get("author", {}).get("username", ""),
                    chat_id=d.get("channel_id", ""),
                    platform="discord",
                    raw=d,
                )
                if msg.text:
                    t = threading.Thread(target=self.on_message, args=(msg,),
                                        daemon=True)
                    t.start()

            # Heartbeat ACK
            elif op == 11:
                pass

        def on_error(ws, error):
            print(f"[Discord WS error]: {error}")

        def on_close(ws, code, reason):
            print(f"[Discord WS closed]: {code} {reason}")

        ws = websocket.WebSocketApp(
            gateway_url,
            on_message=on_message,
            on_error=on_error,
            on_close=on_close,
        )
        ws.run_forever()


# ---- Webhook Adapter (generic) ----

class WebhookAdapter(BaseAdapter):
    """Generic webhook adapter. Runs a tiny HTTP server that receives
    POST requests and sends responses.

    Any platform can integrate by POSTing JSON:
      {"text": "goal", "user_id": "...", "chat_id": "...", "callback_url": "..."}

    Response is sent to callback_url or returned in HTTP response.
    """

    def __init__(self, host: str = "0.0.0.0", port: int = 8199, **kwargs):
        super().__init__(**kwargs)
        self.host = host
        self.port = port
        self._callback_urls: Dict[str, str] = {}

    def send(self, chat_id: str, text: str):
        """Send response via callback URL."""
        url = self._callback_urls.get(chat_id)
        if not url:
            return

        import urllib.request
        try:
            body = json.dumps({"chat_id": chat_id, "text": text}).encode()
            req = urllib.request.Request(url, data=body,
                                        headers={"Content-Type": "application/json"})
            urllib.request.urlopen(req, timeout=10)
        except Exception as e:
            print(f"[Webhook send error]: {e}")

    def run(self):
        """Start HTTP webhook server."""
        from http.server import HTTPServer, BaseHTTPRequestHandler

        adapter = self

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                length = int(self.headers.get("Content-Length", 0))
                body = json.loads(self.rfile.read(length)) if length else {}

                text = body.get("text", "")
                user_id = body.get("user_id", "anonymous")
                chat_id = body.get("chat_id", user_id)
                callback_url = body.get("callback_url", "")

                if callback_url:
                    adapter._callback_urls[chat_id] = callback_url

                msg = Message(
                    text=text,
                    user_id=user_id,
                    chat_id=chat_id,
                    platform="webhook",
                    raw=body,
                )

                # Process async
                t = threading.Thread(target=adapter.on_message, args=(msg,),
                                     daemon=True)
                t.start()

                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"status": "accepted"}).encode())

            def log_message(self, format, *args):
                pass  # Quiet

        server = HTTPServer((self.host, self.port), Handler)
        print(f"🌲 MeshClaw Webhook server started")
        print(f"   Listening on {self.host}:{self.port}")
        print(f"   POST /  {{\"text\": \"goal\", \"user_id\": \"...\", \"callback_url\": \"...\"}}")

        try:
            server.serve_forever()
        except KeyboardInterrupt:
            print("\n🛑 Server stopped.")
            server.server_close()


# ---- Factory ----

def create_adapter(platform: str, token: str = "", brain=None,
                   approval_mode: bool = True, **kwargs) -> BaseAdapter:
    """Create a messenger adapter.

    Args:
        platform: telegram, slack, discord, webhook
        token: Bot token
        brain: Brain instance
        approval_mode: Request approval for dangerous actions

    Returns:
        Configured adapter ready to run()
    """
    adapters = {
        "telegram": TelegramAdapter,
        "slack": SlackAdapter,
        "discord": DiscordAdapter,
        "webhook": WebhookAdapter,
    }

    cls = adapters.get(platform.lower())
    if not cls:
        raise ValueError(f"Unknown platform: {platform}. "
                         f"Available: {', '.join(adapters.keys())}")

    if platform.lower() == "webhook":
        return cls(brain=brain, approval_mode=approval_mode, **kwargs)
    else:
        return cls(token=token, brain=brain, approval_mode=approval_mode, **kwargs)
