package cmd

import (
	"os"
	"path/filepath"
	"testing"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
)

// resetUpdateFlags resets all package-level update flag variables to zero values.
func resetUpdateFlags() {
	updateTitle = ""
	updateDescription = ""
	updateDate = ""
	updateTime = ""
	updateLocation = ""
	updateTags = nil
	updatePriority = ""
	updateRemindBefore = ""
	updateRecurring = ""
	updateEndTime = ""
	updateRelatedPerson = ""
	updateParticipants = nil
	updateAgenda = ""
	updateNotes = ""
	updateProgress = ""
	updateNotifyPriority = ""
}

// TestUpdate_NotifyPriorityHigh verifies that --notify-priority high updates
// the notification_priority field on a record.
func TestUpdate_NotifyPriorityHigh(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	// Create a task record first
	code, out := executeCmd("add", "--type", "task", "--title", "notify update test", "--date", "2025-01-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: exit %d: %s", code, string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	id, _ := data["short_id"].(string)
	if id == "" {
		t.Fatal("expected short_id from add")
	}

	// Update notify-priority to high
	resetUpdateFlags()
	code, out = executeCmd("update", id, "--notify-priority", "high")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("update failed: exit %d: %s", code, string(out))
	}

	// Verify the updated record has notification_priority=high
	lines = parseJSONLMaps(out)
	for _, line := range lines {
		updatedData, ok := line["data"].(map[string]interface{})
		if !ok {
			continue
		}
		record, ok := updatedData["record"].(map[string]interface{})
		if !ok {
			continue
		}
		np, _ := record["notification_priority"].(string)
		if np != "high" {
			t.Errorf("expected notification_priority=high, got %q", np)
		}
	}
	validateAllEnvelopes(t, out)
}

// TestUpdate_NotifyPriorityInvalid verifies that an invalid --notify-priority
// returns an error with error_code=invalid_params.
func TestUpdate_NotifyPriorityInvalid(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	// Create a task record first
	code, out := executeCmd("add", "--type", "task", "--title", "bad priority update", "--date", "2025-01-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: exit %d: %s", code, string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	id, _ := data["short_id"].(string)
	if id == "" {
		t.Fatal("expected short_id from add")
	}

	// Try to update with invalid priority
	resetUpdateFlags()
	code, out = executeCmd("update", id, "--notify-priority", "urgent")
	if code != agentsdk.ExitInvalidParams {
		t.Fatalf("expected exit 2, got %d: %s", code, string(out))
	}

	lines = parseJSONLMaps(out)
	found := false
	for _, line := range lines {
		if line["error_code"] == "invalid_params" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=invalid_params, got %v", lines)
	}
	validateAllEnvelopes(t, out)
}

// TestUpdate_NotifyPriorityNormal verifies that --notify-priority normal sets
// the field to "normal" on a record.
func TestUpdate_NotifyPriorityNormal(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	// Create a task record (meetings auto-default to high, tasks default to empty)
	code, out := executeCmd("add", "--type", "task", "--title", "normal priority update", "--date", "2025-01-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: exit %d: %s", code, string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	id, _ := data["short_id"].(string)
	if id == "" {
		t.Fatal("expected short_id from add")
	}

	// Update notify-priority to normal
	resetUpdateFlags()
	code, out = executeCmd("update", id, "--notify-priority", "normal")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("update failed: exit %d: %s", code, string(out))
	}

	// Verify the updated record has notification_priority=normal
	lines = parseJSONLMaps(out)
	for _, line := range lines {
		updatedData, ok := line["data"].(map[string]interface{})
		if !ok {
			continue
		}
		record, ok := updatedData["record"].(map[string]interface{})
		if !ok {
			continue
		}
		np, _ := record["notification_priority"].(string)
		if np != "normal" {
			t.Errorf("expected notification_priority=normal, got %q", np)
		}
	}
	validateAllEnvelopes(t, out)
}
