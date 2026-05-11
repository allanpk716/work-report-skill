package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helper to create a temp directory and return its path
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wr-migration-test-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// helper to write a file with given content
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// helper to read a file and return its content as string
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestMigration_NoLogsDir tests that migration writes a marker when no logs/ dir exists.
func TestMigration_NoLogsDir(t *testing.T) {
	baseDir := tempDir(t)

	err := MigrateIfNeeded(baseDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Marker should exist
	markerPath := filepath.Join(baseDir, migrationMarker)
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("marker file not created: %v", err)
	}

	// Running again should be a no-op
	err = MigrateIfNeeded(baseDir)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
}

// TestMigrate_MarkerAlreadyExists tests that migration is a no-op when marker exists.
func TestMigration_MarkerAlreadyExists(t *testing.T) {
	baseDir := tempDir(t)

	// Pre-create marker
	writeFile(t, filepath.Join(baseDir, migrationMarker), "migrated at 2026-01-01\n")

	err := MigrateIfNeeded(baseDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestMigrate_LogsWithJSON tests full migration of JSON files from logs/ to done_things/.
func TestMigration_LogsWithJSON(t *testing.T) {
	baseDir := tempDir(t)
	logsDir := filepath.Join(baseDir, oldDirName)

	// Create sample log files with old "type":"log"
	sample1 := `{
  "type": "log",
  "title": "Completed API review",
  "description": "Reviewed PR #123",
  "date": "2026-01-15",
  "time": "10:30",
  "status": "active",
  "saved_at": "2026-01-15T10:30:00Z",
  "short_id": "abc123"
}`
	sample2 := `{
  "type": "log",
  "title": "Fixed bug in auth module",
  "date": "2026-01-16",
  "time": "14:00",
  "status": "active",
  "saved_at": "2026-01-16T14:00:00Z",
  "short_id": "def456"
}`

	writeFile(t, filepath.Join(logsDir, "2026", "01", "15", "20260115_103000_1.json"), sample1)
	writeFile(t, filepath.Join(logsDir, "2026", "01", "16", "20260116_140000_2.json"), sample2)

	err := MigrateIfNeeded(baseDir)
	if err != nil {
		t.Fatalf("migration error: %v", err)
	}

	// Check that done_things/ has the migrated files
	doneThingsDir := filepath.Join(baseDir, "done_things")
	f1 := readFile(t, filepath.Join(doneThingsDir, "2026", "01", "15", "20260115_103000_1.json"))
	f2 := readFile(t, filepath.Join(doneThingsDir, "2026", "01", "16", "20260116_140000_2.json"))

	// Type should be updated
	if !strings.Contains(f1, `"type": "done_things"`) && !strings.Contains(f1, `"type":"done_things"`) {
		t.Errorf("file1 type not updated, got: %s", f1)
	}
	if strings.Contains(f1, `"type": "log"`) || strings.Contains(f1, `"type":"log"`) {
		t.Errorf("file1 still has old type: %s", f1)
	}
	if !strings.Contains(f2, `"type": "done_things"`) && !strings.Contains(f2, `"type":"done_things"`) {
		t.Errorf("file2 type not updated, got: %s", f2)
	}

	// logs/ directory should be removed
	if _, err := os.Stat(logsDir); !os.IsNotExist(err) {
		t.Errorf("logs/ directory should have been removed")
	}

	// Marker should exist
	if _, err := os.Stat(filepath.Join(baseDir, migrationMarker)); err != nil {
		t.Fatalf("marker file not created: %v", err)
	}

	// Verify the migrated files are valid JSON with correct type
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(f1), &probe); err != nil {
		t.Fatalf("file1 invalid JSON: %v", err)
	}
	if probe.Type != "done_things" {
		t.Errorf("file1 type = %q, want %q", probe.Type, "done_things")
	}
}

// TestMigrate_PartialMigration tests crash recovery: some files already in done_things/.
func TestMigration_PartialMigration(t *testing.T) {
	baseDir := tempDir(t)
	logsDir := filepath.Join(baseDir, oldDirName)
	doneDir := filepath.Join(baseDir, "done_things")

	// File 1 already migrated (in done_things/)
	writeFile(t, filepath.Join(doneDir, "2026", "01", "15", "20260115_103000_1.json"),
		`{"type":"done_things","title":"Already migrated","date":"2026-01-15","status":"active","saved_at":"2026-01-15T10:30:00Z"}`)

	// File 2 still in logs/
	writeFile(t, filepath.Join(logsDir, "2026", "01", "16", "20260116_140000_2.json"),
		`{"type":"log","title":"Needs migration","date":"2026-01-16","status":"active","saved_at":"2026-01-16T14:00:00Z"}`)

	err := MigrateIfNeeded(baseDir)
	if err != nil {
		t.Fatalf("migration error: %v", err)
	}

	// Both files should exist in done_things/
	f1 := readFile(t, filepath.Join(doneDir, "2026", "01", "15", "20260115_103000_1.json"))
	if !strings.Contains(f1, "Already migrated") {
		t.Errorf("file1 content changed unexpectedly: %s", f1)
	}

	f2 := readFile(t, filepath.Join(doneDir, "2026", "01", "16", "20260116_140000_2.json"))
	if !strings.Contains(f2, `"type":"done_things"`) && !strings.Contains(f2, `"type": "done_things"`) {
		t.Errorf("file2 type not updated: %s", f2)
	}

	// logs/ should be removed
	if _, err := os.Stat(logsDir); !os.IsNotExist(err) {
		t.Errorf("logs/ directory should have been removed")
	}

	// Marker should exist
	if _, err := os.Stat(filepath.Join(baseDir, migrationMarker)); err != nil {
		t.Fatalf("marker file not created: %v", err)
	}
}

// TestMigrate_NonJSONFiles tests that non-JSON files are skipped.
func TestMigration_NonJSONFiles(t *testing.T) {
	baseDir := tempDir(t)
	logsDir := filepath.Join(baseDir, oldDirName)

	writeFile(t, filepath.Join(logsDir, "notes.txt"), "some random notes")
	writeFile(t, filepath.Join(logsDir, "2026", "01", "15", "20260115_103000_1.json"),
		`{"type":"log","title":"Valid log","date":"2026-01-15","status":"active","saved_at":"2026-01-15T10:30:00Z"}`)

	err := MigrateIfNeeded(baseDir)
	if err != nil {
		t.Fatalf("migration error: %v", err)
	}

	// JSON file should be migrated
	doneDir := filepath.Join(baseDir, "done_things")
	if _, err := os.Stat(filepath.Join(doneDir, "2026", "01", "15", "20260115_103000_1.json")); err != nil {
		t.Errorf("JSON file not migrated: %v", err)
	}

	// Non-JSON file should NOT be in done_things/
	if _, err := os.Stat(filepath.Join(doneDir, "notes.txt")); !os.IsNotExist(err) {
		t.Errorf("non-JSON file should not be migrated")
	}
}

// TestMigrate_MalformedJSON tests that malformed JSON files are still moved.
func TestMigration_MalformedJSON(t *testing.T) {
	baseDir := tempDir(t)
	logsDir := filepath.Join(baseDir, oldDirName)

	writeFile(t, filepath.Join(logsDir, "2026", "01", "15", "bad.json"),
		`{not valid json at all!!!`)

	err := MigrateIfNeeded(baseDir)
	if err != nil {
		t.Fatalf("migration error: %v", err)
	}

	// File should still be moved to done_things/
	doneDir := filepath.Join(baseDir, "done_things")
	content := readFile(t, filepath.Join(doneDir, "2026", "01", "15", "bad.json"))
	if !strings.Contains(content, "not valid json") {
		t.Errorf("malformed JSON content changed unexpectedly: %s", content)
	}
}

// TestMigrate_MarkerContent verifies the marker file contains a timestamp.
func TestMigration_MarkerContent(t *testing.T) {
	baseDir := tempDir(t)

	err := MigrateIfNeeded(baseDir)
	if err != nil {
		t.Fatalf("migration error: %v", err)
	}

	content := readFile(t, filepath.Join(baseDir, migrationMarker))
	if !strings.Contains(content, "migrated at") {
		t.Errorf("marker content missing timestamp: %s", content)
	}

	// Should contain a valid date
	// Extract the timestamp portion
	parts := strings.SplitN(content, "migrated at ", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected marker content: %s", content)
	}
	ts := strings.TrimSpace(parts[1])
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Errorf("marker timestamp not valid RFC3339: %q: %v", ts, err)
	}
}

