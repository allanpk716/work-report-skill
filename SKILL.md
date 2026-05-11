# wr — Work Report CLI for AI Agents

## Overview

**wr** is a pure CLI tool for managing work reports. All output is JSONL (JSON Lines) — one JSON object per line on stdout. There is no human-readable mode. The only consumer is an AI agent.

**Architecture:** Single binary, local filesystem storage. Every command runs as a direct CLI invocation — no background process, no HTTP layer, no long-running service.

**JSONL-only contract:** Every command outputs exactly one JSONL line to stdout. Errors go to stdout as JSONL too — never to stderr. An agent can parse every response the same way.

---

## 快速集成（5 分钟）

Agent 只需读取本段即可完成 add / list / report 三个核心操作。完整参考见下方各章节。

### 前置

```bash
wr config init                    # 最小配置（无需 LLM key）
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

`--date` 在使用 `--title` 时省略默认今天。

```bash
wr complete <short_id>                            # 通过 ID
wr complete --title "Review PR #42"               # --title 时 --date 默认今天
```

### 提醒

```bash
wr remind due                   # 列出所有到期提醒
wr remind push --due            # 推送所有到期提醒并自动完成
```

### 常见错误

| `error_code` | 处理 |
|---|---|
| `record_not_found` | `wr list` 查找正确 ID |
| `invalid_type` | `--type` 须为 meeting / task / reminder / log |

> 完整命令参考、JSONL 格式规范、错误码表见下方各章节。

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

### Error shape

```json
{"version":"1.0","tool":"wr","type":"error","timestamp":"2026-05-03T14:00:00Z","error_code":"record_not_found","message":"record not found: ..."}
```

### Warning shape

```json
{"version":"1.0","tool":"wr","type":"warning","timestamp":"2026-05-03T14:00:00Z","message":"..."}
```

### Progress shape

```json
{"version":"1.0","tool":"wr","type":"progress","timestamp":"2026-05-03T14:00:00Z","percent":50,"message":"Processing..."}
```

---

## Command Reference

### wr add

Add a new work report entry.

**Usage:** `wr add [flags]`

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--type` | string | `""` | Entry type: `meeting`, `task`, `reminder`, `log` |
| `--title` | string | `""` | Entry title |
| `--date` | string | `""` | Date in `YYYY-MM-DD` format. Defaults to today if omitted. |
| `--time` | string | `""` | Time in `HH:MM` format |
| `--description` | string | `""` | Longer description |
| `--tags` | string | `""` | Comma-separated tags (e.g. `"frontend,urgent"`) |
| `--location` | string | `""` | Location |
| `--related-person` | string | `""` | Related person name |
| `--priority` | string | `""` | Priority: `normal`, `high`, `medium` |
| `--remind-before` | string | `""` | Reminder offset (e.g. `15m`, `1h`) |
| `--recurring` | string | `""` | Recurring pattern (e.g. `daily`, `weekly`) |
| `--text` | string | `""` | Natural language text for LLM classification. When provided, `--type`, `--title`, `--date` become optional. |
| `--image` | string | `""` | Image file path for LLM vision classification. |
| `--idempotency-key` | string | `""` | Idempotency key for deduplication. |

**Notes:**
- `--type` and `--title` are required when not using LLM classification (no `--text`/`--image`).
- `--date` defaults to today when omitted and not using LLM classification.
- If both `--text` (or `--image`) and `--type` are provided, explicit flags take precedence over LLM classification.
- If LLM classification returns `cancel_or_update` type, no record is created — the response has `type: "result"` with `data.action` set to `"cancel_or_update"`.

**Example:**

```bash
wr add --type meeting --title "Project sync" --date 2026-05-03 --time 14:00 --location "Room 3A" --tags "project,weekly"
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"type":"meeting","title":"Project sync","date":"2026-05-03","time":"14:00","location":"Room 3A","status":"active","tags":["project","weekly"],"saved_at":"2026-05-03T14:00:00+08:00","short_id":"a1b2c3d4e5f67890"}}
```

Empty fields are omitted from the response — only fields with non-empty values are included.

**Error codes:** `invalid_type`, `invalid_body`, `llm_not_configured`, `llm_error`, `storage_error`

---

### wr list

List work report entries with optional filters.

