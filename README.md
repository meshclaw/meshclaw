# meshclaw

[![PyPI](https://img.shields.io/pypi/v/meshclaw)](https://pypi.org/project/meshclaw/)
[![Python](https://img.shields.io/pypi/pyversions/meshclaw)](https://pypi.org/project/meshclaw/)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

**AI workers, anywhere. Run AI agents on any machine — no cloud, no Docker required.**

```bash
pip install meshclaw
```

---

## What it does

meshclaw turns any machine into an AI worker. One YAML file, one command to start.

```bash
meshclaw templates               # browse built-in templates
meshclaw init assistant          # scaffold config
meshclaw start assistant         # run in background
meshclaw chat assistant          # interactive chat
meshclaw ask assistant "status"  # one-shot
```

---

## Quick Start — Ollama (no API key)

```bash
pip install meshclaw
ollama pull qwen2.5:7b

meshclaw init assistant
# edit ~/.meshclaw/assistant/template.yaml → model: ollama/qwen2.5:7b

meshclaw start assistant
meshclaw chat assistant
```

---

## Quick Start — Claude / OpenAI

```bash
pip install meshclaw
export ANTHROPIC_API_KEY=sk-ant-...
meshclaw init assistant
meshclaw start assistant
```

---

## Template YAML

```yaml
name: my-worker
model: ollama/qwen2.5:7b        # or claude-sonnet-4-6, gpt-4o, etc.
system_prompt: |
  You are a helpful assistant with bash access.

schedule: "every 1h"
schedule_script: |
  #!/bin/bash
  echo "=== $(date) ==="
  df -h && uptime

on_message: "Help with the user's request. Use bash for real data."

notify:
  platform: telegram
  token: YOUR_BOT_TOKEN
  chat_id: YOUR_CHAT_ID
```

---

## Built-in Templates

| Template | Model | Use case |
|---|---|---|
| `assistant` | claude / ollama | General purpose with bash |
| `system-monitor` | ollama | CPU/memory/disk alerts |
| `news` | claude-haiku | Hourly news digest |
| `research` | claude | Web research + summary |
| `orchestrator` | ollama | Fan-out to remote workers |
| `code-reviewer` | claude | Git diff review |
| `mac-assistant` | claude | macOS Calendar, Mail |

---

## CLI Reference

```bash
meshclaw init <template>          # Scaffold from template
meshclaw start <name>             # Start worker in background
meshclaw stop <name>              # Stop worker
meshclaw restart <name>           # Restart worker
meshclaw ps                       # List running workers
meshclaw ask <name> "<message>"   # One-shot message
meshclaw chat <name>              # Interactive chat
meshclaw webchat --worker <name>  # Browser/mobile UI
meshclaw templates                # List built-in templates
meshclaw version                  # Show version
```

---

## Scheduled Tasks

```yaml
# With LLM
schedule: "every 1h"
schedule_task: "Summarize the latest news and report key trends."

# Bash only — real data, no hallucination
schedule: "every 15m"
schedule_script: |
  #!/bin/bash
  df -h / | awk 'NR==2{print "Disk: "$5" used"}'
```

---

## Notifications

```yaml
notify:
  platform: telegram       # telegram / slack / discord / webhook
  token: YOUR_BOT_TOKEN
  chat_id: YOUR_CHAT_ID
```

---

## Orchestration — Multiple Workers

```bash
# Deploy to remote server
meshclaw remote-up 192.168.1.100 system-monitor

# Ask a remote worker via SSH
ssh root@192.168.1.100 "meshclaw ask system-monitor 'disk status'"
```

Fan-out to multiple workers in parallel:

```python
import subprocess, concurrent.futures

WORKERS = {"g1": "192.168.1.101", "g2": "192.168.1.102"}

def ask_remote(ip, name, task):
    r = subprocess.run(["ssh", f"root@{ip}", f"meshclaw ask {name} '{task}'"],
                       capture_output=True, text=True, timeout=120)
    return r.stdout.strip()

with concurrent.futures.ThreadPoolExecutor() as ex:
    results = {g: ex.submit(ask_remote, ip, f"{g}-worker", "status")
               for g, ip in WORKERS.items()}
    for g, f in results.items():
        print(f"{g}: {f.result()}")
```

Use the built-in `orchestrator` template to do this automatically on a schedule.
