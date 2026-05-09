# wr — Work Report CLI for AI Agents

## Overview

**wr** is a CLI tool that lets AI agents manage work reports through a local HTTP daemon. All output is JSONL (JSON Lines) — one JSON object per line on stdout. There is no human-readable mode. The only consumer is an AI agent.

**Architecture:** Thin CLI client + long-running daemon process. The CLI sends HTTP requests to the daemon; the daemon handles storage, scheduling, and LLM classification. The daemon must be running for most commands to work.

**JSONL-only contract:** Every command outputs exactly one JSONL line to stdout. Errors go to stdout as JSONL too — never to stderr. An agent can parse every response the same way.

---

## 快速集成（5 分钟）

Agent 只需读取本段即可完成 add / list / report 三个核心操作。完整参考见下方各章节。

### 前置

```bash
wr config init                    # 最小配置（无需 LLM key）
wr agent daemon ensure-running    # 启动守护进程（幂等）
```

### 添加记录

`--date` 省略时默认今天。`--type` 和 `--title` 必填。

```bash
wr add --type log --title "完成了代码审查"
wr add --type meeting --title "站会" --time 10:00
wr add --type task --title "Review PR #42" --priority high
```

输出为 JSONL：`type: "result"` 成功，`type: "error"` 失败（查看 `error_code`）。

### 查看 / 报告

```bash
wr list              # 今天 active 记录
wr report today      # 今日报告（含 markdown）
wr report week       # 本周报告
```

### 完成 / 取消

```bash
wr complete <short_id>                            # 通过 ID
wr complete --title "Review PR #42" --date 2026-05-08  # 通过标题+日期
```

> `--date` 在 complete/cancel 中**不默认今天**，必须显式提供。

### 常见错误

| `error_code` | 处理 |
|---|---|
| `daemon_not_running` | `wr agent daemon ensure-running` 后重试 |
| `record_not_found` | `wr list` 查找正确 ID |
| `invalid_type` | `--type` 须为 meeting / task / reminder / log |

> 完整命令参考、JSONL 格式规范、错误码表见下方各章节。

---

## Migration Notes

**M005 SDK migration (v0.1.0):** Daemon commands moved from `wr daemon` to the `wr agent daemon` namespace. The old bare paths (`wr daemon start`, `wr daemon stop`, `wr daemon status`) are no longer registered. All daemon management commands are now under `wr agent daemon`:

| Old command | New command |
|-------------|-------------|
| `wr daemon start` | `wr agent daemon start` |
| `wr daemon stop` | `wr agent daemon stop` |
| `wr daemon status` | `wr agent daemon status` |

If you have scripts or workflows using the old paths, update them to include the `agent` prefix.

---

## Quick Start

```
# 1. Init config (minimal — no LLM key required)
wr config init

# 2. Ensure daemon is running (idempotent — starts if needed, succeeds if already running)
wr agent daemon ensure-running

# 3. Add a record (--date defaults to today if omitted)
wr add --type meeting --title "Standup" --time 10:00

# 4. List records (defaults to today)
wr list

# 5. Generate a report
wr report today
```

> **LLM is optional.** Without an LLM key, you must provide `--type` and `--title` (and optionally `--date`). To enable natural-language classification, pass `--llm-text-key sk-xxx --llm-text-model gpt-4o-mini` to `config init`.

---

## JSONL Format

Every CLI command outputs exactly one JSONL line to stdout. The `type` field is the primary discriminator — always inspect `type` first to determine how to handle the response.

### Envelope fields

| Field | Type | Description |
|-------|------|-------------|
| `version` | string | Envelope version, always `"1.0"` |
| `tool` | string | Tool name, always `"wr"` |
| `type` | string | Response type: `result`, `error`, `warning`, or `progress` |
| `timestamp` | string | ISO-8601 UTC timestamp (e.g. `"2026-05-03T14:00:00Z"`) |
| `data` | object | Present when `type` is `result`. Contains the response payload. |
| `error_code` | string | Present when `type` is `error`. Machine-readable error code (see Error Code Reference). |
| `message` | string | Present when `type` is `error` or `warning`. Human-readable description. |
| `percent` | number | Present when `type` is `progress`. Completion percentage (0–100). |

### Result shape

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{...}}
```

- `type` — always `"result"` for successful responses
- `data` — the response payload (object or array)

### Error shape

```json
{"version":"1.0","tool":"wr","type":"error","timestamp":"2026-05-03T14:00:00Z","error_code":"daemon_not_running","message":"daemon not running: ..."}
```

- `type` — always `"error"`
- `error_code` — machine-readable error code (see Error Code Reference)
- `message` — human-readable description of what went wrong

### Warning shape

```json
{"version":"1.0","tool":"wr","type":"warning","timestamp":"2026-05-03T14:00:00Z","message":"..."}
```

- `type` — always `"warning"`
- `message` — description of the warning condition

### Progress shape

```json
{"version":"1.0","tool":"wr","type":"progress","timestamp":"2026-05-03T14:00:00Z","percent":50,"message":"Processing..."}
```

- `type` — always `"progress"`
- `percent` — completion percentage (0–100)
- `message` — progress description

### Cancel/update result

When LLM classification determines the user wants to cancel or update an existing record rather than add a new one, the response is a normal `result` type:

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"action":"cancel_or_update","classification":{...}}}
```

No record is created — inspect `data.action` to determine next steps.

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
| `--date` | string | `""` | Date in `YYYY-MM-DD` format. Defaults to today (in configured timezone) if omitted. |
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
| `--idempotency-key` | string | `""` | Idempotency key for deduplication. Retrying with the same key returns the existing record instead of creating a duplicate. |

**Notes:**
- `--type` and `--title` are required when not using LLM classification (no `--text`/`--image`).
- `--date` defaults to today (in configured timezone) when omitted and not using LLM classification. When using `--text` or `--image`, the LLM provides the date.
- If both `--text` (or `--image`) and `--type` are provided, the explicit flags take precedence over LLM classification.
- If LLM classification returns `cancel_or_update` type, no record is created — the response has `type: "result"` with `data.action` set to `"cancel_or_update"`.

**Manual example:**

```bash
wr add --type meeting --title "Project sync" --date 2026-05-03 --time 14:00 --location "Room 3A" --tags "project,weekly"
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"type":"meeting","title":"Project sync","date":"2026-05-03","time":"14:00","location":"Room 3A","status":"active","tags":["project","weekly"],"saved_at":"2026-05-03T14:00:00+08:00","short_id":"a1b2c3d4e5f67890"}}
```