**Usage:** `wr list [flags]`

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

**Error codes:** `storage_error`

---

### wr update

Update fields of an existing work report entry. Only explicitly provided flags are changed.

**Usage:** `wr update [<short_id>] [flags]`

**Arguments:** Optional positional `<short_id>`. When omitted, `--title` and `--date` become required for content-based lookup.

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--title` | string | `""` | When `<short_id>` provided: update title. Otherwise: lookup by exact title (required). |
| `--description` | string | `""` | Update description |
| `--date` | string | `""` | When `<short_id>` provided: update date. Otherwise: lookup by date (required). |
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
- Completed or cancelled records cannot be updated — returns `already_completed` or `already_cancelled`.
- At least one field flag must be explicitly set, otherwise the command prints help text.
- **Content-based lookup:** When `<short_id>` is omitted, both `--title` and `--date` are required. If multiple active records match, returns `invalid_params` error with the matching IDs listed in the message.

**Example:**

```bash
wr update a1b2c3d4e5f67890 --time 15:00 --location "Room 5B"
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:30:00Z","data":{"type":"meeting","title":"Project sync","date":"2026-05-03","time":"15:00","location":"Room 5B","status":"active","tags":["project","weekly"],"saved_at":"2026-05-03T14:00:00+08:00","updated_at":"2026-05-03T14:30:00+08:00","short_id":"a1b2c3d4e5f67890"}}
```

**Error codes:** `record_not_found`, `already_completed`, `already_cancelled`, `invalid_body`, `invalid_field`, `invalid_params`, `storage_error`

---

### wr complete

Mark an active work report entry as completed.

**Usage:**

```
wr complete [<short_id>]
wr complete --title <title> --date <YYYY-MM-DD>
```

**Arguments:** Optional positional `<short_id>`. When omitted, `--title` becomes required for content-based lookup.

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--title` | string | `""` | Lookup by exact title (required when no `<short_id>`) |
| `--date` | string | `""` | Lookup by date `YYYY-MM-DD`. Defaults to today when `--title` is provided. |

**Notes:**
- Only active records can be completed. Attempting to complete an already-completed or already-cancelled record returns `storage_error`.
- **Content-based lookup:** When `<short_id>` is omitted, `--title` is required. `--date` defaults to today if omitted. If multiple active records match, returns `invalid_params` error with the matching IDs listed in the message.

**Example:**

```bash
wr complete a1b2c3d4e5f67890
wr complete --title "Review PR #42"
wr complete --title "Review PR #42" --date 2026-05-03
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T16:00:00Z","data":{"type":"meeting","title":"Project sync","date":"2026-05-03","time":"15:00","location":"Room 5B","status":"completed","tags":["project","weekly"],"saved_at":"2026-05-03T14:00:00+08:00","updated_at":"2026-05-03T16:00:00+08:00","short_id":"a1b2c3d4e5f67890"}}
```

**Error codes:** `record_not_found`, `already_completed`, `already_cancelled`, `invalid_params`, `storage_error`

---

### wr cancel

Cancel an active work report entry.

**Usage:**

```
wr cancel [<short_id>]
wr cancel --title <title> --date <YYYY-MM-DD>
```

