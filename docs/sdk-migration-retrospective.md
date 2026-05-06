# SDK 迁移重构回顾：通用性问题与改进建议

> 基于 wr (work-report CLI) 项目从自建 internal 包迁移到 agent-cli-sdk 的实践经验，提炼出可反哺 SDK 和设计规范的通用性问题。

## 一、迁移概述

**项目：** wr — 面向 AI Agent 的工作报告 CLI 工具（Go 实现，CLI + Daemon 架构）

**迁移范围：**
- 删除 `internal/jsonl`、`internal/exitcode`、`internal/sandbox` 三个自建包
- 引入 SDK 的 App、Writer、Envelope、ErrorCodeRegistry、Sandbox、ConfigManager、AgentCommands 等核心抽象
- 新增 `cmd/app.go`（App 初始化）、`cmd/errors.go`（错误码注册）、`cmd/config_provider.go`（ConfigProvider 适配器）、`cmd/doctor.go`（健康检查注册）
- 重构 `cmd/agent_daemon.go`（daemon 命令纳入 agent tree）、`internal/client/client.go`（SDK Writer + ExitError）
- 重写所有 daemon handler 使用 SDK Writer 输出

**变更规模：** ~6500 行代码变更，4 个 internal 包删除，3 个里程碑（M003 协议地基 → M005 SDK 集成 → M006 Agent 可用性修复）

**迁移结果：** 核心抽象（Envelope、Writer、ExitCode）体验良好，属于真正的 drop-in replacement。但在"CLI+Daemon 架构"场景下暴露了多个 SDK 覆盖不足的问题。

---

## 二、通用性问题与改进建议

### 问题 1：ErrorCodeRegistry 与 client 包的循环依赖——SDK 未提供远程映射机制

**严重程度：** 高（架构级）

**现象：**
`internal/client` 包需要将 daemon HTTP 返回的 `error_code` 字符串映射为进程退出码。ErrorCodeRegistry 注册在 `cmd/` 包的 `registerErrorCodes()` 里，通过 `app.RegisterErrorCode()` 绑定到 App 单例。但 client 包不能 import cmd（循环依赖），也无法访问 App 实例。

**最终方案：** 在 `client` 包中手写一份 `errorToExitCode()` switch-case 函数，镜像 `cmd/errors.go` 的映射关系。两处必须手动保持同步。

```go
// client/client.go — 手动镜像的映射
func errorToExitCode(code string) int {
    switch code {
    case "invalid_type", "invalid_body", "invalid_field":
        return agentsdk.ExitInvalidParams
    case "daemon_not_running", "llm_error":
        return agentsdk.ExitNetworkError
    // ... 必须与 cmd/errors.go 保持同步
    default:
        return agentsdk.ExitFatalError
    }
}
```

**根因：** SDK 的 ErrorCodeRegistry 是 App-bound 的单例——注册和查找都通过 `app.RegisterErrorCode()` / `app.ErrorCodeToExitCode()` 进行。SDK 没有考虑"CLI client 进程需要根据远程 daemon 的 error_code 做退出码映射"这一场景。在所有 CLI+Daemon 架构的工具中都会出现。

**改进建议：**

1. **SDK 导出独立 Registry：** 允许 `ErrorCodeRegistry` 作为独立实体创建和传递，不必绑定 App 实例
   ```go
   // 建议新增
   func NewErrorCodeRegistry() *ErrorCodeRegistry  // 已有，但无法从 App 外部获取
   func (r *ErrorCodeRegistry) ToExitCode(code string) int  // 已有
   
   // 建议新增：App 导出 Registry 引用
   func (a *App) Registry() *ErrorCodeRegistry
   ```
2. **在 cmd 初始化时注入 Registry 到 client：** 让设计规范推荐 `client.New(registry)` 模式而非让 client 自己猜测映射
3. **设计规范补充：** 明确 CLI+Daemon 架构下 error_code 的双向映射问题——daemon 侧写入 error_code，CLI client 侧映射为退出码

---

### 问题 2：Sandbox 路径在 App 创建时锁死——测试隔离困难

**严重程度：** 中（影响开发体验）

**现象：**
SDK 的 `NewSandbox()` 在 `App.New()` 时一次性计算 `baseDir`（读环境变量或默认 `~/.app-name/`）。测试中需要创建临时 App 实例指向 `t.TempDir()`，但旧的 Sandbox 路径已被全局 `app` 变量持有。

必须做 `resetAppForTest` pattern：
1. 通过 `rootCmd.Find()` 找到并移除旧的 agent command tree
2. 创建新 App 实例
3. 重新注册 ErrorCodeRegistry、ConfigProvider、HealthCheck、CommandMeta
4. 重新添加 AgentCommands 和自定义 daemon 子命令

