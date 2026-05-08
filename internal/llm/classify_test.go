package llm

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClassify_ChineseMeeting(t *testing.T) {
	result := mockClassify(t, "明天下午3点项目评审会", mockResponse{
		Type:  "meeting",
		Title: "项目评审会",
		Date:  "2026-05-03",
		Time:  "15:00",
	})
	if result.Type != "meeting" {
		t.Fatalf("expected type meeting, got %q", result.Type)
	}
	if result.Title != "项目评审会" {
		t.Fatalf("expected title 项目评审会, got %q", result.Title)
	}
	if result.Date != "2026-05-03" {
		t.Fatalf("expected date 2026-05-03, got %q", result.Date)
	}
	if result.Time != "15:00" {
		t.Fatalf("expected time 15:00, got %q", result.Time)
	}
}

func TestClassify_Task(t *testing.T) {
	result := mockClassify(t, "完成季度报告", mockResponse{
		Type:     "task",
		Title:    "完成季度报告",
		Priority: "high",
	})
	if result.Type != "task" {
		t.Fatalf("expected type task, got %q", result.Type)
	}
	if result.Priority != "high" {
		t.Fatalf("expected priority high, got %q", result.Priority)
	}
}

func TestClassify_Reminder(t *testing.T) {
	result := mockClassify(t, "提醒我下午给张总打电话", mockResponse{
		Type:          "reminder",
		Title:         "给张总打电话",
		RelatedPerson: "张总",
	})
	if result.Type != "reminder" {
		t.Fatalf("expected type reminder, got %q", result.Type)
	}
	if result.RelatedPerson != "张总" {
		t.Fatalf("expected related_person 张总, got %q", result.RelatedPerson)
	}
}

func TestClassify_Log(t *testing.T) {
	result := mockClassify(t, "今天完成了需求文档的编写", mockResponse{
		Type:        "log",
		Title:       "完成需求文档编写",
		Description: "今天完成了需求文档的编写",
	})
	if result.Type != "log" {
		t.Fatalf("expected type log, got %q", result.Type)
	}
}

func TestClassify_CancelOrUpdate(t *testing.T) {
	result := mockClassify(t, "取消明天下午的会议 abc12345", mockResponse{
		Type:     "cancel_or_update",
		Title:    "取消明天下午的会议",
		TargetID: "abc12345",
	})
	if result.Type != "cancel_or_update" {
		t.Fatalf("expected type cancel_or_update, got %q", result.Type)
	}
	if result.TargetID != "abc12345" {
		t.Fatalf("expected target_id abc12345, got %q", result.TargetID)
	}
}

func TestClassify_MarkdownWrappedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]interface{}{
			"type":  "meeting",
			"title": "周会",
			"date":  "2026-05-05",
			"time":  "10:00",
		}
		b, _ := json.Marshal(payload)
		// Wrap in markdown code fence
		content := "```json\n" + string(b) + "\n```"
		resp := chatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{{Message: struct {
				Content string `json:"content"`
			}{Content: content}}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "test-model", 30*time.Second)
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	result, err := Classify(client, "下周一上午10点周会", today, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != "meeting" {
		t.Fatalf("expected type meeting, got %q", result.Type)
	}
}

func TestClassify_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{{Message: struct {
				Content string `json:"content"`
			}{Content: "this is not json at all"}}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "test-model", 30*time.Second)
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := Classify(client, "some text", today, loc)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "parse_json" {
		t.Fatalf("expected op parse_json, got %q", ce.Op)
	}
}

func TestClassify_Timeout(t *testing.T) {
	// Use a closed server to get immediate connection-refused error.
	// This tests the error-wrapping path without a long-running handler.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow but not infinite
		time.Sleep(10 * time.Second)
	}))
	// Close immediately so connections fail
	server.Close()

	client := NewClient(server.URL, "test-key", "test-model", 30*time.Second)

	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := Classify(client, "some text", today, loc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "call_api" {
		t.Fatalf("expected op call_api, got %q", ce.Op)
	}
}

