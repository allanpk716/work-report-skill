package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
)

// TestAddBacklog_NoDate verifies that `wr add --type backlog --title 'xxx'`
// succeeds with an empty date field and valid JSONL output.
func TestAddBacklog_NoDate(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	title := "test backlog no date"
	code, out := executeCmd("add", "--type", "backlog", "--title", title)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit 0, got %d: %s", code, string(out))
	}

	// Verify date is empty
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	if data == nil {
		t.Fatal("expected data field")
	}
	date, _ := data["date"].(string)
	if date != "" {
		t.Errorf("expected date to be empty for backlog, got %q", date)
	}
	recType, _ := data["type"].(string)
	if recType != "backlog" {
		t.Errorf("expected type=backlog, got %q", recType)
	}
	shortID, _ := data["short_id"].(string)
	if shortID == "" {
		t.Error("expected non-empty short_id")
	}

	validateAllEnvelopes(t, out)
}

// TestAddBacklog_WithDateRejected verifies that `wr add --type backlog --title 'xxx' --date '2025-06-01'`
// returns an invalid_params error because backlog records cannot have a date.
func TestAddBacklog_WithDateRejected(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	title := "test backlog with date"
	code, out := executeCmd("add", "--type", "backlog", "--title", title, "--date", "2025-06-01")
	if code == agentsdk.ExitSuccess {
		t.Fatal("expected error when adding backlog with explicit date")
	}

	// Verify error_code is invalid_params and message mentions backlog/date
	lines := parseJSONLMaps(out)
	found := false
	for _, line := range lines {
		if line["type"] == "error" {
			errCode, _ := line["error_code"].(string)
			msg, _ := line["message"].(string)
			if errCode == "invalid_params" && strings.Contains(msg, "backlog") && strings.Contains(msg, "date") {
				found = true
			}
			if errCode != "invalid_params" {
				t.Errorf("expected error_code=invalid_params, got %q", errCode)
			}
			if !strings.Contains(msg, "backlog") {
				t.Errorf("expected message to mention 'backlog', got %q", msg)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected invalid_params error mentioning backlog and date, got %v", lines)
	}

	validateAllEnvelopes(t, out)
}

// TestListBacklog verifies that after adding 2 backlog records,
// `wr list --type backlog` returns count=2.
func TestListBacklog(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetListFlags()

	// Add 2 backlogs
	for i := 0; i < 2; i++ {
		resetAddFlags()
		title := fmt.Sprintf("backlog item %d", i)
		code, out := executeCmd("add", "--type", "backlog", "--title", title)
		if code != agentsdk.ExitSuccess {
			t.Fatalf("add backlog %d failed: %s", i, string(out))
		}
	}

	// List backlogs
	resetListFlags()
	code, out := executeCmd("list", "--type", "backlog")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("list failed: %s", string(out))
	}

	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	if data == nil {
		t.Fatal("expected data field")
	}
	count, _ := data["count"].(float64)
	if int(count) != 2 {
		t.Errorf("expected count=2, got %v", count)
	}

	entries, ok := data["entries"].([]interface{})
	if !ok {
		t.Fatal("expected 'entries' array")
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}

	validateAllEnvelopes(t, out)
}

// TestCompleteBacklog verifies that `wr complete <id>` on a backlog record
// succeeds and returns status=completed.
func TestCompleteBacklog(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	title := "backlog to complete"
	code, out := executeCmd("add", "--type", "backlog", "--title", title)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: %s", string(out))
	}

	// Extract short_id
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	// Complete the backlog
	ResetCompleteFlags()
	code, out = executeCmd("complete", shortID)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("complete failed: %s", string(out))
	}

	// Verify status is completed
	lines = parseJSONLMaps(out)
	data = findResultData(lines)
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

// TestCancelBacklog verifies that `wr cancel <id>` on a backlog record
// succeeds and returns status=cancelled.
func TestCancelBacklog(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	title := "backlog to cancel"
	code, out := executeCmd("add", "--type", "backlog", "--title", title)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: %s", string(out))
	}

	// Extract short_id
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	// Cancel the backlog
	ResetCancelFlags()
	code, out = executeCmd("cancel", shortID)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("cancel failed: %s", string(out))
	}

	// Verify status is cancelled
	lines = parseJSONLMaps(out)
	data = findResultData(lines)
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

// TestUpdateBacklogNotes verifies that `wr update <id> --description 'updated'`
// on a backlog record succeeds.
func TestUpdateBacklogNotes(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	title := "backlog to update"
	code, out := executeCmd("add", "--type", "backlog", "--title", title)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed: %s", string(out))
	}

	// Extract short_id
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	shortID, _ := data["short_id"].(string)

	// Update description
	code, out = executeCmd("update", shortID, "--description", "updated notes")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("update failed: %s", string(out))
	}

	// Verify update succeeded
	lines = parseJSONLMaps(out)
	data = findResultData(lines)
	record, ok := data["record"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'record' in output, got %v", data)
	}
	desc, _ := record["description"].(string)
	if desc != "updated notes" {
		t.Errorf("expected description='updated notes', got %q", desc)
	}

	validateAllEnvelopes(t, out)
}
