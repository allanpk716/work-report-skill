package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/config"
)

func TestExportRequiresFormat(t *testing.T) {
	resetExportFlags()

	var stdout bytes.Buffer
	err := exportCmd.RunE(exportCmd, []string{})
	// writeExitError writes to jsonl.DefaultWriter, not stdout — check the error
	if err == nil {
		t.Fatal("expected error when --format is missing")
	}

	var exitErr *agentsdk.ExitError
	if !asExitError(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != agentsdk.ExitInvalidParams {
		t.Errorf("expected exit code %d, got %d", agentsdk.ExitInvalidParams, exitErr.Code)
	}
	_ = stdout
}

func TestExportInvalidFormat(t *testing.T) {
	resetExportFlags()
	exportFormat = "yaml"

	var stdout bytes.Buffer
	_ = stdout

	err := exportCmd.RunE(exportCmd, []string{})
	if err == nil {
		t.Fatal("expected error for invalid format")
	}

	var exitErr *agentsdk.ExitError
	if !asExitError(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != agentsdk.ExitInvalidParams {
		t.Errorf("expected exit code %d, got %d", agentsdk.ExitInvalidParams, exitErr.Code)
	}
}

func TestExportJSONDirectCall(t *testing.T) {
	resetExportFlags()
	exportFormat = "json"
	exportDate = "2026-05-01"
	exportFrom = "2026-04-28"
	exportTo = "2026-05-04"
	exportType = "task"
	exportStatus = "completed"
	exportQuery = "refactor"

	// Create temp home with config
	tmpHome := t.TempDir()
	stateDir := filepath.Join(tmpHome, ".work-report")
	os.MkdirAll(stateDir, 0755)
	cfgPath := filepath.Join(stateDir, "config.json")
	cfg, _ := config.Load(filepath.Join(stateDir, "nonexistent.json"))
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	// Execute with temp home
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", tmpHome)
	os.Setenv("USERPROFILE", tmpHome)
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}()

	code, out := executeCmd("export")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	// Should contain the expected action and format
	if !strings.Contains(string(out), `"action":"export"`) {
		t.Errorf("expected action=export in output, got: %s", string(out))
	}
	if !strings.Contains(string(out), `"format":"json"`) {
		t.Errorf("expected format=json in output, got: %s", string(out))
	}
}

func TestExportFileOutput(t *testing.T) {
	resetExportFlags()
	exportFormat = "json"

	// Create temp home with config
	tmpHome := t.TempDir()
	stateDir := filepath.Join(tmpHome, ".work-report")
	os.MkdirAll(stateDir, 0755)
	cfgPath := filepath.Join(stateDir, "config.json")
	cfg, _ := config.Load(filepath.Join(stateDir, "nonexistent.json"))
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	tmpFile := t.TempDir() + "/export.json"
	exportFile = tmpFile

	// Set up temp home env
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", tmpHome)
	os.Setenv("USERPROFILE", tmpHome)
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}()

	code, _ := executeCmd("export")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("cannot read output file: %v", err)
	}

	if !bytes.Contains(data, []byte(`"action"`)) {
		t.Errorf("expected action in file, got: %s", string(data))
	}
}

func TestExportMarkdownFormat(t *testing.T) {
	resetExportFlags()
	exportFormat = "markdown"

	// Create temp home with config
	tmpHome := t.TempDir()
	stateDir := filepath.Join(tmpHome, ".work-report")
	os.MkdirAll(stateDir, 0755)
	cfgPath := filepath.Join(stateDir, "config.json")
	cfg, _ := config.Load(filepath.Join(stateDir, "nonexistent.json"))
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	// Set up temp home env
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", tmpHome)
	os.Setenv("USERPROFILE", tmpHome)
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}()

	code, out := executeCmd("export")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	if !strings.Contains(string(out), `"format":"markdown"`) {
		t.Errorf("expected format=markdown in output, got: %s", string(out))
	}
}

// helpers

func resetExportFlags() {
	exportFormat = ""
	exportDate = ""
	exportFrom = ""
	exportTo = ""
	exportType = ""
	exportStatus = ""
	exportQuery = ""
	exportFile = ""
}

func resetListFlags() {
	listType = ""
	listDate = ""
	listFrom = ""
	listTo = ""
	listStatus = ""
	listQuery = ""
}

func asExitError(err error, target **agentsdk.ExitError) bool {
	if target == nil {
		return false
	}
	if ee, ok := err.(*agentsdk.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

// suppress unused import warnings
var _ = json.Marshal
