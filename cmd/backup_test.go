package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wr/internal/backup"
)

// resetBackupFlags resets backup command flags between tests.
func resetBackupFlags() {
	backupOutputDir = ""
}

// runBackupCmd executes rootCmd with backup args, capturing JSONL output.
func runBackupCmd(args ...string) []byte {
	return withJSONLCapture(func() {
		resetBackupFlags()
		rootCmd.SetArgs(args)
		_ = rootCmd.Execute()
	})
}

// ---------- backup create ----------

func TestBackupCreate(t *testing.T) {
	tmpHome, cleanup := setupConfigEnv(t)
	defer cleanup()

	// Seed the data directory with a config file.
	dataDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(`{"timezone":"UTC"}`), 0644); err != nil {
		t.Fatal(err)
	}

	out := runBackupCmd("backup", "create")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d (output: %s)", len(lines), string(out))
	}
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0])
	}

	data, ok := lines[0]["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", lines[0]["data"])
	}

	// Verify the backup file was created on disk.
	path, _ := data["path"].(string)
	if path == "" {
		t.Fatal("expected path in response")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("backup file not created at %s", path)
	}

	// Verify size is positive.
	size, _ := data["size_bytes"].(float64)
	if size <= 0 {
		t.Errorf("expected positive size_bytes, got %v", size)
	}

	// Verify output_dir is the default.
	outDir, _ := data["output_dir"].(string)
	expectedOutDir := filepath.ToSlash(filepath.Join(tmpHome, ".work-report", "backups"))
	actualOutDir := filepath.ToSlash(outDir)
	if actualOutDir != expectedOutDir {
		t.Errorf("expected output_dir=%s, got %s", expectedOutDir, actualOutDir)
	}
}

func TestBackupCreateWithOutputFlag(t *testing.T) {
	tmpHome, cleanup := setupConfigEnv(t)
	defer cleanup()

	// Seed data dir.
	dataDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	customDir := filepath.Join(tmpHome, "my-backups")

	out := runBackupCmd("backup", "create", "--output", customDir)
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d", len(lines))
	}
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0])
	}

	data, _ := lines[0]["data"].(map[string]interface{})
	path, _ := data["path"].(string)
	if !strings.HasPrefix(filepath.ToSlash(path), filepath.ToSlash(customDir)) {
		t.Errorf("expected path under custom dir, got %s", path)
	}

	outDir, _ := data["output_dir"].(string)
	if filepath.ToSlash(outDir) != filepath.ToSlash(customDir) {
		t.Errorf("expected output_dir=%s, got %s", customDir, outDir)
	}
}

func TestBackupCreateDataDirNotFound(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	// Do NOT create .work-report/ — data dir won't exist.
	out := runBackupCmd("backup", "create")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d", len(lines))
	}
	if lines[0]["type"] != "error" {
		t.Fatalf("expected type=error, got %v", lines[0])
	}
	msg, _ := lines[0]["message"].(string)
	if !strings.Contains(msg, "data_dir_not_found") {
		t.Errorf("error should mention data_dir_not_found, got: %s", msg)
	}
}

// ---------- backup list ----------

func TestBackupListEmpty(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	out := runBackupCmd("backup", "list")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d", len(lines))
	}
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0])
	}

	data, _ := lines[0]["data"].(map[string]interface{})
	count, _ := data["count"].(float64)
	if count != 0 {
		t.Errorf("expected 0 backups, got %v", count)
	}
	backups, _ := data["backups"].([]interface{})
	if backups == nil || len(backups) != 0 {
		t.Errorf("expected empty backups array, got %v", backups)
	}
}

func TestBackupListWithBackups(t *testing.T) {
	tmpHome, cleanup := setupConfigEnv(t)
	defer cleanup()

	// Seed data dir.
	dataDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Create two backups.
	_, _, err := backup.CreateBackup(dataDir, backup.DefaultConfig().OutputDir)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = backup.CreateBackup(dataDir, backup.DefaultConfig().OutputDir)
	if err != nil {
		t.Fatal(err)
	}

	out := runBackupCmd("backup", "list")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0])
	}

	data, _ := lines[0]["data"].(map[string]interface{})
	count, _ := data["count"].(float64)
	if count != 2 {
		t.Errorf("expected 2 backups, got %v", count)
	}
}