**Arguments:** Optional positional `<short_id>`. When omitted, `--title` becomes required for content-based lookup.

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--title` | string | `""` | Lookup by exact title (required when no `<short_id>`) |
| `--date` | string | `""` | Lookup by date `YYYY-MM-DD`. Defaults to today when `--title` is provided. |

**Notes:**
- Only active records can be cancelled. Attempting to cancel an already-cancelled or already-completed record returns `storage_error`.
- **Content-based lookup:** When `<short_id>` is omitted, `--title` is required. `--date` defaults to today if omitted. If multiple active records match, returns `invalid_params` error with the matching IDs listed in the message.

**Example:**

```bash
wr cancel f0e1d2c3b4a56789
wr cancel --title "Standup"
wr cancel --title "Standup" --date 2026-05-03
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T12:00:00Z","data":{"type":"meeting","title":"Standup","date":"2026-05-03","time":"10:00","status":"cancelled","saved_at":"2026-05-03T10:00:00+08:00","updated_at":"2026-05-03T12:00:00+08:00","short_id":"f0e1d2c3b4a56789"}}
```

**Error codes:** `record_not_found`, `already_completed`, `already_cancelled`, `invalid_params`, `storage_error`

---

### wr remind

Manage reminders — list due reminders and push them via Pushover. This is a command group with subcommands.

**Usage:** `wr remind <subcommand> [flags]`

#### wr remind due

List all currently due reminders.

```
wr remind due [flags]
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--window` | string | `"0"` | Look-ahead window (e.g. `"30m"`, `"1h"`). `0` = no window, only currently overdue reminders. |
| `--include-stale` | bool | `false` | Include stale reminders (overdue > 24 hours). |

**Notes:**
- Reminders without a time component: due when `date < today`. Date == today is not due (no time to trigger).
- Reminders with a time component: due when `datetime <= now`. With `--window`, reminders within `(now, now+window]` are also considered due.
- Stale = overdue by more than 24 hours. Stale reminders are excluded by default; use `--include-stale` to include them.

**Example:**

```bash
wr remind due
wr remind due --window 1h
wr remind due --include-stale
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"action":"remind_due","count":2,"entries":[{"short_id":"a1b2c3d4e5f67890","title":"Team standup","date":"2026-05-03","time":"09:00","is_stale":false},{"short_id":"f0e1d2c3b4a56789","title":"Submit report","date":"2026-05-02","time":"","is_stale":true}]}}
```

**Error codes:** `invalid_params`, `storage_error`

---

#### wr remind push

Push reminders via Pushover. Supports two modes: batch push all due reminders (`--due`) or push a single reminder by `<short_id>`.

```
wr remind push [<short_id>] [flags]
```

**Arguments:** Optional positional `<short_id>`. When provided, pushes that single reminder. When omitted, `--due` is required.

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--due` | bool | `false` | Push all due reminders (batch mode). |
| `--window` | string | `"0"` | Look-ahead window for due detection (same semantics as `wr remind due`). |
| `--include-stale` | bool | `false` | Include stale reminders (> 24 hours overdue). |

**Notes:**
- **Batch mode** (`--due`): Finds all due reminders and pushes each via Pushover. Successfully pushed reminders are automatically completed (atomic push+complete). Push failures leave the record active for retry. Maximum 10 reminders per batch.
- **Single mode** (`<short_id>`): Pushes one specific reminder and completes it on success. Push failure leaves the record active.
- Pushover must be configured (`pushover.api_token` and `pushover.user_key`).

**Example (batch):**

```bash
wr remind push --due
wr remind push --due --window 1h
wr remind push --due --include-stale
```

**Output (batch mode):**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"action":"remind_push_due","pushed":[{"short_id":"a1b2c3d4e5f67890","title":"Team standup"}],"failed":[{"short_id":"f0e1d2c3b4a56789","title":"Submit report","error":"pushover: timeout"}],"total":2}}
```

**Example (single):**

```bash
wr remind push a1b2c3d4e5f67890
```

**Output (single mode):**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"action":"remind_push_single","pushed":[{"short_id":"a1b2c3d4e5f67890","title":"Team standup"}]}}
```

**Error codes:** `pushover_not_configured`, `push_error`, `record_not_found`, `storage_error`, `invalid_params`

---

### wr report

Generate work reports. This is a command group with subcommands.

**Usage:** `wr report <subcommand> [flags]`

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

The `data` object contains typed arrays (`meetings`, `tasks`, `reminders`, `logs`). Each array contains the full record objects for that type. The `summary` contains counts by type and total. The `markdown` field contains the formatted Chinese-language report.

##### wr report date \<YYYY-MM-DD\>

Generate report for a specific date.

```bash
wr report date 2026-05-01
```

##### wr report week

Generate report for the current week (Monday through Sunday).

```bash
wr report week
```

The `days` array contains per-day report objects (same structure as `wr report today`). The top-level `summary` and `markdown` provide the week overview.

##### wr report range

Generate report for a date range.

```bash
wr report range --from 2026-04-27 --to 2026-05-03
```

**Flags:** `--from` (required, `YYYY-MM-DD`), `--to` (required, `YYYY-MM-DD`).

##### wr report push \<subcommand\>

Generate and push a report via Pushover notification. Requires Pushover credentials in config.

