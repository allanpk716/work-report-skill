# wr (Work Report CLI) — 集成评估与改进建议

> 评估者：Hermes Agent (DeepSeek v4 Pro)
> 日期：2026-05-08
> 项目：https://github.com/allanpk716/work-report-skill
> 集成场景：作为 Hermes Agent 的自然语言工作日报助手

---

## 一、概述

`wr` 是一个 Go 编写的 CLI 工作日报告理工具，采用 CLI 客户端 + HTTP 守护进程架构，输出 JSONL 格式，专为 AI Agent 消费设计。以下是从实际集成和使用的角度，对这个工具的全面评估。

---

## 二、完成的工作

### 2.1 部署链路

| 步骤 | 内容 | 结果 |
|------|------|------|
| 环境准备 | 本地安装 Go 1.24.11（无需 sudo） | 成功 |
| 编译 | `go build -o wr .` | 成功，产物 ~12MB |
| 部署 | 复制到 `~/.local/bin/wr` | 成功 |
| 初始化 | `wr config init` | `~/.work-report/config.json` 创建 |
| 启动守护进程 | `wr agent daemon ensure-running` | 监听 localhost:18080 |

### 2.2 LLM 集成

| 配置项 | 值 |
|--------|-----|
| API Base | `https://aihubmix.com/v1` |
| Model | `kimi-k2.6` |
| Provider | `kimi` |
| API Key | 已配置（自动 redact） |

```bash
wr config set llm.text.provider kimi
wr config set llm.text.api_key 'sk-xxx'
wr config set llm.text.model kimi-k2.6
wr config set llm.text.api_base 'https://aihubmix.com/v1'
```

### 2.3 Hermes Skill 创建

在 `~/.hermes/skills/productivity/work-report/SKILL.md` 创建了完整的 Hermes skill，包含：
- 触发词检测（中文：日报、周报、记录一下、完成了 等）
- 全命令工作流（daemon 启动 → 自然语言记录 → 报告生成）
- JSONL 响应解析逻辑
- 错误码处理表
- 常见陷阱与解决方案

### 2.4 集成架构

```
用户 (中文自然语言)
    │
    ▼
Hermes Agent
    │ 加载 work-report skill
    │ 意图识别: "记录一下..." → wr add --text
    │ 意图识别: "日报" → wr report today
    │
    ▼
wr CLI ──HTTP──▶ wr Daemon (:18080)
    ◀──JSONL──     │
                   ├── Storage (~/.work-report/work-records/)
                   ├── LLM Classifier (kimi-k2.6 @ aihubmix)
                   └── Scheduler (Pushover reminders)
```

---

## 三、验证结果

### 3.1 自然语言分类测试

| 输入 | LLM 分类结果 | 正确？ |
|------|-------------|--------|
| `今天下午2点和张三开会讨论Q3规划` | type=meeting, title=与张三讨论Q3规划, time=14:00, related_person=张三 | ✅ |
| `明天之前完成代码审查，优先级高` | type=task, title=完成代码审查, date=2026-05-09, priority=high | ✅ |
| `写完了认证模块的单元测试` | type=log, title=完成认证模块单元测试, date=2026-05-08 | ✅ |
| `今天上午完成了周报编写，下午2点和团队开了迭代复盘会` | type=log, title=完成周报与迭代复盘会, time=14:00 | ⚠️ 合并为单条 log |
| `今天上午10点和王五开了产品需求评审会` | type=log, title=产品需求评审会, time=10:00 | ⚠️ 应为 meeting 而非 log |

### 3.2 日报生成

```markdown
# 工作日报 2026-05-08

📊 汇总: 会议 2 | 任务 2 | 提醒 0 | 日志 5 | 共计 9 条

## 📅 会议 (2)
- 站会 [09:00]
- 与张三讨论Q3规划 [14:00]

## ✅ 任务 (2)
- 编写单元测试 [已取消]
- Review PR #42 [已完成]

## 📝 日志 (5)
- 产品需求评审会
- 完成周报及迭代复盘会
- 完成代码审查
- 完成认证模块单元测试
- 完成周报与迭代复盘会
```

格式良好，emoji + 分类呈现清晰。`data.markdown` 字段可直接作为 Agent 回复。

---

## 四、发现的问题

### 4.1 🔴 严重：守护进程不稳定（持续崩溃）

**现象：** 守护进程在启动后 14-20 秒内自动停止，日志反复出现：

```
[scheduler] started
[daemon] listening on :18080 (pid=xxx)
[scheduler] catchup complete: scanned=0 skipped=0 fired=0 errors=0 recurring_skipped=0
[scheduler] stopped     ← 无任何错误日志就停止了
```

