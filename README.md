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

# Start the daemon
./wr agent daemon ensure-running

# Add a record (--date defaults to today if omitted)
./wr add --type meeting --title "Sprint planning" --time 09:00

# Check today's entries
./wr list

# Generate a report
./wr report today
```

> **LLM is optional.** Without an LLM key, specify `--type` and `--title` manually (and optionally `--date`). To enable natural-language classification, add `--llm-text-key sk-xxx --llm-text-model gpt-4o-mini` to `config init`.

## Configuration

Config lives at `~/.work-report/config.json`. Create it with `wr config init` or edit individual values with `wr config set <key> <value>`.

Key settings:

| Setting | Default | Description |
|---------|---------|-------------|
| `daemon.port` | `18080` | Daemon listen port |
| `timezone` | `Asia/Shanghai` | IANA timezone for date calculations |
| `data_dir` | `~/.work-report/work-records` | Storage directory |

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
