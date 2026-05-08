// Package digest provides scheduled batch summaries of work records.
// This file implements the Summarize pipeline: data query → LLM summary →
// Pushover push with automatic fallback to raw markdown on LLM failure (D010).
package digest

import (
	"context"
	"fmt"
	"time"

	"wr/internal/logger"
	"wr/internal/pushover"
)

// Log prefix for all pipeline messages.
const summarizePrefix = "[digest-summarize]"

// SummaryResult holds the output of a GenerateSummary call.
// It can be consumed by Pushover (Summarize), printed to terminal (preview),
// or stored for later retrieval.
type SummaryResult struct {
	// Text is the final summary text (LLM output or raw markdown fallback).
	Text string
	// RecordCount is the number of records in the queried date range.
	RecordCount int
	// LLMStatus is "success", "fallback" (LLM error, used raw markdown),
	// "no_llm" (no LLM configured), or "error" (unexpected).
	LLMStatus string
	// DateStart is the inclusive start of the queried date range.
	DateStart time.Time
	// DateEnd is the inclusive end of the queried date range.
	DateEnd time.Time
	// Title is the notification title (e.g. "📋 Agenda 01-15").
	Title string
}

// SummarizeInput holds all parameters needed for a single summarize run.
// Dependencies are injected as interfaces for testability.
type SummarizeInput struct {
	// DigestID is the unique digest identifier (for logging/correlation).
	DigestID string

	// Scope defines the date range (today/week/month/custom).
	Scope Scope

	// Direction controls prompt selection (agenda vs report).
	Direction Direction

	// CustomRange is the raw "YYYY-MM-DD:YYYY-MM-DD" string for ScopeCustom.
	// Ignored for other scopes.
	CustomRange string

	// Loc is the timezone for date resolution.
	Loc *time.Location

	// Storage provides the report data query layer.
	Storage StorageProvider

	// LLM provides the chat completions call.
	LLM LLMCaller

	// PushCfg holds Pushover credentials.
	PushCfg pushover.Config

	// PushPriority is the Pushover priority (-2 to 2).
	PushPriority int
}

// StorageProvider abstracts the report generation functions needed by the
// summarize pipeline. Implemented by digest.StorageQueryAdapter (or a mock
// in tests).
type StorageProvider interface {
	// GenerateDay queries records for a single date string (YYYY-MM-DD)
	// and returns the markdown report.
	GenerateDay(date string) (markdown string, totalRecords int, err error)

	// GenerateRange queries records for a date range [from, to] and
	// returns the merged markdown report.
	GenerateRange(from, to string) (markdown string, totalRecords int, err error)
}

// LLMCaller abstracts the LLM chat completions call for testability.
type LLMCaller interface {
	// Call sends messages to the LLM and returns the first choice content.
	// Returns an error on any failure (network, non-200, empty choices, etc.).
	Call(ctx context.Context, systemPrompt, userContent string) (string, error)
}

// PushoverSender abstracts Pushover notification sending for testability.
type PushoverSender interface {
	Send(ctx context.Context, cfg pushover.Config, message, title string, priority int) error
}

// realPushoverSender wraps the package-level pushover.Send function.
type realPushoverSender struct{}

func (s *realPushoverSender) Send(ctx context.Context, cfg pushover.Config, message, title string, priority int) error {
	return pushover.Send(ctx, cfg, message, title, priority)
}

// DefaultPushoverSender returns a PushoverSender backed by the real Pushover API.
func DefaultPushoverSender() PushoverSender {
	return &realPushoverSender{}
}

