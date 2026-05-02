package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// AllowedClassifyTypes are the valid values for ClassifyResult.Type.
var AllowedClassifyTypes = []string{
	"meeting",
	"task",
	"reminder",
	"log",
	"cancel_or_update",
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
		log.Printf("[llm] classify error: api_base=%s latency=%dms error=%v input_len=%d",
			client.apiBase, time.Since(start).Milliseconds(), err, len(text))
		return nil, &ClassifyError{Op: "call_api", Err: err, Text: text}
	}

	result, err := parseClassifyResponse(content)
	if err != nil {
		log.Printf("[llm] classify parse error: latency=%dms error=%v input_len=%d",
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

	log.Printf("[llm] classify ok: type=%s latency=%dms input_len=%d title=%q",
		result.Type, time.Since(start).Milliseconds(), len(text), result.Title)

	return result, nil
}

// buildSystemPrompt constructs the system prompt for the classifier.
func buildSystemPrompt(today time.Time, location *time.Location) string {
	dateStr := today.In(location).Format("2006-01-02")
	weekday := today.In(location).Format("Monday")
	tzName := location.String()

	return fmt.Sprintf(`你是一个工作记录分类助手。用户会输入一段自然语言文字，你需要将其分类为以下5种类型之一，并提取结构化字段。

## 分类类型

1. **meeting** — 会议、评审、讨论、面谈等需要多人参与的活动
2. **task** — 待办任务、工作事项、需要完成的事情
3. **reminder** — 提醒、备忘、需要注意的事项
4. **log** — 工作日志、已完成的事、记录性的文字
5. **cancel_or_update** — 取消、修改、更新已有记录的操作（包含目标记录ID时使用target_id字段）

## 输出格式

返回纯JSON对象（不要markdown代码块），包含以下字段：
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
	if strings.HasPrefix(trimmed, "{") {
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