```go
func setupAgentTest(t *testing.T) (tmpHome string, cleanup func()) {
    tmpHome, homeCleanup := setupTempHome(t)
    appCleanup := resetAppForTest(t, tmpHome)  // ← 复杂的重置逻辑
    
    // 必须重新注册所有内容
    registerErrorCodes()
    registerConfigProvider()
    registerCommandMeta()
    registerAgentDaemonCommands()
    
    return tmpHome, func() { appCleanup(); homeCleanup() }
}
```

**根因：** Sandbox 的路径在构造函数中一次性绑定到 App，没有 `SetBaseDir()` 或 `Reset()` 方法。AgentCommands 在创建时就持有了 Sandbox、Writer 等的引用。

**改进建议：**

1. **App 提供测试辅助方法：**
   ```go
   // 建议新增
   func (a *App) ResetForTest(t *testing.T, tmpHome string)
   ```
   重新初始化 Sandbox、Writer，重新生成 AgentCommands
2. **Sandbox 支持延迟解析：** baseDir 在第一次调用 `Ensure()` 或 `Dirs()` 时才计算，而非构造时
3. **设计规范增加"测试模式"章节：** 说明 SDK 用户应如何隔离测试——这是每个采纳 SDK 的项目都会遇到的问题

---

### 问题 3：ConfigProvider 适配器的错误消息耦合

**严重程度：** 中（隐式契约）

**现象：**
SDK 的 `agent config set` 命令通过字符串匹配判断错误类型：

```go
// SDK agent.go 中的隐式契约
if strings.Contains(errMsg, "not configurable") ||
   strings.Contains(errMsg, "not in whitelist") ||
   strings.Contains(errMsg, "unknown field") {
    // → INPUT_INVALID (exit 2)
} else {
    // → INTERNAL_ERROR (exit 1)
}
```

这意味着 wr 的 ConfigProvider 适配器必须刻意对齐这些错误消息，否则白名单违规会被错误地分类为 `INTERNAL_ERROR`：

```go
// wr cmd/config_provider.go — 必须对齐 SDK 的 magic strings
func (p *wrConfigProvider) Set(jsonPath, value string) error {
    if err := cfg.SetByPath(jsonPath, value); err != nil {
        errMsg := err.Error()
        // wr 原始错误是 "unknown path"，必须重映射为 SDK 期望的 "unknown field"
        if strings.Contains(errMsg, "unknown path") {
            return fmt.Errorf("config: unknown field %q (not in whitelist)", jsonPath)
        }
        return err
    }
    // ...
}
```

**根因：** SDK 用字符串匹配做错误分类，而不是结构化错误类型。这是一种脆弱的隐式契约。

**改进建议：**

1. **定义结构化错误类型：**
   ```go
   // 建议新增
   type WhitelistError struct { Path string }
   func (e *WhitelistError) Error() string { ... }
   
   type ValidationError struct { Field, Reason string }
   func (e *ValidationError) Error() string { ... }
   ```
   SDK 用 `errors.As()` 判断，而非 `strings.Contains()`
2. **导出错误消息常量：** 如果保留字符串匹配，至少导出常量让用户可以引用
   ```go
   const ErrWhitelistPattern = "not in whitelist"
   const ErrUnknownFieldPattern = "unknown field"
   ```
3. **ConfigProvider.Set() 返回分类结果：** 让接口设计避免靠 error 消息做判断

---

### 问题 4：Envelope 构造函数的 `tool` 参数是迁移遗漏点

**严重程度：** 低（但容易遗漏）

**现象：**
原来 `internal/jsonl.NewErrorEnvelope(code, msg)` 只需 2 个参数。SDK 的 `agentsdk.NewErrorEnvelope(tool, code, msg)` 需要 3 个。每个调用点都必须加 `tool` 参数，遗漏一个就会在运行时产出 `tool: ""` 的畸形信封。

wr 项目中大约有 30+ 个调用点需要逐一修改，且编译器不会报错（空字符串是合法的 string）。

**根因：** 构造函数签名不兼容，且没有编译期或运行时校验 tool 非空。

**改进建议：**

1. **App 提供便捷构造器：** 自动填充 tool name
   ```go
   // 建议新增
   func (a *App) NewErrorEnvelope(code, msg string) Envelope {
       return NewErrorEnvelope(a.name, code, msg)
   }
   func (a *App) NewResultEnvelope(data interface{}) Envelope {
       return NewResultEnvelope(a.name, data)
   }
   ```
2. **ValidateEnvelope 校验 tool 非空：** 让问题在第一次输出时就暴露
   ```go
   func ValidateEnvelope(env Envelope) error {
       if env.Tool == "" {
           return fmt.Errorf("envelope tool must not be empty")
       }
       // ...
   }
   ```
3. **设计规范建议：** 迁移指南中明确列出"构造函数签名变化"清单