| Command | Description |
|---------|-------------|
| `wr report push today` | Push today's report |
| `wr report push date <YYYY-MM-DD>` | Push a specific date's report |
| `wr report push week` | Push the current week's report |
| `wr report push range --from X --to Y` | Push a date range report |

**Error codes:** `storage_error`, `pushover_not_configured`, `push_error`, `invalid_body`

---

### wr import

Bulk import work report entries from a JSON file. Validates all records before persisting any — if any record is invalid, nothing is written.

**Usage:** `wr import --file <path>`

**Flags:** `--file` (required) — Path to JSON file containing records.

**Input format:** A JSON array `[{...}]` or an object with a `"records"` key `{"records":[{...}]}`. Each record requires `type`, `title`, and `date`.

**Example:**

```bash
wr import --file /tmp/records.json
```

**Success output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"imported":3}}
```

**Error codes:** `invalid_body`, `import_record`, `storage_error`

---

### wr export

Export work report entries in JSON or Markdown format with optional filters.

**Usage:** `wr export --format <json|markdown> [flags]`

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--format` | string | `""` | Output format: `json` or `markdown` (required) |
| `--date` | string | `""` | Exact date filter (`YYYY-MM-DD`) |
| `--from` | string | `""` | Date range start, inclusive (`YYYY-MM-DD`) |
| `--to` | string | `""` | Date range end, inclusive (`YYYY-MM-DD`) |
| `--type` | string | `""` | Filter by type |
| `--status` | string | `""` | Filter by status: `active`, `completed`, `cancelled`, `all` |
| `--query` | string | `""` | Keyword search in title and description |
| `--file` | string | `""` | Output file path (default: stdout) |

**Notes:**
- Both `wr list` and `wr export` default to active-only records. Use `--status all` to include all.
- For Markdown format without a date filter, today's date is used.
- Use `--file` to write output to disk instead of stdout.

**Example:**

```bash
wr export --format json --from 2026-05-01 --to 2026-05-07
wr export --format markdown --date 2026-05-03
```

**Error codes:** `invalid_params`, `storage_error`

---

### wr digest

Manage digest configurations. Digests are batch summaries of work records, processed by LLM and optionally pushed via Pushover. This is a command group with subcommands.

**Usage:** `wr digest <subcommand>`

#### wr digest add

Create a new digest configuration with a recurring schedule, time scope, and output direction.

```
wr digest add --schedule <expr> --scope <scope> --direction <direction>
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--schedule` | string | `""` | Recurring schedule expression, standard 5-field format (min hour dom month dow) (required). |
| `--scope` | string | `""` | Digest time scope (required): `today`, `yesterday`, `week`, `month`, or custom `YYYY-MM-DD:YYYY-MM-DD`. |
| `--direction` | string | `""` | Output direction (required): `agenda` (forward-looking) or `summary` (retrospective). |

**Example:**

```bash
wr digest add --schedule "0 8 * * 1-5" --scope today --direction agenda
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T08:00:00Z","data":{"id":"d_20260503_a1b2c3","schedule":"0 8 * * 1-5","scope":"today","direction":"agenda","enabled":true,"created_at":"2026-05-03T08:00:00Z","updated_at":"2026-05-03T08:00:00Z"}}
```

**Error codes:** `invalid_scope`, `invalid_schedule`, `invalid_direction`, `invalid_body`, `storage_error`

---

#### wr digest list

List all digest configurations.

```
wr digest list
```

**Error codes:** `storage_error`

---

#### wr digest remove

Remove a digest configuration by ID.

```
wr digest remove <id>
```

**Error codes:** `digest_not_found`

---

#### wr digest enable

Enable a digest configuration by ID.

```
wr digest enable <id>
```

**Error codes:** `digest_not_found`

---

#### wr digest disable

Disable a digest configuration by ID.

```
wr digest disable <id>
```

**Error codes:** `digest_not_found`

---

#### wr digest preview

Preview an LLM-generated digest summary for a given digest configuration. Does not send via Pushover.

