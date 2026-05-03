# wr — Work Report CLI for AI Agents

## Overview

**wr** is a CLI tool that lets AI agents manage work reports through a local HTTP daemon. All output is JSONL (JSON Lines) — one JSON object per line on stdout. There is no human-readable mode. The only consumer is an AI agent.

**Architecture:** Thin CLI client + long-running daemon process. The CLI sends HTTP requests to the daemon; the daemon handles storage, scheduling, and LLM classification. The daemon must be running for most commands to work.

**JSONL-only contract:** Every command outputs exactly one JSONL line to stdout. Errors go to stdout as JSONL too — never to stderr. An agent can parse every response the same way.

---

## Quick Start

```
# 1. Create config with defaults
wr config init

# 2. (Optional) Set LLM keys for natural-language add
wr config set llm.text.api_key sk-xxx
wr config set llm.text.model gpt-4o-mini

# 3. Start the daemon (blocks until stopped)
wr daemon start &

# 4. Add a record
wr add --type meeting --title "Standup" --date 2026-05-03 --time 10:00

# 5. List records
wr list --date 2026-05-03

# 6. Generate a report
wr report today
```

---

## JSONL Format

Every CLI command outputs exactly one JSONL line to stdout. The envelope structure:

### Success shape

```json
{"status":"success","data":{...}}
```

- `status` — always `"success"`
- `data` — the response payload (object or array)

### Error shape

```json
{"status":"error","code":"daemon_not_running","message":"daemon not running: ...","suggestion":"Run 'wr daemon start' to start the daemon, then retry your command."}
```

- `status` — always `"error"`
- `code` — machine-readable error code (see Error Code Reference)
- `message` — human-readable description of what went wrong
- `suggestion` — (sometimes present) recommended next action

### Info shape

```json
{"status":"info","data":{"action":"cancel_or_update","classification":{...}}}
```

- Returned when LLM classification determines the user wants to cancel or update an existing record rather than add a new one.

---

## Command Reference

### wr add

Add a new work report entry.

**Usage:**

```
wr add [flags]
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--type` | string | `""` | Entry type: `meeting`, `task`, `reminder`, `log` |
| `--title` | string | `""` | Entry title |
| `--date` | string | `""` | Date in `YYYY-MM-DD` format |
| `--time` | string | `""` | Time in `HH:MM` format |
| `--description` | string | `""` | Longer description |
| `--tags` | string | `""` | Comma-separated tags (e.g. `"frontend,urgent"`) |
| `--location` | string | `""` | Location |
| `--related-person` | string | `""` | Related person name |
| `--priority` | string | `""` | Priority: `normal`, `high`, `medium` |
| `--remind-before` | string | `""` | Reminder offset (e.g. `15m`, `1h`) |
| `--recurring` | string | `""` | Recurring pattern (e.g. `daily`, `weekly`) |
| `--text` | string | `""` | Natural language text for LLM classification. When provided, `--type`, `--title`, `--date` become optional. |
| `--image` | string | `""` | Image file path for LLM vision classification. When provided, `--type`, `--title`, `--date` become optional. |

**Notes:**
- If both `--text` (or `--image`) and `--type` are provided, the explicit flags take precedence over LLM classification.
- If LLM classification returns `cancel_or_update` type, no record is created — the response has `status: "info"`.

**Manual example:**

```bash
wr add --type meeting --title "Project sync" --date 2026-05-03 --time 14:00 --location "Room 3A" --tags "project,weekly"
```

**Output:**

```json
{"status":"success","data":{"type":"meeting","title":"Project sync","date":"2026-05-03","time":"14:00","location":"Room 3A","tags":["project","weekly"],"priority":"","status":"active","short_id":"a1b2c3d4e5f67890","saved_at":"2026-05-03T14:00:00+08:00"}}
```

**LLM text classification example:**

```bash
wr add --text "明天下午3点和张三讨论Q2季度报告"
```

The daemon classifies this via the LLM text provider and auto-fills type/title/date/time/related_person. Output is the same success shape.

**LLM image classification example:**

```bash
wr add --image /tmp/screenshot.png --text "see attached"
```

The daemon classifies via the LLM vision provider.

**Error codes:** `invalid_type`, `invalid_body`, `llm_not_configured`, `llm_error`, `storage_error`, `daemon_not_running`

---

### wr list

