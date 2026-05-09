// Package backup provides Grandfather-Father-Son (GFS) backup rotation for
// ~/.work-report/ data.  BackupConfig controls retention policy and output
// directory; CreateBackup produces timestamped zip archives.
//
// Config is stored in an independent backup-config.json (MEM079) — not
// merged into the main config.json to keep concerns separate.
package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RetentionPolicy defines how many daily/weekly/monthly backups to keep.
type RetentionPolicy struct {
	Daily   int `json:"daily"`   // e.g. 7
	Weekly  int `json:"weekly"`  // e.g. 4
	Monthly int `json:"monthly"` // e.g. 6
}

// BackupConfig is the top-level backup configuration persisted to
// ~/.work-report/backup-config.json.
type BackupConfig struct {
	Retention RetentionPolicy `json:"retention"`
	OutputDir string          `json:"output_dir"` // absolute path; defaults to ~/.work-report/backups
	Schedule  string          `json:"schedule"`   // 6-field cron expression (sec min hour dom month dow); empty = disabled
	Enabled   bool            `json:"enabled"`    // master switch for scheduled backups
}

// DefaultRetention returns sensible defaults: 7 daily, 4 weekly, 6 monthly.
func DefaultRetention() RetentionPolicy {
	return RetentionPolicy{Daily: 7, Weekly: 4, Monthly: 6}
}

// DefaultConfig returns a BackupConfig with default values.
func DefaultConfig() *BackupConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return &BackupConfig{
		Retention: DefaultRetention(),
		OutputDir: filepath.Join(home, ".work-report", "backups"),
	}
}

// ConfigPath returns the default path for backup-config.json.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("backup: cannot determine home dir: %w", err)
	}
	return filepath.Join(home, ".work-report", "backup-config.json"), nil
}

// LoadConfig reads backup configuration from the given path. If the file does
// not exist it returns DefaultConfig.
func LoadConfig(path string) (*BackupConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("backup: read config %s: %w", path, err)
	}

	var cfg BackupConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("backup: parse config %s: %w", path, err)
	}

	cfg.applyDefaults()
	return &cfg, nil
}

// SaveConfig writes the backup configuration to the given path as pretty JSON,
// creating parent directories as needed.
func SaveConfig(cfg *BackupConfig, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("backup: create dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("backup: marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("backup: write config %s: %w", path, err)
	}
	return nil
}

// applyDefaults fills zero fields with sensible defaults.
func (c *BackupConfig) applyDefaults() {
	if c.Retention.Daily == 0 && c.Retention.Weekly == 0 && c.Retention.Monthly == 0 {
		c.Retention = DefaultRetention()
	}
	if c.OutputDir == "" {
		cfg := DefaultConfig()
		c.OutputDir = cfg.OutputDir
	}
}
