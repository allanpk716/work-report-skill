package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wr/internal/config"
	"wr/internal/daemon"
	"wr/internal/exitcode"
	"wr/internal/jsonl"
	"wr/internal/storage"
)

// executeCmd runs rootCmd with the given args and returns the exit code
// extracted from the returned error via errors.As.
// It captures JSONL output from both jsonl.DefaultWriter and os.Stdout.
func executeCmd(args ...string) (int, []byte) {
	var buf strings.Builder
	orig := jsonl.DefaultWriter
	jsonl.DefaultWriter = jsonl.NewWriter(&buf)
	defer func() { jsonl.DefaultWriter = orig }()

	// Also capture os.Stdout since client.CallDaemon writes responses there
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w

	resetConfigFlags()
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()

	w.Close()
	os.Stdout = oldStdout

	// Read captured stdout
	stdoutData, _ := io.ReadAll(r)

	// Combine both outputs: jsonl.DefaultWriter output first, then captured stdout
	allOutput := append([]byte(buf.String()), stdoutData...)

	if err == nil {
		return exitcode.ExitSuccess, allOutput
	}

	var exitErr *exitcode.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code, allOutput
	}
	return exitcode.ExitFatalError, allOutput
}

// parseJSONLMaps parses JSONL output into a slice of maps.
func parseJSONLMaps(data []byte) []map[string]interface{} {
	var results []map[string]interface{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
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

// validateAllEnvelopes iterates over JSONL output, unmarshals each line into
// jsonl.Envelope, and validates the envelope structure.
func validateAllEnvelopes(t *testing.T, data []byte) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var env jsonl.Envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue // skip non-JSON lines (shouldn't happen in normal output)
		}
		if err := jsonl.ValidateEnvelope(env); err != nil {
			t.Errorf("envelope validation failed: %v; line=%s", err, line)
		}
	}
}

// setupTempHome creates a temp directory and sets HOME/USERPROFILE to it.
// Returns the temp dir and a cleanup function.
func setupTempHome(t *testing.T) (string, func()) {
	t.Helper()
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", tmpHome)
	os.Setenv("USERPROFILE", tmpHome)
	return tmpHome, func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}
}

