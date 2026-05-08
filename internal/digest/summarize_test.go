package digest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Mock implementations ---

// mockStorage implements StorageProvider for testing.
type mockStorage struct {
	dayFn    func(date string) (markdown string, totalRecords int, err error)
	rangeFn  func(from, to string) (markdown string, totalRecords int, err error)
	dayCalls []string
	rangeCalls [][2]string
	mu       sync.Mutex
}

func (m *mockStorage) GenerateDay(date string) (string, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dayCalls = append(m.dayCalls, date)
	if m.dayFn != nil {
		return m.dayFn(date)
	}
	return "# Daily Report\n\nNo records.", 0, nil
}

func (m *mockStorage) GenerateRange(from, to string) (string, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rangeCalls = append(m.rangeCalls, [2]string{from, to})
	if m.rangeFn != nil {
		return m.rangeFn(from, to)
	}
	return "# Range Report\n\nNo records.", 0, nil
}

// mockLLM implements LLMCaller for testing.
type mockLLM struct {
	fn    func(ctx context.Context, systemPrompt, userContent string) (string, error)
	calls []struct {
		system string
		user   string
	}
	mu sync.Mutex
}

func (m *mockLLM) Call(ctx context.Context, systemPrompt, userContent string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, struct {
		system string
		user   string
	}{system: systemPrompt, user: userContent})
	if m.fn != nil {
		return m.fn(ctx, systemPrompt, userContent)
	}
	return "LLM summary result", nil
}

// mockPushover implements PushoverSender for testing.
type mockPushover struct {
	err error
	fn  func(ctx context.Context, cfg interface{}, message, title string, priority int) error
	calls []struct {
		message string
		title   string
		priority int
	}
	mu sync.Mutex
}

func (m *mockPushover) Send(ctx context.Context, cfg interface{}, message, title string, priority int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, struct {
		message  string
		title    string
		priority int
	}{message: message, title: title, priority: priority})
	if m.err != nil {
		return m.err
	}
	return nil
}

// --- Helper ---

// testInput returns a SummarizeInput with sensible defaults for testing.
// Override individual fields as needed.
func testInput() SummarizeInput {
	return SummarizeInput{
		DigestID:     "test-digest-1",
		Scope:        ScopeToday,
		Direction:    DirectionAgenda,
		Loc:          time.UTC,
		Storage:      &mockStorage{},
		LLM:          &mockLLM{},
		PushPriority: 0,
	}
}

// Note: Summarize() currently hardcodes DefaultPushoverSender() internally.
// To test pushover behavior we need to test at a higher level or modify
// the function. For unit tests, we focus on storage and LLM interactions.
// Pushover integration is tested via the existing pushover package tests.

// --- Tests ---

func TestSummarize_HappyPath_SingleDay(t *testing.T) {
	storage := &mockStorage{
		dayFn: func(date string) (string, int, error) {
			return "# 工作日报 2025-01-15\n\n- Task A [进行中]\n- Task B [已完成]", 2, nil
		},
	}
	llm := &mockLLM{
		fn: func(ctx context.Context, sys, user string) (string, error) {
			if sys != "agenda" {
				t.Errorf("expected system prompt 'agenda', got %q", sys)
			}
			if !strings.Contains(user, "Task A") {
				t.Errorf("LLM user content should contain report data")
			}
			return "📋 今日议程\n1. Task A - 优先处理\n2. Task B - 已完成", nil
		},
	}

	input := testInput()
	input.Storage = storage
	input.LLM = llm

	// We can't easily mock Pushover in the current design since Summarize()
	// calls DefaultPushoverSender() internally. We verify the pipeline up
	// to the push step by checking that storage and LLM were called correctly.
	// Pushover will fail because no real credentials, but that's expected.
	err := Summarize(context.Background(), input)
	// We expect pushover to fail since no real credentials are configured.
	if err == nil {
		t.Log("pushover succeeded (unexpected in test env)")
	}

	// Verify storage was called with today's date (UTC).
	storage.mu.Lock()
	if len(storage.dayCalls) != 1 {
		t.Fatalf("expected 1 day call, got %d", len(storage.dayCalls))
	}
	storage.mu.Unlock()

	// Verify LLM was called.
	llm.mu.Lock()
	if len(llm.calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(llm.calls))
	}
	llm.mu.Unlock()
}

