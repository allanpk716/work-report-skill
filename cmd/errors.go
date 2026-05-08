package cmd

import (
	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
)

// registerErrorCodes registers all wr-specific error codes with the SDK ErrorCodeRegistry.
// Centralized per D021 — error code → exit code mapping is a global policy decision.
//
// SDK built-in codes (FATAL_CRASH, INTERNAL_ERROR, INPUT_INVALID, NOT_FOUND, RESOURCE_LOCKED)
// are pre-registered and cannot be overridden.
func registerErrorCodes() {
	// Invalid parameters → exit 2
	validateParams := agentsdk.ExitInvalidParams
	_ = app.RegisterErrorCode("invalid_type", validateParams, "无效的记录类型")
	_ = app.RegisterErrorCode("invalid_body", validateParams, "请求体无效或缺失")
	_ = app.RegisterErrorCode("invalid_field", validateParams, "字段验证失败")
	_ = app.RegisterErrorCode("invalid_params", validateParams, "参数缺失或无效")
	_ = app.RegisterErrorCode("method_not_allowed", validateParams, "HTTP 方法不允许")
	_ = app.RegisterErrorCode("import_record", validateParams, "导入记录验证失败")

	// Network / daemon unreachable → exit 4
	// Note: SDK has no ExitDaemonUnreachable (3); daemon_not_running maps to ExitNetworkError (4)
	// which is semantically closest — network error reaching the daemon.
	netError := agentsdk.ExitNetworkError
	_ = app.RegisterErrorCode("daemon_not_running", netError, "daemon 未运行或无法连接")
	_ = app.RegisterErrorCode("llm_error", netError, "LLM 调用失败")
	_ = app.RegisterErrorCode("llm_not_configured", netError, "LLM 未配置（缺少 API key）")

	// Lock conflict → exit 5
	_ = app.RegisterErrorCode("lock_conflict", agentsdk.ExitLockConflict, "并发访问冲突")

	// Fatal / internal errors → exit 1
	fatal := agentsdk.ExitFatalError
	_ = app.RegisterErrorCode("storage_error", fatal, "存储读写错误")
	_ = app.RegisterErrorCode("record_not_found", fatal, "记录未找到")
	_ = app.RegisterErrorCode("already_completed", fatal, "记录已完成")
	_ = app.RegisterErrorCode("already_cancelled", fatal, "记录已取消")
	_ = app.RegisterErrorCode("push_error", fatal, "Pushover 推送失败")
	_ = app.RegisterErrorCode("pushover_not_configured", fatal, "Pushover 未配置")
	_ = app.RegisterErrorCode("marshal_error", fatal, "JSON 序列化失败")
	_ = app.RegisterErrorCode("unknown", fatal, "未知错误")
	_ = app.RegisterErrorCode("daemon_start_timeout", fatal, "daemon 启动超时")

	// Digest errors
	_ = app.RegisterErrorCode("digest_not_found", fatal, "digest 配置未找到")
	_ = app.RegisterErrorCode("invalid_scope", validateParams, "无效的 digest 范围")
	_ = app.RegisterErrorCode("invalid_schedule", validateParams, "无效的 cron 表达式")
	_ = app.RegisterErrorCode("invalid_direction", validateParams, "无效的 digest 方向")
}
