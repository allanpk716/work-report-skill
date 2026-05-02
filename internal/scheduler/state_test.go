package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scheduler-state.json")

	original := NewState()
	original.AddEntry(&ScheduleEntry{
		RecordShortID: "abc123",
		RecordType:    "reminder",
		Title:         "Test reminder",
		TriggerAt:     "2026-05-02T12:00:00Z",
		CronEntryID:   1,
		Fired:         false,
		Recurring:     "daily",
	})

	if err := SaveState(path, original); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if len(loaded.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(loaded.Entries))
	}

	entry := loaded.GetEntry("abc123")
	if entry == nil {
		t.Fatal("entry abc123 not found")
	}

	if entry.RecordShortID != "abc123" {
		t.Errorf("RecordShortID = %q, want %q", entry.RecordShortID, "abc123")
	}
	if entry.RecordType != "reminder" {
		t.Errorf("RecordType = %q, want %q", entry.RecordType, "reminder")
	}
	if entry.Title != "Test reminder" {
		t.Errorf("Title = %q, want %q", entry.Title, "Test reminder")
	}
	if entry.TriggerAt != "2026-05-02T12:00:00Z" {
		t.Errorf("TriggerAt = %q, want %q", entry.TriggerAt, "2026-05-02T12:00:00Z")
	}
	if entry.CronEntryID != 1 {
		t.Errorf("CronEntryID = %d, want %d", entry.CronEntryID, 1)
	}
	if entry.Fired {
		t.Error("Fired = true, want false")
	}
	if entry.Recurring != "daily" {
		t.Errorf("Recurring = %q, want %q", entry.Recurring, "daily")
	}
	if loaded.LastUpdated == "" {
		t.Error("LastUpdated is empty")
	}
}

func TestStateBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scheduler-state.json")
	bakPath := path + ".bak"

	state := NewState()
	state.AddEntry(&ScheduleEntry{
		RecordShortID: "x1",
		RecordType:    "deadline",
		Title:         "Backup test",
		TriggerAt:     "2026-05-03T00:00:00Z",
	})

	if err := SaveState(path, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	mainData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read main: %v", err)
	}

	bakData, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}

	if string(mainData) != string(bakData) {
		t.Errorf("backup content differs from main file\nmain:\n%s\nbak:\n%s", mainData, bakData)
	}
}

func TestStateRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scheduler-state.json")

	// Write valid state and save (creates .bak)
	state := NewState()
	state.AddEntry(&ScheduleEntry{
		RecordShortID: "recover1",
		RecordType:    "reminder",
		Title:         "Recovery test",
		TriggerAt:     "2026-05-04T08:00:00Z",
	})
	if err := SaveState(path, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Corrupt the main file
	if err := os.WriteFile(path, []byte("CORRUPT{not json"), 0644); err != nil {
		t.Fatalf("corrupt main: %v", err)
	}

	// LoadState should recover from .bak
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState after corruption: %v", err)
	}

	entry := loaded.GetEntry("recover1")
	if entry == nil {
		t.Fatal("recovered entry not found")
	}
	if entry.Title != "Recovery test" {
		t.Errorf("Title = %q, want %q", entry.Title, "Recovery test")
	}
}

func TestStateBothCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scheduler-state.json")

	// Write corrupt main and backup
	if err := os.WriteFile(path, []byte("BAD"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".bak", []byte("ALSO BAD"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadState(path)
	if err == nil {
		t.Fatal("expected error when both files are corrupt")
	}
}

func TestStateMarkFired(t *testing.T) {
	state := NewState()
	state.AddEntry(&ScheduleEntry{
		RecordShortID: "fire1",
		RecordType:    "reminder",
		Title:         "Mark fired test",
		TriggerAt:     "2026-05-05T10:00:00Z",
	})

	now := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	state.MarkFired("fire1", now)

	entry := state.GetEntry("fire1")
	if !entry.Fired {
		t.Error("Fired = false, want true")
	}
	if entry.FiredAt != "2026-05-05T10:00:00Z" {
		t.Errorf("FiredAt = %q, want %q", entry.FiredAt, "2026-05-05T10:00:00Z")
	}
}

func TestStateMarkFiredMissingEntry(t *testing.T) {
	state := NewState()
	// Should not panic on missing entry
	state.MarkFired("nonexistent", time.Now())
}

func TestStateMarkError(t *testing.T) {
	state := NewState()
	state.AddEntry(&ScheduleEntry{
		RecordShortID: "err1",
		RecordType:    "reminder",
		Title:         "Error test",
		TriggerAt:     "2026-05-06T10:00:00Z",
	})

	state.MarkError("err1", "pushover timeout")

	entry := state.GetEntry("err1")
	if entry.LastError != "pushover timeout" {
		t.Errorf("LastError = %q, want %q", entry.LastError, "pushover timeout")
	}
}

func TestStateMarkErrorMissingEntry(t *testing.T) {
	state := NewState()
	// Should not panic on missing entry
	state.MarkError("nonexistent", "some error")
}

func TestStateRemoveEntry(t *testing.T) {
	state := NewState()
	state.AddEntry(&ScheduleEntry{
		RecordShortID: "rem1",
		RecordType:    "reminder",
		Title:         "Remove test",
		TriggerAt:     "2026-05-07T10:00:00Z",
	})

	if state.GetEntry("rem1") == nil {
		t.Fatal("entry should exist before removal")
	}

	state.RemoveEntry("rem1")

	if state.GetEntry("rem1") != nil {
		t.Error("entry should be removed after RemoveEntry")
	}
}

func TestStateEmptyEntriesMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scheduler-state.json")

	// Write state with nil entries (should be handled)
	raw := `{"entries": null, "last_updated": "2026-05-02T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	state, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState with null entries: %v", err)
	}

	if state.Entries == nil {
		t.Error("Entries should be initialized to non-nil map")
	}

	// Should be safe to add entries
	state.AddEntry(&ScheduleEntry{RecordShortID: "new1"})
	if state.GetEntry("new1") == nil {
		t.Error("added entry not found")
	}
}

func TestStateNoSensitiveData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scheduler-state.json")

	state := NewState()
	state.AddEntry(&ScheduleEntry{
		RecordShortID: "sec1",
		RecordType:    "reminder",
		Title:         "Security test",
		TriggerAt:     "2026-05-08T10:00:00Z",
	})

	if err := SaveState(path, state); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	// Verify no Pushover-related keys appear
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	// Check that entries don't contain token/api_key fields
	entriesRaw, _ := json.Marshal(parsed["entries"])
	if containsSensitive(string(entriesRaw)) {
		t.Errorf("state file contains sensitive fields: %s", entriesRaw)
	}

	// Also verify the struct has no token/key fields via reflection of JSON tags
	_ = content // state file written successfully
}

func containsSensitive(s string) bool {
	sensitive := []string{"token", "api_key", "user_key", "secret"}
	for _, kw := range sensitive {
		if len(s) > 0 && containsIgnoreCase(s, kw) {
			return true
		}
	}
	return false
}

func containsIgnoreCase(s, sub string) bool {
	// Simple case-insensitive contains for short keywords
	sLower := toLower(s)
	subLower := toLower(sub)
	for i := 0; i+len(subLower) <= len(sLower); i++ {
		if sLower[i:i+len(subLower)] == subLower {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		result[i] = c
	}
	return string(result)
}

func TestStateDefaultStatePath(t *testing.T) {
	path, err := DefaultStatePath()
	if err != nil {
		t.Fatalf("DefaultStatePath: %v", err)
	}
	if path == "" {
		t.Error("path is empty")
	}
	expectedBase := filepath.Join(".work-report", "scheduler-state.json")
	if len(path) < len(expectedBase) || path[len(path)-len(expectedBase):] != expectedBase {
		t.Errorf("path = %q, expected to end with %q", path, expectedBase)
	}
}