**影响：** Agent 每次操作前必须检查/重启发守护进程，且 LLM 分类耗时 10-20 秒时，守护进程可能在处理中途死掉。

**定位建议：**
- `internal/scheduler/` 的 scheduler 是否有内部超时或 panic recovery 导致静默停止？
- `internal/daemon/state.go` 的状态管理是否有竞态条件？
- 建议添加 daemon 存活探针（定期写 heartbeat 或通过 `/health` 端点验证）

**我看到的日志证据（`~/.work-report/daemon.log`）：**

```
# 进程 103855: 启动 16:44:58, scheduler 停止 16:45:12（存活 14 秒）
# 进程 103931: 启动 16:47:40, 最后可见活动 16:50:26（处理 LLM 请求 20 秒后无日志）
# 进程 104029: 启动 16:51:26, scheduler 停止 16:51:26（存活 0 秒！）
# 进程 104144: 启动 16:53:46, 最后可见活动 16:55:17
```

### 4.2 🟡 中等：命令行工具启动超时

**现象：** `wr agent daemon start` 命令会阻塞，直到守护进程退出。当前端命令（如 `ensure-running`）触发守护进程启动时，如果守护进程很快死亡，命令会超时。

**影响：** Agent 调用 `wr agent daemon start` 后被无限阻塞（不返回 JSONL）。

**改进建议：**
- `wr agent daemon start` 应该在守护进程成功监听端口后立即返回（当前 `onReady` 回调已存在但似乎没生效？）
- 或者在 start 命令添加 `--timeout` 选项，超时后返回错误

### 4.3 🟡 中等：LLM 延迟过高

**现象：** kimi-k2.6 通过 aihubmix 每次分类调用耗时 10-21 秒（实测数据：20723ms, 19245ms, 13091ms, 4027ms, 2129ms, 1896ms）。

**影响：** 用户体验差，且 LLM 调用期间守护进程可能已经死亡。

**改进建议：**
- LLM 客户端的 HTTP 超时当前为 30 秒（`internal/llm/client.go`），可考虑添加流式响应支持
- 或者允许用户配置超时时间（当前硬编码）
- 考虑添加同步/异步分类模式：用户可以选择"快速模式"（不等待 LLM，只记录原文 + 事后分类）

### 4.4 🟡 中等：`wr daemon stop` 不可靠

**现象：** `wr agent daemon stop` 返回成功但进程仍然占用端口。日志中多次出现 `bind: address already in use`。

```
wr agent daemon stop
→ {"type":"result","data":"daemon stopped (was pid=102992, port=18080)"}
→ 但 PID 102992 仍在运行，端口未释放
```

**改进建议：** stop 命令应发送 SIGTERM 后轮询确认进程退出，超时后发送 SIGKILL。

### 4.5 🟡 中等：文档命令路径不一致

| README 写法 | 实际可用 | 
|------------|---------|
| `wr doctor` | ❌ unknown command |
| `wr daemon start` | ❌ 已迁移 |
| `wr agent doctor` | ✅ |
| `wr agent daemon start` | ✅ |

**建议：** README.md 需要同步更新到 v0.1.0 的 SDK 迁移后的命令路径。

### 4.6 🟢 轻度：端口常量不一致

- `internal/config/config.go`: 默认端口 **18080**
- `internal/daemon/server.go` 常量 `DefaultPort`: **17530**

两者不一致。`DefaultPort` 常量当前未被使用（config 默认值优先），但容易误导。

### 4.7 🟢 轻度：无预编译发布

GitHub Releases 为空，用户必须安装 Go 1.24.11+ 从源码编译。对于非 Go 开发者的用户门槛较高。

**建议：** 添加 GitHub Actions workflow 自动发布 linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 的二进制。

### 4.8 🟢 轻度：语义合并问题

当用户输入包含多个行为时（如"上午做了 A，下午做了 B"），LLM 将其合并为单条记录。Agent 无法判断这是用户意图还是分类错误。

**建议：** 对于明显包含多个独立事件的输入，LLM 可返回多条记录（当前 `ClassifyResult` 是单条），或在分类结果中添加 `has_multiple_events: true` hint。

### 4.9 🟢 轻度：scheme 文件缺失导致 scheduler 噪音

每次 daemon 启动都有：
```
scheduler: state file and backup both unreadable: ... no such file or directory (starting fresh)
```

这是正常的新启动状态，但日志级别可能过高（当前是非 error 级别的 warn），产生噪音。

---

## 五、LLM 分类质量分析

### 5.1 分类准确率

| 总测试用例 | 正确 | 部分错误 | 完全错误 |
|-----------|------|---------|---------|
| 5 | 3 | 2 | 0 |

### 5.2 分类错误模式