// GenerateSummary executes steps 1-4 of the digest pipeline:
//  1. Resolve date range from scope
//  2. Query storage for report markdown
//  3. Select prompt based on direction
//  4. Call LLM for summary (fallback to raw markdown on failure per D010)
//
// Returns a SummaryResult containing the summary text and metadata.
// LLM failures are downgraded (raw markdown used) rather than fatal.
func GenerateSummary(ctx context.Context, input SummarizeInput) (*SummaryResult, error) {
	logFields := map[string]interface{}{
		"digest_id": input.DigestID,
		"scope":     string(input.Scope),
		"direction": string(input.Direction),
	}

	// Step 1: Resolve date range.
	logStep(logFields, "resolving date range")
	start, end, err := ResolveDateRange(input.Scope, input.Loc, input.CustomRange)
	if err != nil {
		logger.WithFields(logFields).WithField("error", err).
			Error(summarizePrefix + " date range resolution failed")
		return nil, fmt.Errorf("summarize: resolve date range: %w", err)
	}
	logFields["date_start"] = start.Format("2006-01-02")
	logFields["date_end"] = end.Format("2006-01-02")
	logStep(logFields, "date range resolved")

	// Step 2: Query storage for report markdown.
	logStep(logFields, "querying storage")
	var markdown string
	var recordCount int

	if start.Format("2006-01-02") == end.Format("2006-01-02") {
		// Single day → GenerateDay
		markdown, recordCount, err = input.Storage.GenerateDay(start.Format("2006-01-02"))
	} else {
		// Multi-day → GenerateRange
		markdown, recordCount, err = input.Storage.GenerateRange(start.Format("2006-01-02"), end.Format("2006-01-02"))
	}
	if err != nil {
		logger.WithFields(logFields).WithField("error", err).
			Error(summarizePrefix + " storage query failed")
		return nil, fmt.Errorf("summarize: storage query: %w", err)
	}
	logFields["record_count"] = recordCount
	logStep(logFields, "storage query complete")

	// Step 3: Select prompt based on direction.
	promptName := promptForDirection(input.Direction)
	logFields["prompt_name"] = string(promptName)

	// Step 4: Call LLM for summary, with fallback to raw markdown (D010).
	var summaryText string
	llmStatus := "fallback"

	if input.LLM != nil {
		logStep(logFields, "calling LLM")
		summaryText, err = input.LLM.Call(ctx, promptName, markdown)
		if err != nil {
			logger.WithFields(logFields).WithField("error", err).
				Warn(summarizePrefix + " LLM call failed, falling back to raw markdown")
			summaryText = markdown
		} else {
			llmStatus = "success"
			logStep(logFields, "LLM call succeeded")
		}
	} else {
		llmStatus = "no_llm"
		logger.WithFields(logFields).
			Warn(summarizePrefix + " no LLM configured, using raw markdown")
		summaryText = markdown
	}
	logFields["llm_status"] = llmStatus

	title := digestTitle(input.Direction, start, end)

	logger.WithFields(logFields).Info(summarizePrefix + " summary generated")

	return &SummaryResult{
		Text:        summaryText,
		RecordCount: recordCount,
		LLMStatus:   llmStatus,
		DateStart:   start,
		DateEnd:     end,
		Title:       title,
	}, nil
}

// Summarize executes the full digest pipeline:
//  1. Resolve date range from scope
//  2. Query storage for report markdown
//  3. Select prompt based on direction
//  4. Call LLM for summary (fallback to raw markdown on failure per D010)
//  5. Push summary via Pushover
//
// Returns an error if the pipeline cannot produce any output to push.
// LLM failures are downgraded (raw markdown used) rather than fatal.
// Pushover failures are returned as errors.
func Summarize(ctx context.Context, input SummarizeInput) error {
	result, err := GenerateSummary(ctx, input)
	if err != nil {
		return err
	}

	// Step 5: Push via Pushover.
	logFields := map[string]interface{}{
		"digest_id":  input.DigestID,
		"llm_status": result.LLMStatus,
	}

	logStep(logFields, "sending pushover notification")
	sender := DefaultPushoverSender()
	pushErr := sender.Send(ctx, input.PushCfg, result.Text, result.Title, input.PushPriority)
	if pushErr != nil {
		logFields["push_status"] = "failed"
		logger.WithFields(logFields).WithField("error", pushErr).
			Error(summarizePrefix + " pushover send failed")
		return fmt.Errorf("summarize: pushover send: %w", pushErr)
	}
	logFields["push_status"] = "sent"
	logger.WithFields(logFields).Info(summarizePrefix + " pipeline complete")

	return nil
}

// promptForDirection maps a digest direction to the prompt name used for LLM.
func promptForDirection(d Direction) string {
	switch d {
	case DirectionAgenda:
		return string(PromptNameAgenda)
	case DirectionSummary:
		return string(PromptNameReport)
	default:
		return string(PromptNameReport)
	}
}

// DirectionForPrompt maps a prompt name back to a digest direction.
// This is the reverse of promptForDirection, used by preview commands
// and the daemon callback to reconstruct the direction from a prompt name.
func DirectionForPrompt(name PromptName) Direction {
	switch name {
	case PromptNameAgenda:
		return DirectionAgenda
	case PromptNameReport:
		return DirectionSummary
	default:
		return DirectionSummary
	}
}

// digestTitle generates a Pushover notification title based on direction and
// date range.
func digestTitle(d Direction, start, end time.Time) string {
	dateRange := start.Format("01-02")
	if start.Format("2006-01-02") != end.Format("2006-01-02") {
		dateRange = start.Format("01-02") + "~" + end.Format("01-02")
	}

	switch d {
	case DirectionAgenda:
		return fmt.Sprintf("📋 Agenda %s", dateRange)
	case DirectionSummary:
		return fmt.Sprintf("📊 Report %s", dateRange)
	default:
		return fmt.Sprintf("📋 Digest %s", dateRange)
	}
}

// logStep emits a structured info log with the prefix and accumulated fields.
func logStep(fields map[string]interface{}, msg string) {
	logger.WithFields(fields).Info(summarizePrefix + " " + msg)
}