```
wr digest preview <id>
```

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
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T10:00:00Z","data":{"action":"digest_preview","digest_id":"d_20260501_abc123","scope":"today","direction":"agenda","text":"## 今日待办议程\n\n1. **Review PR #42** [高优先级]","record_count":3,"llm_status":"success","title":"今日待办议程"}}
```

**Error codes:** `digest_not_found`, `internal_error`, `storage_error`

---

### wr prompt

Manage prompt templates used by the digest LLM pipeline. This is a command group with subcommands.

**Usage:** `wr prompt <subcommand>`

**Built-in prompts:** `agenda` (for agenda-style digests), `report` (for report-style digests). You can override any built-in or create custom prompts.

#### wr prompt list

List all prompts with their current text and default status.

```
wr prompt list
```

**Error codes:** `storage_error`

---

#### wr prompt show

Show the effective prompt text for a named prompt.

```
wr prompt show <name>
```

**Error codes:** `prompt_not_found`, `storage_error`

---

#### wr prompt set

Set a custom prompt text for a named prompt. At least one of `--text` or `--file` is required.

```
wr prompt set <name> --text <text>
wr prompt set <name> --file <path>
```

**Flags:** `--text` (prompt text), `--file` (path to file containing prompt text).

**Error codes:** `prompt_not_found`, `invalid_params`, `invalid_body`, `storage_error`

---

#### wr prompt reset

Restore a built-in prompt to its default text. Only works for built-in prompt names (`agenda`, `report`).

```
wr prompt reset <name>
```

**Error codes:** `prompt_not_found`, `storage_error`

---

#### wr prompt preview

Preview LLM prompt output using current data.

```
wr prompt preview <name> [--scope <scope>]
```

**Flags:** `--scope` defaults to `today`. Supports `today`, `yesterday`, `week`, `month`, or custom `YYYY-MM-DD:YYYY-MM-DD`.

**Error codes:** `internal_error`, `invalid_scope`, `prompt_not_found`, `storage_error`

---

### wr agent doctor

Run health checks on data directory, LLM, and Pushover configuration.

```bash
wr agent doctor
```

**Behavior:** Runs three independent health checks:

| Check | Pass condition | Fail / Warning |
|-------|---------------|----------------|
| `data_dir` | Directory exists and is writable | Missing or not writable |
| `llm` | `llm.text.api_key` is set | Warns (not fails) if missing — LLM is optional |
| `pushover` | Both `api_token` and `user_key` are set | Fails if either is missing |

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"checks":[{"name":"data_dir","status":"pass","message":"data_dir \"/home/user/.work-report/work-records\" exists and is writable"},{"name":"llm","status":"warn","message":"llm.text.api_key is not set — LLM features will be unavailable"},{"name":"pushover","status":"pass","message":"pushover credentials are configured"}]}}
```

Status values: `pass`, `fail`, `warn`.

---

### wr agent (SDK commands)

The `wr agent` namespace includes additional commands from the agent SDK:

| Command | Description |
|---------|-------------|
| `wr agent doctor` | Health checks for data dir, LLM, and Pushover |
| `wr agent schema` | Print the JSONL schema for all commands |
| `wr agent errors` | Print the error code registry |
| `wr agent config list` | List all config values |
| `wr agent config set` | Set a config value (delegates to `wr config set`) |
| `wr agent debug` | Print debug information (version, build, environment) |
| `wr agent cache` | Manage the agent response cache |

These are low-level commands primarily useful for agent integration and debugging.

---

### wr status

Show configuration health and data statistics.

**Usage:** `wr status`