// ---------- backup cleanup ----------

func TestBackupCleanupNoBackups(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	out := runBackupCmd("backup", "cleanup")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d", len(lines))
	}
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0])
	}

	data, _ := lines[0]["data"].(map[string]interface{})
	kept, _ := data["kept_count"].(float64)
	if kept != 0 {
		t.Errorf("expected 0 kept, got %v", kept)
	}
	removed, _ := data["removed_count"].(float64)
	if removed != 0 {
		t.Errorf("expected 0 removed, got %v", removed)
	}
}

// ---------- backup config show ----------

func TestBackupConfigShowDefaults(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	out := runBackupCmd("backup", "config", "show")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d", len(lines))
	}
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0])
	}

	data, _ := lines[0]["data"].(map[string]interface{})
	source, _ := data["source"].(string)
	if source != "defaults" {
		t.Errorf("expected source=defaults when no config file, got %s", source)
	}

	// Verify config has expected defaults.
	cfgData, ok := data["config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected config map, got %T", data["config"])
	}
	retention, _ := cfgData["retention"].(map[string]interface{})
	if retention["daily"].(float64) != 7 {
		t.Errorf("expected daily=7, got %v", retention["daily"])
	}
	if retention["weekly"].(float64) != 4 {
		t.Errorf("expected weekly=4, got %v", retention["weekly"])
	}
	if retention["monthly"].(float64) != 6 {
		t.Errorf("expected monthly=6, got %v", retention["monthly"])
	}
}

func TestBackupConfigShowFromFile(t *testing.T) {
	tmpHome, cleanup := setupConfigEnv(t)
	defer cleanup()

	// Write a custom backup config.
	cfgDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &backup.BackupConfig{
		Retention: backup.RetentionPolicy{Daily: 3, Weekly: 2, Monthly: 1},
		OutputDir: filepath.Join(tmpHome, "custom-backups"),
	}
	cfgPath := filepath.Join(cfgDir, "backup-config.json")
	cfgBytes, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, cfgBytes, 0644); err != nil {
		t.Fatal(err)
	}

	out := runBackupCmd("backup", "config", "show")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0])
	}

	data, _ := lines[0]["data"].(map[string]interface{})
	source, _ := data["source"].(string)
	if source != "file" {
		t.Errorf("expected source=file when config exists, got %s", source)
	}

	cfgData, _ := data["config"].(map[string]interface{})
	retention, _ := cfgData["retention"].(map[string]interface{})
	if retention["daily"].(float64) != 3 {
		t.Errorf("expected daily=3, got %v", retention["daily"])
	}
}

// ---------- integration: create + list ----------