func TestSummarize_HappyPath_Range(t *testing.T) {
	storage := &mockStorage{
		rangeFn: func(from, to string) (string, int, error) {
			if from == "2025-01-13" && to == "2025-01-19" {
				return "# 工作周报 2025-01-13 ~ 2025-01-19\n\n- Task X", 5, nil
			}
			return "", 0, fmt.Errorf("unexpected range %s ~ %s", from, to)
		},
	}
	llm := &mockLLM{
		fn: func(ctx context.Context, sys, user string) (string, error) {
			return "📊 周报总结", nil
		},
	}

	input := testInput()
	input.Scope = ScopeWeek
	input.Loc = time.FixedZone("CST", 8*3600)
	input.Storage = storage
	input.LLM = llm

	_ = Summarize(context.Background(), input)

	storage.mu.Lock()
	if len(storage.rangeCalls) != 1 {
		t.Fatalf("expected 1 range call, got %d", len(storage.rangeCalls))
	}
	storage.mu.Unlock()
}

func TestSummarize_LLMFallback_OnError(t *testing.T) {
	storage := &mockStorage{
		dayFn: func(date string) (string, int, error) {
			return "# Raw Markdown\n\n- Task A", 1, nil
		},
	}
	llmErr := errors.New("LLM API returned 500")
	llm := &mockLLM{
		fn: func(ctx context.Context, sys, user string) (string, error) {
			return "", llmErr
		},
	}

	input := testInput()
	input.Storage = storage
	input.LLM = llm

	// Pipeline should not return error from LLM — it falls back.
	err := Summarize(context.Background(), input)
	// Pushover will still fail (no creds), but LLM error should be swallowed.
	if err != nil && !strings.Contains(err.Error(), "pushover") {
		t.Fatalf("expected pushover error or nil, got: %v", err)
	}

	// Verify LLM was called.
	llm.mu.Lock()
	if len(llm.calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(llm.calls))
	}
	llm.mu.Unlock()
}

func TestSummarize_LLMFallback_NilLLM(t *testing.T) {
	storage := &mockStorage{
		dayFn: func(date string) (string, int, error) {
			return "# Raw Markdown\n\n- Task A", 1, nil
		},
	}

	input := testInput()
	input.Storage = storage
	input.LLM = nil // No LLM configured.

	err := Summarize(context.Background(), input)
	// Should not error on missing LLM — falls back to raw markdown.
	if err != nil && !strings.Contains(err.Error(), "pushover") {
		t.Fatalf("expected pushover error or nil, got: %v", err)
	}
}

func TestSummarize_StorageError(t *testing.T) {
	storageErr := errors.New("database locked")
	storage := &mockStorage{
		dayFn: func(date string) (string, int, error) {
			return "", 0, storageErr
		},
	}

	input := testInput()
	input.Storage = storage

	err := Summarize(context.Background(), input)
	if err == nil {
		t.Fatal("expected error from storage failure")
	}
	if !strings.Contains(err.Error(), "storage query") {
		t.Errorf("error should mention storage query, got: %v", err)
	}
}

func TestSummarize_EmptyData(t *testing.T) {
	storage := &mockStorage{
		dayFn: func(date string) (string, int, error) {
			// Zero records — valid but empty markdown.
			return "# 工作日报 2025-01-15\n\n📊 **汇总**: 会议 0 | 任务 0 | 提醒 0 | 日志 0 | 共计 0 条\n", 0, nil
		},
	}
	llm := &mockLLM{
		fn: func(ctx context.Context, sys, user string) (string, error) {
			return "No records for today.", nil
		},
	}

	input := testInput()
	input.Storage = storage
	input.LLM = llm

	_ = Summarize(context.Background(), input)

	llm.mu.Lock()
	if len(llm.calls) != 1 {
		t.Fatalf("expected 1 LLM call even with empty data, got %d", len(llm.calls))
	}
	llm.mu.Unlock()
}

func TestSummarize_InvalidScope(t *testing.T) {
	input := testInput()
	input.Scope = "invalid"

	err := Summarize(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for invalid scope")
	}
	if !strings.Contains(err.Error(), "resolve date range") {
		t.Errorf("error should mention date range, got: %v", err)
	}
}