List work report entries with optional filters.

**Usage:**

```
wr list [flags]
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--type` | string | `""` | Filter by type: `meeting`, `task`, `reminder`, `log` |
| `--date` | string | `""` | Exact date filter (`YYYY-MM-DD`) |
| `--from` | string | `""` | Date range start, inclusive (`YYYY-MM-DD`) |
| `--to` | string | `""` | Date range end, inclusive (`YYYY-MM-DD`) |
| `--status` | string | `""` | Filter by status: `active`, `completed`, `cancelled`, `all` |
| `--query` | string | `""` | Keyword search in title and description |

**Notes:**
- Multiple flags are combined (AND logic).
- Default status filter excludes completed records unless `--status all` or `--status completed` is specified.

**Example:**

```bash
wr list --date 2026-05-03 --type meeting
```

**Output:**

```json
{"status":"success","data":{"action":"list","count":2,"entries":[{"short_id":"a1b2c3d4e5f67890","type":"meeting","title":"Project sync","date":"2026-05-03","time":"14:00","status":"active"},{"short_id":"f0e1d2c3b4a56789","type":"meeting","title":"Standup","date":"2026-05-03","time":"10:00","status":"active"}]}}
```

**Error codes:** `storage_error`, `daemon_not_running`

---

### wr update

Update fields of an existing work report entry. Only explicitly provided flags are changed — flags default to empty string and are only sent when explicitly set on the command line.

**Usage:**

```
wr update <short_id> [flags]
```

