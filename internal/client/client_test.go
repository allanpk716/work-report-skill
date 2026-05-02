package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"wr/internal/daemon"
)

// ── Client with running test server ──

func TestCallDaemonGetWithServer(t *testing.T) {
	handler := daemon.NewServer(0).Router()
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
	if record["status"] != "ok" {
		t.Errorf("status = %v, want ok; body=%s", record["status"], buf.String())
	}
}

func TestCallDaemonPostWithServer(t *testing.T) {
	handler := daemon.NewServer(0).Router()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	port := extractPort(t, ts.URL)
	dir := t.TempDir()
	state := daemon.DaemonState{Port: port, PID: os.Getpid()}
	daemon.WriteState(dir, state)

	var buf bytes.Buffer
	err := callDaemonWithDir(&buf, dir, http.MethodPost, "/api/add",
		strings.NewReader(`{"type":"meeting","title":"sync"}`))
	if err != nil {
		t.Fatalf("CallDaemon: %v", err)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("invalid JSONL: %s", buf.String())
	}
	if record["status"] != "success" {
		t.Errorf("status = %v, want success; body=%s", record["status"], buf.String())
	}
}

// ── Daemon not running tests ──

func TestDaemonNotRunningError(t *testing.T) {
	dir := t.TempDir()

	var buf bytes.Buffer
	err := callDaemonWithDir(&buf, dir, http.MethodGet, "/health", nil)
	if err == nil {
		t.Fatal("expected error when daemon not running")
	}

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("error output not valid JSONL: %s", buf.String())
	}
	if record["status"] != "error" {
		t.Errorf("status = %v, want error", record["status"])
	}
	if record["code"] != "daemon_not_running" {
		t.Errorf("code = %v, want daemon_not_running", record["code"])
	}
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

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("error output not valid JSONL: %s", buf.String())
	}
	if record["status"] != "error" {
		t.Errorf("status = %v, want error", record["status"])
	}
	if record["code"] != "daemon_not_running" {
		t.Errorf("code = %v, want daemon_not_running", record["code"])
	}
}

func TestDaemonCorruptStateNotRunning(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(daemon.StatePath(dir), []byte("corrupt"), 0644)

	var buf bytes.Buffer
	err := callDaemonWithDir(&buf, dir, http.MethodGet, "/health", nil)
	if err == nil {
		t.Fatal("expected error with corrupt state")
	}

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("error output not valid JSONL: %s", buf.String())
	}
	if record["code"] != "daemon_not_running" {
		t.Errorf("code = %v, want daemon_not_running", record["code"])
	}
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
