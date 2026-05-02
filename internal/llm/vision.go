package llm

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxImageSize is the maximum allowed image file size (20 MB, OpenAI limit).
const maxImageSize = 20 * 1024 * 1024

// ContentPart is a single part in a multimodal message content array.
type ContentPart interface {
	contentPartMarker()
}

// TextPart represents a text content part.
type TextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (TextPart) contentPartMarker() {}

// ImageURL represents the image_url object inside an ImageURLPart.
type ImageURL struct {
	URL string `json:"url"`
}

// ImageURLPart represents an image URL content part (supports data URLs).
type ImageURLPart struct {
	Type     string    `json:"type"`
	ImageURL ImageURL `json:"image_url"`
}

func (ImageURLPart) contentPartMarker() {}

// mimeTypeForExt maps file extensions to MIME types.
var mimeTypeForExt = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// EncodeImageFile reads an image file, validates its size and format, and
// returns a base64 data URL suitable for the OpenAI vision API.
func EncodeImageFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("image path is empty")
	}

	ext := strings.ToLower(filepath.Ext(path))
	mime, ok := mimeTypeForExt[ext]
	if !ok {
		return "", fmt.Errorf("unsupported image format %q (supported: .png, .jpg, .jpeg, .gif, .webp)", ext)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("cannot access image file: %w", err)
	}

	if info.Size() > maxImageSize {
		return "", fmt.Errorf("image file size %d bytes exceeds maximum %d bytes (20 MB)", info.Size(), maxImageSize)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read image file: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mime, encoded), nil
}

// ClassifyImage sends an image to the vision LLM for classification and
// returns the structured result. The imagePath is read, base64-encoded, and
// sent as a multimodal message. textContext is optional supplementary text
// from the user.
func ClassifyImage(client *Client, imagePath string, textContext string, today time.Time, location *time.Location) (*ClassifyResult, error) {
	// Validate and encode the image.
	dataURL, err := EncodeImageFile(imagePath)
	if err != nil {
		return nil, &ClassifyError{Op: "validate_input", Err: err, Text: imagePath}
	}

	start := time.Now()
	systemPrompt := buildVisionSystemPrompt(today, location)

	// Build the user instruction text.
	instruction := "请分析这张图片，提取其中的会议、任务、提醒或工作日志信息，并按JSON格式返回分类结果。"
	if textContext != "" {
		instruction += "\n\n补充信息: " + textContext
	}

	// Build multimodal content parts.
	contentParts := []ContentPart{
		TextPart{Type: "text", Text: instruction},
		ImageURLPart{
			Type:     "image_url",
			ImageURL: ImageURL{URL: dataURL},
		},
	}

	messages := []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: contentParts},
	}

	content, err := client.CallChat(context.Background(), messages)
	if err != nil {
		log.Printf("[llm] classify_image error: api_base=%s latency=%dms error=%v image=%s",
			client.apiBase, time.Since(start).Milliseconds(), err, imagePath)
		return nil, &ClassifyError{Op: "call_api", Err: err, Text: imagePath}
	}

	result, err := parseClassifyResponse(content)
	if err != nil {
		log.Printf("[llm] classify_image parse error: latency=%dms error=%v image=%s",
			time.Since(start).Milliseconds(), err, imagePath)
		return nil, &ClassifyError{Op: "parse_json", Err: err, Text: imagePath}
	}

	if !isValidType(result.Type) {
		return nil, &ClassifyError{
			Op:   "validate_type",
			Err:  fmt.Errorf("invalid type %q, must be one of %v", result.Type, AllowedClassifyTypes),
			Text: imagePath,
		}
	}

	log.Printf("[llm] classify_image ok: type=%s latency=%dms model=%s image=%s title=%q",
		result.Type, time.Since(start).Milliseconds(), client.model, imagePath, result.Title)

	return result, nil
}

// buildVisionSystemPrompt constructs the system prompt for image classification.
func buildVisionSystemPrompt(today time.Time, location *time.Location) string {
	dateStr := today.In(location).Format("2006-01-02")
	weekday := today.In(location).Format("Monday")
	tzName := location.String()

	return fmt.Sprintf(`你是一个工作记录分类助手。用户会提供一张图片（截图、照片、日历截图等），你需要分析图片中的内容，识别会议、任务、提醒或工作日志信息，并分类为以下5种类型之一，提取结构化字段。

## 分类类型

1. **meeting** — 会议、评审、讨论、面谈等需要多人参与的活动
2. **task** — 待办任务、工作事项、需要完成的事情
3. **reminder** — 提醒、备忘、需要注意的事项
4. **log** — 工作日志、已完成的事、记录性的内容
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

## 分析指引
- 仔细观察图片中的文字、日期、时间、人物、地点等信息
- 从日历截图、邮件截图、聊天记录截图中提取会议或任务信息
- 如果图片中包含多个条目，只提取最显著/最主要的那个
- 如果无法识别图片内容，返回 type=log, title="图片记录", description="来自图片的工作记录"

## 注意事项
- 只返回JSON，不要附加解释文字
- 如果无法确定某个字段，请省略该字段
- 标题要简洁明了，不超过20个字`, dateStr, weekday, tzName)
}