**Arguments:** Exactly one positional argument — the record's `short_id`.

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--title` | string | `""` | Update title |
| `--description` | string | `""` | Update description |
| `--date` | string | `""` | Update date (`YYYY-MM-DD`) |
| `--time` | string | `""` | Update time (`HH:MM`) |
| `--location` | string | `""` | Update location |
| `--tags` | stringSlice | `nil` | Update tags (comma-separated, replaces entire list) |
| `--priority` | string | `""` | Update priority |
| `--remind-before` | string | `""` | Update remind_before (e.g. `15m`, `1h`) |
| `--recurring` | string | `""` | Update recurring pattern |
| `--end-time` | string | `""` | Update end time (`HH:MM`) |
| `--related-person` | string | `""` | Update related person |
| `--participants` | stringSlice | `nil` | Update participants (comma-separated) |
| `--agenda` | string | `""` | Update agenda |
| `--notes` | string | `""` | Update notes |
| `--progress` | string | `""` | Update progress |

**Notes:**
- Updating time-related fields (`time`, `date`, `remind_before`, `recurring`) triggers scheduler re-registration for reminder notifications.
- Completed or cancelled records cannot be updated — returns `already_completed` or `already_cancelled`.
- At least one field flag must be explicitly set, otherwise the command prints help text to stderr.

**Example:**

```bash
wr update a1b2c3d4e5f67890 --time 15:00 --location "Room 5B"
```

**Output:**

```json
{"status":"success","data":{"type":"meeting","title":"Project sync","date":"2026-05-03","time":"15:00","location":"Room 5B","status":"active","short_id":"a1b2c3d4e5f67890","saved_at":"2026-05-03T14:00:00+08:00","updated_at":"2026-05-03T14:30:00+08:00"}}
```

**Error codes:** `record_not_found`, `already_completed`, `already_cancelled`, `invalid_body`, `invalid_field`, `storage_error`, `daemon_not_running`

---

### wr complete

Mark an active work report entry as completed.

**Usage:**

```
wr complete <short_id>
```

**Arguments:** Exactly one positional argument — the record's `short_id`.

**Flags:** None.

**Notes:**
- Only active records can be completed. Attempting to complete an already-completed or already-cancelled record returns `storage_error`.
- Completing a record unregisters it from the scheduler (reminders stop).

**Example:**

```bash
wr complete a1b2c3d4e5f67890
```

**Output:**

```json
{"status":"success","data":{"type":"meeting","title":"Project sync","date":"2026-05-03","time":"15:00","status":"completed","short_id":"a1b2c3d4e5f67890","saved_at":"2026-05-03T14:00:00+08:00","updated_at":"2026-05-03T16:00:00+08:00"}}
```

**Error codes:** `record_not_found`, `storage_error`, `daemon_not_running`

---

### wr cancel

Cancel an active work report entry.

**Usage:**

```
wr cancel <short_id>
```

**Arguments:** Exactly one positional argument — the record's `short_id`.

**Flags:** None.

**Notes:**
- Only active records can be cancelled. Attempting to cancel an already-cancelled or already-completed record returns `storage_error`.
- Cancelling a record unregisters it from the scheduler.

**Example:**

```bash
wr cancel f0e1d2c3b4a56789
```

**Output:**

```json
{"status":"success","data":{"type":"meeting","title":"Standup","date":"2026-05-03","time":"10:00","status":"cancelled","short_id":"f0e1d2c3b4a56789","saved_at":"2026-05-03T10:00:00+08:00","updated_at":"2026-05-03T12:00:00+08:00"}}
```

**Error codes:** `record_not_found`, `storage_error`, `daemon_not_running`

---

### wr report

Generate work reports. This is a command group with subcommands.

**Usage:**

```
wr report <subcommand> [flags]
```

#### Subcommands

##### wr report today

Generate today's work report.

```bash
wr report today
```

**Output:**

```json
{"status":"success","data":{"date":"2026-05-03","summary":{"meetings":2,"tasks":3,"reminders":1,"logs":4,"total":10},"markdown":"...","entries":[...]}}
```

##### wr report date \<YYYY-MM-DD\>

Generate report for a specific date.

```bash
wr report date 2026-05-01
```

**Arguments:** Exactly one — the date in `YYYY-MM-DD` format.

##### wr report week

Generate report for the current week (Monday through Sunday).

```bash
wr report week
```

**Output:**

```json
{"status":"success","data":{"date_from":"2026-04-27","date_to":"2026-05-03","days_count":7,"summary":{"meetings":8,"tasks":12,"reminders":3,"logs":15,"total":38},"markdown":"..."}}
```

##### wr report range

Generate report for a date range.

```bash
wr report range --from 2026-04-27 --to 2026-05-03
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--from` | string | `""` | Start date (`YYYY-MM-DD`, required) |
| `--to` | string | `""` | End date (`YYYY-MM-DD`, required) |

##### wr report push \<subcommand\>

Generate and push a report via Pushover notification. Requires Pushover credentials in config.

**Sub-subcommands:**

| Command | Description |
|---------|-------------|
| `wr report push today` | Push today's report |
| `wr report push date <YYYY-MM-DD>` | Push a specific date's report |
| `wr report push week` | Push the current week's report |
| `wr report push range --from X --to Y` | Push a date range report |

**Push success output:**

```json
{"status":"success","data":{"date":"2026-05-03","total":10,"pushed":true,"summary":{"meetings":2,"tasks":3,"reminders":1,"logs":4,"total":10}}}
```

**Error codes:** `storage_error`, `pushover_not_configured`, `push_error`, `invalid_body`, `daemon_not_running`

---

### wr daemon

Manage the wr daemon process. This is a command group.

**Usage:**

```
wr daemon <subcommand>
```

#### wr daemon start

Start the daemon. This is a foreground process — it blocks until stopped with SIGINT/SIGTERM. Run in the background with `&` or a process manager.

```bash
wr daemon start > /dev/null 2>&1 &
```

**Flags:** None.

**Behavior:**
- Loads config from `~/.work-report/config.json`
- Creates data directory if it doesn't exist
- Writes daemon state to a state file (port, PID)
- Starts the scheduler for reminder notifications
- Performs catch-up for missed reminders on startup
- Cleans up state file on shutdown

**Notes:**
- The daemon listens on `127.0.0.1:<port>` (default port `18080`).
- All daemon log messages go to stderr, not stdout.
- The daemon version is `0.1.0`.

---

### wr status

Show daemon status and config diagnostics.

**Usage:**

```
wr status
```

**Flags:** None.

**Behavior:**
1. Tries to reach the daemon at `/api/status`.
2. If the daemon is running, returns daemon info, config diagnostics, and scheduler state.
3. If the daemon is not running, falls through to local diagnostics (config existence, Pushover/LLM configuration status, data dir accessibility).

**Daemon running output:**

```json
{"status":"success","data":{"daemon":{"version":"0.1.0","status":"running","pid":12345,"port":18080},"config":{"exists":true,"pushover":{"configured":true},"llm":{"text":{"configured":true},"vision":{"configured":false}},"data_dir":{"path":"/home/user/.work-report/work-records","accessible":true}},"scheduler":{"running":true,"entries_count":3}}}
```

**Daemon not running output:**

```json
{"status":"success","data":{"daemon":{"status":"not_running","suggestion":"Run 'wr daemon start' to start the daemon."},"config":{"exists":true,"pushover":{"configured":false},"llm":{"text":{"configured":true},"vision":{"configured":false}},"data_dir":{"path":"/home/user/.work-report/work-records","accessible":true}}}}
```

---

### wr config

Manage wr configuration. This is a command group with subcommands.

**Usage:**

```
wr config <subcommand> [flags]
```

#### wr config init

Create `~/.work-report/config.json` with sensible defaults. Any provided flags override the defaults.

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--pushover-token` | string | `""` | Pushover API token |
| `--pushover-key` | string | `""` | Pushover user key |
| `--llm-text-key` | string | `""` | LLM text provider API key |
| `--llm-text-model` | string | `""` | LLM text model name |
| `--llm-text-api-base` | string | `""` | LLM text API base URL |
| `--llm-text-provider` | string | `""` | LLM text provider name |
| `--llm-vision-key` | string | `""` | LLM vision provider API key |
| `--llm-vision-model` | string | `""` | LLM vision model name |
| `--llm-vision-api-base` | string | `""` | LLM vision API base URL |
| `--llm-vision-provider` | string | `""` | LLM vision provider name |
| `--daemon-port` | int | `0` (defaults to 18080) | Daemon listen port |
| `--timezone` | string | `""` (defaults to `Asia/Shanghai`) | IANA timezone |

