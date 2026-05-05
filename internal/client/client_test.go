package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	agentsdk "github.com/allanpk716/agent-cli-sdk"
	"wr/internal/daemon"
	"wr/internal/storage"
)

// ── Client with running test server ──

func newTestDaemonServer(t *testing.T) *daemon.Server {
	t.Helper()
	dir := t.TempDir()
	store := storage.New(dir, log.New(io.Discard, "", 0))
	return daemon.NewServer(0, store, nil)
}

func TestCallDaemonGetWithServer(t *testing.T) {
	handler := newTestDaemonServer(t).Router()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	port := extractPort(t, ts.URL)
	dir := t.TempDir()
	state := daemon.DaemonState{Port: port, PID: os.Getpid()}
	if err := daemon.WriteState(dir, state); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := callDaemonWithDir(&buf, dir, http.MethodGet, "/health", nil)
	if err != nil {
		t.Fatalf("CallDaemon: %v", err)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("invalid JSONL: %s", buf.String())
	}
	if record["type"] != "result" {
		t.Errorf("type = %v, want result; body=%s", record["type"], buf.String())
	}
	data, ok := record["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map in envelope, got %T", record["data"])
	}
	if data["version"] != "0.1.0" {
		t.Errorf("data.version = %v, want 0.1.0; body=%s", data["version"], buf.String())
	}
	validateJSONLOutput(t, buf)
}

func TestCallDaemonPostWithServer(t *testing.T) {
	handler := newTestDaemonServer(t).Router()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	port := extractPort(t, ts.URL)
	dir := t.TempDir()
	state := daemon.DaemonState{Port: port, PID: os.Getpid()}
	daemon.WriteState(dir, state)

	var buf bytes.Buffer
	err := callDaemonWithDir(&buf, dir, http.MethodPost, "/api/add",
		strings.NewReader(`{"type":"meeting","title":"sync","date":"2026-05-02"}`))
	if err != nil {
		t.Fatalf("CallDaemon: %v", err)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("invalid JSONL: %s", buf.String())
	}
	if record["type"] != "result" {
		t.Errorf("type = %v, want result; body=%s", record["type"], buf.String())
	}
	data, ok := record["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map in envelope, got %T", record["data"])
	}
	if data["type"] != "meeting" {
		t.Errorf("data.type = %v, want meeting; body=%s", data["type"], buf.String())
	}
	validateJSONLOutput(t, buf)
}

// ── Daemon not running tests ──

func TestDaemonNotRunningError(t *testing.T) {
	dir := t.TempDir()

	var buf bytes.Buffer
	err := callDaemonWithDir(&buf, dir, http.MethodGet, "/health", nil)
	if err == nil {
		t.Fatal("expected error when daemon not running")
	}

	// Verify it's an ExitError with code 4 (daemon unreachable → network error)
	var exitErr *agentsdk.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != agentsdk.ExitNetworkError {
		t.Errorf("ExitError.Code = %d, want %d", exitErr.Code, agentsdk.ExitNetworkError)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("error output not valid JSONL: %s", buf.String())
	}
	if record["type"] != "error" {
		t.Errorf("type = %v, want error", record["type"])
	}
	if record["error_code"] != "daemon_not_running" {
		t.Errorf("error_code = %v, want daemon_not_running", record["error_code"])
	}
	msg, _ := record["message"].(string)
	if !strings.Contains(msg, "wr agent daemon start") {
		t.Error("message should contain actionable suggestion, got:", msg)
	}
	validateJSONLOutput(t, buf)
}

func TestDaemonUnreachableError(t *testing.T) {
	dir := t.TempDir()
	state := daemon.DaemonState{Port: 59999, PID: 1}
	daemon.WriteState(dir, state)

	var buf bytes.Buffer
	err := callDaemonWithDir(&buf, dir, http.MethodGet, "/health", nil)
	if err == nil {
		t.Fatal("expected error when daemon unreachable")
	}

	var exitErr *agentsdk.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != agentsdk.ExitNetworkError {
		t.Errorf("ExitError.Code = %d, want %d", exitErr.Code, agentsdk.ExitNetworkError)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("error output not valid JSONL: %s", buf.String())
	}
	if record["type"] != "error" {
		t.Errorf("type = %v, want error", record["type"])
	}
	if record["error_code"] != "daemon_not_running" {
		t.Errorf("error_code = %v, want daemon_not_running", record["error_code"])
	}
	msg, _ := record["message"].(string)
	if !strings.Contains(msg, "wr agent daemon start") {
		t.Error("message should contain actionable suggestion, got:", msg)
	}
	validateJSONLOutput(t, buf)
}

func TestDaemonCorruptStateNotRunning(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(daemon.StatePath(dir), []byte("corrupt"), 0644)

	var buf bytes.Buffer
	err := callDaemonWithDir(&buf, dir, http.MethodGet, "/health", nil)
	if err == nil {
		t.Fatal("expected error with corrupt state")
	}

	var exitErr *agentsdk.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != agentsdk.ExitNetworkError {
		t.Errorf("ExitError.Code = %d, want %d", exitErr.Code, agentsdk.ExitNetworkError)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("error output not valid JSONL: %s", buf.String())
	}
	if record["error_code"] != "daemon_not_running" {
		t.Errorf("error_code = %v, want daemon_not_running", record["error_code"])
	}
	msg, _ := record["message"].(string)
	if !strings.Contains(msg, "wr agent daemon start") {
		t.Error("message should contain actionable suggestion, got:", msg)
	}
	validateJSONLOutput(t, buf)
}

