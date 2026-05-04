package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureSandboxDirs(t *testing.T) {
	baseDir := t.TempDir()

	if err := EnsureSandboxDirs(baseDir); err != nil {
		t.Fatalf("EnsureSandboxDirs returned error: %v", err)
	}

	for _, sub := range []string{DirLocks, DirCrashDumps, DirCache} {
		path := filepath.Join(baseDir, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("subdirectory %s not created: %v", sub, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory, got file", sub)
		}
	}
}

func TestEnsureSandboxDirsIdempotent(t *testing.T) {
	baseDir := t.TempDir()

	if err := EnsureSandboxDirs(baseDir); err != nil {
		t.Fatalf("first call returned error: %v", err)
	}
	if err := EnsureSandboxDirs(baseDir); err != nil {
		t.Fatalf("second call returned error: %v", err)
	}

	// Verify directories still exist
	for _, sub := range []string{DirLocks, DirCrashDumps, DirCache} {
		path := filepath.Join(baseDir, sub)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("subdirectory %s missing after second call: %v", sub, err)
		}
	}
}

func TestOpenDaemonLogCreatesFile(t *testing.T) {
	baseDir := t.TempDir()

	f, err := OpenDaemonLog(baseDir)
	if err != nil {
		t.Fatalf("OpenDaemonLog returned error: %v", err)
	}
	defer f.Close()

	expectedPath := DaemonLogPath(baseDir)
	info, err := os.Stat(expectedPath)
	if err != nil {
		t.Fatalf("daemon log file not created at %s: %v", expectedPath, err)
	}
	if info.IsDir() {
		t.Error("daemon log path is a directory, expected file")
	}
}

func TestOpenDaemonLogAppendMode(t *testing.T) {
	baseDir := t.TempDir()

	// First write
	f1, err := OpenDaemonLog(baseDir)
	if err != nil {
		t.Fatalf("first OpenDaemonLog returned error: %v", err)
	}
	firstLine := "first line\n"
	if _, err := f1.WriteString(firstLine); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	f1.Close()

	// Second write via re-opening
	f2, err := OpenDaemonLog(baseDir)
	if err != nil {
		t.Fatalf("second OpenDaemonLog returned error: %v", err)
	}
	secondLine := "second line\n"
	if _, err := f2.WriteString(secondLine); err != nil {
		t.Fatalf("second write failed: %v", err)
	}
	f2.Close()

	// Verify both lines present (append, not overwrite)
	data, err := os.ReadFile(DaemonLogPath(baseDir))
	if err != nil {
		t.Fatalf("failed to read daemon log: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "first line") {
		t.Error("daemon log missing first line — append mode not working")
	}
	if !strings.Contains(content, "second line") {
		t.Error("daemon log missing second line")
	}
	if strings.Count(content, "\n") != 2 {
		t.Errorf("expected 2 newlines in daemon log, got %d", strings.Count(content, "\n"))
	}
}

func TestDaemonLogPath(t *testing.T) {
	baseDir := "/home/user/.work-report"
	expected := filepath.Join(baseDir, "daemon.log")

	got := DaemonLogPath(baseDir)
	if got != expected {
		t.Errorf("DaemonLogPath(%q) = %q, want %q", baseDir, got, expected)
	}
}
