package jsonl

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// parseEnvelopes parses JSONL output into a slice of Envelope maps.
func parseEnvelopes(buf *bytes.Buffer) []map[string]interface{} {
	var results []map[string]interface{}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		results = append(results, m)
	}
	return results
}

// assertEnvelopeMeta checks required metadata fields on a parsed envelope.
func assertEnvelopeMeta(t *testing.T, env map[string]interface{}, expectedType string) {
	t.Helper()
	if env["version"] != "1.0" {
		t.Errorf("expected version=1.0, got %v", env["version"])
	}
	if env["tool"] != "wr" {
		t.Errorf("expected tool=wr, got %v", env["tool"])
	}
	if env["type"] != expectedType {
		t.Errorf("expected type=%s, got %v", expectedType, env["type"])
	}
	ts, ok := env["timestamp"].(string)
	if !ok || ts == "" {
		t.Errorf("expected non-empty timestamp, got %v", env["timestamp"])
	} else if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("timestamp not RFC3339Nano: %v", ts)
	}
}

func TestSuccessWithData(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.Success(map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("Success returned error: %v", err)
	}
	lines := parseEnvelopes(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	assertEnvelopeMeta(t, lines[0], "result")
	data, ok := lines[0]["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", lines[0]["data"])
	}
	if data["key"] != "value" {
		t.Errorf("expected data.key=value, got %v", data["key"])
	}
	// error_code should be absent (omitempty)
	if _, has := lines[0]["error_code"]; has {
		t.Errorf("result envelope should not have error_code")
	}
}