// ── Daemon error response mapping tests ──

func TestDaemonErrorResponse_InvalidType(t *testing.T) {
	// Mock daemon that returns an error envelope with error_code=invalid_type
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		env := agentsdk.NewErrorEnvelope("wr", "invalid_type", "invalid type")
		b, _ := json.Marshal(env)
		w.Write(b)
	}))
	defer ts.Close()

	port := extractPort(t, ts.URL)
	dir := t.TempDir()
	daemon.WriteState(dir, daemon.DaemonState{Port: port, PID: os.Getpid()})

	var buf bytes.Buffer
	err := callDaemonWithDir(&buf, dir, http.MethodPost, "/api/add",
		strings.NewReader(`{"type":"bad"}`))
	if err == nil {
		t.Fatal("expected error for daemon error response")
	}

	var exitErr *agentsdk.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != agentsdk.ExitInvalidParams {
		t.Errorf("ExitError.Code = %d, want %d (ExitInvalidParams)", exitErr.Code, agentsdk.ExitInvalidParams)
	}
	validateJSONLOutput(t, buf)
}

func TestDaemonErrorResponse_LLMError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		env := agentsdk.NewErrorEnvelope("wr", "llm_error", "LLM classification failed")
		b, _ := json.Marshal(env)
		w.Write(b)
	}))
	defer ts.Close()

	port := extractPort(t, ts.URL)
	dir := t.TempDir()
	daemon.WriteState(dir, daemon.DaemonState{Port: port, PID: os.Getpid()})

	var buf bytes.Buffer
	err := callDaemonWithDir(&buf, dir, http.MethodGet, "/api/list", nil)
	if err == nil {
		t.Fatal("expected error for daemon error response")
	}

	var exitErr *agentsdk.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != agentsdk.ExitNetworkError {
		t.Errorf("ExitError.Code = %d, want %d (ExitNetworkError)", exitErr.Code, agentsdk.ExitNetworkError)
	}
	validateJSONLOutput(t, buf)
}

func TestDaemonSuccessResponse_NoError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		env := agentsdk.NewResultEnvelope("wr", map[string]interface{}{"status": "ok"})
		b, _ := json.Marshal(env)
		w.Write(b)
	}))
	defer ts.Close()

	port := extractPort(t, ts.URL)
	dir := t.TempDir()
	daemon.WriteState(dir, daemon.DaemonState{Port: port, PID: os.Getpid()})

	var buf bytes.Buffer
	err := callDaemonWithDir(&buf, dir, http.MethodGet, "/health", nil)
	if err != nil {
		t.Fatalf("expected no error for success response, got: %v", err)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("invalid JSONL: %s", buf.String())
	}
	if record["type"] != "result" {
		t.Errorf("type = %v, want result", record["type"])
	}
	validateJSONLOutput(t, buf)
}

// ── Helpers ──

// callDaemonWithDir is a test helper that replicates CallDaemon logic
// but uses a specific state dir instead of the default one.
func callDaemonWithDir(w io.Writer, dir, method, path string, body io.Reader) error {
	state, err := daemon.ReadState(dir)
	if err != nil {
		return writeDaemonError(w, "daemon not running: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d%s", state.Port, path)
	httpClient := &http.Client{Timeout: clientTimeout}

	var req *http.Request
	if body != nil {
		req, err = http.NewRequest(method, url, body)
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		return writeDaemonError(w, "daemon not running: %v", err)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return writeDaemonError(w, "daemon not running: daemon unreachable at port %d", state.Port)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return writeDaemonError(w, "daemon response read failed: %v", err)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(respBody), &record); err != nil {
		return writeDaemonError(w, "malformed daemon response")
	}

	w.Write(respBody)

	// Mirror production behavior: check for daemon error envelopes.
	if record["type"] == "error" {
		errorCode, _ := record["error_code"].(string)
		msg, _ := record["message"].(string)
		return &agentsdk.ExitError{
			Code: errorToExitCode(errorCode),
			Err:  errors.New(msg),
		}
	}

	return nil
}

func extractPort(t *testing.T, url string) int {
	t.Helper()
	// url is like http://127.0.0.1:PORT
	parts := strings.Split(url, ":")
	if len(parts) != 3 {
		t.Fatalf("unexpected test server URL: %s", url)
	}
	var port int
	fmt.Sscanf(parts[2], "%d", &port)
	return port
}

// validateJSONLOutput reads JSONL from buf, unmarshals each line into
// jsonl.Envelope, and validates the envelope structure.
func validateJSONLOutput(t *testing.T, buf bytes.Buffer) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var env agentsdk.Envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("invalid JSONL line: %s\nerr: %v", line, err)
		}
		if err := agentsdk.ValidateEnvelope(env); err != nil {
			t.Errorf("envelope validation failed: %v; line=%s", err, line)
		}
	}
}