// setupFakeDaemon creates an httptest.Server and configures the temp home
// to point to it. Returns the server (caller must defer Close()).
func setupFakeDaemon(t *testing.T, tmpHome string, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	addr := srv.Listener.Addr().String()
	parts := strings.Split(addr, ":")
	port := parts[len(parts)-1]

	var portInt int
	fmt.Sscanf(port, "%d", &portInt)
	if err := daemon.WriteState(stateDir, daemon.DaemonState{Port: portInt, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(stateDir, "config.json")
	cfg, err := config.Load(filepath.Join(stateDir, "nonexistent.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Daemon.Port = portInt
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	return srv
}

// --- Test Success (exit 0) ---

func TestSuccessExit0(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := jsonl.SuccessEnvelope(map[string]interface{}{"message": "ok"})
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		fmt.Fprintf(w, "%s\n", b)
	}))
	defer srv.Close()

	code, out := executeCmd("list")
	if code != exitcode.ExitSuccess {
		t.Errorf("expected exit code 0, got %d", code)
	}
	validateAllEnvelopes(t, out)
}

// --- Test Invalid Params (exit 2) from daemon error_code ---

func TestInvalidParamsExit2(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := jsonl.ErrorEnvelope("invalid_type", "missing required field: type")
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		fmt.Fprintf(w, "%s\n", b)
	}))
	defer srv.Close()

	code, out := executeCmd("add", "--type", "task", "--title", "test", "--date", "2024-01-01")
	if code != exitcode.ExitInvalidParams {
		t.Errorf("expected exit code 2, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	found := false
	for _, line := range lines {
		if line["error_code"] == "invalid_type" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=invalid_type in output, got %v", lines)
	}
	validateAllEnvelopes(t, out)
}

// --- Test Daemon Unreachable (exit 3) ---

func TestDaemonUnreachableExit3(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Create a state file pointing to a port with nothing listening
	stateDir := filepath.Join(tmpHome, ".work-report")
	os.MkdirAll(stateDir, 0755)
	daemon.WriteState(stateDir, daemon.DaemonState{Port: 19999, PID: 99999})

	cfgPath := filepath.Join(stateDir, "config.json")
	cfg, _ := config.Load(filepath.Join(stateDir, "nonexistent.json"))
	cfg.Daemon.Port = 19999
	cfg.Save(cfgPath)

	code, out := executeCmd("list")
	if code != exitcode.ExitDaemonUnreachable {
		t.Errorf("expected exit code 3, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["error_code"] != "daemon_not_running" {
		t.Errorf("expected error_code=daemon_not_running, got %v", lines[0]["error_code"])
	}
	validateAllEnvelopes(t, out)
}

// --- Test LLM Error (exit 4) from daemon error_code ---

func TestLLMErrorExit4(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := jsonl.ErrorEnvelope("llm_error", "LLM classification failed: timeout")
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		fmt.Fprintf(w, "%s\n", b)
	}))
	defer srv.Close()

	code, out := executeCmd("add", "--type", "task", "--title", "test", "--date", "2024-01-01")
	if code != exitcode.ExitNetworkError {
		t.Errorf("expected exit code 4, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	found := false
	for _, line := range lines {
		if line["error_code"] == "llm_error" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=llm_error in output, got %v", lines)
	}
	validateAllEnvelopes(t, out)
}

// --- Test Fatal Error (exit 1) from daemon error_code ---

func TestFatalErrorExit1(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := jsonl.ErrorEnvelope("storage_error", "failed to read records")
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		fmt.Fprintf(w, "%s\n", b)
	}))
	defer srv.Close()

	code, out := executeCmd("list")
	if code != exitcode.ExitFatalError {
		t.Errorf("expected exit code 1, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	found := false
	for _, line := range lines {
		if line["error_code"] == "storage_error" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=storage_error in output, got %v", lines)
	}
	validateAllEnvelopes(t, out)
}

// --- Test Lock Conflict (exit 5) from daemon error_code ---

func TestLockConflictExit5(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := jsonl.ErrorEnvelope("lock_conflict", "concurrent modification conflict")
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		fmt.Fprintf(w, "%s\n", b)
	}))
	defer srv.Close()

	code, out := executeCmd("add", "--type", "task", "--title", "test", "--date", "2024-01-01")
	if code != exitcode.ExitLockConflict {
		t.Errorf("expected exit code 5, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	found := false
	for _, line := range lines {
		if line["error_code"] == "lock_conflict" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=lock_conflict in output, got %v", lines)
	}
	validateAllEnvelopes(t, out)
}

// --- Test Config Validation (exit 2) ---

func TestConfigValidationExit2(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	// Invalid port should trigger validation error → exit 2
	code, out := executeCmd("config", "init", "--daemon-port", "99999")
	if code != exitcode.ExitInvalidParams {
		t.Errorf("expected exit code 2 for invalid port, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "error" {
		t.Errorf("expected type=error, got %v", lines[0]["type"])
	}
	validateAllEnvelopes(t, out)
}

// --- Test Report Range Missing Flags (exit 2) ---

func TestReportRangeMissingFlagsExit2(t *testing.T) {
	code, out := executeCmd("report", "range")
	if code != exitcode.ExitInvalidParams {
		t.Errorf("expected exit code 2 for missing range flags, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "error" {
		t.Errorf("expected type=error, got %v", lines[0]["type"])
	}
	validateAllEnvelopes(t, out)
}

// --- Test Update No Fields (exit 2) ---

func TestUpdateNoFieldsExit2(t *testing.T) {
	code, out := executeCmd("update", "test-id")
	if code != exitcode.ExitInvalidParams {
		t.Errorf("expected exit code 2 for no fields, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "error" {
		t.Errorf("expected type=error, got %v", lines[0]["type"])
	}
	validateAllEnvelopes(t, out)
}

// --- Test Config Set Unknown Path (exit 2) ---

func TestConfigSetUnknownPathExit2(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	// Init first
	executeCmd("config", "init")

	// Set unknown path → exit 2
	code, out := executeCmd("config", "set", "nonexistent.path", "value")
	if code != exitcode.ExitInvalidParams {
		t.Errorf("expected exit code 2 for unknown config path, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "error" {
		t.Errorf("expected type=error, got %v", lines[0]["type"])
	}
	validateAllEnvelopes(t, out)
}

// --- Test Daemon Not Running No State File (exit 3) ---

func TestDaemonNotRunningNoStateExit3(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	// No state file at all — daemon not running
	code, out := executeCmd("list")
	if code != exitcode.ExitDaemonUnreachable {
		t.Errorf("expected exit code 3, got %d", code)
	}
	validateAllEnvelopes(t, out)
}

// --- Test with real daemon router (httptest + storage.Storage) ---

func TestExitCodeWithDaemonRouter(t *testing.T) {
	// Create temp storage
	tmpDir := t.TempDir()
	store := storage.New(tmpDir, nil)

	cfg, _ := config.Load(filepath.Join(tmpDir, "nonexistent.json"))
	srv := daemon.NewServer(0, store, cfg)
	router := srv.Router()

	httpSrv := httptest.NewServer(router)
	defer httpSrv.Close()

	// Set up temp home with state pointing to test server
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	addr := httpSrv.Listener.Addr().String()
	parts := strings.Split(addr, ":")
	port := parts[len(parts)-1]

	stateDir := filepath.Join(tmpHome, ".work-report")
	os.MkdirAll(stateDir, 0755)
	var portInt int
	fmt.Sscanf(port, "%d", &portInt)
	daemon.WriteState(stateDir, daemon.DaemonState{Port: portInt, PID: os.Getpid()})

	cfgPath := filepath.Join(stateDir, "config.json")
	cfgSave, _ := config.Load(filepath.Join(stateDir, "nonexistent.json"))
	cfgSave.Daemon.Port = portInt
	cfgSave.Save(cfgPath)

	// Test: list with real daemon (empty) → should succeed (exit 0)
	code, out := executeCmd("list")
	if code != exitcode.ExitSuccess {
		t.Errorf("expected exit code 0 for list, got %d", code)
	}
	validateAllEnvelopes(t, out)

	// Test: add with invalid type → should be exit 2
	code, out = executeCmd("add", "--type", "invalid", "--title", "test", "--date", "2024-01-01")
	if code != exitcode.ExitInvalidParams {
		t.Errorf("expected exit code 2 for invalid type, got %d", code)
	}
	lines := parseJSONLMaps(out)
	found := false
	for _, line := range lines {
		if line["error_code"] == "invalid_type" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=invalid_type in output, got %v", lines)
	}
	validateAllEnvelopes(t, out)
}

// --- Import CLI tests ---

func TestExportWithoutFormatExit2(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	resetExportFlags()
	code, out := executeCmd("export")
	if code != exitcode.ExitInvalidParams {
		t.Errorf("expected exit code 2 for export without --format, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "error" {
		t.Errorf("expected type=error, got %v", lines[0]["type"])
	}
	validateAllEnvelopes(t, out)
}

func TestExportInvalidFormatCSVExit2(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	resetExportFlags()
	code, out := executeCmd("export", "--format", "csv")
	if code != exitcode.ExitInvalidParams {
		t.Errorf("expected exit code 2 for export --format csv, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "error" {
		t.Errorf("expected type=error, got %v", lines[0]["type"])
	}
	// Error message should mention that csv is invalid
	msg, _ := lines[0]["message"].(string)
	if !strings.Contains(msg, "json") && !strings.Contains(msg, "markdown") {
		t.Errorf("expected error message to mention valid formats, got %q", msg)
	}
	validateAllEnvelopes(t, out)
}

func TestImportWithoutFileFlag(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	importFilePath = "" // ensure clean state
	code, out := executeCmd("import")
	if code != exitcode.ExitInvalidParams {
		t.Errorf("expected exit code 2 for import without --file, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "error" {
		t.Errorf("expected type=error, got %v", lines[0]["type"])
	}
	if lines[0]["error_code"] != "invalid_params" {
		t.Errorf("expected error_code=invalid_params, got %v", lines[0]["error_code"])
	}
	validateAllEnvelopes(t, out)
}

func TestImportNonexistentFile(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	importFilePath = "" // ensure clean state
	code, out := executeCmd("import", "--file", "/nonexistent/path/to/records.json")
	if code != exitcode.ExitInvalidParams {
		t.Errorf("expected exit code 2 for nonexistent file, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	found := false
	for _, line := range lines {
		if line["error_code"] == "invalid_body" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=invalid_body, got %v", lines)
	}
	validateAllEnvelopes(t, out)
}
