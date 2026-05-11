package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
	"wr/internal/config"
)

// TestCompleteTitleDefaultDate verifies that `wr complete --title 'X'` defaults
// --date to today when --date is omitted (matching wr add behavior).
func TestCompleteTitleDefaultDate(t *testing.T) {
	ResetCompleteFlags()
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Add a task record for today
	today := todayInLocation(loadConfig())
	title := "complete default date test"
	code, out := executeCmd("add", "--type", "task", "--title", title, "--date", today)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: %s", string(out))
	}

	// Complete by title without --date — should default to today
	ResetCompleteFlags()
	code, out = executeCmd("complete", "--title", title)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit 0, got %d; output: %s", code, string(out))
	}

	// Verify status is completed
	lines := parseJSONLMaps(out)
	data := findResultData(lines)
	record, ok := data["record"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'record' in output, got %v", data)
	}
	status, _ := record["status"].(string)
	if status != "completed" {
		t.Errorf("expected status=completed, got %q", status)
	}

	validateAllEnvelopes(t, out)
}

// TestCompleteTitleExplicitDate verifies that explicit --date still works.
func TestCompleteTitleExplicitDate(t *testing.T) {
	ResetCompleteFlags()
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	date := "2025-03-15"
	title := "complete explicit date test"
	code, _ := executeCmd("add", "--type", "task", "--title", title, "--date", date)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed")
	}

	// Complete with explicit --date
	ResetCompleteFlags()
	code, out := executeCmd("complete", "--title", title, "--date", date)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit 0, got %d; output: %s", code, string(out))
	}

	validateAllEnvelopes(t, out)
}

// TestCancelTitleDefaultDate verifies that `wr cancel --title 'X'` defaults
// --date to today when --date is omitted.
func TestCancelTitleDefaultDate(t *testing.T) {
	ResetCancelFlags()
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	today := todayInLocation(loadConfig())
	title := "cancel default date test"
	code, _ := executeCmd("add", "--type", "task", "--title", title, "--date", today)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed")
	}

	// Cancel by title without --date — should default to today
	ResetCancelFlags()
	code, out := executeCmd("cancel", "--title", title)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit 0, got %d; output: %s", code, string(out))
	}

	// Verify status is cancelled
	lines := parseJSONLMaps(out)
	data := findResultData(lines)
	record, ok := data["record"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'record' in output, got %v", data)
	}
	status, _ := record["status"].(string)
	if status != "cancelled" {
		t.Errorf("expected status=cancelled, got %q", status)
	}

	validateAllEnvelopes(t, out)
}

// TestCancelTitleExplicitDate verifies that explicit --date still works.
func TestCancelTitleExplicitDate(t *testing.T) {
	ResetCancelFlags()
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	date := "2025-03-15"
	title := "cancel explicit date test"
	code, _ := executeCmd("add", "--type", "task", "--title", title, "--date", date)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed")
	}

	ResetCancelFlags()
	code, out := executeCmd("cancel", "--title", title, "--date", date)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit 0, got %d; output: %s", code, string(out))
	}

	validateAllEnvelopes(t, out)
}

// TestCompleteTitleWrongDateStillFails verifies that when the user provides an
// explicit --date that doesn't match, the lookup still fails (no silent override).
func TestCompleteTitleWrongDateStillFails(t *testing.T) {
	ResetCompleteFlags()
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	date := "2025-03-15"
	title := "complete wrong date test"
	code, _ := executeCmd("add", "--type", "task", "--title", title, "--date", date)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed")
	}

	// Try to complete with wrong explicit date — should fail
	ResetCompleteFlags()
	code, out := executeCmd("complete", "--title", title, "--date", "2025-12-31")
	if code == agentsdk.ExitSuccess {
		t.Fatal("expected failure for wrong date lookup")
	}

	validateAllEnvelopes(t, out)
}

// TestCompleteByIDStillWorks verifies positional ID still works without title/date.
func TestCompleteByIDStillWorks(t *testing.T) {
	ResetCompleteFlags()
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	date := "2025-06-01"
	title := fmt.Sprintf("complete by id test %d", os.Getpid())
	code, out := executeCmd("add", "--type", "task", "--title", title, "--date", date)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: %s", string(out))
	}

	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	ResetCompleteFlags()
	code, out = executeCmd("complete", shortID)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("complete by id failed: %s", string(out))
	}

	validateAllEnvelopes(t, out)
}