Empty fields (e.g. `priority`, `description`, `remind_before`, `recurring`) are omitted from the response — only fields with non-empty values are included.

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

**Idempotency key example:**

```bash
wr add --type task --title "Daily standup" --date 2026-05-03 --idempotency-key "standup-2026-05-03"
```

If called again with the same `--idempotency-key`, the existing record is returned — no duplicate is created. Use this in retry loops or scheduled workflows to avoid double-entry.

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
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T10:00:00Z","data":{"action":"list","count":2,"entries":[{"date":"2026-05-03","short_id":"a1b2c3d4e5f67890","status":"active","time":"14:00","title":"Project sync","type":"meeting"},{"date":"2026-05-03","short_id":"f0e1d2c3b4a56789","status":"active","time":"10:00","title":"Standup","type":"meeting"}]}}
```

**Error codes:** `storage_error`, `daemon_not_running`

---

### wr update

Update fields of an existing work report entry. Only explicitly provided flags are changed — flags default to empty string and are only sent when explicitly set on the command line.

**Usage:**

```
wr update [<short_id>] [flags]
```

**Arguments:** Optional positional `<short_id>`. When omitted, `--title` and `--date` become required for content-based lookup.

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--title` | string | `""` | When `<short_id>` provided: update title. Otherwise: lookup by exact title (required for lookup). |
| `--description` | string | `""` | Update description |
| `--date` | string | `""` | When `<short_id>` provided: update date (`YYYY-MM-DD`). Otherwise: lookup by date (required for lookup). |
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
- **Content-based lookup:** When `<short_id>` is omitted, both `--title` and `--date` are required as lookup criteria. The `--title` and `--date` flags serve as query params (not field updates). If multiple active records match, returns `multiple_matches` error. If no match, returns `record_not_found`.

**By short_id example:**

```bash
wr update a1b2c3d4e5f67890 --time 15:00 --location "Room 5B"
```

**Content-based lookup example:**

```bash
wr update --title "Project sync" --date 2026-05-03 --time 15:00 --location "Room 5B"
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:30:00Z","data":{"type":"meeting","title":"Project sync","date":"2026-05-03","time":"15:00","location":"Room 5B","status":"active","tags":["project","weekly"],"saved_at":"2026-05-03T14:00:00+08:00","updated_at":"2026-05-03T14:30:00+08:00","short_id":"a1b2c3d4e5f67890"}}
```

**Error codes:** `record_not_found`, `multiple_matches`, `already_completed`, `already_cancelled`, `invalid_body`, `invalid_field`, `invalid_params`, `storage_error`, `daemon_not_running`

---

Mark an active work report entry as completed.

**Usage:**

```
wr complete [<short_id>]
wr complete --title <title> --date <YYYY-MM-DD>
```

**Arguments:** Optional positional `<short_id>`. When omitted, `--title` and `--date` become required for content-based lookup.

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--title` | string | `""` | Lookup by exact title (required when no `<short_id>`) |
| `--date` | string | `""` | Lookup by date `YYYY-MM-DD` (required when no `<short_id>`) |

**Notes:**
- Only active records can be completed. Attempting to complete an already-completed or already-cancelled record returns `storage_error`.
- Completing a record unregisters it from the scheduler (reminders stop).
- **Content-based lookup:** When `<short_id>` is omitted, both `--title` and `--date` are required. If multiple active records match, returns `multiple_matches` error.

**By short_id example:**

```bash
wr complete a1b2c3d4e5f67890
```

**Content-based lookup example:**

```bash
wr complete --title "Review PR #42" --date 2026-05-03
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T16:00:00Z","data":{"type":"meeting","title":"Project sync","date":"2026-05-03","time":"15:00","location":"Room 5B","status":"completed","tags":["project","weekly"],"saved_at":"2026-05-03T14:00:00+08:00","updated_at":"2026-05-03T16:00:00+08:00","short_id":"a1b2c3d4e5f67890"}}
```

**Error codes:** `record_not_found`, `multiple_matches`, `storage_error`, `daemon_not_running`

---

### wr cancel

Cancel an active work report entry.

**Usage:**

```
wr cancel [<short_id>]
wr cancel --title <title> --date <YYYY-MM-DD>
```

**Arguments:** Optional positional `<short_id>`. When omitted, `--title` and `--date` become required for content-based lookup.

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--title` | string | `""` | Lookup by exact title (required when no `<short_id>`) |
| `--date` | string | `""` | Lookup by date `YYYY-MM-DD` (required when no `<short_id>`) |

**Notes:**
- Only active records can be cancelled. Attempting to cancel an already-cancelled or already-completed record returns `storage_error`.
- Cancelling a record unregisters it from the scheduler.
- **Content-based lookup:** When `<short_id>` is omitted, both `--title` and `--date` are required. If multiple active records match, returns `multiple_matches` error.

**By short_id example:**

```bash
wr cancel f0e1d2c3b4a56789
```

**Content-based lookup example:**

```bash
wr cancel --title "Standup" --date 2026-05-03
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T12:00:00Z","data":{"type":"meeting","title":"Standup","date":"2026-05-03","time":"10:00","status":"cancelled","saved_at":"2026-05-03T10:00:00+08:00","updated_at":"2026-05-03T12:00:00+08:00","short_id":"f0e1d2c3b4a56789"}}
```

**Error codes:** `record_not_found`, `multiple_matches`, `storage_error`, `daemon_not_running`

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
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T18:00:00Z","data":{"date":"2026-05-03","meetings":[],"tasks":[],"reminders":[],"logs":[],"summary":{"total":0,"meetings":0,"tasks":0,"reminders":0,"logs":0},"markdown":"# 工作日报 2026-05-03\n\n📊 **汇总**: 会议 0 | 任务 0 | 提醒 0 | 日志 0 | 共计 0 条\n\n"}}
```

The `data` object contains typed arrays (`meetings`, `tasks`, `reminders`, `logs`) instead of a flat `entries` array. Each array contains the full record objects for that type. The `summary` contains counts by type and total. The `markdown` field contains the formatted Chinese-language report.

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
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T18:00:00Z","data":{"date_from":"2026-04-27","date_to":"2026-05-03","days_count":7,"days":[{"date":"2026-04-27","meetings":[],"tasks":[],"reminders":[],"logs":[],"summary":{"total":0,"meetings":0,"tasks":0,"reminders":0,"logs":0},"markdown":"# 工作日报 2026-04-27\n\n📊 **汇总**: 会议 0 | 任务 0 | 提醒 0 | 日志 0 | 共计 0 条\n\n"},{"date":"2026-04-28","meetings":[],"tasks":[],"reminders":[],"logs":[],"summary":{"total":0,"meetings":0,"tasks":0,"reminders":0,"logs":0},"markdown":"# 工作日报 2026-04-28\n\n📊 **汇总**: 会议 0 | 任务 0 | 提醒 0 | 日志 0 | 共计 0 条\n\n"}],"merged_meetings":null,"merged_tasks":null,"merged_reminders":null,"merged_logs":null,"summary":{"total":0,"meetings":0,"tasks":0,"reminders":0,"logs":0},"markdown":"# 工作周报 2026-04-27 ~ 2026-05-03\n\n📊 **汇总** (7天): 会议 0 | 任务 0 | 提醒 0 | 日志 0 | 共计 0 条\n\n"}}
```