**Flags:** None.

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"config":{"timezone":"Asia/Shanghai","data_dir":"/home/user/.work-report/work-records","pushover":"configured","llm_text":"configured","llm_vision":"not_configured"},"data":{"total_records":3,"by_type":{"meeting":2,"task":1},"by_status":{"active":3},"digests":1,"backups":5}}}
```

The output includes `config` (timezone, data_dir, pushover/llm configured status) and `data` (total records, counts by type and status, digest count, backup count).

---

### wr config

Manage wr configuration. This is a command group with subcommands.

**Usage:** `wr config <subcommand> [flags]`

#### wr config init

Create `~/.work-report/config.json` with sensible defaults. All flags are optional — the tool works without any LLM key.

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
| `--timezone` | string | `""` | IANA timezone (default: `Asia/Shanghai`) |

**Example:**

```bash
wr config init --llm-text-key sk-xxx --llm-text-model gpt-4o-mini
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"path":"/home/user/.work-report/config.json"}}
```

#### wr config set \<key\> \<value\>

Set a config value by dot-notation key.

**Valid keys:**

| Key | Value type | Description |
|-----|-----------|-------------|
| `pushover.api_token` | string | Pushover API token |
| `pushover.user_key` | string | Pushover user key |
| `llm.text.provider` | string | LLM text provider name |
| `llm.text.api_key` | string | LLM text API key |
| `llm.text.api_base` | string | LLM text API base URL |
| `llm.text.model` | string | LLM text model name |
| `llm.text.timeout` | int | LLM text timeout (seconds) |
| `llm.vision.provider` | string | LLM vision provider name |
| `llm.vision.api_key` | string | LLM vision API key |
| `llm.vision.api_base` | string | LLM vision API base URL |
| `llm.vision.model` | string | LLM vision model name |
| `llm.vision.timeout` | int | LLM vision timeout (seconds) |
| `data_dir` | string | Data directory path |
| `timezone` | string | IANA timezone string |

#### wr config show

Display current config with all secrets redacted (first 4 chars shown, rest masked).

```bash
wr config show
```

**Output:**

```json
{"version":"1.0","tool":"wr","type":"result","timestamp":"2026-05-03T14:00:00Z","data":{"pushover":{"api_token":"secr****","user_key":"secr****"},"llm":{"text":{"provider":"","api_key":"secr****","model":"gpt-4o-mini"},"vision":{"provider":"","api_key":"","model":""}},"data_dir":"/home/user/.work-report/work-records","timezone":"Asia/Shanghai"}}
```

---

### wr backup

Manage data backups with Grandfather-Father-Son (GFS) rotation. All backup commands are local-only — they operate directly on the filesystem.

**Usage:** `wr backup <subcommand> [flags]`

**Persistent flag:** `--output <dir>` — Override backup output directory. Available on all backup subcommands.

#### wr backup create

Create a timestamped zip backup immediately.

```
wr backup create [--output <dir>]
```

Zips config.json, work-records/, digests.json, and related files. Filename: `wr-backup-YYYYMMDD-HHMMSS.zip`.

**Error codes:** `data_dir_not_found`, `backup_failed`

---

#### wr backup list

List all existing backups, sorted newest first.

```
wr backup list [--output <dir>]
```

---

#### wr backup cleanup

Run GFS rotation to remove old backups based on retention policy.

```
wr backup cleanup [--output <dir>]
```

Three rules are evaluated (daily, weekly, monthly) and unioned — a backup protected by ANY rule is retained.

**Error codes:** `rotation_failed`

---

#### wr backup config show

Display the current backup configuration.

```
wr backup config show
```

Default retention: 7 daily, 4 weekly, 6 monthly. Default output directory: `~/.work-report/backups`.

---

#### wr backup config set

Update backup configuration. Only explicitly-provided flags are updated.

```
wr backup config set [flags]
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--output-dir` | string | `""` | Backup output directory |
| `--retention-daily` | int | `0` | Number of daily backups to keep |
| `--retention-weekly` | int | `0` | Number of weekly backups to keep |
| `--retention-monthly` | int | `0` | Number of monthly backups to keep |

**Notes:**
- Only explicitly-set flags are updated. Omitted flags retain their current values.
- Config is stored at `~/.work-report/backup-config.json` (separate from main `config.json`).

**Example:**

```bash
wr backup config set --retention-daily 14
```

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
| `invalid_type` | Missing or unrecognized record type | Ensure `--type` is one of: `meeting`, `task`, `reminder`, `log`. Or provide `--text`/`--image` for LLM classification. |
| `invalid_body` | Missing required fields or malformed request body | Check that required flags (`--title`, `--date`, `--type`) are provided. |
| `invalid_field` | Attempted to update a field that is not allowed | Check the field name in the update command. |
| `record_not_found` | No record matches the given short_id | List records with `wr list` to find the correct short_id. |
| `already_completed` | Attempted to update a completed record | Completed records cannot be modified. Use `wr list --status completed` to view them. |
| `already_cancelled` | Attempted to update a cancelled record | Cancelled records cannot be modified. |
| `storage_error` | Filesystem or storage layer error | Check data directory permissions and disk space. |
| `llm_not_configured` | LLM API key is missing for the requested classification mode | LLM is optional. Either provide `--type` and `--title` explicitly, or run `wr config set llm.text.api_key <key>`. |
| `llm_error` | LLM API call failed | Check API key validity, network connectivity, and model name. Retry once. |
| `pushover_not_configured` | Pushover credentials are missing | Run `wr config set pushover.api_token <token>` and `wr config set pushover.user_key <key>`. |
| `push_error` | Pushover notification delivery failed | Check Pushover credentials and network. |
| `import_record` | Per-record validation failure during import | Check the record at the specified index for missing or invalid fields. Fix and retry. |
| `invalid_params` | Missing or unsupported command parameter | Check the command's required flags. For export, `--format` must be `json` or `markdown`. |
| `multiple_matches` | Content-based lookup matched more than one active record (returned as `invalid_params`) | Use `wr list` to find the exact `short_id` and use that instead. The error message lists all matching IDs. |
| `digest_not_found` | No digest configuration matches the given ID | List digests with `wr digest list` to find the correct ID. |
| `invalid_scope` | Invalid digest scope value | Scope must be one of: `today`, `yesterday`, `week`, `month`, or `YYYY-MM-DD:YYYY-MM-DD`. |
| `invalid_schedule` | Invalid schedule expression for digest | Check the expression syntax (5-field format: min hour dom month dow). |
| `invalid_direction` | Invalid digest direction value | Direction must be `agenda` or `summary`. |
| `prompt_not_found` | Prompt name has no default and no override | Only built-in names (`agenda`, `report`) can be reset. |
| `internal_error` | Digest/prompt preview pipeline failure | Check LLM configuration and available records. Retry once. |
| `data_dir_not_found` | Data directory does not exist | Run `wr config init` first to create the data directory. |
| `backup_failed` | Zip creation failed | Check disk space and write permissions on the backup output directory. |
| `rotation_failed` | GFS rotation failed during backup cleanup | Check backup directory permissions. Inspect with `wr backup list`. |
| `config_not_found` | Backup config file not found | Defaults are used when the file is missing. Check filesystem permissions. |
| `lock_conflict` | Concurrent access conflict (cross-process file lock) | Another `wr` process is writing to the same data. Wait a moment and retry. |
| `storage_locked` | Storage file is locked by another process | Wait for the other process to finish, or remove stale lock files if the other process has exited. |
| `marshal_error` | JSON serialization failed | Internal error — check data integrity. File a bug if persistent. |
| `method_not_allowed` | Unsupported HTTP method | Internal routing error. File a bug. |
| `unknown` | Unrecognized error | Internal error — check logs for details. File a bug if persistent. |

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

The `short_id` is a 16-character hex string (e.g. `a1b2c3d4e5f67890`) derived by SHA-256 hashing the record's filename stem. It is not reversible. Use `wr list` to discover short_ids.

---

## Common Workflows

### First-Time Setup

```bash
wr config init

