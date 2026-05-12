package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"wr/internal/logger"
)

// AllowedClassifyTypes are the valid values for ClassifyResult.Type.
var AllowedClassifyTypes = []string{
	"meeting",
	"task",
	"reminder",
	"done_things",
	"cancel_or_update",
	"personal",
	"backlog",
}

// ClassifyResult holds the structured fields extracted by the LLM classifier.
type ClassifyResult struct {
	Type          string `json:"type"`
	Title         string `json:"title"`
	Date          string `json:"date,omitempty"`
	Time          string `json:"time,omitempty"`
	EndTime       string `json:"end_time,omitempty"`
	Description   string `json:"description,omitempty"`
	Location      string `json:"location,omitempty"`
	RelatedPerson string `json:"related_person,omitempty"`
	Priority      string `json:"priority,omitempty"`
	RemindBefore  string `json:"remind_before,omitempty"`
	Recurring     string `json:"recurring,omitempty"`
	TargetID      string `json:"target_id,omitempty"`
}

// ClassifyError wraps errors from the classification pipeline.
type ClassifyError struct {
	Op   string // operation that failed, e.g. "call_api", "parse_json", "validate_type"
	Err  error
	Text string // original input text (truncated to 100 chars)
}

func (e *ClassifyError) Error() string {
	return fmt.Sprintf("llm classify %s: %v (input: %q)", e.Op, e.Err, truncate(e.Text, 100))
}

func (e *ClassifyError) Unwrap() error {
	return e.Err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Classify sends the input text to the LLM for classification and returns
// the structured result.  The today parameter is used in the system prompt
// for relative date resolution (明天, 下周一, etc.).  The location parameter
// provides the user's timezone for date/time context.
func Classify(client *Client, text string, today time.Time, location *time.Location) (*ClassifyResult, error) {
	if strings.TrimSpace(text) == "" {
		return nil, &ClassifyError{Op: "validate_input", Err: fmt.Errorf("empty text"), Text: text}
	}

	start := time.Now()
	systemPrompt := buildSystemPrompt(today, location)
	messages := []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: text},
	}

	content, err := client.CallChat(context.Background(), messages)
	if err != nil {
		logger.Errorf("classify error: api_base=%s latency=%dms error=%v input_len=%d",
			client.apiBase, time.Since(start).Milliseconds(), err, len(text))
		return nil, &ClassifyError{Op: "call_api", Err: err, Text: text}
	}

	result, err := parseClassifyResponse(content)
	if err != nil {
		logger.Errorf("classify parse error: latency=%dms error=%v input_len=%d",
			time.Since(start).Milliseconds(), err, len(text))
		return nil, &ClassifyError{Op: "parse_json", Err: err, Text: text}
	}

	if !isValidType(result.Type) {
		return nil, &ClassifyError{
			Op:   "validate_type",
			Err:  fmt.Errorf("invalid type %q, must be one of %v", result.Type, AllowedClassifyTypes),
			Text: text,
		}
	}

	logger.Infof("classify ok: type=%s latency=%dms input_len=%d title=%q",
		result.Type, time.Since(start).Milliseconds(), len(text), result.Title)

	return result, nil
}