1. **会议误识别为日志**：`"开了产品需求评审会"` → log（应为 meeting）。关键词"开会/评审"应更强烈地映射到 meeting 类型。
2. **复合事件合并**：`"上午完成了周报编写，下午2点...开了迭代复盘会"` → 整并为单条 log。高信息密度的输入应拆分为多条记录。

### 5.3 分类系统提示词建议

当前 `internal/llm/classify.go:buildSystemPrompt` 的提示词质量不错，但可以增强：

```
差异点 1: 优先匹配规则
当前没有显式的优先级规则。建议添加：
- 如果文本包含"会/会议/讨论/评审/面谈" → 强烈倾向 meeting
- 如果文本包含"完成/做了/写完了/搞定了" → 强烈倾向 log
- 如果文本包含"要/需要/必须/得/待" → 强烈倾向 task

差异点 2: 多事件拆分
当前系统提示词没有处理复合输入的场景。建议添加：
- 如果输入描述多个独立事件，返回一个数组或分多次调用

差异点 3: 模型选择
kimi-k2.6 的延迟（10-20s）对于实时分类偏慢，可考虑使用更快的模型（如 gpt-4o-mini 通常 <2s）
```

---

## 六、架构观察

### 6.1 好的设计

- **JSONL 单一输出格式** — Agent 友好，解析统一
- **错误码体系** — `daemon_not_running`, `record_not_found` 等，清晰可操作
- **幂等性支持** — `--idempotency-key` 减少 Agent 重试时的重复记录
- **两类添加方式** — 手动 `--type --title` 和 LLM `--text` 互不干扰
- **时区感知** — `Asia/Shanghai` 默认 + 可配置
- **红action 处理** — 敏感字段自动遮蔽
- **配置白名单** — `ValidConfigPaths()` 防止 typo

### 6.2 可改进的架构点

- **守护进程生命周期管理** — 当前守护进程容易意外死亡，建议增加 supervisor 机制或 systemd 集成
- **LLM 调用的非阻塞化** — 当前 `add` 请求同步等待 LLM 分类，导致阻塞。可考虑：先返回 202 accepted，后台异步分类后通过回调通知
- **迁移兼容期** — v0.1.0 的 SDK 迁移改变了命令路径（`wr daemon *` → `wr agent daemon *`），但未提供向后兼容的别名

---

## 七、对 Agent 使用场景的建议

### 7.1 推荐使用模式

```bash
# 1. 守护进程保活（每次操作前）
wr agent daemon ensure-running || {
    kill -9 $(lsof -ti:18080) 2>/dev/null
    wr agent daemon start
}

# 2. 简单记录（跳过 LLM，低延迟）
wr add --type log --title "完成了XX" --date 2026-05-08

# 3. 自然语言记录（支持 LLM 分类）
wr add --text "今天下午2点和张三开会"

# 4. 日报
wr report today    # 读取 data.markdown

# 5. 周报
wr report week     # 读取 data.markdown
```

### 7.2 Agent 端需要处理的问题

1. 守护进程健康检查（每次操作前）
2. LLM 超时重试（30s + 指数退避）
3. LLM 分类失败时的降级（回退到手动 `--type` 模式）
4. `cancel_or_update` 响应的特殊处理（不创建记录）
5. 复合输入的拆分提示（引导用户逐条记录）

---

## 八、改进优先级建议

| 优先级 | 问题 | 建议方案 |
|--------|------|---------|
| P0 | 守护进程不稳定 | 排查 scheduler 异常退出原因，增加存活探针 |
| P0 | `daemon start` 命令超时 | 确认 onReady 回调生效；daemon 监听成功后立即返回 |
| P1 | `daemon stop` 不可靠 | stop → SIGTERM → 轮询 → SIGKILL 级联 |
| P1 | 添加 GitHub Releases | CI 自动构建多架构二进制 |
| P1 | README 命令路径更新 | 同步 v0.1.0 路径变更 |
| P2 | LLM 延迟 | 支持配置超时、考虑异步分类模式 |
| P2 | LLM 分类提示词优化 | 添加优先级规则和多事件拆分 |
| P2 | 端口常量统一 | config.go 和 server.go 对齐 |
| P3 | scheduler 日志降噪 | 首次启动的 state file not found 改为 DEBUG 级别 |

---

## 九、总结

`wr` 是一个设计方向正确的工作日报工具，JSONL 输出 + 错误码体系 + 自然语言分类的组合对 AI Agent 非常友好。当前最大的障碍是**守护进程稳定性**——该问题导致所有基于 wr 的自动化流程（包括 Hermes Agent 集成）都需要额外的保活和重试逻辑。

核心功能（记录管理、报告生成、LLM 分类）都已可用且通过验证。如果能修复守护进程寿命问题并发布预编译二进制，这个工具将成为一个优秀的生产力助手组件。