The `days` array contains per-day report objects (same structure as `wr report today`). The `merged_*` fields contain cross-day aggregated records (or `null` when empty). The top-level `summary` and `markdown` provide the week overview.

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
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T18:00:00Z","data":{"date":"2026-05-03","total":10,"pushed":true,"summary":{"meetings":2,"tasks":3,"reminders":1,"logs":4,"total":10}}}
```

**Error codes:** `storage_error`, `pushover_not_configured`, `push_error`, `invalid_body`, `daemon_not_running`

---

### wr import

Bulk import work report entries from a JSON file. Validates all records before persisting any — if any record is invalid, nothing is written.

**Usage:**

```
wr import --file <path>
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--file` | string | `""` | Path to JSON file containing records to import (required) |

**Input format:**

The file must contain either:
- A JSON array of record objects: `[{"type":"meeting","title":"...","date":"..."}, ...]`
- A JSON object with a `"records"` key: `{"records":[{"type":"meeting","title":"...","date":"..."}]}`

Each record object supports the same fields as `wr add` (type, title, date, time, description, tags, location, related_person, priority, remind_before, recurring, end_time, participants, agenda, notes, progress).

**Required fields per record:** `type` (must be one of: `meeting`, `task`, `reminder`, `log`), `title`, `date` (`YYYY-MM-DD`).

**Behavior:**
- All records are validated before any are persisted (fail-fast with rollback).
- Imported records receive fresh ShortIDs and new `saved_at` timestamps.
- If any record fails validation, NO records are written to storage.
- The CLI uses a 30-second timeout (longer than other commands) to accommodate bulk imports.

**Example:**

```bash
wr import --file /tmp/records.json
```

**Input file example:**

```json
[
  {"type":"meeting","title":"Sprint planning","date":"2026-05-01","time":"09:00","participants":["Alice","Bob"]},
  {"type":"task","title":"Review PR #42","date":"2026-05-02","priority":"high"},
  {"type":"log","title":"Deployed v2.1","date":"2026-05-03","progress":"completed"}
]
```

**Success output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"imported":3}}
```

**Validation error output (record at index 1 missing date):**

```json
{"version":"1.0","tool":"wr","type":"error","timestamp":"2026-05-03T14:00:00Z","error_code":"import_record","message":"record at index 1: missing required field: date"}
```

**Error codes:** `invalid_body` (file read error, malformed JSON, wrong JSON structure), `import_record` (per-record validation failure with index and field info), `storage_error`, `daemon_not_running`

---

### wr export

Export work report entries in JSON or Markdown format with optional filters.

**Usage:**

```
wr export --format <json|markdown> [flags]
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--format` | string | `""` | Output format: `json` or `markdown` (required) |
| `--date` | string | `""` | Exact date filter (`YYYY-MM-DD`) |
| `--from` | string | `""` | Date range start, inclusive (`YYYY-MM-DD`) |
| `--to` | string | `""` | Date range end, inclusive (`YYYY-MM-DD`) |
| `--type` | string | `""` | Filter by type: `meeting`, `task`, `reminder`, `log` |
| `--status` | string | `""` | Filter by status: `active`, `completed`, `cancelled`, `all` |
| `--query` | string | `""` | Keyword search in title and description |
| `--file` | string | `""` | Output file path (default: stdout) |

**Notes:**
- `--format` is required. `--format csv` (or any value other than `json`/`markdown`) returns `invalid_params` error.
- Multiple filter flags are combined (AND logic), same as `wr list`.
- Both `wr list` and `wr export` default to active-only records. Use `--status all` to include completed/cancelled records, or `--status completed` for completed only.
- For Markdown format without a date filter, today's date is used (consistent with `wr report today`).
- Use `--file` to write output to disk instead of stdout. When `--file` is specified, nothing is written to stdout on success.

**JSON format example:**

```bash
wr export --format json --from 2026-05-01 --to 2026-05-07
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"count":3,"format":"json","records":[{"type":"meeting","title":"Sprint planning","date":"2026-05-01","time":"09:00","status":"active","saved_at":"2026-05-01T09:00:00+08:00","short_id":"a1b2c3d4e5f67890"},{"type":"task","title":"Review PR #42","date":"2026-05-02","priority":"high","status":"active","saved_at":"2026-05-02T10:00:00+08:00","short_id":"f0e1d2c3b4a56789"},{"type":"log","title":"Deployed v2.1","date":"2026-05-03","status":"active","saved_at":"2026-05-03T14:00:00+08:00","short_id":"c3d4e5f6a7b89012"}]}}
```

**Markdown format example:**

```bash
wr export --format markdown --date 2026-05-03
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"count":3,"format":"markdown","content":"# 工作日报 2026-05-03\n\n📊 **汇总**: 会议 1 | 任务 1 | 提醒 0 | 日志 1 | 共计 3 条\n\n## 📅 会议 (1)\n\n- Sprint planning [09:00]\n\n## ✅ 任务 (1)\n\n- Review PR #42 [进行中]\n\n## 📝 日志 (1)\n\n- Deployed v2.1\n\n"}}
```

Markdown reports use Chinese headings with emoji decorators. The `content` field contains the full Markdown string.

**Export to file:**

```bash
wr export --format json --from 2026-05-01 --to 2026-05-07 --file output.json
```

Writes the response directly to `output.json`. No stdout output on success.

**Empty results (no matching records):** Returns success with empty array (JSON) or empty report (Markdown). Not an error.

**Error codes:** `invalid_params` (missing or unsupported format), `storage_error`, `daemon_not_running`

---

### wr digest

Manage digest configurations. Digests are scheduled batch summaries of work records, processed by LLM and optionally pushed via Pushover. This is a command group with subcommands.

**Usage:**

```
wr digest <subcommand>
```

#### wr digest add

Create a new digest configuration with a cron schedule, time scope, and output direction.

