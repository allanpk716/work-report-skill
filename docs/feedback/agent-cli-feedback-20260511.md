# Agent-Friendly CLI 设计反馈

> 基于 Hermes Agent 实际调用 `wr` (work-report-skill v0.3.1) 的经验总结  
> 日期：2026-05-11  
> 目标读者：CLI 工具开发者

---

## 🟢 做得好的（其他工具应借鉴）

### 1. JSONL 输出 = Agent 的母语

每条命令 stdout 输出恰好一行 JSON，结构固定。不需要 `grep`/`sed`/`awk` 解析。`type` 字段做顶层路由（`result`/`error`/`warning`），`error_code` 枚举做错误分支。

```json
{"version":"1.0","tool":"wr","type":"result","data":{...}}
{"version":"1.0","tool":"wr","type":"error","error_code":"record_not_found","message":"..."}
```

**建议：stdout = 结构化，debug 日志另走 stderr。**

### 2. `--quiet` 标志

虽然实现不完美（见下方），但设计意图对了。Agent 需要「只给我结果，别唠嗑」。

### 3. 纯 CLI，零依赖

`cp wr ~/.local/bin/wr` 就完成升级。没有 daemon、没有 systemd、没有端口占用、没有数据库迁移。这对 Agent 运维极其友好。

### 4. `wr agent doctor` 健康检查

一键验证 LLM、Pushover、数据目录全链路。Agent 可以在执行任务前自检："这工具配置好了吗？" → 一条命令回答。

### 5. 错误码驱动

每个错误有唯一 `error_code`，Agent 不需要解析 message 字符串就能做分支处理。这比返回 "Error: something went wrong" 强 100 倍。

---

## 🔴 需要改进的（设计师请提前规避）

### 1. ⚠️ stdout 污染：stderr 日志泄漏进业务输出

```
time="..." level=info msg="file lock acquired"  ← 不该出现在这里
{"version":"1.0","tool":"wr","type":"result",...}  ← Agent 要的是这行
```

`--quiet` 仍无法完全抑制 info 日志。Agent 被迫用 `2>&1 | tail -1` 取最后一行 — 脆弱，且多行输出时会丢数据。

**规则：`--quiet` 下 stderr 必须干净。或者 info/debug 日志只在没有 `--quiet` 时输出。**

### 2. ⚠️ 字段命名不一致

```json
// 信封层：snake_case
{"type":"result", "error_code":"...", "timestamp":"..."}

// entries 数组：PascalCase
{"ShortID":"xxx", "Type":"meeting", "Title":"...", "Date":"..."}
```

Agent 解析时两种命名并存，需要记忆 "envelope 用小写，entries 用大写" — 这是不必要的认知负担。

**规则：全量用一种风格。选 snake_case 对 JSON 最自然。**

### 3. ⚠️ 文档与实现不同步

| 文档说 | 实际 |
|--------|------|
| `data.text` | `data.summary` |
| 支持 `--end-time` | `unknown flag: --end-time` |

Agent 按文档写代码 → 运行时炸 → 需要人类介入排查。比没有文档更糟。

**规则：文档从代码生成，或 CI 中做 flag 存在性校验。**

### 4. ⚠️ flag 语义不对称

v0.3.0 中 `add --date` 默认今天，但 `complete --date` 不默认。Agent 按惯性假设对称 → 触发 `record_not_found`。v0.3.1 修了，但这类不对称是常见坑。

**规则：同名 flag 的默认值语义应该一致。**

### 5. ⚠️ LLM 分类的隐式依赖

`wr add --text "与张三沟通项目"` 依赖 LLM 推断日期。文本不含 "今天" 时，LLM 可能漏掉 date 字段 → `invalid_body: missing required field: date`。

Agent 的恢复路径：fallback 到手动 flag。但如果 CLI 在 `--text` 模式下自动默认 date=today（当 LLM 没返回时），Agent 就不需要这个分支。

**规则：核心字段（date）应有可靠默认值，不依赖 LLM。**

### 6. ⚠️ 不可变类型的"惊喜"

```bash
wr complete <log_id>
# → logs cannot be completed
```

设计上合理（log = 已完成活动），但 Agent 的角度是 "complete 对所有记录类型都适用" → 撞墙。

**规则：类型特定的约束要在文档中显眼标注，最好 `--help` 里就写明。**

### 7. ⚠️ 文件锁冲突

高并发（cron 每 5 分钟 + 手动操作）可能碰到 `lock_timeout`。目前冲突概率低，但随着 cron 增多会上升。

**规则：提供重试建议或在错误响应中给 `retry_after_ms` 字段。**

---

## 📋 Agent-Friendly CLI Checklist

给 CLI 开发者的一页纸清单：

```
□ stdout 只输出结构化 JSON/JSONL，一行一条
□ stderr 留给日志，--quiet 下 stderr 为空
□ 每个错误有唯一 error_code，不靠 message 字符串匹配
□ 字段命名全量一致（snake_case 优先）
□ 同名 flag 的默认值在所有子命令中语义一致
□ 文档从代码生成或 CI 校验
□ 核心字段有合理默认值，不依赖外部服务推断
□ 类型特定的限制在 --help 和错误消息中显式说明
□ 提供 health check / doctor 命令
□ 升级路径简单（最好 cp binary 就搞定）
```