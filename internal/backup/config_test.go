package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Retention.Daily != 7 {
		t.Errorf("expected daily=7, got %d", cfg.Retention.Daily)
	}
	if cfg.Retention.Weekly != 4 {
		t.Errorf("expected weekly=4, got %d", cfg.Retention.Weekly)
	}
	if cfg.Retention.Monthly != 6 {
		t.Errorf("expected monthly=6, got %d", cfg.Retention.Monthly)
	}
	if cfg.OutputDir == "" {
		t.Error("expected non-empty output_dir")
	}
}

func TestLoadConfigMissing(t *testing.T) {
	cfg, err := LoadConfig("/nonexistent/path/backup-config.json")
	if err != nil {
		t.Fatalf("unexpected error for missing file: %v", err)
	}
	if cfg.Retention.Daily != 7 {
		t.Errorf("expected default daily=7, got %d", cfg.Retention.Daily)
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backup-config.json")

	original := &BackupConfig{
		Retention: RetentionPolicy{Daily: 10, Weekly: 8, Monthly: 12},
		OutputDir: filepath.Join(dir, "my-backups"),
	}

	if err := SaveConfig(original, path); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if loaded.Retention.Daily != 10 {
		t.Errorf("expected daily=10, got %d", loaded.Retention.Daily)
	}
	if loaded.Retention.Weekly != 8 {
		t.Errorf("expected weekly=8, got %d", loaded.Retention.Weekly)
	}
	if loaded.Retention.Monthly != 12 {
		t.Errorf("expected monthly=12, got %d", loaded.Retention.Monthly)
	}
	if loaded.OutputDir != filepath.Join(dir, "my-backups") {
		t.Errorf("expected output_dir=%s, got %s", filepath.Join(dir, "my-backups"), loaded.OutputDir)
	}
}

func TestSaveConfigCreatesDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "backup-config.json")

	cfg := DefaultConfig()
	if err := SaveConfig(cfg, path); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
}

func TestLoadConfigInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backup-config.json")
	if err := os.WriteFile(path, []byte("{invalid json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := &BackupConfig{}
	cfg.applyDefaults()

	if cfg.Retention.Daily != 7 {
		t.Errorf("expected daily=7 after applyDefaults, got %d", cfg.Retention.Daily)
	}
	if cfg.OutputDir == "" {
		t.Error("expected non-empty output_dir after applyDefaults")
	}
}

func TestLoadConfigJSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backup-config.json")

	original := &BackupConfig{
		Retention: RetentionPolicy{Daily: 3, Weekly: 2, Monthly: 1},
		OutputDir: "/tmp/backups",
	}

	data, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Retention.Daily != 3 || loaded.Retention.Weekly != 2 || loaded.Retention.Monthly != 1 {
		t.Errorf("retention mismatch: %+v", loaded.Retention)
	}
}

func TestSaveConfigUnwritableDir(t *testing.T) {
	// On Unix, use a path under /proc to get an unwritable directory.
	// On Windows, use a clearly invalid path with a null byte or a deeply
	// nested nonexistent path where MkdirAll will fail due to permissions.
	// We use a path under a read-only root to trigger the MkdirAll error.
	//
	// The most portable approach: write to a path whose parent is a file
	// (not a directory), so MkdirAll fails.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "afile")
	if err := os.WriteFile(filePath, []byte("not a dir"), 0644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(filePath, "nested", "backup-config.json")

	err := SaveConfig(DefaultConfig(), configPath)
	if err == nil {
		t.Fatal("expected error when saving to path under a file")
	}
}