```
wr digest add --schedule <cron> --scope <scope> --direction <direction>
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--schedule` | string | `""` | Cron expression (required). Standard 5-field format (min hour dom month dow). |
| `--scope` | string | `""` | Digest time scope (required): `today`, `yesterday`, `week`, `month`, or custom date range `YYYY-MM-DD:YYYY-MM-DD`. |
| `--direction` | string | `""` | Output direction (required): `agenda` (forward-looking) or `summary` (retrospective). |

**Notes:**
- All three flags are required.
- The cron expression uses standard 5-field format (not 6-field with seconds).
- The daemon registers the cron schedule immediately after creation. Existing digests are re-synced.
- Auto-generated ID format: `d_YYYYMMDD_<random6>` (e.g. `d_20260503_a1b2c3`).

**Example:**

```bash
wr digest add --schedule "0 8 * * 1-5" --scope today --direction agenda
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T08:00:00Z","data":{"id":"d_20260503_a1b2c3","schedule":"0 8 * * 1-5","scope":"today","direction":"agenda","enabled":true,"created_at":"2026-05-03T08:00:00Z","updated_at":"2026-05-03T08:00:00Z"}}
```

**Error codes:** `invalid_scope`, `invalid_schedule`, `invalid_direction`, `invalid_body`, `storage_error`, `daemon_not_running`

---

#### wr digest list

List all digest configurations.

```
wr digest list
```

**Flags:** None.

**Example:**

```bash
wr digest list
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T10:00:00Z","data":{"action":"list","count":2,"digests":[{"id":"d_20260501_abc123","schedule":"0 8 * * 1-5","scope":"today","direction":"agenda","enabled":true,"created_at":"2026-05-01T08:00:00Z","updated_at":"2026-05-01T08:00:00Z"},{"id":"d_20260502_def456","schedule":"0 18 * * 5","scope":"week","direction":"summary","enabled":false,"created_at":"2026-05-02T10:00:00Z","updated_at":"2026-05-02T12:00:00Z"}]}}
```

**Error codes:** `storage_error`, `daemon_not_running`

---

#### wr digest remove

Remove a digest configuration by ID.

```
wr digest remove <id>
```

**Arguments:** Exactly one — the digest ID.

**Example:**

```bash
wr digest remove d_20260501_abc123
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T10:00:00Z","data":{"action":"remove","id":"d_20260501_abc123","message":"digest removed"}}
```

**Error codes:** `digest_not_found`, `daemon_not_running`

---

#### wr digest enable

Enable a digest configuration by ID. The cron schedule is re-registered.

```
wr digest enable <id>
```

**Arguments:** Exactly one — the digest ID.

**Example:**

```bash
wr digest enable d_20260502_def456
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T10:00:00Z","data":{"id":"d_20260502_def456","schedule":"0 18 * * 5","scope":"week","direction":"summary","enabled":true,"created_at":"2026-05-02T10:00:00Z","updated_at":"2026-05-03T10:00:00Z"}}
```

**Error codes:** `digest_not_found`, `daemon_not_running`

---

#### wr digest disable

Disable a digest configuration by ID. The cron schedule is unregistered.

```
wr digest disable <id>
```

**Arguments:** Exactly one — the digest ID.

**Example:**

```bash
wr digest disable d_20260502_def456
```

**Output:** Same shape as `wr digest enable` with `"enabled":false`.

**Error codes:** `digest_not_found`, `daemon_not_running`

---

#### wr digest preview

Preview an LLM-generated digest summary in the terminal for a given digest configuration. Does not send via Pushover.

```
wr digest preview <id>
```

**Arguments:** Exactly one — the digest ID.

**Notes:**
- Runs the full digest pipeline (fetch records by scope → generate LLM summary).
- If LLM is not configured, falls back to raw markdown report.
- Output includes `llm_status` field: `"success"` (LLM used) or `"fallback"` (raw markdown).

**Example:**

```bash
wr digest preview d_20260501_abc123
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T10:00:00Z","data":{"action":"digest_preview","digest_id":"d_20260501_abc123","scope":"today","direction":"agenda","text":"## 今日待办议程\n\n1. **Review PR #42** [高优先级]\n2. **Sprint planning** [09:00]","record_count":3,"llm_status":"success","title":"今日待办议程"}}
```

**Error codes:** `digest_not_found`, `internal_error`, `storage_error`, `daemon_not_running`

---

### wr prompt

Manage prompt templates used by the digest LLM pipeline. This is a command group with subcommands.

**Usage:**

```
wr prompt <subcommand>
```

**Built-in prompts:** `agenda` (for agenda-style digests), `report` (for report-style digests). Both have Chinese default text. You can override any built-in or create custom prompts.

#### wr prompt list

List all prompts with their current text and default status.

```
wr prompt list
```

**Flags:** None.

**Example:**

```bash
wr prompt list
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T10:00:00Z","data":{"action":"list","count":2,"prompts":[{"name":"agenda","text":"你是一个专业的工作助手...","is_default":true},{"name":"report","text":"你是一个专业的工作助手...","is_default":true,"updated_at":"2026-05-02T15:00:00Z"}]}}
```

`is_default` is `true` when the prompt text is the built-in default, `false` when it has been overridden.

**Error codes:** `storage_error`, `daemon_not_running`

---

#### wr prompt show

Show the effective prompt text for a named prompt (returns default text if no override is set).

```
wr prompt show <name>
```

**Arguments:** Exactly one — the prompt name.

**Example:**

```bash
wr prompt show agenda
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T10:00:00Z","data":{"action":"show","name":"agenda","text":"你是一个专业的工作助手。请根据以下工作记录，生成今日待办议程（Agenda）。\n\n要求：\n1. 按优先级排序，标注紧急程度\n2. 列出未完成的上期任务\n3. 识别潜在的阻塞问题\n4. 建议时间分配","is_default":true}}
```

**Error codes:** `prompt_not_found`, `storage_error`, `daemon_not_running`

---

#### wr prompt set

Set a custom prompt text for a named prompt. At least one of `--text` or `--file` is required.

```
wr prompt set <name> --text <text>
wr prompt set <name> --file <path>
```