# (Optional) Add LLM key for natural-language classification
wr config set llm.text.api_key sk-your-key
wr config set llm.text.model gpt-4o-mini

# (Optional) Add Pushover for push notifications
wr config set pushover.api_token your-token
wr config set pushover.user_key your-key

# Verify everything is configured correctly
wr agent doctor
```

### Daily Usage

```bash
# Add entries (--date defaults to today if omitted)
wr add --type meeting --title "Sprint planning" --time 09:00 --participants "Alice,Bob"
wr add --type task --title "Review PR #42" --priority high
wr add --type log --title "Deployed v2.1 to staging"

# Add with idempotency key (safe in retry loops — no duplicates)
wr add --type task --title "Daily standup" --idempotency-key "standup-2026-05-03"

# Check today's entries
wr list

# Complete a task by title (no --date needed, defaults to today)
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
wr report today
wr report date 2026-05-01
wr report week
wr report range --from 2026-04-27 --to 2026-05-03

# Push any of the above via Pushover
wr report push today
wr report push week
```

### Using LLM Classification (Optional)

```bash
# Text classification — auto-detects type, title, date, etc.
wr add --text "明天上午10点和产品团队开需求评审会"

# Image classification — uses vision LLM
wr add --image /tmp/whiteboard.jpg --text "whiteboard notes from meeting"