func TestBackupCreateAndList(t *testing.T) {
	tmpHome, cleanup := setupConfigEnv(t)
	defer cleanup()

	// Seed data dir.
	dataDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(`{"timezone":"UTC"}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a backup.
	outCreate := runBackupCmd("backup", "create")
	assertValidJSONLEnvelope(t, outCreate)
	linesCreate := parseJSONL(outCreate)
	if linesCreate[0]["type"] != "result" {
		t.Fatalf("create failed: %v", linesCreate[0])
	}
	createData, _ := linesCreate[0]["data"].(map[string]interface{})
	createdPath, _ := createData["path"].(string)
	createdSize, _ := createData["size_bytes"].(float64)

	// List and verify.
	outList := runBackupCmd("backup", "list")
	assertValidJSONLEnvelope(t, outList)
	linesList := parseJSONL(outList)
	if linesList[0]["type"] != "result" {
		t.Fatalf("list failed: %v", linesList[0])
	}
	listData, _ := linesList[0]["data"].(map[string]interface{})
	count, _ := listData["count"].(float64)
	if count != 1 {
		t.Errorf("expected 1 backup, got %v", count)
	}

	backups, _ := listData["backups"].([]interface{})
	backupEntry, _ := backups[0].(map[string]interface{})
	backupSize, _ := backupEntry["size"].(float64)
	if backupSize != createdSize {
		t.Errorf("list size %v != create size %v", backupSize, createdSize)
	}

	// Verify the file on disk matches.
	if _, err := os.Stat(createdPath); os.IsNotExist(err) {
		t.Fatalf("backup file not found at %s", createdPath)
	}
}

// ---------- backup config set ----------

func TestBackupConfigSetSchedule(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	out := runBackupCmd("backup", "config", "set", "--schedule", "0 0 2 * * *")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d (output: %s)", len(lines), string(out))
	}
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0])
	}

	data, _ := lines[0]["data"].(map[string]interface{})
	saved, _ := data["saved"].(bool)
	if !saved {
		t.Error("expected saved=true")
	}

	// Verify config file was created on disk.
	cfgPath, _ := data["config_path"].(string)
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Fatalf("config file not created at %s", cfgPath)
	}

	// Reload and verify schedule was persisted.
	cfg, err := backup.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}
	if cfg.Schedule != "0 0 2 * * *" {
		t.Errorf("expected schedule='0 0 2 * * *', got %s", cfg.Schedule)
	}
}

func TestBackupConfigSetMultipleFlags(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	out := runBackupCmd("backup", "config", "set",
		"--schedule", "0 30 3 * * *",
		"--retention-daily", "14",
		"--retention-weekly", "8",
		"--retention-monthly", "12",
		"--enabled",
	)
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0])
	}

	data, _ := lines[0]["data"].(map[string]interface{})
	saved, _ := data["saved"].(bool)
	if !saved {
		t.Error("expected saved=true")
	}

	// Reload and verify all fields.
	cfgPath, _ := data["config_path"].(string)
	cfg, err := backup.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}
	if cfg.Schedule != "0 30 3 * * *" {
		t.Errorf("expected schedule='0 30 3 * * *', got %s", cfg.Schedule)
	}
	if cfg.Retention.Daily != 14 {
		t.Errorf("expected daily=14, got %d", cfg.Retention.Daily)
	}
	if cfg.Retention.Weekly != 8 {
		t.Errorf("expected weekly=8, got %d", cfg.Retention.Weekly)
	}
	if cfg.Retention.Monthly != 12 {
		t.Errorf("expected monthly=12, got %d", cfg.Retention.Monthly)
	}
	if !cfg.Enabled {
		t.Error("expected enabled=true")
	}
}

func TestBackupConfigSetDaemonNotRunning(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	// No daemon running — command should still succeed and save config.
	out := runBackupCmd("backup", "config", "set", "--schedule", "0 0 4 * * *")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d (output: %s)", len(lines), string(out))
	}
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result (success despite no daemon), got %v", lines[0])
	}

	// Verify config was persisted.
	data, _ := lines[0]["data"].(map[string]interface{})
	saved, _ := data["saved"].(bool)
	if !saved {
		t.Error("expected saved=true even when daemon is not running")
	}

	cfgPath, _ := data["config_path"].(string)
	cfg, err := backup.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}
	if cfg.Schedule != "0 0 4 * * *" {
		t.Errorf("expected schedule='0 0 4 * * *', got %s", cfg.Schedule)
	}
}

func TestBackupConfigSetPreservesExistingValues(t *testing.T) {
	tmpHome, cleanup := setupConfigEnv(t)
	defer cleanup()

	// First, set multiple values.
	runBackupCmd("backup", "config", "set",
		"--schedule", "0 0 1 * * *",
		"--retention-daily", "10",
		"--output-dir", filepath.Join(tmpHome, "my-backups"),
	)

	// Then, change only schedule — other values should be preserved.
	out := runBackupCmd("backup", "config", "set", "--schedule", "0 0 5 * * *")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0])
	}

	cfgPath, _ := lines[0]["data"].(map[string]interface{})["config_path"].(string)
	cfg, err := backup.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}

	// Schedule changed.
	if cfg.Schedule != "0 0 5 * * *" {
		t.Errorf("expected schedule='0 0 5 * * *', got %s", cfg.Schedule)
	}
	// Daily retention preserved from previous set.
	if cfg.Retention.Daily != 10 {
		t.Errorf("expected daily=10 (preserved), got %d", cfg.Retention.Daily)
	}
	// Output dir preserved from previous set.
	expectedDir := filepath.ToSlash(filepath.Join(tmpHome, "my-backups"))
	actualDir := filepath.ToSlash(cfg.OutputDir)
	if actualDir != expectedDir {
		t.Errorf("expected output_dir=%s (preserved), got %s", expectedDir, actualDir)
	}
}