**Arguments:** Exactly one — the prompt name.

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--text` | string | `""` | Prompt text directly |
| `--file` | string | `""` | Path to a file containing the prompt text |

**Notes:**
- At least one of `--text` or `--file` is required. If both are provided, `--text` takes precedence.
- Any prompt name is accepted (not limited to built-ins). Custom names can be used for specialized prompts.
- Overriding a built-in prompt does not delete the default — use `wr prompt reset` to restore it.

**Example (text):**

```bash
wr prompt set agenda --text "Generate a concise bullet-point agenda from the following records."
```

**Example (file):**

```bash
wr prompt set agenda --file /tmp/my-agenda-prompt.txt
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T10:00:00Z","data":{"action":"set","name":"agenda","message":"prompt updated"}}
```

**Error codes:** `prompt_not_found`, `invalid_params` (neither `--text` nor `--file` provided), `invalid_body`, `storage_error`, `daemon_not_running`

---

#### wr prompt reset

Restore a built-in prompt to its default text. Only works for built-in prompt names (`agenda`, `report`).

```
wr prompt reset <name>
```

**Arguments:** Exactly one — the prompt name (must be a built-in name).

**Notes:**
- Returns `prompt_not_found` if the name is not a built-in prompt (no default to restore to).
- If the prompt is already at its default (no override exists), this is a no-op and still returns success.

**Example:**

```bash
wr prompt reset agenda
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T10:00:00Z","data":{"action":"reset","name":"agenda","text":"你是一个专业的工作助手。请根据以下工作记录，生成今日待办议程（Agenda）。\n\n要求：\n1. 按优先级排序，标注紧急程度\n2. 列出未完成的上期任务\n3. 识别潜在的阻塞问题\n4. 建议时间分配","message":"prompt reset to default"}}
```

**Error codes:** `prompt_not_found`, `storage_error`, `daemon_not_running`

---

#### wr prompt preview

Preview LLM prompt output using current data. Runs the digest pipeline with the named prompt's text and current records.

```
wr prompt preview <name> [--scope <scope>]
```

**Arguments:** Exactly one — the prompt name.

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--scope` | string | `""` (defaults to `today`) | Digest scope: `today`, `yesterday`, `week`, `month`, or custom `YYYY-MM-DD:YYYY-MM-DD`. |

**Notes:**
- Default scope is `today`.
- The direction is inferred from the prompt name: `agenda` → `agenda`, `report` → `summary`. For custom prompt names, defaults to `summary`.
- If LLM is not configured, falls back to raw markdown report.

**Example:**

```bash
wr prompt preview agenda
wr prompt preview report --scope week
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T10:00:00Z","data":{"action":"prompt_preview","prompt_name":"agenda","prompt_text":"你是一个专业的工作助手...","scope":"today","direction":"agenda","text":"## 今日待办议程\n\n1. **Review PR #42** [高优先级]","record_count":3,"llm_status":"success","title":"今日待办议程"}}
```

**Error codes:** `internal_error`, `invalid_scope`, `prompt_not_found`, `storage_error`, `daemon_not_running`

---

### wr agent daemon

Manage the wr agent daemon process. This is a command group.

**Usage:**

```
wr agent daemon <subcommand>
```

#### wr agent daemon start

Start the daemon. This is a foreground process — it blocks until stopped with SIGINT/SIGTERM. Run in the background with `&` or a process manager.

```bash
wr agent daemon start > /dev/null 2>&1 &
```

**Flags:** None.

**Behavior:**
- Loads config from `~/.work-report/config.json`
- Creates data directory if it doesn't exist
- Writes daemon state to a state file (port, PID)
- Starts the scheduler for reminder notifications
- Performs catch-up for missed reminders on startup
- Cleans up state file on shutdown
- Sends an async Pushover startup notification after the HTTP server is ready (see below)

**Startup notification:** When Pushover is configured (`pushover.api_token` and `pushover.user_key` are both non-empty), the daemon sends a notification titled "wr daemon 已上线" containing the hostname, port, and PID. This runs in a goroutine so it never blocks the daemon startup. If Pushover is not configured, the notification is silently skipped (logged at debug level). If the push fails (network error, bad credentials), the error is logged as a warning but does not affect daemon operation. All startup notification log messages use the `[startup-notify]` prefix.

**Notes:**
- The daemon listens on `127.0.0.1:<port>` (default port `18080`).
- All daemon log messages go to stderr, not stdout.
- The daemon version is `0.1.0`.

#### wr agent daemon ensure-running

Ensure the daemon is running — start it if needed, or return success if already running. This is the recommended way for agents to guarantee daemon availability before issuing commands.

```bash
wr agent daemon ensure-running
```

**Flags:** None.

**Behavior:**
1. Checks if the daemon is already running (state file + port check).
2. If running: calls `/api/status`, wraps response with `source: "already_running"`, returns success.
3. If not running: cleans stale state, starts the daemon in detached mode, polls until the port is bound (up to 10s), then returns success with `source: "started"`.
4. On timeout: returns `daemon_start_timeout` error.

**Key difference from `start --detach`:** `start --detach` errors when the daemon is already running. `ensure-running` is idempotent — it always succeeds if the daemon is available, regardless of whether it was just started or already running.

**Success output (daemon already running):**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"config":{"data_dir":{"accessible":true,"path":"..."},"exists":true,"llm":{"text":{"configured":true},"vision":{"configured":false}},"pushover":{"configured":true},"redacted":{"daemon":{"port":18080},"data_dir":"...","llm":{"text":{"api_key":"secr****","model":"","provider":""},"vision":{"api_key":"","model":"","provider":""}},"pushover":{"api_token":"secr****","user_key":"secr****"},"timezone":"Asia/Shanghai"}},"daemon":{"pid":12345,"port":18080,"status":"running","version":"0.1.0"},"datetime":{"current_date":"2026-05-03","current_datetime":"2026-05-03T22:00:00+08:00","current_time":"22:00:00","timezone":"Asia/Shanghai","weekday":"Friday"},"pid":12345,"port":18080,"records":{"active_logs":0,"active_meetings":2,"active_reminders":0,"active_tasks":1,"total_active":3},"scheduler":{"entries_count":2,"running":true},"source":"already_running"}}
```

**Success output (daemon just started):**

Same structure as above, with `"source":"started"` instead of `"source":"already_running"`. The output includes full daemon diagnostics (config, datetime, records, scheduler) regardless of whether the daemon was just started or was already running.

**Error codes:** `daemon_start_timeout`, `daemon_not_running`, `invalid_body`

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
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"config":{"data_dir":{"accessible":true,"path":"..."},"exists":true,"llm":{"text":{"configured":true},"vision":{"configured":false}},"pushover":{"configured":true},"redacted":{"daemon":{"port":18080},"data_dir":"...","llm":{"text":{"api_key":"secr****","model":"gpt-4o-mini","provider":""},"vision":{"api_key":"","model":"","provider":""}},"pushover":{"api_token":"secr****","user_key":"secr****"},"timezone":"Asia/Shanghai"}},"daemon":{"pid":12345,"port":18080,"status":"running","version":"0.1.0"},"datetime":{"current_date":"2026-05-03","current_datetime":"2026-05-03T22:00:00+08:00","current_time":"22:00:00","timezone":"Asia/Shanghai","weekday":"Friday"},"records":{"active_logs":0,"active_meetings":2,"active_reminders":0,"active_tasks":1,"total_active":3},"scheduler":{"entries_count":2,"running":true}}}
```