// ClassifyBatch sends the input text to the LLM and returns zero or more
// ClassifyResult entries.  The system prompt instructs the LLM to return a
// JSON array when the input contains multiple independent events, or a single
// object for one event.  The function handles both response shapes transparently.
func ClassifyBatch(client *Client, text string, today time.Time, location *time.Location) ([]ClassifyResult, error) {
	if strings.TrimSpace(text) == "" {
		return nil, &ClassifyError{Op: "validate_input", Err: fmt.Errorf("empty text"), Text: text}
	}

	start := time.Now()
	systemPrompt := buildSystemPrompt(today, location)
	messages := []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: text},
	}

	content, err := client.CallChat(context.Background(), messages)
	if err != nil {
		logger.Errorf("classify_batch error: api_base=%s latency=%dms error=%v input_len=%d",
			client.apiBase, time.Since(start).Milliseconds(), err, len(text))
		return nil, &ClassifyError{Op: "call_api", Err: err, Text: text}
	}

	results, err := parseClassifyBatchResponse(content)
	if err != nil {
		logger.Errorf("classify_batch parse error: latency=%dms error=%v input_len=%d",
			time.Since(start).Milliseconds(), err, len(text))
		return nil, &ClassifyError{Op: "parse_json", Err: err, Text: text}
	}

	// Validate every result's type.
	for i, r := range results {
		if !isValidType(r.Type) {
			return nil, &ClassifyError{
				Op:   "validate_type",
				Err:  fmt.Errorf("result[%d]: invalid type %q, must be one of %v", i, r.Type, AllowedClassifyTypes),
				Text: text,
			}
		}
	}

	logger.Infof("classify_batch ok: count=%d latency=%dms input_len=%d",
		len(results), time.Since(start).Milliseconds(), len(text))

	return results, nil
}

// parseClassifyBatchResponse extracts ClassifyResult entries from the LLM
// response.  It handles two shapes:
//   - JSON array:  [{...}, {...}, ...]  →  returns all entries
//   - JSON object: {...}               →  returns single-element slice
//   - markdown code-fenced JSON         →  unwrapped first
func parseClassifyBatchResponse(content string) ([]ClassifyResult, error) {
	jsonStr := extractJSON(content)
	trimmed := strings.TrimSpace(jsonStr)

	var results []ClassifyResult

	// Try parsing as JSON array first.
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &results); err != nil {
			return nil, fmt.Errorf("parse JSON array from LLM response: %w\nraw content: %s", err, truncate(content, 200))
		}
		return results, nil
	}

	// Fall back to single object wrapped in a slice.
	var single ClassifyResult
	if err := json.Unmarshal([]byte(trimmed), &single); err != nil {
		return nil, fmt.Errorf("parse JSON from LLM response: %w\nraw content: %s", err, truncate(content, 200))
	}
	return []ClassifyResult{single}, nil
}

