package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// writeTestConfig writes a BackupConfig as backup-config.json in dir/.work-report/.
func writeTestConfig(t *testing.T, dir string, cfg *BackupConfig) {
	t.Helper()
	wrDir := filepath.Join(dir, ".work-report")
	if err := os.MkdirAll(wrDir, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrDir, "backup-config.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// patchConfigPath redirects ConfigPath to return a path inside the temp dir.
func patchConfigPath(dir string) func() {
	origHome := os.Getenv("HOME")
	origUserprofile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", dir)
	os.Setenv("USERPROFILE", dir)
	return func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserprofile)
	}
}

// TestBackupScheduler_SyncValidSchedule tests that Sync() registers a cron
// entry when the config has a valid schedule and Enabled=true.
func TestBackupScheduler_SyncValidSchedule(t *testing.T) {
	dir := t.TempDir()
	defer patchConfigPath(dir)()

	writeTestConfig(t, dir, &BackupConfig{
		Retention: RetentionPolicy{Daily: 7, Weekly: 4, Monthly: 6},
		OutputDir: filepath.Join(dir, "backups"),
		Schedule:  "0 0 3 * * *",
		Enabled:   true,
	})

	bs := NewBackupScheduler()
	if bs.Registered() {
		t.Fatal("expected not registered before Sync()")
	}

	bs.Sync()
	if !bs.Registered() {
		t.Fatal("expected registered after Sync() with valid schedule")
	}

	bs.Stop()
}

// TestBackupScheduler_SyncDisabled tests that Sync() does not register when
// Enabled=false.
func TestBackupScheduler_SyncDisabled(t *testing.T) {
	dir := t.TempDir()
	defer patchConfigPath(dir)()

	writeTestConfig(t, dir, &BackupConfig{
		Retention: RetentionPolicy{Daily: 7, Weekly: 4, Monthly: 6},
		OutputDir: filepath.Join(dir, "backups"),
		Schedule:  "0 0 3 * * *",
		Enabled:   false,
	})

	bs := NewBackupScheduler()
	bs.Sync()
	if bs.Registered() {
		t.Fatal("expected not registered when disabled")
	}

	bs.Stop()
}

// TestBackupScheduler_SyncEmptySchedule tests that Sync() does not register
// when Schedule is empty.
func TestBackupScheduler_SyncEmptySchedule(t *testing.T) {
	dir := t.TempDir()
	defer patchConfigPath(dir)()

	writeTestConfig(t, dir, &BackupConfig{
		Retention: RetentionPolicy{Daily: 7, Weekly: 4, Monthly: 6},
		OutputDir: filepath.Join(dir, "backups"),
		Schedule:  "",
		Enabled:   true,
	})

	bs := NewBackupScheduler()
	bs.Sync()
	if bs.Registered() {
		t.Fatal("expected not registered with empty schedule")
	}

	bs.Stop()
}

// TestBackupScheduler_SyncInvalidCron tests that Sync() does not register
// when the cron expression is invalid.
func TestBackupScheduler_SyncInvalidCron(t *testing.T) {
	dir := t.TempDir()
	defer patchConfigPath(dir)()

	writeTestConfig(t, dir, &BackupConfig{
		Retention: RetentionPolicy{Daily: 7, Weekly: 4, Monthly: 6},
		OutputDir: filepath.Join(dir, "backups"),
		Schedule:  "not-a-cron-expression",
		Enabled:   true,
	})

	bs := NewBackupScheduler()
	bs.Sync()
	if bs.Registered() {
		t.Fatal("expected not registered with invalid cron expression")
	}

	bs.Stop()
}