The running output includes `datetime` (server date/time info), `records` (active record counts by type), `scheduler` state, and a `redacted` config view (keys masked as `secr****`).

**Daemon not running output:**

```json
{"version":"1.0","tool":"wr","type":"error","timestamp":"2026-05-03T14:00:00Z","error_code":"daemon_not_running","message":"daemon not running: ..."}
```

When the daemon is not running, `wr status` returns a JSONL error with `error_code: "daemon_not_running"` and a descriptive `message` that includes the suggestion to run `wr agent daemon start`.

---

### wr config

Manage wr configuration. This is a command group with subcommands.

**Usage:**

```
wr config <subcommand> [flags]
```

#### wr config init

Create `~/.work-report/config.json` with sensible defaults. All flags are optional — the tool works without any LLM key (manual mode). Any provided flags override the defaults.

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
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"path":"/home/user/.work-report/config.json"}}
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
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"key":"llm.text.api_key","value":"sk-newkey123"}}
```

#### wr config show

Display current config with all secrets redacted (first 4 chars shown, rest masked).

```bash
wr config show
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"pushover":{"api_token":"secr****","user_key":"secr****"},"llm":{"text":{"provider":"","api_key":"secr****","model":"gpt-4o-mini"},"vision":{"provider":"","api_key":"","model":""}},"data_dir":"/home/user/.work-report/work-records","daemon":{"port":18080},"timezone":"Asia/Shanghai"}}
```

Secrets are redacted as `secr****` (first 4 chars shown, rest masked).

---

### wr backup

Manage data backups with Grandfather-Father-Son (GFS) rotation. All backup commands are **local-only** — they do not require the daemon to be running. This is a command group with subcommands.

**Usage:**

```
wr backup <subcommand> [flags]
```

**Persistent flag:** `--output <dir>` (string) — Override backup output directory. Available on all backup subcommands (create, list, cleanup). When omitted, the output directory from backup config is used.

#### wr backup create

Create a timestamped zip backup immediately.

```
wr backup create [--output <dir>]
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--output` | string | `""` | Override backup output directory (persistent on parent) |

**Notes:**
- Local-only (no daemon required).
- Zips config.json, work-records/, digests.json, scheduler-state.json, and logs/.
- Filename: `wr-backup-YYYYMMDD-HHMMSS.zip` with incrementing suffix on collision (`-1`, `-2`, ...).
- Missing optional files (e.g. scheduler-state.json) are silently skipped — the backup still succeeds.
- Output directory is created automatically if it doesn't exist.

**Example:**

```bash
wr backup create
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"path":"/home/user/.work-report/backups/wr-backup-20260503-140000.zip","size_bytes":12345,"output_dir":"/home/user/.work-report/backups"}}
```

**Error codes:** `data_dir_not_found`, `backup_failed`

---

#### wr backup list

List all existing backups, sorted newest first.

```
wr backup list [--output <dir>]
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--output` | string | `""` | Override backup output directory (persistent on parent) |

**Notes:**
- Sorted newest first. Empty directory returns empty array.
- Only files matching `wr-backup-*.zip` pattern are listed.

**Example:**

```bash
wr backup list
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"backups":[{"filename":"wr-backup-20260503-140000.zip","size":12345,"created_at":"2026-05-03T14:00:00Z"},{"filename":"wr-backup-20260502-090000.zip","size":10200,"created_at":"2026-05-02T09:00:00Z"}],"output_dir":"/home/user/.work-report/backups","count":2}}
```

**Error codes:** none specific (generic fatal on config/load error)

---

#### wr backup cleanup

Run GFS rotation to remove old backups based on retention policy.

```
wr backup cleanup [--output <dir>]
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--output` | string | `""` | Override backup output directory (persistent on parent) |

**Notes:**
- Runs Grandfather-Father-Son (GFS) rotation based on backup-config.json retention policy.
- Three rules are evaluated: daily (newest per calendar day), weekly (newest per ISO week), monthly (newest per calendar month).
- Rules are **unioned** — a backup protected by ANY rule is retained.
- Unprotected backups are removed from disk.
- Deletion errors are logged but do not cause the command to fail.

**Example:**

```bash
wr backup cleanup
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"kept":[{"filename":"wr-backup-20260503-140000.zip","size":12345,"created_at":"2026-05-03T14:00:00Z"}],"removed":[{"filename":"wr-backup-20260425-090000.zip","size":9800,"created_at":"2026-04-25T09:00:00Z"}],"kept_count":1,"removed_count":1}}
```

**Error codes:** `rotation_failed`

---

#### wr backup config show

Display the current backup configuration.

```
wr backup config show
```

**Flags:** None.

**Notes:**
- If no backup config file exists, returns defaults with `"source": "defaults"`.
- If the config file exists, returns its content with `"source": "file"`.
- Default retention: 7 daily, 4 weekly, 6 monthly.
- Default output directory: `~/.work-report/backups`.

**Example:**

```bash
wr backup config show
```

**Output (config file exists):**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"config":{"retention":{"daily":7,"weekly":4,"monthly":6},"output_dir":"/home/user/.work-report/backups","schedule":"","enabled":false},"config_path":"/home/user/.work-report/backup-config.json","source":"file"}}
```

