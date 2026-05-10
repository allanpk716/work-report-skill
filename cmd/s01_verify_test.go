package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
)

// unwrapData extracts the "data" field from a JSONL envelope map.
// Returns the data map or nil if not present.
func unwrapData(line map[string]interface{}) map[string]interface{} {
	data, _ := line["data"].(map[string]interface{})
	return data
}

// findResultData finds the first "result" envelope and returns its data map.
func findResultData(lines []map[string]interface{}) map[string]interface{} {
	for _, line := range lines {
		if line["type"] == "result" {
			return unwrapData(line)
		}
	}
	return nil
}

// TestAddWithoutDaemon verifies that `wr add` works without a daemon and
// produces JSONL output with a short_id.
func TestAddWithoutDaemon(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Add a record without LLM (manual type/title/date)
	code, out := executeCmd("add", "--type", "log", "--title", "S01 integration test entry", "--date", "2025-01-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "result" {
		t.Errorf("expected type=result, got %v", lines[0]["type"])
	}

	// Verify short_id is present inside the data envelope
	data := unwrapData(lines[0])
	if data == nil {
		t.Fatal("expected data field in envelope")
	}
	shortID, ok := data["short_id"].(string)
	if !ok || shortID == "" {
		t.Errorf("expected non-empty short_id in output data, got %v", data["short_id"])
	}

	validateAllEnvelopes(t, out)
}

// TestListWithoutDaemon verifies that `wr list` works without a daemon and
// the count matches what was added.
func TestListWithoutDaemon(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Add 3 records
	for i := 0; i < 3; i++ {
		code, _ := executeCmd("add", "--type", "log", "--title", fmt.Sprintf("list test entry %d", i), "--date", "2025-01-01")
		if code != agentsdk.ExitSuccess {
			t.Fatalf("add %d failed", i)
		}
	}

	// List all
	code, out := executeCmd("list", "--date", "2025-01-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0 for list, got %d; output: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}

	// The list command outputs records in "entries" array.
	// ListedRecord fields are exported (no json tags), so keys are capitalized.
	recordCount := 0
	for _, line := range lines {
		data := unwrapData(line)
		if data == nil {
			continue
		}
		entries, ok := data["entries"].([]interface{})
		if !ok {
			continue
		}
		for _, e := range entries {
			entry, ok := e.(map[string]interface{})
			if !ok {
				continue
			}
			if _, hasShortID := entry["ShortID"].(string); hasShortID {
				recordCount++
			}
		}
	}
	if recordCount < 3 {
		t.Errorf("expected at least 3 records, got %d", recordCount)
	}

	validateAllEnvelopes(t, out)
}

// TestCompleteWithoutDaemon verifies that `wr complete` works without a daemon
// and the record status changes.
func TestCompleteWithoutDaemon(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Add a record (task type — logs can't be completed)
	code, out := executeCmd("add", "--type", "task", "--title", "complete test entry", "--date", "2025-01-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: %s", string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	// Complete the record
	code, out = executeCmd("complete", shortID)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0 for complete, got %d; output: %s", code, string(out))
	}

	// Verify the completed record has status=completed
	lines = parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	data = unwrapData(lines[0])
	// Complete returns the record under "record" key
	record, ok := data["record"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'record' in complete output, got %v", data)
	}
	status, _ := record["status"].(string)
	if status != "completed" {
		t.Errorf("expected status=completed, got %q", status)
	}

	validateAllEnvelopes(t, out)
}

// TestReportPushTodayNoDaemon verifies that report generation works.
func TestReportPushTodayNoDaemon(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Add a record for today
	today := todayInLocation(loadConfig())
	code, _ := executeCmd("add", "--type", "log", "--title", "report test entry", "--date", today)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed")
	}

	// Generate report (no push)
	code, out := executeCmd("report", "today")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0 for report today, got %d; output: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "result" {
		t.Errorf("expected type=result, got %v", lines[0]["type"])
	}

	validateAllEnvelopes(t, out)
}

// TestConcurrentAdd spawns goroutines doing concurrent AddRecord and verifies
// no duplicates by short_id.
func TestConcurrentAdd(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	const concurrency = 10
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			code, _ := executeCmd("add", "--type", "log",
				"--title", fmt.Sprintf("concurrent test %d", idx),
				"--date", "2025-01-01")
			if code != agentsdk.ExitSuccess {
				errCh <- fmt.Errorf("goroutine %d: exit code %d", idx, code)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}

	// List and verify no duplicate short_ids
	code, out := executeCmd("list", "--date", "2025-01-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("list failed: %s", string(out))
	}

	lines := parseJSONLMaps(out)
	seen := make(map[string]bool)
	for _, line := range lines {
		data := unwrapData(line)
		if data == nil {
			continue
		}
		entries, ok := data["entries"].([]interface{})
		if !ok {
			continue
		}
		for _, e := range entries {
			entry, ok := e.(map[string]interface{})
			if !ok {
				continue
			}
			// ListedRecord uses exported field names (no json tags)
			shortID, ok := entry["ShortID"].(string)
			if !ok || shortID == "" {
				continue
			}
			if seen[shortID] {
				t.Errorf("duplicate short_id found: %s", shortID)
			}
			seen[shortID] = true
		}
	}

	if len(seen) < concurrency {
		t.Errorf("expected at least %d unique records, got %d", concurrency, len(seen))
	}
}

// TestDigestCommandsNoDaemon verifies that digest CRUD commands work without daemon.
func TestDigestCommandsNoDaemon(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Add
	code, out := executeCmd("digest", "add", "--schedule", "0 8 * * *", "--scope", "today", "--direction", "agenda")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("digest add failed: %s", string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	dm, _ := data["digest"].(map[string]interface{})
	id, _ := dm["id"].(string)

	// List
	code, out = executeCmd("digest", "list")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("digest list failed: %s", string(out))
	}

	// Disable
	code, out = executeCmd("digest", "disable", id)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("digest disable failed: %s", string(out))
	}

	// Enable
	code, out = executeCmd("digest", "enable", id)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("digest enable failed: %s", string(out))
	}

	// Remove
	code, out = executeCmd("digest", "remove", id)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("digest remove failed: %s", string(out))
	}
}

