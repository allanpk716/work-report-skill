# wr — Work Report CLI

A CLI tool that helps you manage work reports through a local HTTP daemon. Built with Go, designed for AI agent consumption (JSONL output), but perfectly usable by humans too.

## Features

- **Record management** — Add, update, complete, and cancel work entries (meetings, tasks, reminders, logs)
- **LLM classification** — Describe work in natural language or attach images; the daemon auto-classifies them into structured records
- **Report generation** — Generate daily, weekly, or custom-range reports in Markdown or JSON
- **Push notifications** — Push reports via Pushover
- **Data import/export** — Bulk import from JSON, export to JSON or Markdown
- **Reminder scheduler** — Get notified before meetings and tasks via Pushover
- **Idempotent adds** — Retry-safe record creation with idempotency keys

## Quick Start

```bash
# Build
go build -o wr .

# Initialize config (minimal — no LLM key needed)
./wr config init

# (Optional) Enable push notifications via Pushover
./wr config set pushover.api_token your-app-token
./wr config set pushover.user_key your-user-key

# Start the daemon
./wr agent daemon ensure-running

# Add a record (--date defaults to today if omitted)
./wr add --type meeting --title "Sprint planning" --time 09:00

# Add with advance reminder (triggers Pushover notification 15 min before)
./wr add --type meeting --title "Design review" --time 14:00 --remind-before 15m

# Check today's entries
./wr list

# Generate a report
./wr report today

# Push today's report to your phone (requires Pushover)
./wr report push today
```

> **LLM is optional.** Without an LLM key, specify `--type` and `--title` manually (and optionally `--date`). To enable natural-language classification, add `--llm-text-key sk-xxx --llm-text-model gpt-4o-mini` to `config init`.

## Configuration

Config lives at `~/.work-report/config.json`. Create it with `wr config init` or edit individual values with `wr config set <key> <value>`.

### Core settings

| Setting | Default | Description |
|---------|---------|-------------|
| `daemon.port` | `18080` | Daemon listen port |
| `timezone` | `Asia/Shanghai` | IANA timezone for date calculations |
| `data_dir` | `~/.work-report/work-records` | Storage directory |

### Push notifications (Pushover)

Configure [Pushover](https://pushover.net/) credentials to receive push notifications for reminders and reports on your phone/desktop.

| Setting | Description |
|---------|-------------|
| `pushover.api_token` | Your Pushover application API token |
| `pushover.user_key` | Your Pushover user key |

```bash
# Set credentials individually
wr config set pushover.api_token your-app-token
wr config set pushover.user_key your-user-key

# Or pass them at init time
wr config init --pushover-token your-app-token --pushover-key your-user-key
```

> **Without Pushover credentials**, reminder scheduling still runs internally but notifications won't be delivered. `wr report push` will return `pushover_not_configured`.

### LLM classification (optional)

| Setting | Description |
|---------|-------------|
| `llm.text.provider` | Text LLM provider name |
| `llm.text.api_key` | Text LLM API key |
| `llm.text.model` | Text LLM model name (e.g. `gpt-4o-mini`) |
| `llm.text.api_base` | Text LLM API base URL (for custom endpoints) |
| `llm.vision.provider` | Vision LLM provider name |
| `llm.vision.api_key` | Vision LLM API key |
| `llm.vision.model` | Vision LLM model name |
| `llm.vision.api_base` | Vision LLM API base URL |

## Push Notifications & Reminders

The daemon includes a built-in scheduler that automatically determines which records need Pushover push notifications and when to send them.

### Which records trigger a push?

| Record type | Condition | Example |
|-------------|-----------|---------|
| `reminder` | Always — any reminder with a date and time will trigger a push | `wr add --type reminder --title "Submit report" --date 2026-05-10 --time 17:00` |
| `meeting` | Only when `--remind-before` is set | `wr add --type meeting --title "Sprint planning" --time 09:00 --remind-before 15m` |
| `task` | Only when `--remind-before` is set | `wr add --type task --title "Review PR" --time 14:00 --remind-before 30m` |
| `log` | Never — logs are informational only | — |

### How it works

1. **Add a record** with a qualifying type/field → the daemon automatically registers a cron job
2. **At the computed trigger time** (record time minus `remind_before`), Pushover sends the notification
3. **One-time records** auto-remove after firing; **recurring records** (`daily`/`weekly`/`monthly`) repeat on schedule
4. **Update time-related fields** → the scheduler re-registers with the new time
5. **Complete or cancel** → the scheduler unregisters, no more notifications
6. **Daemon restarts** → missed reminders are caught up and sent with a `【延迟提醒】` prefix

### Quick examples

```bash
# Simple reminder — fires at 10:00 on May 10
wr add --type reminder --title "Standup" --date 2026-05-10 --time 10:00

# Meeting with 15-minute advance reminder — fires at 08:45
wr add --type meeting --title "Sprint planning" --date 2026-05-10 --time 09:00 --remind-before 15m

# Recurring weekly reminder (every week on the same weekday)
wr add --type reminder --title "Weekly 1:1" --date 2026-05-10 --time 14:00 --recurring weekly

# Push today's report to your phone
wr report push today
```

## Command Overview

| Command | Description |
|---------|-------------|
| `wr add` | Add a new work record |
| `wr list` | List records with filters |
| `wr update` | Update an existing record |
| `wr complete` | Mark a record as completed |
| `wr cancel` | Cancel a record |
| `wr report` | Generate reports (today / date / week / range) |
| `wr report push` | Push reports via Pushover |
| `wr import` | Bulk import from JSON file |
| `wr export` | Export to JSON or Markdown |
| `wr status` | Show daemon status and config |
| `wr config init` | Create config with defaults |
| `wr config set` | Set a config value |
| `wr config show` | Display config (secrets redacted) |
| `wr agent daemon ensure-running` | Start daemon if not running |

## Development

```bash
# Build
go build -o wr .

# Run tests
go test ./...

# Run a single package's tests
go test ./internal/storage/...
```

## Documentation

- **[SKILL.md](SKILL.md)** — Complete API reference with JSONL format specs, error codes, and all command details. This is the authoritative reference for both humans and AI agents.