**Output (no config file — returns defaults):**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"config":{"retention":{"daily":7,"weekly":4,"monthly":6},"output_dir":"/home/user/.work-report/backups","schedule":"","enabled":false},"config_path":"/home/user/.work-report/backup-config.json","source":"defaults"}}
```

**Error codes:** none specific

---

#### wr backup config set

Update backup configuration. Only explicitly-provided flags are updated — omitted flags keep their current values. Best-effort daemon sync after save.

```
wr backup config set [flags]
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--schedule` | string | `""` | Cron expression for scheduled backups (6-field: sec min hour dom month dow) |
| `--output-dir` | string | `""` | Backup output directory |
| `--retention-daily` | int | `0` | Number of daily backups to keep |
| `--retention-weekly` | int | `0` | Number of weekly backups to keep |
| `--retention-monthly` | int | `0` | Number of monthly backups to keep |
| `--enabled` | bool | `false` | Enable or disable scheduled backups |

**Notes:**
- Only explicitly-set flags are updated (uses `Flags().Changed` internally). Omitted flags retain their current values.
- Config is stored at `~/.work-report/backup-config.json` (separate from main `config.json`).
- Best-effort daemon sync after save — the command succeeds even if the daemon is not running. If daemon sync fails, the config is still saved to disk.
- `--enabled` requires explicit bool value: `--enabled=true` or `--enabled=false` (not a toggle).

**Example:**

```bash
wr backup config set --schedule "0 30 2 * * *" --retention-daily 14 --enabled=true
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"config":{"retention":{"daily":14,"weekly":4,"monthly":6},"output_dir":"/home/user/.work-report/backups","schedule":"0 30 2 * * *","enabled":true},"config_path":"/home/user/.work-report/backup-config.json","saved":true}}
```

**Error codes:** `backup_sync_failed` (daemon sync failure — config is still saved to disk)

---

### wr --version

Print the wr version. This is the only command that outputs plain text instead of JSONL.

```bash
wr --version
```

**Output (plain text, not JSONL):**

```
wr version dev
```

---

## Error Code Reference

Complete table of error codes that may appear in the `"error_code"` field of error responses.

| Code | Meaning | Recommended Agent Response |
|------|---------|---------------------------|
| `daemon_not_running` | Daemon is not reachable (no state file, corrupt state, connection refused, timeout) | Run `wr agent daemon start` in background, wait for readiness, then retry the original command. |
| `invalid_type` | Missing or unrecognized record type | Ensure `--type` is one of: `meeting`, `task`, `reminder`, `log`. Or provide `--text`/`--image` for LLM classification. |
| `invalid_body` | Missing required fields or malformed request body | Check that required flags (`--title`, `--date`, `--type`) are provided. |
| `invalid_field` | Attempted to update a field that is not allowed | Check the field name in the update command. |
| `record_not_found` | No record matches the given short_id | List records with `wr list` to find the correct short_id. |
| `already_completed` | Attempted to update a completed record | Completed records cannot be modified. Use `wr list --status completed` to view them. |
| `already_cancelled` | Attempted to update a cancelled record | Cancelled records cannot be modified. |
| `storage_error` | Filesystem or storage layer error | Check data directory permissions and disk space. |
| `llm_not_configured` | LLM API key is missing for the requested classification mode | LLM is optional. Either provide `--type` and `--title` explicitly, or run `wr config set llm.text.api_key <key>` to enable natural-language classification. |
| `llm_error` | LLM API call failed | Check API key validity, network connectivity, and model name. Retry once. |
| `pushover_not_configured` | Pushover credentials are missing | Run `wr config set pushover.api_token <token>` and `wr config set pushover.user_key <key>`. |
| `push_error` | Pushover notification delivery failed | Check Pushover credentials and network. |
| `import_record` | Per-record validation failure during import | Check the record at the specified index for missing or invalid fields (type, title, or date). Fix the record in the import file and retry. |
| `invalid_params` | Missing or unsupported command parameter (e.g., `--format`) | Check the command's required flags. For export, `--format` must be `json` or `markdown`. |
| `multiple_matches` | Content-based lookup (`--title` + `--date`) matched more than one active record | Narrow the query with a more specific title, or use `wr list` to find the exact `short_id` and use that instead. |
| `daemon_start_timeout` | Daemon failed to start within 10 seconds | Check for port conflicts, filesystem permissions on `~/.work-report/`, or zombie daemon processes. Kill stale processes and retry. |
| `digest_not_found` | No digest configuration matches the given ID | List digests with `wr digest list` to find the correct ID. |
| `invalid_scope` | Invalid digest scope value | Scope must be one of: `today`, `yesterday`, `week`, `month`, or `YYYY-MM-DD:YYYY-MM-DD`. |
| `invalid_schedule` | Invalid cron expression for digest schedule | Check the cron syntax (5-field format: min hour dom month dow). |
| `invalid_direction` | Invalid digest direction value | Direction must be `agenda` or `summary`. |
| `prompt_not_found` | Prompt name has no default and no override (for reset/show/preview) | Only built-in names (`agenda`, `report`) can be reset. Use `wr prompt list` to see available prompts. |
| `internal_error` | Digest/prompt preview pipeline failure (LLM or data error) | Check LLM configuration and available records for the given scope. Retry once. |
| `data_dir_not_found` | `~/.work-report/` data directory does not exist | Run `wr config init` first to create the data directory. |
| `backup_failed` | Zip creation failed (disk space, permissions, or I/O error) | Check disk space and write permissions on the backup output directory. |
| `rotation_failed` | GFS rotation failed during backup cleanup | Check backup directory permissions. The rotation may have partially completed — inspect with `wr backup list`. |
| `config_not_found` | Backup config file not found | Should not occur in normal usage — defaults are used when the file is missing. If this error appears, check filesystem permissions on `~/.work-report/`. |
| `backup_sync_failed` | Daemon sync failed after backup config save | Config is still saved to disk. Start the daemon with `wr agent daemon ensure-running` and retry. |

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
# 1. Init config (minimal — no LLM key required)
wr config init

# 2. (Optional) Add LLM key for natural-language classification
wr config set llm.text.api_key sk-your-key
wr config set llm.text.model gpt-4o-mini

# 3. (Optional) Add Pushover for push notifications
wr config set pushover.api_token your-token
wr config set pushover.user_key your-key

# 4. Ensure daemon is running (idempotent)
wr agent daemon ensure-running

# 5. Verify it's running
wr status
```

### Daily Usage

```bash
# Ensure daemon is running (safe to call repeatedly)
wr agent daemon ensure-running

# Add entries throughout the day (--date defaults to today if omitted)
wr add --type meeting --title "Sprint planning" --time 09:00 --participants "Alice,Bob"
wr add --type task --title "Review PR #42" --priority high
wr add --type log --title "Deployed v2.1 to staging"

# Add with idempotency key (safe in retry loops — no duplicates)
wr add --type task --title "Daily standup" --idempotency-key "standup-2026-05-03"

# Check today's entries
wr list

# Complete a task by title instead of short_id
wr complete --title "Review PR #42"

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

### Using LLM Classification (Optional)

LLM classification is an optional feature. Configure an LLM key to use natural-language input; without it, use explicit flags.

```bash
# Text classification — daemon auto-detects type, title, date, etc.
wr add --text "明天上午10点和产品团队开需求评审会"

# Image classification — daemon uses vision LLM
wr add --image /tmp/whiteboard.jpg --text "whiteboard notes from meeting"

# If LLM returns cancel_or_update, the response has type "result" and no record is created
```

### Data Import

```bash
# Import records from a JSON file
wr import --file /tmp/records.json