**Example:**

```bash
wr config init --llm-text-key sk-xxx --llm-text-model gpt-4o-mini
```

**Output:**

```json
{"status":"success","data":{"path":"/home/user/.work-report/config.json"}}
```

#### wr config set \<key\> \<value\>

Set a config value by dot-notation key. Loads config, applies the change, validates, and saves.

**Arguments:** Exactly two — the config key and the new value.

**Valid keys:**

| Key | Value type | Description |
|-----|-----------|-------------|
| `pushover.api_token` | string | Pushover API token |
| `pushover.user_key` | string | Pushover user key |
| `llm.text.provider` | string | LLM text provider name |
| `llm.text.api_key` | string | LLM text API key |
| `llm.text.api_base` | string | LLM text API base URL |
| `llm.text.model` | string | LLM text model name |
| `llm.vision.provider` | string | LLM vision provider name |
| `llm.vision.api_key` | string | LLM vision API key |
| `llm.vision.api_base` | string | LLM vision API base URL |
| `llm.vision.model` | string | LLM vision model name |
| `data_dir` | string | Data directory path |
| `daemon.port` | int (as string) | Daemon listen port (1–65535) |
| `timezone` | string | IANA timezone string |

**Example:**

```bash
wr config set llm.text.api_key sk-newkey123
```

**Output:**

```json
{"status":"success","data":{"key":"llm.text.api_key","value":"sk-newkey123"}}
```

#### wr config show

Display current config with all secrets redacted (first 4 chars shown, rest masked).

```bash
wr config show
```

**Output:**

```json
{"status":"success","data":{"pushover":{"api_token":"sk-x****","user_key":"user****"},"llm":{"text":{"provider":"","api_key":"sk-x****","api_base":"","model":"gpt-4o-mini"},"vision":{"provider":"","api_key":"","api_base":"","model":""}},"data_dir":"/home/user/.work-report/work-records","daemon":{"port":18080},"timezone":"Asia/Shanghai"}}
```

---

### wr --version

Print the wr version.

```bash
wr --version
```

---

## Error Code Reference

Complete table of error codes that may appear in the `"code"` field of error responses.