# If LLM returns cancel_or_update, the response has type "result" and no record is created
```

### Data Import

```bash
wr import --file /tmp/records.json

# Verify imported records
wr list --date 2026-05-03
```

### Data Export

```bash
# Export as JSON
wr export --format json --from 2026-05-01 --to 2026-05-07

# Export as Markdown
wr export --format markdown --date 2026-05-03

# Export to file
wr export --format json --type meeting --file meetings.json
```

### Backup

```bash
# Create an immediate backup (local-only)
wr backup create

# Verify and clean up
wr backup list
wr backup cleanup
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
  "timezone": "Asia/Shanghai"
}
```

### Defaults

| Setting | Default |
|---------|---------|
| `timezone` | `Asia/Shanghai` |
| `data_dir` | `~/.work-report/work-records` |

### LLM Text vs Vision

- **`llm.text`** — Used when `wr add --text "..."` is called. Classifies natural language descriptions into typed records.
- **`llm.vision`** — Used when `wr add --image /path` is called. Classifies images into typed records. Can be a different provider/model than text.

Each has independent `provider`, `api_key`, `api_base`, and `model` settings.

---

## Tips & Gotchas

1. **JSONL goes to stdout only, never stderr.** The root command silences Cobra's usage and error output. All structured output (including errors) is JSONL on stdout.

2. **ShortID is a hash, not a file path.** The 16-character hex ID is derived from the record filename via SHA-256. It cannot be reversed. Always use `wr list` to discover short_ids.

3. **Completed/cancelled records are immutable.** You cannot update, complete, or cancel a record that is already completed or cancelled. Check status with `wr list --status all` first.

4. **LLM classification is optional.** Without an LLM key, use `--type`, `--title`, and optionally `--date` (defaults to today). With `--text` or `--image`, the LLM fills in fields automatically. Explicit flags take precedence over LLM results.

5. **`wr config set` validates before saving.** Invalid values (bad timezone) are rejected before the config file is modified.

6. **Pushover requires both api_token and user_key.** Both must be non-empty for push commands to work. Missing either returns `pushover_not_configured`.

7. **Report date defaults to "today" in configured timezone.** The tool uses the timezone from config (default: `Asia/Shanghai`) to determine "today".

8. **Tags are comma-separated strings.** In `wr add`, use `--tags "tag1,tag2"`. In `wr update`, `--tags` replaces the entire list (does not append).

9. **Import validates all records before writing any.** If record at index 5 has a missing field, records 0–4 are NOT written either. Fix the invalid record and retry.

10. **Imported records get fresh ShortIDs.** The original ShortIDs from the source are not preserved.

11. **Both list and export default to active-only.** Use `--status all` to include completed/cancelled records.

12. **Export Markdown without date filter defaults to today.** JSON export without date filters returns all records.

13. **Import file accepts two JSON shapes.** A bare JSON array `[{...}]` or an object with a `records` key `{"records":[{...}]}`.

14. **Content-based lookup for update/complete/cancel defaults --date to today.** Use `--title` without `--date` for today's records. If multiple records match, you'll get an `invalid_params` error listing all matching IDs — use `wr list` to find the exact `short_id`.

15. **Use idempotency keys for retry-safe adds.** Pass `--idempotency-key <unique-key>` to deduplicate. Ideal for retry loops or any workflow where the same add might execute twice.

16. **`wr remind push --due` is atomic push+complete.** Successfully pushed reminders are automatically completed. Push failures leave the record active for retry. Maximum 10 reminders per batch.

17. **Backup config is separate from main config.** Backup settings live in `~/.work-report/backup-config.json`. Use `wr backup config show` to view and `wr backup config set` to modify.

18. **GFS rotation uses a distinct-bucket strategy.** For each time granularity (daily/weekly/monthly), the newest backup per distinct calendar bucket is kept. Rules are unioned — a backup protected by ANY rule is retained.

19. **`wr agent doctor` diagnoses configuration issues.** Runs three health checks (data_dir, LLM, Pushover) and reports pass/fail/warn. LLM missing key is a warning (not a failure). Useful for quick troubleshooting.

20. **All commands are direct CLI invocations.** No background process or setup is needed. Every command reads config and data directly from disk. Just run `wr config init` and start using the tool.