// TestMigrate_Idempotent tests that calling MigrateIfNeeded multiple times is safe.
func TestMigration_Idempotent(t *testing.T) {
	baseDir := tempDir(t)

	// First call — no logs/ dir
	if err := MigrateIfNeeded(baseDir); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Second call — marker exists, should be no-op
	if err := MigrateIfNeeded(baseDir); err != nil {
		t.Fatalf("second call: %v", err)
	}

	// Create logs/ dir after marker exists
	logsDir := filepath.Join(baseDir, oldDirName)
	writeFile(t, filepath.Join(logsDir, "2026", "01", "15", "20260115_103000_1.json"),
		`{"type":"log","title":"Late file","date":"2026-01-15","status":"active","saved_at":"2026-01-15T10:30:00Z"}`)

	// Third call — marker exists, should NOT migrate (already marked done)
	if err := MigrateIfNeeded(baseDir); err != nil {
		t.Fatalf("third call: %v", err)
	}

	// logs/ should still exist (not migrated because marker was already written)
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		t.Errorf("logs/ dir should still exist when marker was pre-written")
	}
}

// TestMigrate_LogsIsFile tests edge case where "logs" is a file, not a directory.
func TestMigration_LogsIsFile(t *testing.T) {
	baseDir := tempDir(t)
	writeFile(t, filepath.Join(baseDir, oldDirName), "I am a file, not a dir")

	err := MigrateIfNeeded(baseDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Marker should still be written
	if _, err := os.Stat(filepath.Join(baseDir, migrationMarker)); err != nil {
		t.Fatalf("marker not created: %v", err)
	}
}