func TestClassify_Non200Status(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"auth failure", 401},
		{"rate limit", 429},
		{"server error", 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(`{"error": "test error"}`))
			}))
			defer server.Close()

			client := NewClient(server.URL, "test-key", "test-model", 30*time.Second)
			today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
			loc := time.FixedZone("CST", 8*3600)

			_, err := Classify(client, "some text", today, loc)
			if err == nil {
				t.Fatal("expected error for non-200 status, got nil")
			}
			var ce *ClassifyError
			if !errors.As(err, &ce) {
				t.Fatalf("expected ClassifyError, got %T: %v", err, err)
			}
			if ce.Op != "call_api" {
				t.Fatalf("expected op call_api, got %q", ce.Op)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected APIError in chain, got %T: %v", err, err)
			}
			if apiErr.StatusCode != tt.statusCode {
				t.Fatalf("expected status %d, got %d", tt.statusCode, apiErr.StatusCode)
			}
		})
	}
}

func TestClassify_EmptyText(t *testing.T) {
	client := NewClient("http://localhost", "key", "model", 30*time.Second)
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := Classify(client, "", today, loc)
	if err == nil {
		t.Fatal("expected error for empty text, got nil")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "validate_input" {
		t.Fatalf("expected op validate_input, got %q", ce.Op)
	}
}

func TestClassify_EmptyWhitespace(t *testing.T) {
	client := NewClient("http://localhost", "key", "model", 30*time.Second)
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := Classify(client, "   \t\n  ", today, loc)
	if err == nil {
		t.Fatal("expected error for whitespace-only text, got nil")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "validate_input" {
		t.Fatalf("expected op validate_input, got %q", ce.Op)
	}
}

func TestClassify_UnknownType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]interface{}{
			"type":  "unknown_type",
			"title": "测试",
		}
		b, _ := json.Marshal(payload)
		resp := chatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{{Message: struct {
				Content string `json:"content"`
			}{Content: string(b)}}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "test-model", 30*time.Second)
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := Classify(client, "一些文字", today, loc)
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "validate_type" {
		t.Fatalf("expected op validate_type, got %q", ce.Op)
	}
}

func TestClassify_LongText(t *testing.T) {
	// Generate a long text input (>4000 chars)
	longText := ""
	for i := 0; i < 5000; i++ {
		longText += "这是一段很长的文字用于测试边界情况"
	}

	result := mockClassify(t, longText, mockResponse{
		Type:  "log",
		Title: "长文本记录",
	})
	if result.Type != "log" {
		t.Fatalf("expected type log, got %q", result.Type)
	}
}

func TestExtractJSON_RawJSON(t *testing.T) {
	input := `{"type":"meeting","title":"test"}`
	got := extractJSON(input)
	if got != input {
		t.Fatalf("expected raw JSON, got %q", got)
	}
}

