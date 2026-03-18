"""MeshClaw Gmail + Calendar plugin.

Register with Brain:
    from meshclaw.plugins.gmail_calendar import register_gmail_calendar
    register_gmail_calendar(brain)

Requires: pip install google-api-python-client google-auth
Env: GOOGLE_CREDENTIALS_JSON or path to service account / OAuth token.
"""

import json
import os
from pathlib import Path


def _get_credentials():
    """Load Google credentials from env or file."""
    try:
        from google.oauth2.credentials import Credentials
        from google.oauth2 import service_account
        from google.auth.transport.requests import Request
    except ImportError:
        return None, "Install: pip install google-api-python-client google-auth"

    path = os.environ.get("GOOGLE_CREDENTIALS_JSON", "")
    if path and Path(path).exists():
        with open(path) as f:
            data = json.load(f)
    else:
        data = os.environ.get("GOOGLE_CREDENTIALS_JSON")
        if isinstance(data, str):
            data = json.loads(data)
        else:
            return None, "Set GOOGLE_CREDENTIALS_JSON (path or JSON string)"

    if "type" in data and data["type"] == "service_account":
        creds = service_account.Credentials.from_service_account_info(
            data, scopes=[
                "https://www.googleapis.com/auth/gmail.readonly",
                "https://www.googleapis.com/auth/gmail.send",
                "https://www.googleapis.com/auth/gmail.modify",
                "https://www.googleapis.com/auth/calendar",
            ]
        )
        return creds, None
    elif "refresh_token" in data:
        creds = Credentials(
            token=None,
            refresh_token=data["refresh_token"],
            token_uri="https://oauth2.googleapis.com/token",
            client_id=data.get("client_id", ""),
            client_secret=data.get("client_secret", ""),
        )
        creds.refresh(Request())
        return creds, None
    return None, "Unsupported credentials format"


# ---- Tool definitions ----

GMAIL_CALENDAR_TOOLS = [
    {
        "name": "gmail_list",
        "description": "List recent emails from Gmail inbox. Use to check new messages, find emails by subject.",
        "parameters": {
            "type": "object",
            "properties": {
                "max_results": {"type": "integer", "description": "Max emails to return (default 10)", "default": 10},
                "query": {"type": "string", "description": "Gmail search query (e.g. 'is:unread', 'from:user@example.com')"},
            },
        }
    },
    {
        "name": "gmail_send",
        "description": "Send an email via Gmail.",
        "parameters": {
            "type": "object",
            "properties": {
                "to": {"type": "string", "description": "Recipient email address"},
                "subject": {"type": "string", "description": "Email subject"},
                "body": {"type": "string", "description": "Email body (plain text)"},
            },
            "required": ["to", "subject", "body"],
        }
    },
    {
        "name": "gmail_reply",
        "description": "Reply to an existing email by message ID.",
        "parameters": {
            "type": "object",
            "properties": {
                "message_id": {"type": "string", "description": "Gmail message ID to reply to"},
                "body": {"type": "string", "description": "Reply body"},
            },
            "required": ["message_id", "body"],
        }
    },
    {
        "name": "calendar_list",
        "description": "List upcoming calendar events.",
        "parameters": {
            "type": "object",
            "properties": {
                "max_results": {"type": "integer", "description": "Max events (default 10)", "default": 10},
                "days_ahead": {"type": "integer", "description": "Days ahead to look (default 7)", "default": 7},
            },
        }
    },
    {
        "name": "calendar_add",
        "description": "Add an event to Google Calendar.",
        "parameters": {
            "type": "object",
            "properties": {
                "summary": {"type": "string", "description": "Event title"},
                "start": {"type": "string", "description": "Start datetime (ISO 8601 or 'tomorrow 3pm')"},
                "end": {"type": "string", "description": "End datetime"},
                "description": {"type": "string", "description": "Event description"},
            },
            "required": ["summary", "start"],
        }
    },
]


def _gmail_list(max_results: int = 10, query: str = "", **kwargs) -> str:
    creds, err = _get_credentials()
    if err:
        return err
    try:
        from googleapiclient.discovery import build
        from email.mime.text import MIMEText
        import base64

        service = build("gmail", "v1", credentials=creds)
        q = query or "in:inbox"
        results = service.users().messages().list(userId="me", maxResults=max_results, q=q).execute()
        messages = results.get("messages", [])
        if not messages:
            return "No emails found."
        lines = []
        for m in messages:
            msg = service.users().messages().get(userId="me", id=m["id"]).execute()
            payload = msg.get("payload", {})
            headers = {h["name"]: h["value"] for h in payload.get("headers", [])}
            subj = headers.get("Subject", "(no subject)")
            from_ = headers.get("From", "")
            lines.append(f"- [{m['id']}] {from_}: {subj[:60]}")
        return "\n".join(lines)
    except Exception as e:
        return f"Gmail error: {e}"