// TestPromptCommandsNoDaemon verifies that prompt commands work without daemon.
func TestPromptCommandsNoDaemon(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// List
	code, out := executeCmd("prompt", "list")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("prompt list failed: %s", string(out))
	}

	// Show
	code, out = executeCmd("prompt", "show", "agenda")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("prompt show failed: %s", string(out))
	}

	// Set
	code, out = executeCmd("prompt", "set", "agenda", "--text", "test prompt")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("prompt set failed: %s", string(out))
	}

	// Verify set
	code, out = executeCmd("prompt", "show", "agenda")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("prompt show after set failed: %s", string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	text, _ := data["text"].(string)
	if text != "test prompt" {
		t.Errorf("expected 'test prompt', got %q", text)
	}

	// Reset
	code, out = executeCmd("prompt", "reset", "agenda")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("prompt reset failed: %s", string(out))
	}

	// Verify reset
	code, out = executeCmd("prompt", "show", "agenda")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("prompt show after reset failed: %s", string(out))
	}
	lines = parseJSONLMaps(out)
	data = unwrapData(lines[0])
	text, _ = data["text"].(string)
	if text == "test prompt" {
		t.Error("expected default prompt after reset, still has custom text")
	}
}

// TestBackupCommandsNoDaemon verifies that backup commands work without daemon.
func TestBackupCommandsNoDaemon(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// backup list (may be empty, should succeed)
	code, out := executeCmd("backup", "list")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("backup list failed: %s", string(out))
	}

	// backup config show
	code, out = executeCmd("backup", "config", "show")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("backup config show failed: %s", string(out))
	}

	validateAllEnvelopes(t, out)
}

// TestBackupConfigSetNoDaemonSync verifies that backup config set works
// without daemon (no sync error).
func TestBackupConfigSetNoDaemonSync(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// backup config set (should succeed without daemon sync)
	code, out := executeCmd("backup", "config", "set", "--enabled")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("backup config set failed (daemon sync removed, should work): %s", string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "result" {
		t.Errorf("expected type=result, got %v", lines[0]["type"])
	}

	validateAllEnvelopes(t, out)
}

// TestNoDaemonErrorCodes verifies that daemon-specific error codes
// (daemon_not_running, daemon_start_timeout, backup_sync_failed) are no longer
// registered in the error code registry.
func TestNoDaemonErrorCodes(t *testing.T) {
	if app == nil {
		InitApp()
	}
	reg := app.Registry()
	if reg == nil {
		t.Fatal("error code registry is nil")
	}

	// Verify storage_locked is registered
	storageLockedExit := reg.ToExitCode("storage_locked")
	if storageLockedExit != agentsdk.ExitLockConflict {
		t.Errorf("expected storage_locked → exit code %d, got %d", agentsdk.ExitLockConflict, storageLockedExit)
	}

	// Verify lock_conflict is still registered
	lockConflictExit := reg.ToExitCode("lock_conflict")
	if lockConflictExit != agentsdk.ExitLockConflict {
		t.Errorf("expected lock_conflict → exit code %d, got %d", agentsdk.ExitLockConflict, lockConflictExit)
	}
}

// TestExportJSONNoDaemon verifies that export works without daemon.
func TestExportJSONNoDaemon(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Add a record
	code, _ := executeCmd("add", "--type", "log", "--title", "export test", "--date", "2025-01-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed")
	}

	// Export as JSON
	resetExportFlags()
	code, out := executeCmd("export", "--format", "json")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("export failed: %s", string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}

	validateAllEnvelopes(t, out)
}