func TestPromptForDirection(t *testing.T) {
	tests := []struct {
		d        Direction
		expected string
	}{
		{DirectionAgenda, "agenda"},
		{DirectionSummary, "report"},
		{Direction("unknown"), "report"}, // default fallback
	}
	for _, tt := range tests {
		got := promptForDirection(tt.d)
		if got != tt.expected {
			t.Errorf("promptForDirection(%q) = %q, want %q", tt.d, got, tt.expected)
		}
	}
}

func TestDigestTitle(t *testing.T) {
	start := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	if got := digestTitle(DirectionAgenda, start, end); !strings.Contains(got, "Agenda") {
		t.Errorf("expected 'Agenda' in title, got: %s", got)
	}

	end = time.Date(2025, 1, 19, 0, 0, 0, 0, time.UTC)
	title := digestTitle(DirectionSummary, start, end)
	if !strings.Contains(title, "Report") {
		t.Errorf("expected 'Report' in title, got: %s", title)
	}
	if !strings.Contains(title, "~") {
		t.Errorf("expected range separator '~' in title, got: %s", title)
	}
}

func TestSummarize_SingleDayVsRange(t *testing.T) {
	storage := &mockStorage{
		dayFn: func(date string) (string, int, error) {
			return "# Daily\n\n- Item", 1, nil
		},
		rangeFn: func(from, to string) (string, int, error) {
			return "# Range\n\n- Items", 3, nil
		},
	}
	llm := &mockLLM{
		fn: func(ctx context.Context, sys, user string) (string, error) {
			return "summary", nil
		},
	}

	// Test single day (today) — should use GenerateDay.
	input := testInput()
	input.Scope = ScopeToday
	input.Storage = storage
	input.LLM = llm

	_ = Summarize(context.Background(), input)

	storage.mu.Lock()
	if len(storage.dayCalls) != 1 {
		t.Errorf("today scope: expected 1 day call, got %d", len(storage.dayCalls))
	}
	if len(storage.rangeCalls) != 0 {
		t.Errorf("today scope: expected 0 range calls, got %d", len(storage.rangeCalls))
	}
	storage.mu.Unlock()

	// Reset for range test.
	storage.dayCalls = nil
	storage.rangeCalls = nil

	// Test week range — should use GenerateRange.
	input.Scope = ScopeWeek
	_ = Summarize(context.Background(), input)

	storage.mu.Lock()
	if len(storage.dayCalls) != 0 {
		t.Errorf("week scope: expected 0 day calls, got %d", len(storage.dayCalls))
	}
	if len(storage.rangeCalls) != 1 {
		t.Errorf("week scope: expected 1 range call, got %d", len(storage.rangeCalls))
	}
	storage.mu.Unlock()
}

func TestSummarize_LLMTimeout_ContextCancelled(t *testing.T) {
	storage := &mockStorage{
		dayFn: func(date string) (string, int, error) {
			return "# Report\n\n- Task A", 1, nil
		},
	}
	llm := &mockLLM{
		fn: func(ctx context.Context, sys, user string) (string, error) {
			return "", context.Canceled
		},
	}

	input := testInput()
	input.Storage = storage
	input.LLM = llm

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := Summarize(ctx, input)
	// LLM failure should trigger fallback, not a pipeline error.
	// Only pushover failure should surface.
	if err != nil && !strings.Contains(err.Error(), "pushover") {
		t.Fatalf("expected only pushover error, got: %v", err)
	}
}

func TestSummarize_LargeMarkdown(t *testing.T) {
	// Build a markdown string >4000 chars to test boundary condition.
	var b strings.Builder
	b.WriteString("# Large Report\n\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "- Task %03d: This is a detailed task description with enough text to make it long. [进行中]\n", i)
	}
	largeMarkdown := b.String()
	if len(largeMarkdown) < 4000 {
		t.Fatalf("test setup error: markdown only %d chars, need >4000", len(largeMarkdown))
	}

	storage := &mockStorage{
		dayFn: func(date string) (string, int, error) {
			return largeMarkdown, 200, nil
		},
	}
	llm := &mockLLM{
		fn: func(ctx context.Context, sys, user string) (string, error) {
			// Verify LLM received the full markdown.
			if len(user) < 4000 {
				t.Errorf("LLM user content was truncated: %d chars", len(user))
			}
			return "Large summary", nil
		},
	}

	input := testInput()
	input.Storage = storage
	input.LLM = llm

	_ = Summarize(context.Background(), input)

	llm.mu.Lock()
	if len(llm.calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(llm.calls))
	}
	llm.mu.Unlock()
}
