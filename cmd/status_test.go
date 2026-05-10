package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/config"
	"wr/internal/digest"
	"wr/internal/models"
	"wr/internal/storage"
)

// TestStatusBasic verifies that wr status outputs a JSONL result envelope
// with config and data sections, even when no config or records exist.
func TestStatusBasic(t *testing.T) {
	tmpHome, homeCleanup := setupTempHome(t)
	defer homeCleanup()

	cleanup := resetAppForTest(t, tmpHome)
	defer cleanup()

	// Capture JSONL output
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	resetConfigFlags()
	rootCmd.SetArgs([]string{"status"})
	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	if output == "" {
		t.Fatal("expected JSONL output from status, got empty string")
	}

	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}

	if envelope["type"] != "result" {
		t.Errorf("expected type=result, got %v", envelope["type"])
	}

	data, ok := envelope["data"].(map[string]interface{})
	if !ok {
		t.Fatal("envelope.data is not a map")
	}

	// Verify config section
	cfgSection, ok := data["config"].(map[string]interface{})
	if !ok {
		t.Fatal("data.config is not a map")
	}
	for _, key := range []string{"timezone", "data_dir", "pushover", "llm_text", "llm_vision"} {
		if _, ok := cfgSection[key]; !ok {
			t.Errorf("missing config.%s field", key)
		}
	}

	// Verify data section
	dataSection, ok := data["data"].(map[string]interface{})
	if !ok {
		t.Fatal("data.data is not a map")
	}
	for _, key := range []string{"total_records", "by_type", "by_status", "digests", "backups"} {
		if _, ok := dataSection[key]; !ok {
			t.Errorf("missing data.%s field", key)
		}
	}
}

// TestStatusWithConfig verifies that wr status shows "configured" for pushover
// and LLM when API keys are set.
func TestStatusWithConfig(t *testing.T) {
	tmpHome, homeCleanup := setupTempHome(t)
	defer homeCleanup()

	cleanup := resetAppForTest(t, tmpHome)
	defer cleanup()

	// Write a config with API keys
	stateDir := filepath.Join(tmpHome, ".work-report")
	cfgPath := filepath.Join(stateDir, "config.json")
	cfg := &config.Config{
		Timezone: "UTC",
	}
	cfg.LLM.Text.APIKey = "sk-test-123"
	cfg.LLM.Vision.APIKey = "sk-vision-456"
	cfg.Pushover.APIToken = "tok-abc"
	cfg.Pushover.UserKey = "user-xyz"
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	// Capture JSONL output
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	resetConfigFlags()
	rootCmd.SetArgs([]string{"status"})
	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}

	data := envelope["data"].(map[string]interface{})
	cfgSection := data["config"].(map[string]interface{})

	// Verify configured status
	if cfgSection["pushover"] != "configured" {
		t.Errorf("expected pushover=configured, got %v", cfgSection["pushover"])
	}
	if cfgSection["llm_text"] != "configured" {
		t.Errorf("expected llm_text=configured, got %v", cfgSection["llm_text"])
	}
	if cfgSection["llm_vision"] != "configured" {
		t.Errorf("expected llm_vision=configured, got %v", cfgSection["llm_vision"])
	}
}

// TestStatusWithRecords verifies that wr status counts records by type and status.
func TestStatusWithRecords(t *testing.T) {
	tmpHome, homeCleanup := setupTempHome(t)
	defer homeCleanup()

	cleanup := resetAppForTest(t, tmpHome)
	defer cleanup()

	// Write config
	stateDir := filepath.Join(tmpHome, ".work-report")
	cfgPath := filepath.Join(stateDir, "config.json")
	cfg := &config.Config{
		Timezone: "UTC",
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	// Load config back so DataDir is populated by applyDefaults
	loadedCfg, loadErr := config.Load(cfgPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}

	// Create records in the correct data dir
	store := storage.New(loadedCfg.DataDir)

	store.AddRecord(taskRecordPtr("Test Task", "2026-01-15"))
	store.AddRecord(taskRecordPtr("Another Task", "2026-01-15"))
	store.AddRecord(meetingRecordPtr("Team Standup", "2026-01-15"))
	store.AddRecord(logRecordPtr("Work log entry", "2026-01-15"))

	// Capture JSONL output
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	resetConfigFlags()
	rootCmd.SetArgs([]string{"status"})
	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}

	data := envelope["data"].(map[string]interface{})
	dataSection := data["data"].(map[string]interface{})

	total := int(dataSection["total_records"].(float64))
	if total != 4 {
		t.Errorf("expected total_records=4, got %d", total)
	}

	byType := dataSection["by_type"].(map[string]interface{})
	if int(byType["task"].(float64)) != 2 {
		t.Errorf("expected by_type.task=2, got %v", byType["task"])
	}
	if int(byType["meeting"].(float64)) != 1 {
		t.Errorf("expected by_type.meeting=1, got %v", byType["meeting"])
	}
	if int(byType["log"].(float64)) != 1 {
		t.Errorf("expected by_type.log=1, got %v", byType["log"])
	}
}