---

### 问题 5：Daemon HTTP Handler 的 Writer 桥接缺乏 SDK 原生支持

**严重程度：** 中（高频需求）

**现象：**
wr 的 daemon 是一个 HTTP server，所有 handler 都需要把 `http.ResponseWriter` 转为 SDK Writer。当前每次都手写 `daemonWriter(w)` helper：

```go
// wr internal/daemon/handler.go — 每个 handler 都需要这个桥接
func daemonWriter(w http.ResponseWriter) *agentsdk.Writer {
    w.Header().Set("Content-Type", "application/jsonl")
    w.WriteHeader(http.StatusOK)
    return agentsdk.NewWriter(w, "wr")
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
    daemonWriter(w).Success(map[string]interface{}{"status": "ok"})
}

func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
    daemonWriter(w).ErrorWithCode("invalid_type", "...")
}
```

这不是 wr 独有的需求——所有 CLI+Daemon 架构的工具都需要这个桥接。

**根因：** SDK 只有 `Writer` 概念（写 JSONL 到 `io.Writer`），没有 `HTTPWriter` 概念。HTTP 响应头设置留给用户。

**改进建议：**

1. **SDK 提供 HTTP Writer 工厂：**
   ```go
   // 建议新增
   func NewHTTPWriter(w http.ResponseWriter, tool string) *Writer {
       w.Header().Set("Content-Type", "application/jsonl")
       w.WriteHeader(http.StatusOK)
       return NewWriter(w, tool)
   }
   ```
2. **或提供 Daemon Handler 适配器：**
   ```go
   type DaemonHandlerFunc func(w *Writer, r *http.Request)
   func HTTPAdapter(tool string, fn DaemonHandlerFunc) http.HandlerFunc
   ```
3. **设计规范增加 "Daemon 模式" 章节：** 说明 SDK 在 HTTP daemon 场景下的推荐用法，包括 Content-Type、HTTP 状态码、Writer 桥接

---

### 问题 6：App.AgentCommands() 注册时机与自定义子命令的时序约束未文档化

**严重程度：** 低（但有坑）

**现象：**
wr 需要在 `agent` command tree 下注册自己的 `daemon` 子命令。实现方式：

```go
func registerAgentDaemonCommands() {
    // 必须在 rootCmd.AddCommand(app.AgentCommands()) 之后调用
    agentCmd, _, err := rootCmd.Find([]string{"agent"})
    if err != nil || agentCmd == nil {
        return  // agent tree 还没注册，静默跳过
    }
    
    // Cobra 的 Find() 在找不到子命令时返回 parent 而非 nil！
    if existing, _, _ := agentCmd.Find([]string{"daemon"}); existing != nil && existing != agentCmd {
        return  // 已注册，跳过
    }
    
    agentCmd.AddCommand(daemonGroupCmd)
}
```

踩了 Cobra 的坑：`Find()` 在子命令不存在时返回 parent command 而非 nil。必须用 `existing != agentCmd` 而非 `existing != nil` 做判断。

**根因：** SDK 没有提供"在 agent tree 下注册自定义子命令"的官方钩子。

**改进建议：**

1. **App 提供子命令注册钩子：**
   ```go
   // 建议新增
   func (a *App) RegisterAgentSubcommand(cmd *cobra.Command)
   ```
   在 `AgentCommands()` 返回前执行用户的自定义注册
2. **或让 AgentCommands 接受可选参数：**
   ```go
   func (a *App) AgentCommands(extra ...*cobra.Command) *cobra.Command
   ```
3. **设计规范明确：** "如果工具需要在 agent tree 下扩展自定义命令，应通过 `RegisterAgentSubcommand()` 注册"

---

### 问题 7：Windows 跨平台兼容性未被 SDK 覆盖

**严重程度：** 中（Windows 用户必现）

**现象：**
迁移过程中踩了多个 Windows 坑：

| 问题 | 影响 | 修复方式 |
|------|------|---------|
| `t.TempDir()` 返回反斜杠路径 | 嵌入 JSON 时变成非法 JSON | 所有测试用 `json.Marshal` 替代 `fmt.Sprintf` |
| `IsPortInUse` 只检查 `127.0.0.1` | Windows dual-stack 下端口检测结果不准 | 同时检查 `127.0.0.1` 和 `[::1]` |
| `os.Rename` 不能跨盘符 | `ConfigManager.Save()` 的原子写入在跨卷时失败 | 不使用 SDK ConfigManager，保留自有 Save 逻辑 |

**根因：** SDK 的 `ConfigManager.Save()` 用 `os.Rename(tmpFile, cm.filePath)` 做原子写入，在 Windows 上如果 temp 和 target 不在同一个卷上会失败。SDK 测试和示例都基于 Unix 假设。

**改进建议：**

