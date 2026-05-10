package backup

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupDataDir creates a temporary directory that mimics ~/.work-report/
// with sample data files.
func setupDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// config.json
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"timezone":"UTC"}`), 0644); err != nil {
		t.Fatal(err)
	}

	// work-records/ with a record file
	wrDir := filepath.Join(dir, "work-records")
	if err := os.MkdirAll(wrDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrDir, "2024-01-01.json"), []byte(`[{"title":"test"}]`), 0644); err != nil {
		t.Fatal(err)
	}

	// digests.json
	if err := os.WriteFile(filepath.Join(dir, "digests.json"), []byte(`[]`), 0644); err != nil {
		t.Fatal(err)
	}

	// logs/ directory
	logsDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, "daemon.log"), []byte("log line\n"), 0644); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestCreateBackup(t *testing.T) {
	dataDir := setupDataDir(t)
	outputDir := t.TempDir()

	zipPath, size, err := CreateBackup(dataDir, outputDir)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	if size == 0 {
		t.Error("expected non-zero backup size")
	}

	// Verify the zip exists.
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatalf("zip file not found: %v", err)
	}

	// Verify filename format.
	base := filepath.Base(zipPath)
	if !strings.HasPrefix(base, "wr-backup-") || !strings.HasSuffix(base, ".zip") {
		t.Errorf("unexpected filename format: %s", base)
	}

	// Verify zip contents.
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}

	// Check key files exist in the archive.
	for _, expected := range []string{
		"config.json",
		"work-records/2024-01-01.json",
		"digests.json",
		"logs/daemon.log",
	} {
		if !names[expected] {
			t.Errorf("expected %q in archive, not found", expected)
		}
	}
}

func TestFilenameCollision(t *testing.T) {
	dataDir := setupDataDir(t)
	outputDir := t.TempDir()

	// Create a backup.
	path1, _, err := CreateBackup(dataDir, outputDir)
	if err != nil {
		t.Fatalf("first CreateBackup: %v", err)
	}

	// Manually create a second file with the same timestamp name by reusing
	// the same filename (simulating collision within the same second).
	base1 := filepath.Base(path1)
	collisionPath := filepath.Join(outputDir, base1)
	if err := os.WriteFile(collisionPath, []byte("collision"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create another backup — should get a suffixed name.
	path2, _, err := CreateBackup(dataDir, outputDir)
	if err != nil {
		t.Fatalf("second CreateBackup: %v", err)
	}

	base2 := filepath.Base(path2)
	if base2 == base1 {
		t.Errorf("expected different filename on collision, got %s", base2)
	}
	if !strings.HasPrefix(base2, "wr-backup-") || !strings.HasSuffix(base2, ".zip") {
		t.Errorf("unexpected collision filename: %s", base2)
	}
}

func TestCreateBackupDataDirNotFound(t *testing.T) {
	outputDir := t.TempDir()
	_, _, err := CreateBackup("/nonexistent/data/dir", outputDir)
	if err == nil {
		t.Fatal("expected error for missing data dir")
	}
	if !strings.Contains(err.Error(), "data_dir_not_found") {
		t.Errorf("expected data_dir_not_found error, got: %v", err)
	}
}

func TestCreateBackupMissingOptionalFile(t *testing.T) {
	// Data dir with only config.json — optional files like scheduler-state.json
	// should be silently skipped.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	outputDir := t.TempDir()
	zipPath, size, err := CreateBackup(dir, outputDir)
	if err != nil {
		t.Fatalf("CreateBackup with missing optional files: %v", err)
	}

	if size == 0 {
		t.Error("expected non-zero size even with partial data")
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	if len(zr.File) == 0 {
		t.Error("expected at least one file in archive")
	}
}

func TestListBackups(t *testing.T) {
	outputDir := t.TempDir()

	// Create some fake backup files.
	for i, age := range []int{2, 0, 1} { // out-of-order creation times
		name := filepath.Join(outputDir, "wr-backup-20240101-000000.zip")
		if i > 0 {
			name = filepath.Join(outputDir, "wr-backup-20240101-000000.zip")
		}
		f, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()

		// Set modification time to create sort order.
		mtime := time.Now().Add(-time.Duration(age) * time.Hour)
		if err := os.Chtimes(name, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	// Create one more with a different timestamp.
	f, err := os.Create(filepath.Join(outputDir, "wr-backup-20240102-120000.zip"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	metas, err := ListBackups(outputDir)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}

	if len(metas) < 2 {
		t.Errorf("expected at least 2 backups, got %d", len(metas))
	}

	// Newest first.
	for i := 1; i < len(metas); i++ {
		if metas[i].CreatedAt.After(metas[i-1].CreatedAt) {
			t.Errorf("backups not sorted newest first: [%d] %v after [%d] %v",
				i, metas[i].CreatedAt, i-1, metas[i-1].CreatedAt)
		}
	}
}

func TestListBackupsEmpty(t *testing.T) {
	metas, err := ListBackups("/nonexistent/path")
	if err != nil {
		t.Fatalf("expected nil error for nonexistent dir, got: %v", err)
	}
	if len(metas) != 0 {
		t.Errorf("expected empty list, got %d", len(metas))
	}
}
