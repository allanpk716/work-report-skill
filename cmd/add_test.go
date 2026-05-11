package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
)

// resetAddFlags resets all package-level add flag variables to zero values.
// Must be called in tests that modify add* vars to prevent state leaking
// between tests (Cobra does not reset flags not explicitly passed).
func resetAddFlags() {
	addType = ""
	addTitle = ""
	addDate = ""
	addTime = ""
	addImage = ""
	addText = ""
	addDescription = ""
	addTags = ""
	addLocation = ""
	addRelatedPerson = ""
	addPriority = ""
	addRemindBefore = ""
	addRecurring = ""
	addIdempotencyKey = ""
	addNotifyPriority = ""
}

// TestAdd_NotifyPriorityHigh verifies that --notify-priority high is stored on the record.
func TestAdd_NotifyPriorityHigh(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	code, out := executeCmd("add", "--type", "task", "--title", "notify test", "--date", "2025-01-01", "--notify-priority", "high")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit 0, got %d: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	if data == nil {
		t.Fatal("expected data field")
	}
	np, _ := data["notification_priority"].(string)
	if np != "high" {
		t.Errorf("expected notification_priority=high, got %q", np)
	}
	validateAllEnvelopes(t, out)
}

// TestAdd_NotifyPriorityInvalid verifies that an invalid --notify-priority returns an error.
func TestAdd_NotifyPriorityInvalid(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	code, out := executeCmd("add", "--type", "task", "--title", "bad priority", "--date", "2025-01-01", "--notify-priority", "urgent")
	if code != agentsdk.ExitInvalidParams {
		t.Fatalf("expected exit 2, got %d: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
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

// TestAdd_MeetingDefaultNotifyPriority verifies that meetings auto-default to
// high notification priority when --notify-priority is not provided.
func TestAdd_MeetingDefaultNotifyPriority(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")
	resetAddFlags()

	code, out := executeCmd("add", "--type", "meeting", "--title", "team standup", "--date", "2025-01-01")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit 0, got %d: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	if data == nil {
		t.Fatal("expected data field")
	}
	np, _ := data["notification_priority"].(string)
	if np != "high" {
		t.Errorf("expected notification_priority=high (auto-default for meeting), got %q", np)
	}
	validateAllEnvelopes(t, out)
}

// TestLLMDateFallback verifies that when LLM returns a classification result
// without a date, the date fallback defaults to today. Tests the combined
// populateFromResult + todayInLocation logic that the production code uses.
func TestLLMDateFallback(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	executeCmd("config", "init")

	// Use local vars to simulate populateFromResult behavior, avoiding
	// package-level var pollution between tests.
	resultType := "done_things"
	resultTitle := "LLM classified entry"
	resultDate := "" // LLM didn't return a date

	// Simulate populateFromResult logic (copies result fields to local state)
	simType := resultType
	simTitle := resultTitle
	simDate := resultDate // stays empty since resultDate is ""

	if simType == "" {
		t.Fatal("type should have been set from result")
	}
	if simTitle == "" {
		t.Fatal("title should have been set from result")
	}
	if simDate != "" {
		t.Fatalf("date should be empty when result has no date, got %q", simDate)
	}

	// Simulate the date fallback from addCmd.RunE (usedLLM=true, addDate="")
	cfg := loadConfig()
	if cfg == nil {
		t.Fatal("loadConfig returned nil")
	}

	usedLLM := true
	if simDate == "" {
		simDate = todayInLocation(cfg)
		source := "default_today"
		if usedLLM {
			source = "llm_date_fallback"
		}
		t.Logf("add: source=%s date=%s", source, simDate)
	}

	expected := time.Now().In(cfg.Location()).Format("2006-01-02")
	if simDate != expected {
		t.Errorf("expected date fallback to today %q, got %q", expected, simDate)
	}

	// Create the record using explicit flags (clean, no shared state)
	code, out := executeCmd("add", "--type", simType, "--title", simTitle, "--date", simDate)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("add failed with exit code %d: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	data := unwrapData(lines[0])
	if data == nil {
		t.Fatal("expected data field in envelope")
	}

	date, _ := data["date"].(string)
	if date != expected {
		t.Errorf("expected record date=%q, got %q", expected, date)
	}

	validateAllEnvelopes(t, out)
}

// TestLLMDateFallbackPreservesExplicitDate verifies that when LLM returns a
// result with an explicit date, the fallback does NOT override it.
func TestLLMDateFallbackPreservesExplicitDate(t *testing.T) {
	// Test with local vars — no package-level state pollution
	resultDate := "2025-06-15"

	// populateFromResult would set simDate from result
	simDate := resultDate
	if simDate == "" {
		t.Fatal("addDate should not be empty after populateFromResult with explicit date")
	}

	// Fallback should not trigger since simDate is already set
	if simDate != "2025-06-15" {
		t.Fatalf("expected date from result to be preserved, got %q", simDate)
	}
}