// TestImportJSONNoDaemon verifies that import works without daemon.
func TestImportJSONNoDaemon(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Create import file
	importFile := filepath.Join(tmpHome, "import.json")
	records := []map[string]interface{}{
		{
			"type":  "log",
			"title": "imported entry 1",
			"date":  "2025-01-01",
		},
		{
			"type":  "task",
			"title": "imported entry 2",
			"date":  "2025-01-01",
		},
	}
	data, _ := json.Marshal(records)
	if err := os.WriteFile(importFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	// Import
	code, out := executeCmd("import", "--file", importFile)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("import failed: %s", string(out))
	}

	// Verify 2 records were imported — find the summary line
	lines := parseJSONLMaps(out)
	// The last result line should be the summary with "imported" count
	for _, line := range lines {
		data := unwrapData(line)
		if data == nil {
			continue
		}
		imported, ok := data["imported"].(float64)
		if ok && int(imported) < 2 {
			t.Errorf("expected at least 2 imported records, got %d", int(imported))
		}
	}

	validateAllEnvelopes(t, out)
}

// TestCancelWithoutDaemon verifies that cancel works without daemon.
func TestCancelWithoutDaemon(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Add (task type — logs can't be cancelled)
	code, out := executeCmd("add", "--type", "task", "--title", "cancel test", "--date", "2025-01-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: %s", string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	// Cancel
	code, out = executeCmd("cancel", shortID)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0 for cancel, got %d; output: %s", code, string(out))
	}

	lines = parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	data = unwrapData(lines[0])
	// Cancel returns the record under "record" key
	record, ok := data["record"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'record' in cancel output, got %v", data)
	}
	status, _ := record["status"].(string)
	if status != "cancelled" {
		t.Errorf("expected status=cancelled, got %q", status)
	}

	validateAllEnvelopes(t, out)
}

// TestAllCommandsNoDaemonSummary is a quick smoke test that runs the most
// important commands and verifies they all return exit 0 or the expected
// error code, without needing a running daemon.
func TestAllCommandsNoDaemonSummary(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	code, _ := executeCmd("config", "init")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("config init failed")
	}

	// Add a record (task type — logs can't be completed)
	code, out := executeCmd("add", "--type", "task", "--title", "smoke test", "--date", "2025-01-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: %s", string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	// Verify all commands work
	tests := []struct {
		args        []string
		expectCode  int
		description string
	}{
		{[]string{"list"}, agentsdk.ExitSuccess, "list"},
		{[]string{"list", "--date", "2025-01-01"}, agentsdk.ExitSuccess, "list with date"},
		{[]string{"complete", shortID}, agentsdk.ExitSuccess, "complete"},
		{[]string{"report", "date", "2025-01-01"}, agentsdk.ExitSuccess, "report date"},
		{[]string{"export", "--format", "json"}, agentsdk.ExitSuccess, "export json"},
		{[]string{"digest", "list"}, agentsdk.ExitSuccess, "digest list"},
		{[]string{"prompt", "list"}, agentsdk.ExitSuccess, "prompt list"},
		{[]string{"backup", "list"}, agentsdk.ExitSuccess, "backup list"},
		{[]string{"backup", "config", "show"}, agentsdk.ExitSuccess, "backup config show"},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			resetExportFlags()
			resetListFlags()
			code, out := executeCmd(tc.args...)
			if code != tc.expectCode {
				t.Errorf("expected exit code %d for %s, got %d; output: %s",
					tc.expectCode, strings.Join(tc.args, " "), code, string(out))
			}
		})
	}
}
