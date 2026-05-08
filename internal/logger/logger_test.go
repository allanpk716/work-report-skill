package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_CreatesLogDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := Init(logDir)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Verify the log directory was created.
	info, err := os.Stat(logDir)
	if err != nil {
		t.Fatalf("log dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("log path is not a directory")
	}

	Shutdown()
}

func TestInit_CreatesLogFile(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := Init(logDir)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Write a log entry to trigger file creation.
	Infof("test log entry from TestInit_CreatesLogFile")

	// Verify a log file matching the daemon-- pattern exists.
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("cannot read log dir: %v", err)
	}

	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "daemon--") && strings.HasSuffix(e.Name(), ".log") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no daemon log file found in %s, entries: %v", logDir, entries)
	}

	Shutdown()
}

func TestInfof_Warnf_Errorf_ProduceOutput(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := Init(logDir)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// These should not panic.
	Infof("info message %d", 1)
	Warnf("warn message %d", 2)
	Errorf("error message %d", 3)

	Shutdown()
}

func TestWithField_WithFields(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := Init(logDir)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Should not panic.
	WithField("key", "value").Info("single field")
	WithFields(map[string]interface{}{"a": 1, "b": 2}).Info("multi fields")

	Shutdown()
}

func TestShutdown_Idempotent(t *testing.T) {
	// Calling Shutdown multiple times should not panic.
	Shutdown()
	Shutdown()
}