func TestSuccessNilData(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.Success(nil)
	if err != nil {
		t.Fatalf("Success returned error: %v", err)
	}
	lines := parseEnvelopes(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	assertEnvelopeMeta(t, lines[0], "result")
	// data should be absent (nil → omitempty)
	if _, has := lines[0]["data"]; has {
		t.Errorf("nil data should be omitted")
	}
}

func TestSuccessEmptyString(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.Success("")
	if err != nil {
		t.Fatalf("Success returned error: %v", err)
	}
	lines := parseEnvelopes(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	assertEnvelopeMeta(t, lines[0], "result")
}

func TestError(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.Error("something went wrong")
	if err != nil {
		t.Fatalf("Error returned error: %v", err)
	}
	lines := parseEnvelopes(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	assertEnvelopeMeta(t, lines[0], "error")
	if lines[0]["message"] != "something went wrong" {
		t.Errorf("expected message='something went wrong', got %v", lines[0]["message"])
	}
	if lines[0]["error_code"] != "error" {
		t.Errorf("expected error_code='error', got %v", lines[0]["error_code"])
	}
	// data should be absent
	if _, has := lines[0]["data"]; has {
		t.Errorf("error envelope should not have data")
	}
}

func TestErrorWithCode(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.ErrorWithCode("custom_code", "custom error")
	if err != nil {
		t.Fatalf("ErrorWithCode returned error: %v", err)
	}
	lines := parseEnvelopes(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	assertEnvelopeMeta(t, lines[0], "error")
	if lines[0]["error_code"] != "custom_code" {
		t.Errorf("expected error_code='custom_code', got %v", lines[0]["error_code"])
	}
	if lines[0]["message"] != "custom error" {
		t.Errorf("expected message='custom error', got %v", lines[0]["message"])
	}
}

func TestWarning(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.Warning("be careful")
	if err != nil {
		t.Fatalf("Warning returned error: %v", err)
	}
	lines := parseEnvelopes(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	assertEnvelopeMeta(t, lines[0], "warning")
	if lines[0]["message"] != "be careful" {
		t.Errorf("expected message='be careful', got %v", lines[0]["message"])
	}
}

func TestProgressEnvelope(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	env := ProgressEnvelope(75, "processing...")
	err := w.WriteEnvelope(env)
	if err != nil {
		t.Fatalf("WriteEnvelope returned error: %v", err)
	}
	lines := parseEnvelopes(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	assertEnvelopeMeta(t, lines[0], "progress")
	if lines[0]["percent"] != float64(75) {
		t.Errorf("expected percent=75, got %v", lines[0]["percent"])
	}
	if lines[0]["message"] != "processing..." {
		t.Errorf("expected message='processing...', got %v", lines[0]["message"])
	}
}

func TestSuccessEnvelopeConstructor(t *testing.T) {
	env := SuccessEnvelope(map[string]int{"count": 5})
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope failed: %v", err)
	}
	if env.Version != "1.0" {
		t.Errorf("expected version 1.0, got %s", env.Version)
	}
	if env.Tool != "wr" {
		t.Errorf("expected tool wr, got %s", env.Tool)
	}
	if env.Type != "result" {
		t.Errorf("expected type result, got %s", env.Type)
	}
	if env.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
}

func TestErrorEnvelopeConstructor(t *testing.T) {
	env := ErrorEnvelope("bad_input", "bad input")
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope failed: %v", err)
	}
	if env.Type != "error" {
		t.Errorf("expected type error, got %s", env.Type)
	}
	if env.ErrorCode != "bad_input" {
		t.Errorf("expected error_code='bad_input', got %s", env.ErrorCode)
	}
	if env.Message != "bad input" {
		t.Errorf("expected message='bad input', got %s", env.Message)
	}
}

func TestValidateEnvelope_MissingVersion(t *testing.T) {
	env := Envelope{Tool: "wr", Type: "result", Timestamp: time.Now().Format(time.RFC3339Nano)}
	err := ValidateEnvelope(env)
	if err == nil {
		t.Fatal("expected error for missing version")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error should mention version: %v", err)
	}
}

func TestValidateEnvelope_MissingTool(t *testing.T) {
	env := Envelope{Version: "1.0", Type: "result", Timestamp: time.Now().Format(time.RFC3339Nano)}
	err := ValidateEnvelope(env)
	if err == nil {
		t.Fatal("expected error for missing tool")
	}
}

func TestValidateEnvelope_MissingType(t *testing.T) {
	env := Envelope{Version: "1.0", Tool: "wr", Timestamp: time.Now().Format(time.RFC3339Nano)}
	err := ValidateEnvelope(env)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestValidateEnvelope_MissingTimestamp(t *testing.T) {
	env := Envelope{Version: "1.0", Tool: "wr", Type: "result"}
	err := ValidateEnvelope(env)
	if err == nil {
		t.Fatal("expected error for missing timestamp")
	}
}

func TestValidateEnvelope_InvalidType(t *testing.T) {
	env := Envelope{Version: "1.0", Tool: "wr", Type: "unknown", Timestamp: time.Now().Format(time.RFC3339Nano)}
	err := ValidateEnvelope(env)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
	if !strings.Contains(err.Error(), "invalid type") {
		t.Errorf("error should mention invalid type: %v", err)
	}
}

func TestValidateEnvelope_ValidTypes(t *testing.T) {
	types := []string{"result", "error", "warning", "progress"}
	for _, typ := range types {
		env := Envelope{Version: "1.0", Tool: "wr", Type: typ, Timestamp: time.Now().Format(time.RFC3339Nano)}
		if err := ValidateEnvelope(env); err != nil {
			t.Errorf("type %q should be valid: %v", typ, err)
		}
	}
}

func TestUnicodeCharacters(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.Error("错误：中文测试 🎉")
	if err != nil {
		t.Fatalf("Error returned error: %v", err)
	}
	lines := parseEnvelopes(&buf)
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
	lines := parseEnvelopes(&buf)
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
	lines := parseEnvelopes(&buf)
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
	lines := parseEnvelopes(&buf)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	assertEnvelopeMeta(t, lines[0], "result")
	assertEnvelopeMeta(t, lines[1], "result")
	assertEnvelopeMeta(t, lines[2], "error")
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

func TestWriteEnvelope(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	env := ProgressEnvelope(50, "halfway")
	err := w.WriteEnvelope(env)
	if err != nil {
		t.Fatalf("WriteEnvelope returned error: %v", err)
	}
	lines := parseEnvelopes(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	assertEnvelopeMeta(t, lines[0], "progress")
	if lines[0]["percent"] != float64(50) {
		t.Errorf("expected percent=50, got %v", lines[0]["percent"])
	}
}

func TestEnvelopeFieldSeparation(t *testing.T) {
	// Verify that result and error envelopes have clean field separation:
	// result: has data, no error_code/message
	// error: has error_code/message, no data
	var buf bytes.Buffer
	w := NewWriter(&buf)
	_ = w.Success(map[string]string{"x": "1"})
	_ = w.Error("fail")
	lines := parseEnvelopes(&buf)

	resultEnv := lines[0]
	if _, has := resultEnv["error_code"]; has {
		t.Error("result envelope should not have error_code")
	}
	if _, has := resultEnv["message"]; has {
		t.Error("result envelope should not have message")
	}
	if _, has := resultEnv["data"]; !has {
		t.Error("result envelope should have data")
	}

	errorEnv := lines[1]
	if _, has := errorEnv["data"]; has {
		t.Error("error envelope should not have data")
	}
	if _, has := errorEnv["error_code"]; !has {
		t.Error("error envelope should have error_code")
	}
	if _, has := errorEnv["message"]; !has {
		t.Error("error envelope should have message")
	}
}
