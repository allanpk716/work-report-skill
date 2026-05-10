package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
)

// executeCmd runs rootCmd with the given args and returns the exit code
// extracted from the returned error via errors.As.
// It captures JSONL output from both jsonl.DefaultWriter and os.Stdout.
func executeCmd(args ...string) (int, []byte) {
	// Ensure app is initialized (tests run without main.go calling InitApp)
	if app == nil {
		InitApp()
	}
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

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
		return agentsdk.ExitSuccess, allOutput
	}

	var exitErr *agentsdk.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code, allOutput
	}
	return agentsdk.ExitFatalError, allOutput
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
// agentsdk.Envelope, and validates the envelope structure.
func validateAllEnvelopes(t *testing.T, data []byte) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var env agentsdk.Envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue // skip non-JSON lines (shouldn't happen in normal output)
		}
		if err := agentsdk.ValidateEnvelope(env); err != nil {
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

// --- Test Success (exit 0) ---

func TestSuccessExit0(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config in temp home
	executeCmd("config", "init")

	// Reset list flags from any prior test
	resetListFlags()

	// list should succeed with direct call (empty result set)
	code, out := executeCmd("list")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d", code)
	}
	validateAllEnvelopes(t, out)
}

// --- Test Invalid Params (exit 2) from direct call ---
// `wr list --type invalid` triggers invalid_type error from the direct-call list command.

func TestInvalidParamsExit2(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config in temp home
	executeCmd("config", "init")

	// Reset list flags from any prior test
	resetListFlags()

	code, out := executeCmd("list", "--type", "invalid")
	if code != agentsdk.ExitInvalidParams {
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

// --- Test LLM Error (exit 4) from direct call ---
// `wr add --text "test"` with no LLM config triggers llm_not_configured error.

func TestLLMErrorExit4(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config in temp home (no LLM configured)
	executeCmd("config", "init")

	// --text without LLM config triggers llm_not_configured → exit 4
	code, out := executeCmd("add", "--text", "test meeting at 3pm")
	if code != agentsdk.ExitNetworkError {
		t.Errorf("expected exit code 4, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	found := false
	for _, line := range lines {
		if ec, ok := line["error_code"].(string); ok && (ec == "llm_error" || ec == "llm_not_configured") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=llm_error or llm_not_configured in output, got %v", lines)
	}
	validateAllEnvelopes(t, out)
}

// --- Test Fatal Error (exit 1) from direct call ---
// `wr complete <nonexistent-id>` triggers record_not_found error.

func TestFatalErrorExit1(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config in temp home
	executeCmd("config", "init")

	// wr complete <nonexistent-id> → record_not_found → exit 1
	code, out := executeCmd("complete", "nonexistent-id-that-does-not-exist")
	if code != agentsdk.ExitFatalError {
		t.Errorf("expected exit code 1, got %d", code)
	}
	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	found := false
	for _, line := range lines {
		if line["error_code"] == "record_not_found" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=record_not_found in output, got %v", lines)
	}
	validateAllEnvelopes(t, out)
}

// --- Test Lock Conflict (exit 5) from direct call ---
// We can't easily trigger a lock conflict from CLI, so we test that
// the error code is properly registered and maps to exit 5.

func TestLockConflictExit5(t *testing.T) {
	// Verify that lock_conflict error code maps to ExitLockConflict (5)
	if app == nil {
		InitApp()
	}
	reg := app.Registry()
	if reg == nil {
		t.Fatal("error code registry is nil")
	}
	exitCode := reg.ToExitCode("lock_conflict")
	if exitCode != agentsdk.ExitLockConflict {
		t.Errorf("expected lock_conflict → exit code %d, got %d", agentsdk.ExitLockConflict, exitCode)
	}
}

// --- Test Report Range Missing Flags (exit 2) ---

func TestReportRangeMissingFlagsExit2(t *testing.T) {
	code, out := executeCmd("report", "range")
	if code != agentsdk.ExitInvalidParams {
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
	if code != agentsdk.ExitInvalidParams {
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
	if code != agentsdk.ExitInvalidParams {
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

// --- Test Direct Call List Works Without Daemon ---
// `wr list` now works directly via storage without needing a daemon.

func TestDaemonNotRunningNoStateExit4(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config in temp home so loadConfig succeeds
	executeCmd("config", "init")

	// Reset list flags from any prior test
	resetListFlags()

	// list should work without daemon — just returns empty results
	code, out := executeCmd("list")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0 (list works without daemon), got %d; output: %s", code, string(out))
	}
	validateAllEnvelopes(t, out)
}

// --- Test with real storage (no daemon needed) ---

func TestExitCodeWithDaemonRouter(t *testing.T) {
	// Set up temp home
	_, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config in temp home
	executeCmd("config", "init")

	// Reset list flags from any prior test
	resetListFlags()

	// Test: list with direct call (empty storage) → should succeed (exit 0)
	code, out := executeCmd("list")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0 for list, got %d; output: %s", code, string(out))
	}
	validateAllEnvelopes(t, out)

	// Test: add with invalid type → should be exit 2 (direct call)
	code, out = executeCmd("add", "--type", "invalid", "--title", "test", "--date", "2024-01-01")
	if code != agentsdk.ExitInvalidParams {
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
	if code != agentsdk.ExitInvalidParams {
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
	if code != agentsdk.ExitInvalidParams {
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
	if code != agentsdk.ExitInvalidParams {
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
	if code != agentsdk.ExitInvalidParams {
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