| Code | Meaning | Recommended Agent Response |
|------|---------|---------------------------|
| `daemon_not_running` | Daemon is not reachable (no state file, corrupt state, connection refused, timeout) | Run `wr daemon start` in background, wait briefly, then retry the original command. |
| `invalid_type` | Missing or unrecognized record type | Ensure `--type` is one of: `meeting`, `task`, `reminder`, `log`. Or provide `--text`/`--image` for LLM classification. |
| `invalid_body` | Missing required fields or malformed request body | Check that required flags (`--title`, `--date`, `--type`) are provided. |
| `invalid_field` | Attempted to update a field that is not allowed | Check the field name in the update command. |
| `record_not_found` | No record matches the given short_id | List records with `wr list` to find the correct short_id. |
| `already_completed` | Attempted to update a completed record | Completed records cannot be modified. Use `wr list --status completed` to view them. |
| `already_cancelled` | Attempted to update a cancelled record | Cancelled records cannot be modified. |
| `storage_error` | Filesystem or storage layer error | Check data directory permissions and disk space. |
| `llm_not_configured` | LLM API key is missing for the requested classification mode | Run `wr config set llm.text.api_key <key>` (or `llm.vision.api_key` for image). |
| `llm_error` | LLM API call failed | Check API key validity, network connectivity, and model name. Retry once. |
| `pushover_not_configured` | Pushover credentials are missing | Run `wr config set pushover.api_token <token>` and `wr config set pushover.user_key <key>`. |
| `push_error` | Pushover notification delivery failed | Check Pushover credentials and network. |

---

## Record Types & Fields

### Common Fields (all types)

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Record type: `meeting`, `task`, `reminder`, `log` |
| `title` | string | Entry title |
| `description` | string | Longer description (optional) |
| `date` | string | Date in `YYYY-MM-DD` format |
| `time` | string | Time in `HH:MM` format (optional) |
| `end_time` | string | End time in `HH:MM` format (optional) |
| `location` | string | Location (optional) |
| `related_person` | string | Related person (optional) |
| `remind_before` | string | Reminder offset like `15m`, `1h` (optional) |
| `priority` | string | `normal`, `high`, or `medium` (optional) |
| `status` | string | `active`, `completed`, or `cancelled` |
| `tags` | []string | Tags list (optional) |
| `saved_at` | string | ISO-8601 timestamp when record was created |
| `updated_at` | string | ISO-8601 timestamp when record was last modified (optional) |
| `short_id` | string | 16-character hex identifier for CLI reference |

### Meeting-specific Fields

| Field | Type | Description |
|-------|------|-------------|
| `participants` | []string | Meeting participants |
| `agenda` | string | Meeting agenda |

### Task-specific Fields

| Field | Type | Description |
|-------|------|-------------|
| `completed_at` | string | ISO-8601 timestamp when task was completed |
| `raw_input` | string | Original user input text |
| `processed_at` | string | ISO-8601 timestamp when task was processed |
| `related_persons` | []string | Related persons |
| `reminder` | string | Reminder offset (e.g. `30m`) |

### Reminder-specific Fields

| Field | Type | Description |
|-------|------|-------------|
| `notes` | string | Additional notes |
| `recurring` | string | Recurring pattern (e.g. `daily`, `weekly`) |

### Log-specific Fields

| Field | Type | Description |
|-------|------|-------------|
| `priority` | string | Priority level |
| `progress` | string | Progress description |

### ShortID

The `short_id` is a 16-character hex string (e.g. `a1b2c3d4e5f67890`) derived by SHA-256 hashing the record's filename stem and taking the first 16 hex characters. It is not a filesystem path — it is a hash. Use it as the identifier for `update`, `complete`, and `cancel` commands.

ShortIDs are deterministic from the filename but are not reversible. To find a record's short_id, use `wr list`.

---

## Common Workflows

### First-Time Setup

```bash
# 1. Initialize config
wr config init

# 2. (Optional) Configure LLM for natural language input
wr config set llm.text.api_key sk-your-key
wr config set llm.text.model gpt-4o-mini

# 3. (Optional) Configure Pushover for push notifications
wr config set pushover.api_token your-token
wr config set pushover.user_key your-key

# 4. Start the daemon
wr daemon start > /dev/null 2>&1 &

# 5. Verify it's running
wr status
```

### Daily Usage

```bash
# Add entries throughout the day
wr add --type meeting --title "Sprint planning" --date 2026-05-03 --time 09:00 --participants "Alice,Bob"
wr add --type task --title "Review PR #42" --date 2026-05-03 --priority high
wr add --type log --title "Deployed v2.1 to staging" --date 2026-05-03

# Check today's entries
wr list --date 2026-05-03

# Complete a task
wr complete a1b2c3d4e5f67890

# Generate end-of-day report
wr report today

# Push report via Pushover
wr report push today
```

### Record Lifecycle