# Import file can be a JSON array
echo '[{"type":"task","title":"Write tests","date":"2026-05-03"}]' > /tmp/one.json
wr import --file /tmp/one.json

# Or an object with "records" key
echo '{"records":[{"type":"log","title":"Deployed v2.1","date":"2026-05-03"}]}' > /tmp/obj.json
wr import --file /tmp/obj.json

# Verify imported records appear in list
wr list --date 2026-05-03
```

### Data Export

```bash
# Export all records for a date range as JSON
wr export --format json --from 2026-05-01 --to 2026-05-07

# Export today's records as Markdown
wr export --format markdown --date today

# Export specific type to a file
wr export --format json --type meeting --from 2026-05-01 --to 2026-05-31 --file meetings.json

# Export all records (no date filter — defaults to today for markdown)
wr export --format json

# Export completed tasks
wr export --format json --status completed --type task
```

### Backup and Restore

```bash
# 1. Create an immediate backup (local-only, no daemon needed)
wr backup create

# 2. Verify the backup was created
wr backup list

# 3. Configure scheduled backups (optional, requires daemon for cron)
wr backup config set --schedule "0 30 2 * * *" --enabled=true

# 4. Manually run GFS rotation to clean up old backups
wr backup cleanup

# 5. For selective record transfer between machines, use export/import
wr export --format json --from 2026-05-01 --to 2026-05-07 --file transfer.json
wr import --file transfer.json
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

1. **Daemon must be running.** Almost every command (add, list, update, complete, cancel, report, status) requires the daemon. The only commands that work without it are `wr config init`, `wr config set`, and `wr config show`. Use `wr agent daemon ensure-running` — it's idempotent and starts the daemon if needed. If you see `daemon_not_running`, run `ensure-running` and retry.

2. **JSONL goes to stdout only, never stderr.** The root command sets `SilenceUsage` and `SilenceErrors` to prevent Cobra from writing non-JSONL text to stderr. All structured output (including errors) is JSONL on stdout.

3. **ShortID is a hash, not a file path.** The 16-character hex ID is derived from the record filename via SHA-256. It cannot be reversed to find the file. Always use `wr list` to discover short_ids.

4. **Scheduler re-registration on update.** When you update time-related fields (`time`, `date`, `remind_before`, `recurring`), the daemon unregisters the old scheduler entry and re-registers with the new values. This is automatic.

5. **Completed/cancelled records are immutable.** You cannot update, complete, or cancel a record that is already completed or cancelled. Check status with `wr list --status all` first.

6. **LLM classification is optional.** Without an LLM key, use `--type`, `--title`, and optionally `--date` (defaults to today). With `--text` or `--image`, the LLM fills in `type`, `title`, and `date` automatically. Explicit flags take precedence over LLM results.

7. **`wr config set` validates before saving.** Invalid values (bad port, unknown timezone) are rejected before the config file is modified. The config is always in a valid state on disk.

8. **Pushover requires both api_token and user_key.** Both must be non-empty for push commands to work. Missing either one returns `pushover_not_configured`.

9. **Report date defaults to "today" in configured timezone.** The daemon uses the timezone from config (default: `Asia/Shanghai`) to determine "today".

10. **Tags are comma-separated strings.** In `wr add`, use `--tags "tag1,tag2"`. In `wr update`, use `--tags "tag1,tag2"` (replaces the entire list, does not append).

11. **Import validates all records before writing any.** If record at index 5 has a missing field, records 0–4 are NOT written either. Fix the invalid record and retry the entire import.

12. **Imported records get fresh ShortIDs.** The original ShortIDs from the source are not preserved. Use `wr list` to find the new ShortIDs after import.

13. **Both list and export default to active-only.** `wr list` and `wr export` both exclude completed/cancelled records by default. Use `--status all` on either command to include all records, or `--status completed` for completed only.

14. **Export Markdown without date filter defaults to today.** If you don't specify `--date`, `--from`, or `--to`, Markdown export uses today's date. JSON export without date filters returns all records.

15. **Import file accepts two JSON shapes.** The file can be a bare JSON array `[{...}]` or an object with a `records` key `{"records":[{...}]}`. Both produce the same result.

16. **Content-based lookup for update/complete/cancel.** When you don't know the `short_id`, use `--title` and `--date` flags instead of a positional argument. Both are required for lookup. Example: `wr complete --title "Standup" --date 2026-05-03`. If multiple records match, you'll get a `multiple_matches` error — use `wr list` to find the specific `short_id`.

17. **Use idempotency keys for retry-safe adds.** Pass `--idempotency-key <unique-key>` on `wr add` to deduplicate. If the same key is used again, the existing record is returned without creating a duplicate. Ideal for cron jobs, retry loops, or any workflow where the same add might execute twice.

18. **`ensure-running` is preferred over `start` for agent workflows.** Unlike `wr agent daemon start` (which errors if already running) or `start --detach`, `ensure-running` is idempotent. It returns success whether the daemon was just started or already running, with a `source` field ("started" or "already_running") to disambiguate.

19. **Backup config is separate from main config.** Backup settings live in `~/.work-report/backup-config.json`, not in the main `config.json`. This keeps concerns separate — backup retention, output directory, and schedule don't mix with daemon/LLM/Pushover config. Use `wr backup config show` to view and `wr backup config set` to modify.

20. **`wr backup` commands are local-only (no daemon needed).** `wr backup create`, `wr backup list`, `wr backup cleanup`, and `wr backup config` commands work entirely through local filesystem operations. They do not require the daemon to be running. The only daemon interaction is `wr backup config set` which does a best-effort sync (non-fatal if daemon is unavailable).

21. **GFS rotation uses a distinct-bucket strategy.** For each time granularity (daily/weekly/monthly), the rotation algorithm keeps the newest backup per distinct calendar bucket until the retention count is reached. Rules are unioned — a backup protected by ANY rule is retained. Example: with `daily:7, weekly:4, monthly:6`, a backup from 3 weeks ago is kept if it's the newest in its ISO week, even if there are already 7+ daily backups.

22. **`wr backup config set --enabled` requires explicit bool.** Use `--enabled=true` or `--enabled=false` — it is not a toggle. Omitting `--enabled` entirely leaves the current enabled state unchanged (same as all other `config set` flags).

23. **Daemon sends a Pushover startup notification.** After `wr agent daemon start` (or `ensure-running`), if Pushover is configured, you'll receive a push notification titled "wr daemon 已上线" with the hostname, port, and PID. This is non-blocking and failures are silent. If you don't want this notification, simply don't configure Pushover credentials.
