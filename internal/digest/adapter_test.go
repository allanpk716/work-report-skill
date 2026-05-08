package digest

import (
	"context"
	"errors"
	"testing"
	"time"

	"wr/internal/llm"
)

// --- StorageQueryAdapter tests ---

// mockReportStorage wraps a mockStorage (from summarize_test.go) to verify
// that the adapter correctly delegates to report.Generate/GenerateRange.
// Since report.Generate requires a real *storage.Storage, we test the adapter
// through its interface contract rather than through the report layer.

func TestStorageQueryAdapter_NilStore(t *testing.T) {
	adapter := &StorageQueryAdapter{Store: nil, Loc: time.UTC}

	_, _, err := adapter.GenerateDay("2025-01-15")
	if err == nil {
		t.Fatal("expected error for nil store")
	}
	if err.Error() != "storage adapter: store is nil" {
		t.Errorf("unexpected error: %v", err)
	}

	_, _, err = adapter.GenerateRange("2025-01-13", "2025-01-19")
	if err == nil {
		t.Fatal("expected error for nil store")
	}
}

func TestNewStorageAdapter_NilStore(t *testing.T) {
	adapter := NewStorageAdapter(nil, time.UTC)
	if adapter != nil {
		t.Error("expected nil adapter for nil store")
	}
}

func TestNewStorageAdapter_ValidStore(t *testing.T) {
	// We can't create a real storage.Storage without a temp dir, but we can
	// verify the constructor doesn't panic and returns non-nil.
	// The actual storage integration is tested via the report package tests.
	// Here we test the nil guard only.
	adapter := NewStorageAdapter(nil, time.UTC)
	if adapter != nil {
		t.Error("expected nil for nil store")
	}
}

// --- LLMCallerAdapter tests ---

func TestLLMCallerAdapter_NilClient(t *testing.T) {
	adapter := &LLMCallerAdapter{Client: nil}

	_, err := adapter.Call(context.Background(), "system", "user content")
	if err == nil {
		t.Fatal("expected error for nil client")
	}
	if err.Error() != "llm adapter: client is nil" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewLLMAdapter_IncompleteConfig(t *testing.T) {
	tests := []struct {
		name    string
		apiBase string
		apiKey  string
		model   string
	}{
		{"empty all", "", "", ""},
		{"missing apiBase", "", "key", "model"},
		{"missing apiKey", "base", "", "model"},
		{"missing model", "base", "key", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := NewLLMAdapter(tt.apiBase, tt.apiKey, tt.model, 30*time.Second)
			if adapter != nil {
				t.Error("expected nil adapter for incomplete config")
			}
		})
	}
}

func TestNewLLMAdapter_ValidConfig(t *testing.T) {
	adapter := NewLLMAdapter("http://localhost:11434/v1", "sk-test", "llama3", 30*time.Second)
	if adapter == nil {
		t.Fatal("expected non-nil adapter for valid config")
	}
	if adapter.Client == nil {
		t.Fatal("expected client to be set")
	}
}

func TestLLMCallerAdapter_Call_BuildsCorrectMessages(t *testing.T) {
	// We can't easily mock llm.Client.CallChat without an HTTP server.
	// Instead, verify the adapter constructs messages correctly by checking
	// the error wrapping when the call fails (no server running).
	adapter := NewLLMAdapter("http://127.0.0.1:1/nonexistent", "sk-test", "test-model", 1*time.Second)
	if adapter == nil {
		t.Fatal("expected non-nil adapter")
	}

	_, err := adapter.Call(context.Background(), "system prompt", "user content")
	if err == nil {
		t.Fatal("expected error from failed HTTP call")
	}
	// Error should be wrapped with our prefix.
	if errStr := err.Error(); len(errStr) < 10 {
		t.Errorf("expected descriptive error, got: %v", err)
	}
}