// TestBackupScheduler_SyncUnregister tests that calling Sync() with
// Enabled=false after a previous Sync() with Enabled=true removes the entry.
func TestBackupScheduler_SyncUnregister(t *testing.T) {
	dir := t.TempDir()
	defer patchConfigPath(dir)()

	// First config: enabled with valid schedule.
	writeTestConfig(t, dir, &BackupConfig{
		Retention: RetentionPolicy{Daily: 7, Weekly: 4, Monthly: 6},
		OutputDir: filepath.Join(dir, "backups"),
		Schedule:  "0 0 3 * * *",
		Enabled:   true,
	})

	bs := NewBackupScheduler()
	bs.Sync()
	if !bs.Registered() {
		t.Fatal("expected registered after first Sync()")
	}

	// Update config to disabled.
	writeTestConfig(t, dir, &BackupConfig{
		Retention: RetentionPolicy{Daily: 7, Weekly: 4, Monthly: 6},
		OutputDir: filepath.Join(dir, "backups"),
		Schedule:  "0 0 3 * * *",
		Enabled:   false,
	})

	bs.Sync()
	if bs.Registered() {
		t.Fatal("expected unregistered after Sync() with disabled config")
	}

	bs.Stop()
}

// TestBackupScheduler_StartStop tests that Start and Stop work without errors.
func TestBackupScheduler_StartStop(t *testing.T) {
	bs := NewBackupScheduler()
	bs.Start()
	time.Sleep(50 * time.Millisecond)
	bs.Stop()
}

// TestBackupScheduler_CallbackFired tests that the cron callback actually
// invokes CreateBackup + GFSRotate when triggered. We use a 2-second interval
// so the test completes quickly.
func TestBackupScheduler_CallbackFired(t *testing.T) {
	dir := t.TempDir()
	defer patchConfigPath(dir)()

	backupsDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create the work-report data directory with a config file so CreateBackup
	// has something to archive.
	workDir := filepath.Join(dir, ".work-report")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "config.json"), []byte(`{"test": true}`), 0644); err != nil {
		t.Fatal(err)
	}

	writeTestConfig(t, dir, &BackupConfig{
		Retention: RetentionPolicy{Daily: 1, Weekly: 1, Monthly: 1},
		OutputDir: backupsDir,
		Schedule:  "*/2 * * * * *", // every 2 seconds
		Enabled:   true,
	})

	bs := NewBackupScheduler()
	bs.Start()
	bs.Sync()

	// Wait up to 5 seconds for the callback to fire (2-second interval + margin).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		backups, err := ListBackups(backupsDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(backups) > 0 {
			bs.Stop()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	bs.Stop()
	t.Fatal("timed out waiting for backup callback to fire")
}

// TestBackupScheduler_SyncNoConfigFile tests that Sync() gracefully handles
// a missing config file (should use defaults, which have no schedule).
func TestBackupScheduler_SyncNoConfigFile(t *testing.T) {
	dir := t.TempDir()
	defer patchConfigPath(dir)()

	bs := NewBackupScheduler()
	bs.Sync()
	if bs.Registered() {
		t.Fatal("expected not registered when no config file exists (defaults have no schedule)")
	}

	bs.Stop()
}

// TestBackupScheduler_ConcurrentSync tests that concurrent Sync() calls don't
// panic or race.
func TestBackupScheduler_ConcurrentSync(t *testing.T) {
	dir := t.TempDir()
	defer patchConfigPath(dir)()

	writeTestConfig(t, dir, &BackupConfig{
		Retention: RetentionPolicy{Daily: 7, Weekly: 4, Monthly: 6},
		OutputDir: filepath.Join(dir, "backups"),
		Schedule:  "0 0 3 * * *",
		Enabled:   true,
	})

	bs := NewBackupScheduler()
	bs.Start()

	var started atomic.Int32
	done := make(chan struct{})

	for i := 0; i < 10; i++ {
		go func() {
			started.Add(1)
			bs.Sync()
			if started.Load() == 10 {
				select {
				case done <- struct{}{}:
				default:
				}
			}
		}()
	}

	select {
	case <-done:
		// All goroutines completed.
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Sync() timed out")
	}

	bs.Stop()
}
