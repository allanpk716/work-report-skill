package jsonl

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func parseLines(buf *bytes.Buffer) []map[string]interface{} {
	var results []map[string]interface{}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue // skip blank lines
		}
		results = append(results, m)
	}
	return results
}

func TestSuccessWithData(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.Success(map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("Success returned error: %v", err)
	}
	lines := parseLines(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0]["status"] != "success" {
		t.Errorf("expected status=success, got %v", lines[0]["status"])
	}
	data, ok := lines[0]["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", lines[0]["data"])
	}
	if data["key"] != "value" {
		t.Errorf("expected data.key=value, got %v", data["key"])
	}
}

func TestSuccessNilData(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.Success(nil)
	if err != nil {
		t.Fatalf("Success returned error: %v", err)
	}
	lines := parseLines(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0]["status"] != "success" {
		t.Errorf("expected status=success, got %v", lines[0]["status"])
	}
}

func TestSuccessEmptyString(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.Success("")
	if err != nil {
		t.Fatalf("Success returned error: %v", err)
	}
	lines := parseLines(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
}

func TestError(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.Error("something went wrong")
	if err != nil {
		t.Fatalf("Error returned error: %v", err)
	}
	lines := parseLines(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0]["status"] != "error" {
		t.Errorf("expected status=error, got %v", lines[0]["status"])
	}
	if lines[0]["message"] != "something went wrong" {
		t.Errorf("expected message='something went wrong', got %v", lines[0]["message"])
	}
}

func TestWarning(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.Warning("be careful")
	if err != nil {
		t.Fatalf("Warning returned error: %v", err)
	}
	lines := parseLines(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0]["status"] != "warning" {
		t.Errorf("expected status=warning, got %v", lines[0]["status"])
	}
	if lines[0]["message"] != "be careful" {
		t.Errorf("expected message='be careful', got %v", lines[0]["message"])
	}
}

func TestUnicodeCharacters(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.Error("错误：中文测试 🎉")
	if err != nil {
		t.Fatalf("Error returned error: %v", err)
	}
	lines := parseLines(&buf)
	if lines[0]["message"] != "错误：中文测试 🎉" {
		t.Errorf("unicode mangled: %v", lines[0]["message"])
	}
}

func TestNestedStruct(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	type Inner struct {
		Value int `json:"value"`
	}
	type Outer struct {
		Name  string `json:"name"`
		Inner Inner  `json:"inner"`
	}
	err := w.Success(Outer{Name: "test", Inner: Inner{Value: 42}})
	if err != nil {
		t.Fatalf("Success returned error: %v", err)
	}
	lines := parseLines(&buf)
	data, ok := lines[0]["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", lines[0]["data"])
	}
	if data["name"] != "test" {
		t.Errorf("expected name=test, got %v", data["name"])
	}
	inner, ok := data["inner"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected inner map, got %T", data["inner"])
	}
	if inner["value"] != float64(42) {
		t.Errorf("expected inner.value=42, got %v", inner["value"])
	}
}

func TestArray(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.Success([]string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Success returned error: %v", err)
	}
	lines := parseLines(&buf)
	data, ok := lines[0]["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array, got %T", lines[0]["data"])
	}
	if len(data) != 3 {
		t.Errorf("expected 3 items, got %d", len(data))
	}
}

func TestMultipleLines(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	_ = w.Success(map[string]string{"item": "1"})
	_ = w.Success(map[string]string{"item": "2"})
	_ = w.Error("oops")
	lines := parseLines(&buf)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0]["status"] != "success" {
		t.Errorf("line 0: expected success, got %v", lines[0]["status"])
	}
	if lines[1]["status"] != "success" {
		t.Errorf("line 1: expected success, got %v", lines[1]["status"])
	}
	if lines[2]["status"] != "error" {
		t.Errorf("line 2: expected error, got %v", lines[2]["status"])
	}
}

func TestEachLineIsValidJSON(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	_ = w.Success(nil)
	_ = w.Error("test")
	_ = w.Warning("warn")

	for i, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if !json.Valid([]byte(line)) {
			t.Errorf("line %d is not valid JSON: %q", i, line)
		}
	}
}