func TestExtractJSON_CodeFence(t *testing.T) {
	input := "```json\n{\"type\":\"task\",\"title\":\"test\"}\n```"
	got := extractJSON(input)
	expected := `{"type":"task","title":"test"}`
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestExtractJSON_CodeFenceNoLang(t *testing.T) {
	input := "```\n{\"type\":\"reminder\",\"title\":\"test\"}\n```"
	got := extractJSON(input)
	expected := `{"type":"reminder","title":"test"}`
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestExtractJSON_RawArray(t *testing.T) {
	input := `[{"type":"meeting","title":"a"},{"type":"task","title":"b"}]`
	got := extractJSON(input)
	if got != input {
		t.Fatalf("expected raw JSON array, got %q", got)
	}
}

func TestExtractJSON_CodeFenceArray(t *testing.T) {
	input := "```json\n[{\"type\":\"meeting\"},{\"type\":\"task\"}]\n```"
	got := extractJSON(input)
	expected := `[{"type":"meeting"},{"type":"task"}]`
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	today := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	prompt := buildSystemPrompt(today, loc)

	// Should contain today's date
	if !contains(prompt, "2026-05-02") {
		t.Error("prompt should contain today's date")
	}
	// Should contain timezone
	if !contains(prompt, "CST") {
		t.Error("prompt should contain timezone name")
	}
	// Should mention all 5 types
	for _, typ := range AllowedClassifyTypes {
		if !contains(prompt, typ) {
			t.Errorf("prompt should mention type %q", typ)
		}
	}
}

func TestBuildSystemPrompt_MeetingKeywords(t *testing.T) {
	today := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	prompt := buildSystemPrompt(today, loc)

	// Verify the meeting priority section heading exists.
	if !contains(prompt, "会议关键词优先级") {
		t.Error("prompt should contain '会议关键词优先级' section heading")
	}

	// Verify key meeting-triggering keywords are present.
	keywords := []string{"开会", "会议", "评审", "Review", "讨论", "面谈", "周会", "站会", "1v1", "一对一"}
	for _, kw := range keywords {
		if !contains(prompt, kw) {
			t.Errorf("prompt should contain meeting keyword %q", kw)
		}
	}

	// Verify the priority rule statement.
	if !contains(prompt, "必须优先") {
		t.Error("prompt should state meeting keywords have priority ('必须优先')")
	}
	if !contains(prompt, "meeting") {
		t.Error("prompt should reference meeting type in priority section")
	}
}

func TestBuildSystemPrompt_MultiEventInstructions(t *testing.T) {
	today := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	prompt := buildSystemPrompt(today, loc)

	// Verify the multi-event section heading exists.
	if !contains(prompt, "多事件检测") {
		t.Error("prompt should contain '多事件检测' section heading")
	}

	// Verify instructions for returning JSON array.
	if !contains(prompt, "JSON 数组") {
		t.Error("prompt should instruct returning JSON array for multiple events")
	}
	if !contains(prompt, "[{...},{...}]") {
		t.Error("prompt should show array format [{...},{...}]")
	}

	// Verify criteria for detecting multiple events.
	if !contains(prompt, "时间分隔") {
		t.Error("prompt should mention time-based separation as multi-event signal")
	}
	if !contains(prompt, "不同动作类型") {
		t.Error("prompt should mention different action types as multi-event signal")
	}

	// Verify the output format section describes both shapes.
	if !contains(prompt, "单个事件时返回JSON对象") {
		t.Error("prompt should state single event returns object")
	}
	if !contains(prompt, "多个事件时返回JSON数组") {
		t.Error("prompt should state multiple events return array")
	}
}

func TestClassifyBatch_SingleResult(t *testing.T) {
	want := mockResponse{Type: "meeting", Title: "项目评审会", Date: "2026-05-03", Time: "15:00"}
	results := mockClassifyBatch(t, "明天下午3点项目评审会", []mockResponse{want})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Type != "meeting" {
		t.Fatalf("expected type meeting, got %q", results[0].Type)
	}
	if results[0].Title != "项目评审会" {
		t.Fatalf("expected title 项目评审会, got %q", results[0].Title)
	}
}

func TestClassifyBatch_MultipleResults(t *testing.T) {
	items := []mockResponse{
		{Type: "log", Title: "完成需求文档", Date: "2026-05-02"},
		{Type: "meeting", Title: "项目评审会", Date: "2026-05-02", Time: "14:00"},
	}
	results := mockClassifyBatch(t, "上午完成了需求文档，下午开了项目评审会", items)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Type != "log" {
		t.Fatalf("result[0]: expected type log, got %q", results[0].Type)
	}
	if results[1].Type != "meeting" {
		t.Fatalf("result[1]: expected type meeting, got %q", results[1].Type)
	}
}

func TestClassifyBatch_EmptyArray(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal([]interface{}{})
		resp := chatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{{Message: struct {
				Content string `json:"content"`
			}{Content: string(b)}}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "test-model", 30*time.Second)
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	results, err := ClassifyBatch(client, "今天什么都没做", today, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for empty array, got %d", len(results))
	}
}

func TestClassifyBatch_InvalidType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal([]map[string]string{
			{"type": "meeting", "title": "周会"},
			{"type": "invalid_type", "title": "测试"},
		})
		resp := chatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{{Message: struct {
				Content string `json:"content"`
			}{Content: string(b)}}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "test-model", 30*time.Second)
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := ClassifyBatch(client, "some text", today, loc)
	if err == nil {
		t.Fatal("expected error for invalid type in batch, got nil")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "validate_type" {
		t.Fatalf("expected op validate_type, got %q", ce.Op)
	}
}

func TestClassifyBatch_EmptyText(t *testing.T) {
	client := NewClient("http://localhost", "key", "model", 30*time.Second)
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := ClassifyBatch(client, "", today, loc)
	if err == nil {
		t.Fatal("expected error for empty text, got nil")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "validate_input" {
		t.Fatalf("expected op validate_input, got %q", ce.Op)
	}
}

func TestParseClassifyBatchResponse_SingleObject(t *testing.T) {
	content := `{"type":"meeting","title":"周会","date":"2026-05-05","time":"10:00"}`
	results, err := parseClassifyBatchResponse(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Type != "meeting" {
		t.Fatalf("expected type meeting, got %q", results[0].Type)
	}
	if results[0].Title != "周会" {
		t.Fatalf("expected title 周会, got %q", results[0].Title)
	}
}

func TestParseClassifyBatchResponse_Array(t *testing.T) {
	content := `[{"type":"log","title":"完成文档"},{"type":"meeting","title":"评审会","time":"14:00"}]`
	results, err := parseClassifyBatchResponse(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Type != "log" {
		t.Fatalf("result[0]: expected type log, got %q", results[0].Type)
	}
	if results[1].Type != "meeting" {
		t.Fatalf("result[1]: expected type meeting, got %q", results[1].Type)
	}
	if results[1].Time != "14:00" {
		t.Fatalf("result[1]: expected time 14:00, got %q", results[1].Time)
	}
}

func TestParseClassifyBatchResponse_EmptyArray(t *testing.T) {
	results, err := parseClassifyBatchResponse(`[]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for empty array, got %d", len(results))
	}
}

func TestParseClassifyBatchResponse_CodeFencedArray(t *testing.T) {
	content := "```json\n[{\"type\":\"task\",\"title\":\"A\"},{\"type\":\"reminder\",\"title\":\"B\"}]\n```"
	results, err := parseClassifyBatchResponse(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Type != "task" {
		t.Fatalf("result[0]: expected type task, got %q", results[0].Type)
	}
	if results[1].Type != "reminder" {
		t.Fatalf("result[1]: expected type reminder, got %q", results[1].Type)
	}
}

func TestParseClassifyBatchResponse_InvalidJSON(t *testing.T) {
	_, err := parseClassifyBatchResponse("not json at all")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// --- Test helpers ---

type mockResponse struct {
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

// mockClassify creates a mock server that returns the given response and runs
// Classify against it.
func mockClassify(t *testing.T, input string, want mockResponse) *ClassifyResult {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal(want)
		resp := chatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{{Message: struct {
				Content string `json:"content"`
			}{Content: string(b)}}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "test-model", 30*time.Second)
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	result, err := Classify(client, input, today, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return result
}

// mockClassifyBatch creates a mock server that returns the given responses as a
// JSON array and runs ClassifyBatch against it.
func mockClassifyBatch(t *testing.T, input string, wants []mockResponse) []ClassifyResult {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If single item, return as object to test fallback path.
		var content string
		if len(wants) == 1 {
			b, _ := json.Marshal(wants[0])
			content = string(b)
		} else {
			b, _ := json.Marshal(wants)
			content = string(b)
		}
		resp := chatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{{Message: struct {
				Content string `json:"content"`
			}{Content: content}}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "test-model", 30*time.Second)
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	results, err := ClassifyBatch(client, input, today, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return results
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && len(sub) > 0 && stringContains(s, sub)))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