// TestStatusWithDigests verifies that wr status counts digest configs.
func TestStatusWithDigests(t *testing.T) {
	tmpHome, homeCleanup := setupTempHome(t)
	defer homeCleanup()

	cleanup := resetAppForTest(t, tmpHome)
	defer cleanup()

	// Write config
	stateDir := filepath.Join(tmpHome, ".work-report")
	cfgPath := filepath.Join(stateDir, "config.json")
	cfg := &config.Config{
		Timezone: "UTC",
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	// Create digests
	digestPath := filepath.Join(stateDir, "digests.json")
	ds := digest.NewStore(digestPath)
	ds.Add(digest.DigestConfig{
		Schedule:  "0 8 * * *",
		Scope:     digest.ScopeToday,
		Direction: digest.DirectionAgenda,
	})
	ds.Add(digest.DigestConfig{
		Schedule:  "0 18 * * 1-5",
		Scope:     digest.ScopeWeek,
		Direction: digest.DirectionSummary,
	})

	// Capture JSONL output
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	resetConfigFlags()
	rootCmd.SetArgs([]string{"status"})
	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}

	data := envelope["data"].(map[string]interface{})
	dataSection := data["data"].(map[string]interface{})

	digests := int(dataSection["digests"].(float64))
	if digests != 2 {
		t.Errorf("expected digests=2, got %d", digests)
	}
}

// TestStatusWithBackups verifies that wr status counts backup files.
func TestStatusWithBackups(t *testing.T) {
	tmpHome, homeCleanup := setupTempHome(t)
	defer homeCleanup()

	cleanup := resetAppForTest(t, tmpHome)
	defer cleanup()

	// Write config
	stateDir := filepath.Join(tmpHome, ".work-report")
	cfgPath := filepath.Join(stateDir, "config.json")
	cfg := &config.Config{
		Timezone: "UTC",
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	// Create backup output dir and fake backup files
	backupDir := filepath.Join(stateDir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Write backup config using proper JSON marshaling
	bCfgPath := filepath.Join(stateDir, "backup-config.json")
	bCfg := map[string]interface{}{
		"retention": map[string]int{"daily": 7, "weekly": 4, "monthly": 6},
		"output_dir": backupDir,
		"schedule": "",
		"enabled": false,
	}
	bCfgData, marshalErr := json.Marshal(bCfg)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err := os.WriteFile(bCfgPath, bCfgData, 0644); err != nil {
		t.Fatal(err)
	}

	// Create fake backup files
	for _, name := range []string{"wr-backup-20260115-080000.zip", "wr-backup-20260114-080000.zip", "wr-backup-20260113-080000.zip"} {
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("fake"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Capture JSONL output
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	resetConfigFlags()
	rootCmd.SetArgs([]string{"status"})
	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}

	data := envelope["data"].(map[string]interface{})
	dataSection := data["data"].(map[string]interface{})

	backups := int(dataSection["backups"].(float64))
	if backups != 3 {
		t.Errorf("expected backups=3, got %d", backups)
	}
}

// TestStatusEnvelopeValidation verifies all status outputs pass
// agentsdk.ValidateEnvelope().
func TestStatusEnvelopeValidation(t *testing.T) {
	tmpHome, homeCleanup := setupTempHome(t)
	defer homeCleanup()

	cleanup := resetAppForTest(t, tmpHome)
	defer cleanup()

	// Write config with all fields populated
	stateDir := filepath.Join(tmpHome, ".work-report")
	cfgPath := filepath.Join(stateDir, "config.json")
	cfg := &config.Config{
		Timezone: "UTC",
	}
	cfg.LLM.Text.APIKey = "sk-test"
	cfg.LLM.Vision.APIKey = "sk-vision"
	cfg.Pushover.APIToken = "tok"
	cfg.Pushover.UserKey = "user"
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	// Add records so data section is non-trivial
	loadedCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(loadedCfg.DataDir)
	addTestRecords(t, store, 2, models.TypeTask, models.StatusActive)
	addTestRecords(t, store, 1, models.TypeMeeting, models.StatusActive)

	// Capture and validate
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	resetConfigFlags()
	rootCmd.SetArgs([]string{"status"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("status failed: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	var env agentsdk.Envelope
	if err := json.Unmarshal([]byte(output), &env); err != nil {
		t.Fatalf("cannot unmarshal into Envelope: %v", err)
	}
	if err := agentsdk.ValidateEnvelope(env); err != nil {
		t.Errorf("envelope validation failed: %v", err)
	}
}

// --- test helpers ---

// addTestRecords populates the store with count records of the given type and status.
func addTestRecords(t *testing.T, store *storage.Storage, count int, recordType models.RecordType, status string) {
	t.Helper()
	for i := 0; i < count; i++ {
		switch recordType {
		case models.TypeTask:
			store.AddRecord(&models.TaskRecord{
				CommonFields: models.CommonFields{
					Type:   models.TypeTask,
					Title:  "Test Task",
					Date:   "2026-01-15",
					Status: status,
				},
			})
		case models.TypeMeeting:
			store.AddRecord(&models.MeetingRecord{
				CommonFields: models.CommonFields{
					Type:   models.TypeMeeting,
					Title:  "Test Meeting",
					Date:   "2026-01-15",
					Status: status,
				},
			})
		case models.TypeLog:
			store.AddRecord(&models.LogRecord{
				CommonFields: models.CommonFields{
					Type:   models.TypeLog,
					Title:  "Test Log",
					Date:   "2026-01-15",
					Status: status,
				},
			})
		}
	}
}

func taskRecordPtr(title, date string) *models.TaskRecord {
	return &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:   models.TypeTask,
			Title:  title,
			Date:   date,
			Status: models.StatusActive,
		},
	}
}

func meetingRecordPtr(title, date string) *models.MeetingRecord {
	return &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:   models.TypeMeeting,
			Title:  title,
			Date:   date,
			Status: models.StatusActive,
		},
	}
}

func logRecordPtr(title, date string) *models.LogRecord {
	return &models.LogRecord{
		CommonFields: models.CommonFields{
			Type:   models.TypeLog,
			Title:  title,
			Date:   date,
			Status: models.StatusActive,
		},
	}
}
