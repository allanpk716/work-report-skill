# wr — Work Report CLI

A pure CLI tool for managing work reports. Built with Go, designed for AI agent consumption (JSONL output), but fully usable by humans too.

## Features

- **Record management** — Add, update, complete, and cancel work entries (meetings, tasks, reminders, done_things, personal)
- **LLM classification** — Describe work in natural language or attach images; auto-classifies them into structured records
- **Report generation** — Generate daily, weekly, or custom-range reports in Markdown or JSON
- **Push notifications** — Push reports directly to Pushover
- **Digest summaries** — Schedule periodic LLM-powered digest summaries with optional Pushover delivery
- **Prompt templates** — Manage custom LLM prompt templates for digest generation
- **Data import/export** — Bulk import from JSON, export to JSON or Markdown
- **Idempotent adds** — Retry-safe record creation with idempotency keys
- **Data backup** — Timestamped zip backups with Grandfather-Father-Son rotation
- **Notification priority** — Control Pushover urgency with `--notify-priority` (normal/high) per record
- **Reminder push** — Detect due reminders and push via Pushover, auto-complete on success
- **Health checks** — `wr agent doctor` verifies LLM, Pushover, and data directory configuration

## Quick Start

```bash
# Build
go build -o wr .

# Initialize config (minimal — no LLM key needed)
./wr config init

# (Optional) Enable push notifications via Pushover
./wr config set pushover.api_token your-app-token
./wr config set pushover.user_key your-user-key

# (Optional) Verify LLM, Pushover, and data directory are configured
./wr agent doctor

# Add a record with high-priority notification (--notify-priority defaults to high for meetings)
./wr add --type meeting --title "Sprint planning" --time 09:00 --notify-priority high

# Add a personal record
./wr add --type personal --title "吃药" --time 14:00

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
| `timezone` | `Asia/Shanghai` | IANA timezone for date calculations |
| `data_dir` | `~/.work-report/work-records` | Storage directory |

### Push notifications (Pushover)

Configure [Pushover](https://pushover.net/) credentials to receive push notifications for reports and digests on your phone/desktop.

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

> **Without Pushover credentials**, `wr report push` and `wr digest` push delivery will return `pushover_not_configured`.

### Notification priority

Control the urgency of Pushover notifications on a per-record basis using `--notify-priority`.

| Value | Pushover priority | Behavior |
|-------|-------------------|----------|
| `high` | 1 | Bypasses quiet hours, always delivers immediately |
| `normal` | 0 | Respects quiet hours (default for non-meeting records) |

**Default behavior:** Meetings default to `high` priority; all other record types default to `normal`.

```bash
# Add a meeting with high-priority notification (default for meetings)
./wr add --type meeting --title "Sprint planning" --notify-priority high

# Add a task with normal-priority notification
./wr add --type task --title "Review PR" --notify-priority normal

# Update an existing record's notification priority
./wr update <id> --notify-priority high
```

### LLM classification (optional)

| Setting | Description |
|---------|-------------|
| `llm.text.provider` | Text LLM provider name |
| `llm.text.api_key` | Text LLM API key |
| `llm.text.model` | Text LLM model name (e.g. `gpt-4o-mini`) |
| `llm.text.api_base` | Text LLM API base URL (for custom endpoints) |
| `llm.text.timeout` | Text LLM HTTP request timeout in seconds (default: 30) |
| `llm.vision.provider` | Vision LLM provider name |
| `llm.vision.api_key` | Vision LLM API key |
| `llm.vision.model` | Vision LLM model name |
| `llm.vision.api_base` | Vision LLM API base URL |
| `llm.vision.timeout` | Vision LLM HTTP request timeout in seconds (default: 30) |

## Data Backup

The `wr backup` command creates timestamped zip archives of your `~/.work-report/` data with automatic Grandfather-Father-Son (GFS) rotation.

### What gets backed up

`config.json`, `work-records/`, `digests.json`

### Backup location and naming

- **Default directory:** `~/.work-report/backups/`
- **Filename format:** `wr-backup-YYYYMMDD-HHMMSS.zip`
- Config stored separately at `~/.work-report/backup-config.json`

### GFS rotation

Backups are automatically classified by age and pruned according to a retention policy:

| Tier | Default retention | Description |
|------|-------------------|-------------|
| Daily | 7 | Last 7 daily backups |
| Weekly | 4 | Last 4 weekly backups (one per week) |
| Monthly | 6 | Last 6 monthly backups (one per month) |

Run `wr backup cleanup` to apply rotation and remove backups that exceed the policy.

### Backup configuration

Backup settings live in `~/.work-report/backup-config.json` (independent from the main `config.json`).

| Setting | Default | Description |
|---------|---------|-------------|
| `output_dir` | `~/.work-report/backups` | Backup output directory |
| `retention.daily` | `7` | Daily backups to keep |
| `retention.weekly` | `4` | Weekly backups to keep |
| `retention.monthly` | `6` | Monthly backups to keep |

### Quick examples

```bash
# Create a backup
wr backup create

# List all backups
wr backup list

# Run GFS rotation cleanup
wr backup cleanup

# View backup config
wr backup config show
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
| `wr digest` | Manage digest configurations |
| `wr prompt` | Manage LLM prompt templates |
| `wr import` | Bulk import from JSON file |
| `wr export` | Export to JSON or Markdown |
| `wr status` | Show configuration and data statistics |
| `wr config init` | Create config with defaults |
| `wr config set` | Set a config value |
| `wr config show` | Display config (secrets redacted) |
| `wr agent doctor` | Run health checks (LLM, Pushover, data directory) |
| `wr agent schema` | Print the JSONL schema for all commands |
| `wr remind due` | List due reminders |
| `wr remind push` | Push reminders via Pushover |
| `wr backup create` | Create a zip backup immediately |
| `wr backup list` | List all backups with metadata |
| `wr backup cleanup` | Run GFS rotation to remove old backups |
| `wr backup config show` | Display backup configuration |
| `wr backup config set` | Update backup configuration |

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

- **[SKILL.md](SKILL.md)** — AI agent integration manual with JSONL format specs, error codes, and command reference.