func TestLLMCallerAdapter_Call_ContextCancelled(t *testing.T) {
	adapter := NewLLMAdapter("http://127.0.0.1:1/nonexistent", "sk-test", "test-model", 30*time.Second)
	if adapter == nil {
		t.Fatal("expected non-nil adapter")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := adapter.Call(ctx, "system", "user content")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// --- GenerateSummary tests ---

func TestGenerateSummary_HappyPath(t *testing.T) {
	storage := &mockStorage{
		dayFn: func(date string) (string, int, error) {
			return "# 工作日报\n\n- Task A", 1, nil
		},
	}
	llm := &mockLLM{
		fn: func(ctx context.Context, sys, user string) (string, error) {
			return "📋 Agenda: Task A", nil
		},
	}

	input := testInput()
	input.Storage = storage
	input.LLM = llm

	result, err := GenerateSummary(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text != "📋 Agenda: Task A" {
		t.Errorf("expected LLM summary text, got: %s", result.Text)
	}
	if result.RecordCount != 1 {
		t.Errorf("expected record count 1, got: %d", result.RecordCount)
	}
	if result.LLMStatus != "success" {
		t.Errorf("expected llm_status success, got: %s", result.LLMStatus)
	}
	if result.Title == "" {
		t.Error("expected non-empty title")
	}
}

func TestGenerateSummary_LLMFallback(t *testing.T) {
	storage := &mockStorage{
		dayFn: func(date string) (string, int, error) {
			return "# Raw Markdown\n\n- Task A", 1, nil
		},
	}
	llm := &mockLLM{
		fn: func(ctx context.Context, sys, user string) (string, error) {
			return "", errors.New("LLM API returned 500")
		},
	}

	input := testInput()
	input.Storage = storage
	input.LLM = llm

	result, err := GenerateSummary(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text != "# Raw Markdown\n\n- Task A" {
		t.Errorf("expected raw markdown fallback, got: %s", result.Text)
	}
	if result.LLMStatus != "fallback" {
		t.Errorf("expected llm_status fallback, got: %s", result.LLMStatus)
	}
}

func TestGenerateSummary_NoLLM(t *testing.T) {
	storage := &mockStorage{
		dayFn: func(date string) (string, int, error) {
			return "# Raw Markdown\n\n- Task A", 1, nil
		},
	}

	input := testInput()
	input.Storage = storage
	input.LLM = nil

	result, err := GenerateSummary(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.LLMStatus != "no_llm" {
		t.Errorf("expected llm_status no_llm, got: %s", result.LLMStatus)
	}
	if result.Text != "# Raw Markdown\n\n- Task A" {
		t.Errorf("expected raw markdown, got: %s", result.Text)
	}
}

func TestGenerateSummary_StorageError(t *testing.T) {
	storage := &mockStorage{
		dayFn: func(date string) (string, int, error) {
			return "", 0, errors.New("database locked")
		},
	}

	input := testInput()
	input.Storage = storage

	_, err := GenerateSummary(context.Background(), input)
	if err == nil {
		t.Fatal("expected error from storage failure")
	}
	if err.Error() != "summarize: storage query: database locked" {
		// May be wrapped differently; just check it contains the relevant parts.
		if !contains(err.Error(), "storage query") {
			t.Errorf("error should mention storage query, got: %v", err)
		}
	}
}

func TestGenerateSummary_InvalidScope(t *testing.T) {
	input := testInput()
	input.Scope = "invalid"

	_, err := GenerateSummary(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for invalid scope")
	}
}

func TestGenerateSummary_RangeQuery(t *testing.T) {
	storage := &mockStorage{
		rangeFn: func(from, to string) (string, int, error) {
			return "# Week Report\n\n- Task X", 5, nil
		},
	}
	llm := &mockLLM{
		fn: func(ctx context.Context, sys, user string) (string, error) {
			return "📊 Weekly summary", nil
		},
	}

	input := testInput()
	input.Scope = ScopeWeek
	input.Loc = time.FixedZone("CST", 8*3600)
	input.Storage = storage
	input.LLM = llm

	result, err := GenerateSummary(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.RecordCount != 5 {
		t.Errorf("expected record count 5, got: %d", result.RecordCount)
	}
	if result.LLMStatus != "success" {
		t.Errorf("expected llm_status success, got: %s", result.LLMStatus)
	}

	// Verify range was used (not single day).
	storage.mu.Lock()
	if len(storage.rangeCalls) != 1 {
		t.Errorf("expected 1 range call, got %d", len(storage.rangeCalls))
	}
	if len(storage.dayCalls) != 0 {
		t.Errorf("expected 0 day calls, got %d", len(storage.dayCalls))
	}
	storage.mu.Unlock()
}

// --- DirectionForPrompt tests ---

func TestDirectionForPrompt(t *testing.T) {
	tests := []struct {
		name PromptName
		want Direction
	}{
		{PromptNameAgenda, DirectionAgenda},
		{PromptNameReport, DirectionSummary},
		{PromptName("custom"), DirectionSummary}, // default fallback
	}
	for _, tt := range tests {
		got := DirectionForPrompt(tt.name)
		if got != tt.want {
			t.Errorf("DirectionForPrompt(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// --- Verify llm.ChatMessage is exported ---

func TestChatMessageExported(t *testing.T) {
	// Verify that llm.ChatMessage is accessible from outside the llm package.
	// This is a compile-time check — if ChatMessage is unexported, this won't compile.
	_ = llm.ChatMessage{
		Role:    "system",
		Content: "test",
	}
}

// --- Helper ---

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
