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
// summarize pipeline. Implemented by report.StorageQueryAdapter (or a mock
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
		return fmt.Errorf("summarize: resolve date range: %w", err)
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
		return fmt.Errorf("summarize: storage query: %w", err)
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
		logger.WithFields(logFields).
			Warn(summarizePrefix + " no LLM configured, using raw markdown")
		summaryText = markdown
	}
	logFields["llm_status"] = llmStatus

	// Step 5: Push via Pushover.
	title := digestTitle(input.Direction, start, end)
	logStep(logFields, "sending pushover notification")

	sender := DefaultPushoverSender()
	pushErr := sender.Send(ctx, input.PushCfg, summaryText, title, input.PushPriority)
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