```bash
# 1. Add a record
wr add --type task --title "Write unit tests" --date 2026-05-03 --priority high
# → returns short_id: "abcdef1234567890"

# 2. Update it
wr update abcdef1234567890 --priority medium --tags "testing,backend"

# 3. Complete it
wr complete abcdef1234567890

# 4. Verify in list (need --status to see completed)
wr list --date 2026-05-03 --status all
```

### Report Generation

```bash
# Today's report
wr report today

# Specific date
wr report date 2026-05-01

# This week (Mon–Sun)
wr report week

# Custom range
wr report range --from 2026-04-27 --to 2026-05-03

# Push any of the above via Pushover
wr report push today
wr report push date 2026-05-01
wr report push week
wr report push range --from 2026-04-27 --to 2026-05-03
```

### Using LLM Classification

```bash
# Text classification — daemon auto-detects type, title, date, etc.
wr add --text "明天上午10点和产品团队开需求评审会"

# Image classification — daemon uses vision LLM
wr add --image /tmp/whiteboard.jpg --text "whiteboard notes from meeting"

# If LLM returns cancel_or_update, the response has status "info" and no record is created
```

---

## Configuration

### Config File Location

```
~/.work-report/config.json
```

### Config Structure

```json
{
  "pushover": {
    "api_token": "",
    "user_key": ""
  },
  "llm": {
    "text": {
      "provider": "",
      "api_key": "",
      "api_base": "",
      "model": ""
    },
    "vision": {
      "provider": "",
      "api_key": "",
      "api_base": "",
      "model": ""
    }
  },
  "data_dir": "~/.work-report/work-records",
  "daemon": {
    "port": 18080
  },
  "timezone": "Asia/Shanghai"
}
```

### Defaults

| Setting | Default |
|---------|---------|
| `daemon.port` | `18080` |
| `timezone` | `Asia/Shanghai` |
| `data_dir` | `~/.work-report/work-records` |

### LLM Text vs Vision

The config separates LLM providers into `llm.text` and `llm.vision`:

- **`llm.text`** — Used when `wr add --text "..."` is called. Classifies natural language descriptions into typed records.
- **`llm.vision`** — Used when `wr add --image /path/to/file` is called. Classifies images (screenshots, photos) into typed records. Can be a different provider/model than text.

Each has independent `provider`, `api_key`, `api_base`, and `model` settings.

### Validation

- `daemon.port` must be 1–65535.
- `timezone` must be a valid IANA timezone string.
- Config is validated on load and on save. `wr config set` validates before saving.

---

## Tips & Gotchas

1. **Daemon must be running.** Almost every command (add, list, update, complete, cancel, report, status) requires the daemon. The only commands that work without it are `wr config init`, `wr config set`, and `wr config show`. If you see `daemon_not_running`, start the daemon with `wr daemon start` and retry.

2. **JSONL goes to stdout only, never stderr.** The root command sets `SilenceUsage` and `SilenceErrors` to prevent Cobra from writing non-JSONL text to stderr. All structured output (including errors) is JSONL on stdout.

3. **ShortID is a hash, not a file path.** The 16-character hex ID is derived from the record filename via SHA-256. It cannot be reversed to find the file. Always use `wr list` to discover short_ids.

4. **Scheduler re-registration on update.** When you update time-related fields (`time`, `date`, `remind_before`, `recurring`), the daemon unregisters the old scheduler entry and re-registers with the new values. This is automatic.

5. **Completed/cancelled records are immutable.** You cannot update, complete, or cancel a record that is already completed or cancelled. Check status with `wr list --status all` first.

6. **LLM classification makes type/title/date optional.** When using `--text` or `--image`, the LLM fills in `type`, `title`, and `date` automatically. Explicit flags (`--type`, `--title`, `--date`) take precedence over LLM results.

7. **`wr config set` validates before saving.** Invalid values (bad port, unknown timezone) are rejected before the config file is modified. The config is always in a valid state on disk.

8. **Pushover requires both api_token and user_key.** Both must be non-empty for push commands to work. Missing either one returns `pushover_not_configured`.

9. **Report date defaults to "today" in configured timezone.** The daemon uses the timezone from config (default: `Asia/Shanghai`) to determine "today".

10. **Tags are comma-separated strings.** In `wr add`, use `--tags "tag1,tag2"`. In `wr update`, use `--tags "tag1,tag2"` (replaces the entire list, does not append).