// TestCompleteTitleNotFound verifies that `wr complete --title 'nonexistent'`
// returns a record_not_found error when no matching record exists.
func TestCompleteTitleNotFound(t *testing.T) {
	ResetCompleteFlags()
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Attempt to complete a non-existent title without --date
	ResetCompleteFlags()
	code, out := executeCmd("complete", "--title", "nonexistent_task_xyz")
	if code == agentsdk.ExitSuccess {
		t.Fatal("expected failure for non-existent title lookup")
	}

	// Verify error in output (title lookup uses invalid_params when no record found)
	lines := parseJSONLMaps(out)
	found := false
	for _, line := range lines {
		if line["type"] == "error" {
			errCode, _ := line["error_code"].(string)
			if errCode == "invalid_params" || errCode == "record_not_found" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("expected error in output for non-existent title, got %v", lines)
	}

	validateAllEnvelopes(t, out)
}

// TestLLMTimeoutDefaults verifies that a freshly loaded config defaults
// LLM text and vision timeouts to 30 seconds.
func TestLLMTimeoutDefaults(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config (creates a fresh config.json)
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Load config directly from the temp home
	cfgPath := filepath.Join(tmpHome, ".work-report", "config.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.LLM.Text.Timeout != 30 {
		t.Errorf("expected LLM.Text.Timeout=30, got %d", cfg.LLM.Text.Timeout)
	}
	if cfg.LLM.Vision.Timeout != 30 {
		t.Errorf("expected LLM.Vision.Timeout=30, got %d", cfg.LLM.Vision.Timeout)
	}
}

// TestCompleteDoneThingsReturnsTypeError verifies that `wr complete` on a
// done_things record returns a type_not_completable error with a clear
// message explaining that done_things are factual records.
func TestCompleteDoneThingsReturnsTypeError(t *testing.T) {
	ResetCompleteFlags()
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Add a done_things record
	date := "2025-06-15"
	title := "completed code review"
	code, out := executeCmd("add", "--type", "done_things", "--title", title, "--date", date)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add done_things failed: %s", string(out))
	}

	// Extract short_id from add output
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	// Attempt to complete it
	ResetCompleteFlags()
	code, out = executeCmd("complete", shortID)
	if code == agentsdk.ExitSuccess {
		t.Fatal("expected error when completing done_things record")
	}

	// Verify error_code is type_not_completable and message mentions "factual records"
	lines = parseJSONLMaps(out)
	found := false
	for _, line := range lines {
		if line["type"] == "error" {
			errCode, _ := line["error_code"].(string)
			msg, _ := line["message"].(string)
			if errCode == "type_not_completable" && strings.Contains(msg, "factual records") {
				found = true
			}
			if errCode != "type_not_completable" {
				t.Errorf("expected error_code=type_not_completable, got %q", errCode)
			}
			if !strings.Contains(msg, "factual records") {
				t.Errorf("expected message to mention 'factual records', got %q", msg)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected type_not_completable error, got %v", lines)
	}

	validateAllEnvelopes(t, out)
}

// TestCancelDoneThingsReturnsTypeError verifies that `wr cancel` on a
// done_things record returns a type_not_cancellable error with a clear
// message explaining that done_things are factual records.
func TestCancelDoneThingsReturnsTypeError(t *testing.T) {
	ResetCancelFlags()
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Add a done_things record
	date := "2025-06-15"
	title := "completed deployment"
	code, out := executeCmd("add", "--type", "done_things", "--title", title, "--date", date)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add done_things failed: %s", string(out))
	}

	// Extract short_id from add output
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	// Attempt to cancel it
	ResetCancelFlags()
	code, out = executeCmd("cancel", shortID)
	if code == agentsdk.ExitSuccess {
		t.Fatal("expected error when cancelling done_things record")
	}

	// Verify error_code is type_not_cancellable and message mentions "factual records"
	lines = parseJSONLMaps(out)
	found := false
	for _, line := range lines {
		if line["type"] == "error" {
			errCode, _ := line["error_code"].(string)
			msg, _ := line["message"].(string)
			if errCode == "type_not_cancellable" && strings.Contains(msg, "factual records") {
				found = true
			}
			if errCode != "type_not_cancellable" {
				t.Errorf("expected error_code=type_not_cancellable, got %q", errCode)
			}
			if !strings.Contains(msg, "factual records") {
				t.Errorf("expected message to mention 'factual records', got %q", msg)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected type_not_cancellable error, got %v", lines)
	}

	validateAllEnvelopes(t, out)
}
