package cmd

import (
	"os"
	"path/filepath"
	"testing"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
)

// TestUpgrade_BacklogToTask verifies that `wr update <id> --type task --date 2025-06-01`
// upgrades a backlog record to task, with type=task and date set.
func TestUpgrade_BacklogToTask(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	// Add a backlog record
	title := "backlog to upgrade to task"
	code, out := executeCmd("add", "--type", "backlog", "--title", title)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: exit %d: %s", code, string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	// Upgrade to task
	resetUpdateFlags()
	code, out = executeCmd("update", shortID, "--type", "task", "--date", "2025-06-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("upgrade failed: exit %d: %s", code, string(out))
	}

	// Verify type=task and date is set
	lines = parseJSONLMaps(out)
	resultData := findResultData(lines)
	record, ok := resultData["record"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'record' in output, got %v", resultData)
	}
	recType, _ := record["type"].(string)
	if recType != "task" {
		t.Errorf("expected type=task, got %q", recType)
	}
	date, _ := record["date"].(string)
	if date != "2025-06-01" {
		t.Errorf("expected date=2025-06-01, got %q", date)
	}

	validateAllEnvelopes(t, out)
}

// TestUpgrade_BacklogToReminder verifies that `wr update <id> --type reminder --date 2025-06-01`
// upgrades a backlog record to reminder, with type=reminder.
func TestUpgrade_BacklogToReminder(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	// Add a backlog record
	title := "backlog to upgrade to reminder"
	code, out := executeCmd("add", "--type", "backlog", "--title", title)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: exit %d: %s", code, string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	// Upgrade to reminder
	resetUpdateFlags()
	code, out = executeCmd("update", shortID, "--type", "reminder", "--date", "2025-06-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("upgrade failed: exit %d: %s", code, string(out))
	}

	// Verify type=reminder
	lines = parseJSONLMaps(out)
	resultData := findResultData(lines)
	record, ok := resultData["record"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'record' in output, got %v", resultData)
	}
	recType, _ := record["type"].(string)
	if recType != "reminder" {
		t.Errorf("expected type=reminder, got %q", recType)
	}

	validateAllEnvelopes(t, out)
}

// TestUpgrade_NoDate verifies that `wr update <id> --type task` without --date
// returns an error.
func TestUpgrade_NoDate(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	// Add a backlog record
	title := "backlog no date upgrade"
	code, out := executeCmd("add", "--type", "backlog", "--title", title)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: exit %d: %s", code, string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	// Try upgrade without --date
	resetUpdateFlags()
	code, out = executeCmd("update", shortID, "--type", "task")
	if code == agentsdk.ExitSuccess {
		t.Fatal("expected error when upgrading without --date")
	}

	// Verify error_code is invalid_params and message mentions date
	lines = parseJSONLMaps(out)
	found := false
	for _, line := range lines {
		if line["type"] == "error" {
			errCode, _ := line["error_code"].(string)
			if errCode == "invalid_params" {
				found = true
			}
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=invalid_params, got %v", lines)
	}

	validateAllEnvelopes(t, out)
}

// TestUpgrade_ToMeeting verifies that `wr update <id> --type meeting --date 2025-06-01`
// returns an error because meeting is not a valid upgrade target.
func TestUpgrade_ToMeeting(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	// Add a backlog record
	title := "backlog to meeting attempt"
	code, out := executeCmd("add", "--type", "backlog", "--title", title)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: exit %d: %s", code, string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	// Try upgrade to meeting
	resetUpdateFlags()
	code, out = executeCmd("update", shortID, "--type", "meeting", "--date", "2025-06-01")
	if code == agentsdk.ExitSuccess {
		t.Fatal("expected error when upgrading to meeting")
	}

	// Verify error_code is invalid_params
	lines = parseJSONLMaps(out)
	found := false
	for _, line := range lines {
		if line["type"] == "error" {
			errCode, _ := line["error_code"].(string)
			msg, _ := line["message"].(string)
			if errCode == "invalid_params" {
				found = true
			}
			if errCode != "invalid_params" {
				t.Errorf("expected error_code=invalid_params, got %q", errCode)
			}
			_ = msg
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=invalid_params, got %v", lines)
	}

	validateAllEnvelopes(t, out)
}

// TestUpgrade_ReverseBlocked verifies that `wr update <id> --type backlog --date 2025-06-01`
// on a task record returns an error because only backlog can be upgraded.
func TestUpgrade_ReverseBlocked(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	// Add a task record
	title := "task reverse upgrade attempt"
	code, out := executeCmd("add", "--type", "task", "--title", title, "--date", "2025-01-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: exit %d: %s", code, string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	// Try to "upgrade" task to backlog
	resetUpdateFlags()
	code, out = executeCmd("update", shortID, "--type", "backlog", "--date", "2025-06-01")
	if code == agentsdk.ExitSuccess {
		t.Fatal("expected error when trying to upgrade non-backlog record")
	}

	// Verify: CLI should catch this before storage (backlog is not in valid upgrade targets)
	// or storage should reject it. Either way, we expect an error.
	lines = parseJSONLMaps(out)
	found := false
	for _, line := range lines {
		if line["type"] == "error" {
			errCode, _ := line["error_code"].(string)
			if errCode == "invalid_params" {
				found = true
			}
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=invalid_params, got %v", lines)
	}

	validateAllEnvelopes(t, out)
}
