// Package digest provides adapters that bridge the internal storage/LLM
// layer with the digest pipeline's StorageProvider and LLMCaller interfaces.
package digest

import (
	"context"
	"fmt"
	"time"

	"wr/internal/llm"
	"wr/internal/logger"
	"wr/internal/report"
	"wr/internal/storage"
)

// StorageQueryAdapter wraps a *storage.Storage instance and implements
// StorageProvider by calling report.GenerateToday/GenerateRange and
// returning the .Markdown field.
type StorageQueryAdapter struct {
	Store *storage.Storage
	Loc   *time.Location
}

// GenerateDay generates a daily report for the given date string and
// returns the markdown text and total record count.
func (a *StorageQueryAdapter) GenerateDay(date string) (string, int, error) {
	if a.Store == nil {
		return "", 0, fmt.Errorf("storage adapter: store is nil")
	}
	rpt, err := report.Generate(a.Store, date)
	if err != nil {
		return "", 0, fmt.Errorf("storage adapter: generate day %s: %w", date, err)
	}
	return rpt.Markdown, rpt.Summary.Total, nil
}

// GenerateRange generates reports for the inclusive date range [from, to]
// and returns the merged markdown text and total record count.
func (a *StorageQueryAdapter) GenerateRange(from, to string) (string, int, error) {
	if a.Store == nil {
		return "", 0, fmt.Errorf("storage adapter: store is nil")
	}
	loc := a.Loc
	if loc == nil {
		loc = time.UTC
	}
	rpt, err := report.GenerateRange(a.Store, from, to, loc)
	if err != nil {
		return "", 0, fmt.Errorf("storage adapter: generate range %s:%s: %w", from, to, err)
	}
	return rpt.Markdown, rpt.Summary.Total, nil
}

// LLMCallerAdapter wraps an *llm.Client and implements LLMCaller by
// building system+user chat messages and calling CallChat.
type LLMCallerAdapter struct {
	Client *llm.Client
}

// Call sends a system prompt and user content to the LLM via CallChat.
// The system prompt is used as the first message (role=system), and
// the user content is the second message (role=user).
func (a *LLMCallerAdapter) Call(ctx context.Context, systemPrompt, userContent string) (string, error) {
	if a.Client == nil {
		return "", fmt.Errorf("llm adapter: client is nil")
	}
	messages := []llm.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent},
	}
	result, err := a.Client.CallChat(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("llm adapter: call failed: %w", err)
	}
	return result, nil
}

// NewStorageAdapter creates a StorageQueryAdapter from daemon dependencies.
// Returns nil and logs an error if store is nil.
func NewStorageAdapter(store *storage.Storage, loc *time.Location) *StorageQueryAdapter {
	if store == nil {
		logger.Error("[digest-adapter] cannot create storage adapter: store is nil")
		return nil
	}
	return &StorageQueryAdapter{Store: store, Loc: loc}
}

// NewLLMAdapter creates an LLMCallerAdapter from an LLM config.
// Returns nil (not an error) if the LLM config is incomplete — callers
// should treat this as "no LLM available" and fall back to raw markdown.
func NewLLMAdapter(apiBase, apiKey, model string, timeout time.Duration) *LLMCallerAdapter {
	if apiBase == "" || apiKey == "" || model == "" {
		logger.Warn("[digest-adapter] LLM config incomplete, adapter will be nil (raw markdown fallback)")
		return nil
	}
	client := llm.NewClient(apiBase, apiKey, model, timeout)
	return &LLMCallerAdapter{Client: client}
}