def _gmail_send(to: str, subject: str, body: str, **kwargs) -> str:
    creds, err = _get_credentials()
    if err:
        return err
    try:
        from googleapiclient.discovery import build
        from email.mime.text import MIMEText
        import base64

        message = MIMEText(body)
        message["to"] = to
        message["subject"] = subject
        raw = base64.urlsafe_b64encode(message.as_bytes()).decode()
        service = build("gmail", "v1", credentials=creds)
        service.users().messages().send(userId="me", body={"raw": raw}).execute()
        return f"Email sent to {to}"
    except Exception as e:
        return f"Gmail send error: {e}"


def _gmail_reply(message_id: str, body: str, **kwargs) -> str:
    creds, err = _get_credentials()
    if err:
        return err
    try:
        from googleapiclient.discovery import build
        from email.mime.text import MIMEText
        import base64

        service = build("gmail", "v1", credentials=creds)
        msg = service.users().messages().get(userId="me", id=message_id, format="metadata").execute()
        payload = msg.get("payload", {})
        headers = {h["name"]: h["value"] for h in payload.get("headers", [])}
        to = headers.get("Reply-To") or headers.get("From", "")
        subj = headers.get("Subject", "")
        msg_id = next((h["value"] for h in payload.get("headers", []) if h["name"] == "Message-ID"), "")
        reply = MIMEText(body)
        reply["to"] = to
        reply["subject"] = f"Re: {subj}" if subj and not subj.startswith("Re:") else subj or "Re:"
        if msg_id:
            reply["In-Reply-To"] = msg_id
        raw = base64.urlsafe_b64encode(reply.as_bytes()).decode()
        service.users().messages().send(userId="me", body={"raw": raw, "threadId": msg.get("threadId")}).execute()
        return f"Reply sent to {to}"
    except Exception as e:
        return f"Gmail reply error: {e}"


def _calendar_list(max_results: int = 10, days_ahead: int = 7, **kwargs) -> str:
    creds, err = _get_credentials()
    if err:
        return err
    try:
        from googleapiclient.discovery import build
        from datetime import datetime, timedelta

        service = build("calendar", "v3", credentials=creds)
        now = datetime.utcnow()
        end = now + timedelta(days=days_ahead)
        events = service.events().list(
            calendarId="primary",
            timeMin=now.isoformat() + "Z",
            timeMax=end.isoformat() + "Z",
            maxResults=max_results,
            singleEvents=True,
            orderBy="startTime",
        ).execute()
        items = events.get("items", [])
        if not items:
            return "No upcoming events."
        lines = []
        for e in items:
            start = e.get("start", {}).get("dateTime", e.get("start", {}).get("date", ""))
            summary = e.get("summary", "(no title)")
            lines.append(f"- {start}: {summary}")
        return "\n".join(lines)
    except Exception as e:
        return f"Calendar error: {e}"


def _calendar_add(summary: str, start: str, end: str = "", description: str = "", **kwargs) -> str:
    creds, err = _get_credentials()
    if err:
        return err
    try:
        from googleapiclient.discovery import build

        def to_iso(s: str) -> str:
            s = (s or "").strip()
            if not s:
                return ""
            if "T" in s:
                return s if "Z" in s or "+" in s or s[-1].isdigit() else s + "Z"
            if "-" in s and len(s) >= 10:
                return s + "T00:00:00Z"
            return s

        start_iso = to_iso(start)
        end_iso = to_iso(end) if end else start_iso
        if not start_iso:
            return "Calendar add error: invalid start time"

        service = build("calendar", "v3", credentials=creds)
        body = {
            "summary": summary,
            "start": {"dateTime": start_iso, "timeZone": "UTC"},
            "end": {"dateTime": end_iso, "timeZone": "UTC"},
        }
        if description:
            body["description"] = description
        event = service.events().insert(calendarId="primary", body=body).execute()
        return f"Event created: {event.get('htmlLink', summary)}"
    except Exception as e:
        return f"Calendar add error: {e}"


def register_gmail_calendar(brain):
    """Register Gmail and Calendar tools with a Brain instance."""
    handlers = [
        ("gmail_list", _gmail_list),
        ("gmail_send", _gmail_send),
        ("gmail_reply", _gmail_reply),
        ("calendar_list", _calendar_list),
        ("calendar_add", _calendar_add),
    ]
    for tool_def, (_, handler_fn) in zip(GMAIL_CALENDAR_TOOLS, handlers):
        brain.register_tool(
            name=tool_def["name"],
            description=tool_def["description"],
            parameters=tool_def.get("parameters", {"type": "object", "properties": {}}),
            handler=handler_fn,
        )