1. **ConfigManager.Save 跨平台适配：**
   ```go
   // 改进：先尝试 os.Rename，失败则直接 WriteFile
   if err := os.Rename(tmpFile, cm.filePath); err != nil {
       // Fallback for Windows cross-volume rename
       if err := os.WriteFile(cm.filePath, data, 0644); err != nil {
           return fmt.Errorf("config: write: %w", err)
       }
       os.Remove(tmpFile)
   }
   ```
2. **增加 Windows CI：** 至少 `GOOS=windows go build` 和 `GOOS=windows go test`（不运行，仅编译检查）
3. **设计规范增加"跨平台注意事项"段落**

---

### 问题 8：ConfigManager[T] 泛型的"全有或全无"适配困境

**严重程度：** 低（但有文档价值）

**现象：**
wr 原有的 `internal/config` 包有完善的 Config 系统：自定义 struct、`Load()`/`Save()`/`Validate()`/`SetByPath()`/`Redacted()`。迁移时有两个选择：

- **A) 用 `ConfigManager[Config]` 替换整个 config 包** → 不可行，wr 的 config 有嵌套 struct、自定义 validation、特定文件路径约定
- **B) 写一个适配器实现 `ConfigProvider` 接口** → 可行但需手动对齐错误消息（问题 3）

选了 B，因为 `ConfigManager[T]` 的反射逻辑不能覆盖 wr 的自定义需求。

**根因：** `ConfigManager[T]` 同时管文件 I/O、反射序列化、白名单、脱敏。如果用户已有完善的 config 系统，只能写适配器。

**改进建议：**

1. **文档明确说明两级选择：**
   - **简单项目：** 直接使用 `ConfigManager[MyConfig]`，开箱即用
   - **已有 config 系统：** 只需实现 `ConfigProvider` 接口（`ListRedacted()`, `Set()`, `Whitelist()`）
2. **提供 ConfigProvider 适配器示例代码**
3. **ConfigProvider 接口设计已经足够灵活**（这是 SDK 做得好的地方），但需要在文档中更突出"接口可独立实现"这个选项

---

## 三、SDK 做得好的地方（值得保持）

在指出问题的同时，也要记录 SDK 设计中迁移体验优秀的部分：

| 抽象 | 评价 |
|------|------|
| **Envelope** | 字段布局与旧 `internal/jsonl.Envelope` 完全一致，真正的 drop-in replacement |
| **Writer** | `io.Writer` 抽象干净，quiet 模式、trace ID 注入自动处理 |
| **ErrorCodeRegistry** | 内置码不可覆盖、自定义码注册简洁、`ToExitCode()` fallback 合理 |
| **AgentCommands** | schema/errors/doctor/debug/cache 五个 meta 命令开箱即用，覆盖了 agent 需要的 90% 自省能力 |
| **ExitError + Execute()** | panic recovery + signal handler + JSONL FATAL_CRASH 一体化，消除了大量样板代码 |
| **ConfigProvider 接口** | 接口设计灵活，允许完全自定义实现 |

---

## 四、总结：对 SDK 和设计规范的核心建议

### 按优先级排序

| 优先级 | 问题 | 建议改进 | 影响范围 |
|--------|------|---------|---------|
| P0 | ErrorCodeRegistry App-bound | 导出 Registry 引用，支持注入到 client 包 | 所有 CLI+Daemon 架构 |
| P0 | ConfigProvider 错误分类靠字符串匹配 | 定义结构化错误类型 | 所有使用 ConfigProvider 的项目 |
| P1 | Daemon HTTP Writer 桥接无 SDK 支持 | 提供 `NewHTTPWriter` | 所有 CLI+Daemon 架构 |
| P1 | Windows 兼容性盲区 | ConfigManager.Save 跨平台适配 + CI | Windows 用户 |
| P2 | Sandbox/App 测试隔离 | 提供 `ResetForTest()` | 所有项目的测试代码 |
| P2 | Agent tree 扩展钩子 | 提供 `RegisterAgentSubcommand()` | 需要扩展 agent tree 的项目 |
| P3 | tool 参数遗漏无运行时校验 | ValidateEnvelope 校验 tool 非空 | 迁移初期的体验 |
| P3 | ConfigManager 适配困境 | 文档明确两级选择 | 已有 config 系统的项目 |

### 一句话总结

SDK 的核心抽象（Envelope、Writer、ExitCode）迁移体验优秀，是真正的 drop-in replacement。但在 **"CLI+Daemon 架构"** 这个高频场景下，SDK 缺少了 daemon 侧的桥接支持（HTTP Writer、远程 error_code 映射）和自定义扩展的官方钩子（agent tree 子命令注册）。这些问题不是 wr 独有的——任何采纳 SDK 的非平凡 CLI 工具都会遇到。