// buildSystemPrompt constructs the system prompt for the classifier.
func buildSystemPrompt(today time.Time, location *time.Location) string {
	dateStr := today.In(location).Format("2006-01-02")
	weekday := today.In(location).Format("Monday")
	tzName := location.String()

	return fmt.Sprintf(`你是一个工作记录分类助手。用户会输入一段自然语言文字，你需要将其分类为以下7种类型之一，并提取结构化字段。

## 分类类型

1. **meeting** — 会议、评审、讨论、面谈等需要多人参与的活动
2. **task** — 待办任务、工作事项、需要完成的事情
3. **reminder** — 提醒、备忘、需要注意的事项
4. **done_things** — 工作日志、已完成的事、记录性的文字
5. **cancel_or_update** — 取消、修改、更新已有记录的操作（包含目标记录ID时使用target_id字段）
6. **personal** — 个人事务：吃药、买菜、体检、家务、交水电费、购物、私事等非工作相关的事项
7. **backlog** — 模糊的未来意图、想法、待研究的课题：没有明确日期/时间的待办事项

## Backlog 分类规则

当输入表达了一个**模糊的未来意图或想法**，但**没有明确的日期或时间**时，分类为 **backlog** 而非 task 或 reminder。

典型特征：
- 使用"帮我记一下"、"我有个想法"、"记一下"、"有空看看"、"回头研究一下"等表达
- 没有提到具体的日期、时间、截止日期
- 表达的是一种"以后再说"的意图

**判断规则：**
- 没有日期/时间 → **backlog**
- 有日期/时间 → 按内容归类为 **task** 或 **reminder**

示例：
- "帮我记一下要研究 wasm" → backlog
- "我有个想法，可以做一个内部工具" → backlog
- "有空看看这篇关于微服务的文章" → backlog
- "下周研究一下 wasm" → task（有时间"下周"）

## 个人事务分类

当输入明显是私人生活相关（吃药、买菜、体检、家务、交水电费、购物、看病、接送孩子等）时分类为 **personal**，而非 reminder 或 task。
不要将私人生活事项归类为工作类型。例如"每天提醒我吃药"应分类为 personal 而非 reminder。

## 会议关键词优先级

当输入中出现以下关键词时，**必须优先**分类为 **meeting** 类型，即使上下文可能暗示其他类型：
- 开会、会议、评审、Review、讨论、面谈、沟通、访谈、约谈
- 周会、站会、早会、夕会、例会、复盘会、同步会、对齐会
- 1v1、一对一、面谈、约聊、约了（某人）
- 与（某人）开/讨论/沟通/评审

仅当关键词明显用于否定或取消语境（如"取消会议"、"不用开会了"）时，才考虑 cancel_or_update 或 done_things 类型。

## 多事件检测

当用户输入包含**多个独立事件**时（例如"上午完成了需求文档，下午开了项目评审会"，或"10点周会，2点给客户打电话"），必须为每个事件单独生成一条记录，以 **JSON 数组** 格式返回：[{...},{...}]。

判断多事件的依据：
- 时间分隔：不同时间段的事件（上午/下午/10点/2点）
- 明显分隔符：逗号、分号、句号分隔的独立事件
- 不同动作类型：一个完成了一个任务，另一个是会议或提醒

当输入只描述**单个事件**时，返回单个 JSON 对象 {...}。

## 输出格式

返回纯JSON（不要markdown代码块），包含以下字段：
- 单个事件时返回JSON对象：{...}
- 多个事件时返回JSON数组：[{...},{...}]
- type: 分类类型（必填）
- title: 标题摘要（必填）
- date: 日期，格式 YYYY-MM-DD（如适用）
- time: 开始时间，格式 HH:MM（如适用）
- end_time: 结束时间，格式 HH:MM（如适用）
- description: 详细描述（如适用）
- location: 地点（如适用）
- related_person: 相关人员（如适用）
- priority: 优先级，可选 normal/high/medium（如适用）
- remind_before: 提前提醒时间，如 "15m", "30m", "1h"（如适用）
- recurring: 重复规则，如 "daily", "weekly", "monthly"（如适用）
- target_id: 当类型为cancel_or_update时，目标记录的short_id（如适用）

## 当前时间信息
- 今天: %s（%s）
- 时区: %s

## 相对日期解析规则
- 明天 → 今天+1天
- 后天 → 今天+2天
- 下周一/下周二... → 下一个对应的星期几
- 这周五/这周六... → 本周对应的星期几
- X号/X日 → 当月X日（如已过则下月）

## 注意事项
- 只返回JSON，不要附加解释文字
- 如果无法确定某个字段，请省略该字段
- 标题要简洁明了，不超过20个字`, dateStr, weekday, tzName)
}

// parseClassifyResponse extracts JSON from the LLM response content, handling
// both raw JSON and markdown code-fenced responses (```json ... ```).
func parseClassifyResponse(content string) (*ClassifyResult, error) {
	jsonStr := extractJSON(content)

	var result ClassifyResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("parse JSON from LLM response: %w\nraw content: %s", err, truncate(content, 200))
	}

	return &result, nil
}

// codeFenceRe matches markdown code fences with optional language tag.
var codeFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*\n?(.*?)```")

// extractJSON tries to extract JSON from a possibly markdown-wrapped string.
func extractJSON(content string) string {
	trimmed := strings.TrimSpace(content)

	// Try direct JSON parse first.
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return trimmed
	}

	// Try to extract from code fences.
	matches := codeFenceRe.FindStringSubmatch(trimmed)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}

	// Fallback: return as-is and let json.Unmarshal provide a clear error.
	return trimmed
}

// isValidType checks if the given type string is one of the allowed values.
func isValidType(t string) bool {
	for _, allowed := range AllowedClassifyTypes {
		if t == allowed {
			return true
		}
	}
	return false
}
