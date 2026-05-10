# WR 使用反馈与改进建议

> Hermes Agent 视角 — 2026-05-10

---

## 一、当前使用方式

### 1.1 部署架构

```
wr CLI (v0.2.2) → 纯本地文件操作 → ~/.work-report/work-records/
                                  → ~/.work-report/config.json

Hermes cron  → 每5分钟 wr list → 筛选到期提醒 → Pushover → wr complete
```

wr 已移除 daemon（v0.3.0），提醒推送完全由 Hermes cron 接管。

### 1.2 日常命令使用频率

| 命令 | 频率 | 用途 |
|------|------|------|
| `wr add --type reminder --title "..." --date ... --time ...` | 高 | 记录提醒 |
| `wr add --type meeting --title "..." --time ...` | 中 | 记录会议 |
| `wr add --type task --title "..."` | 中 | 记录任务 |
| `wr add --type log --title "..."` | 中 | 记录完成事项 |
| `wr add --text "..."` | 低 | LLM 自然语言录入（慢，10-20秒） |
| `wr list --status active` | 高 | Hermes cron 每5分钟调用 |
| `wr complete <short_id>` | 中 | 完成记录 |
| `wr complete --title "..." --date ...` | 低 | 按标题完成 |
| `wr report today` | 中 | 生成日报 |
| `wr digest list / add / preview` | 中 | 定时摘要管理 |
| `wr backup create` | 低 | 手动备份 |

---

## 二、遇到过的问题

### 2.1 Daemon 静默死亡（已通过移除 daemon 解决）

- **现象**：daemon 进程消失，`.daemon.json` 状态文件丢失
- **影响**：定时摘要不触发，提醒不推送
- **根因**：裸进程在 WSL 休眠/Windows 锁屏后被终止，无自恢复能力
- **解决**：v0.3.0 移除 daemon，改用外部调度

### 2.2 升级时 Text file busy

- **现象**：daemon 运行时替换二进制文件失败
- **解决**：改为纯 CLI（无守护进程），直接 cp 即可

### 2.3 5 字段 cron 兼容性（v0.2.0 bug，已修复）

- **现象**：标准 5 字段 cron 被拒绝（"invalid cron expression, skipping"）
- **修复**：v0.2.2 支持标准 5 字段

### 2.4 LLM 超时导致摘要降级

- **现象**：`wr digest preview` 调用 kimi-k2.6 经常超时
- **影响**：摘要退化为原始 markdown，无 AI 润色
- **现状**：反正是 fallback（非 failure），可接受

### 2.5 --date 在 complete/cancel 中不默认 today

- **现象**：`wr complete --title "X"` 报错，必须显式传 `--date`
- **困惑**：add 默认 today，complete 却不默认

### 2.6 提醒缺乏原子操作

- **现象**：推送提醒需要 4-5 步跨工具协作（wr list → 判断 → push → complete）
- **影响**：任何一环出问题都导致重复推送或遗漏

---

## 三、改进建议

### 3.1 核心需求：`wr remind` 子命令组

最想要的能力，一个原子命令完成「到期检测 + 推送 + 标记完成」：

```
wr remind push --due              # 推送所有到期提醒 + Pushover + complete
wr remind push <short_id>         # 单独推送某条
wr remind due                     # 列出到期的提醒（dry-run）
wr remind due --window 30m         # 列出未来30分钟内到期的（预览）
```

**为什么需要这个？**

当前一条提醒的完整生命周期需要：

```
Hermes cron 触发
  → wr list --status active          (拉全量)
  → 解析 JSONL 输出                  (语言模型解析)
  → 对比当前时间判断是否到期          (语言模型判断)
  → wr report push today             (推送)
  → wr complete <short_id>           (标记完成)
```

如果 wr 有 `wr remind push --due`，Hermes cron 只需要：

```
*/5 * * * *  wr remind push --due
```

一条命令，外部调度器只需要当时钟，不需要理解 wr 的数据格式。

### 3.2 `wr remind push --due` 行为规范

- 找到所有 `date + time <= now` 的活跃 reminder
- 逐条通过 Pushover 推送提醒内容（标题 + 时间）
- 推送成功后立即标记完成
- 推送失败的不标记完成（下次继续重试）
- 返回 JSONL 格式：`{pushed: [...], failed: [...], skipped: 0}`
- 每次运行最多处理 N 条（建议 10），防止积压雪崩

### 3.3 --date 默认值统一

`complete` / `cancel` 的 `--date` 应该和 `add` 一样默认 today，减少困惑。

### 3.4 LLM 超时时间可配置

当前 LLM 5 秒超时太短，kimi-k2.6 经常 10-20 秒才返回。建议可配置或默认提升到 30 秒。

### 3.5 过期提醒自动清理

5/9 的「查看日程」「看调研报告」提醒至今仍为 active。建议：
- `wr remind due` 不显示已过期超过 24h 的
- 或 `wr cleanup --stale 24h` 自动标记过期提醒

### 3.6 提醒确认机制（可选）

推送后需要确认才标记完成，而不是推了就完成：

```
wr remind push --due --require-confirm
```

用户回复确认后，Hermes 调 `wr remind confirm <short_id>` 完成标记。

---

## 四、与 Hermes 的协作模型（建议）

```
                  ┌─────────────────────────┐
                  │      Hermes Agent        │
                  │                          │
用户说"提醒我" ──▶│  wr add --type reminder   │──▶ JSON 文件落盘
                  │  cronjob create (one-shot)│──▶ 到点触发
                  │                          │
                  │  到点后:                  │
                  │  wr remind push --due     │──▶ Pushover + complete
                  └─────────────────────────┘
```

wr 自包含提醒引擎，Hermes 仅充当时钟和创建入口。