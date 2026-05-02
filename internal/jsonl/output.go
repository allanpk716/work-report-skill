// Package jsonl provides JSONL (JSON Lines) output helpers for CLI commands.
// Every CLI output goes through these functions to ensure consistent format.
package jsonl

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Record represents a single JSONL line.
type Record struct {
	Status  string      `json:"status"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// Writer outputs JSONL records to an io.Writer. Defaults to stdout.
type Writer struct {
	out io.Writer
}

// DefaultWriter is the global writer using stdout.
var DefaultWriter = &Writer{out: os.Stdout}

// NewWriter creates a Writer targeting the given writer.
func NewWriter(w io.Writer) *Writer {
	return &Writer{out: w}
}

// Success writes a success JSONL line with optional data.
func (w *Writer) Success(data interface{}) error {
	return w.write(Record{Status: "success", Data: data})
}

// Error writes an error JSONL line with a message.
func (w *Writer) Error(message string) error {
	return w.write(Record{Status: "error", Message: message})
}

// Warning writes a warning JSONL line with a message.
func (w *Writer) Warning(message string) error {
	return w.write(Record{Status: "warning", Message: message})
}

func (w *Writer) write(r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("jsonl: marshal: %w", err)
	}
	_, err = fmt.Fprintf(w.out, "%s\n", b)
	return err
}

// Success writes a success JSONL line to stdout.
func Success(data interface{}) error { return DefaultWriter.Success(data) }

// Error writes an error JSONL line to stdout.
func Error(message string) error { return DefaultWriter.Error(message) }

// Warning writes a warning JSONL line to stdout.
func Warning(message string) error { return DefaultWriter.Warning(message) }
