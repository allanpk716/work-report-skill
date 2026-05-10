package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
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
