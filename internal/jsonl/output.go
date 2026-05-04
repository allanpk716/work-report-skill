// Package jsonl provides JSONL (JSON Lines) output helpers for CLI commands.
// Every CLI output goes through these functions to ensure consistent envelope format.
//
// Envelope spec: {version, tool, type, timestamp, data, error_code, message, percent}
// All fields use omitempty so only relevant fields appear per type.
package jsonl

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// Canonical envelope field values.
const (
	EnvelopeVersion = "1.0"
	ToolName        = "wr"

	// Envelope type constants.
	TypeResult   = "result"
	TypeError    = "error"
	TypeWarning  = "warning"
	TypeProgress = "progress"
)

// Envelope is the canonical JSONL output format. Every line written to stdout
// by wr CLI commands and daemon responses uses this structure.
type Envelope struct {
	Version   string      `json:"version"`
	Tool      string      `json:"tool"`
	Type      string      `json:"type"`
	Timestamp string      `json:"timestamp"`
	Data      interface{} `json:"data,omitempty"`
	ErrorCode string      `json:"error_code,omitempty"`
	Message   string      `json:"message,omitempty"`
	Percent   int         `json:"percent,omitempty"`
}

// nowFunc allows tests to override timestamp generation.
var nowFunc = func() time.Time { return time.Now().UTC() }

// SuccessEnvelope creates a result envelope with the given data payload.
func SuccessEnvelope(data interface{}) Envelope {
	return Envelope{
		Version:   EnvelopeVersion,
		Tool:      ToolName,
		Type:      TypeResult,
		Timestamp: nowFunc().Format(time.RFC3339Nano),
		Data:      data,
	}
}

// ErrorEnvelope creates an error envelope with the given error code and message.
func ErrorEnvelope(errorCode string, message string) Envelope {
	return Envelope{
		Version:   EnvelopeVersion,
		Tool:      ToolName,
		Type:      TypeError,
		Timestamp: nowFunc().Format(time.RFC3339Nano),
		ErrorCode: errorCode,
		Message:   message,
	}
}

// ProgressEnvelope creates a progress envelope with percent and optional message.
func ProgressEnvelope(percent int, message string) Envelope {
	return Envelope{
		Version:   EnvelopeVersion,
		Tool:      ToolName,
		Type:      TypeProgress,
		Timestamp: nowFunc().Format(time.RFC3339Nano),
		Percent:   percent,
		Message:   message,
	}
}

// ValidateEnvelope checks that an envelope contains the required metadata fields.
// Returns nil if valid, or an error describing the first missing/invalid field.
// Intended for use in cross-package tests.
func ValidateEnvelope(env Envelope) error {
	if env.Version == "" {
		return fmt.Errorf("jsonl: envelope missing version")
	}
	if env.Tool == "" {
		return fmt.Errorf("jsonl: envelope missing tool")
	}
	if env.Type == "" {
		return fmt.Errorf("jsonl: envelope missing type")
	}
	if env.Timestamp == "" {
		return fmt.Errorf("jsonl: envelope missing timestamp")
	}
	switch env.Type {
	case TypeResult, TypeError, TypeWarning, TypeProgress:
		// valid
	default:
		return fmt.Errorf("jsonl: envelope has invalid type: %q", env.Type)
	}
	return nil
}

// Writer outputs JSONL envelopes to an io.Writer. Defaults to stdout.
type Writer struct {
	out io.Writer
}

// DefaultWriter is the global writer using stdout.
var DefaultWriter = &Writer{out: os.Stdout}

// NewWriter creates a Writer targeting the given writer.
func NewWriter(w io.Writer) *Writer {
	return &Writer{out: w}
}

// Success writes a result envelope with the given data.
func (w *Writer) Success(data interface{}) error {
	return w.write(SuccessEnvelope(data))
}

// Error writes an error envelope with code "error" and the given message.
func (w *Writer) Error(message string) error {
	return w.write(ErrorEnvelope("error", message))
}

// ErrorWithCode writes an error envelope with the specified error code and message.
func (w *Writer) ErrorWithCode(code string, message string) error {
	return w.write(ErrorEnvelope(code, message))
}

// Warning writes a warning envelope with the given message.
func (w *Writer) Warning(message string) error {
	return w.write(Envelope{
		Version:   EnvelopeVersion,
		Tool:      ToolName,
		Type:      TypeWarning,
		Timestamp: nowFunc().Format(time.RFC3339Nano),
		Message:   message,
	})
}

// WriteEnvelope writes a pre-built envelope directly.
func (w *Writer) WriteEnvelope(env Envelope) error {
	return w.write(env)
}

func (w *Writer) write(env Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("jsonl: marshal: %w", err)
	}
	_, err = fmt.Fprintf(w.out, "%s\n", b)
	return err
}

// Success writes a result envelope to stdout.
func Success(data interface{}) error { return DefaultWriter.Success(data) }

// Error writes an error envelope to stdout with code "error".
func Error(message string) error { return DefaultWriter.Error(message) }

// Warning writes a warning envelope to stdout.
func Warning(message string) error { return DefaultWriter.Warning(message) }
