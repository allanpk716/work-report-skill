package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"wr/internal/daemon"
	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
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

func TestExportBuildsCorrectPath(t *testing.T) {
	resetExportFlags()
	exportFormat = "json"
	exportDate = "2026-05-01"
	exportFrom = "2026-04-28"
	exportTo = "2026-05-04"
	exportType = "task"
	exportStatus = "completed"
	exportQuery = "refactor"

	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path + "?" + r.URL.RawQuery
		fmt.Fprintf(w, `{"type":"success","message":"exported"}`)
		w.Header().Set("Content-Type", "application/json")
	}))
	defer srv.Close()

	dir, _ := daemon.DefaultStateDir()
	_ = os.MkdirAll(dir, 0755)
	_ = daemon.WriteState(dir, daemon.DaemonState{Port: parsePort(srv.URL)})
	defer daemon.RemoveState(dir)

	err := exportCmd.RunE(exportCmd, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "/api/export?format=json&date=2026-05-01&from=2026-04-28&to=2026-05-04&type=task&status=completed&query=refactor"
	if capturedPath != expected {
		t.Errorf("expected path %q, got %q", expected, capturedPath)
	}
}

func TestExportFileOutput(t *testing.T) {
	resetExportFlags()
	exportFormat = "json"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"type":"success","data":{"entries":[]}}`)
		w.Header().Set("Content-Type", "application/json")
	}))
	defer srv.Close()

	dir, _ := daemon.DefaultStateDir()
	_ = os.MkdirAll(dir, 0755)
	_ = daemon.WriteState(dir, daemon.DaemonState{Port: parsePort(srv.URL)})
	defer daemon.RemoveState(dir)

	tmpFile := t.TempDir() + "/export.json"
	exportFile = tmpFile

	err := exportCmd.RunE(exportCmd, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("cannot read output file: %v", err)
	}

	if !bytes.Contains(data, []byte(`"type":"success"`)) {
		t.Errorf("expected success in file, got: %s", string(data))
	}
}

func TestExportMarkdownFormat(t *testing.T) {
	resetExportFlags()
	exportFormat = "markdown"

	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path + "?" + r.URL.RawQuery
		fmt.Fprintf(w, `{"type":"success","data":"# Work Report\n\nNo entries."}`)
		w.Header().Set("Content-Type", "application/json")
	}))
	defer srv.Close()

	dir, _ := daemon.DefaultStateDir()
	_ = os.MkdirAll(dir, 0755)
	_ = daemon.WriteState(dir, daemon.DaemonState{Port: parsePort(srv.URL)})
	defer daemon.RemoveState(dir)

	err := exportCmd.RunE(exportCmd, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(capturedPath, "format=markdown") {
		t.Errorf("expected format=markdown in path, got: %s", capturedPath)
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

func parsePort(url string) int {
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == ':' {
			var port int
			fmt.Sscanf(url[i+1:], "%d", &port)
			return port
		}
	}
	return 0
}

// suppress unused import warnings
var _ = json.Marshal
var _ = agentsdk.NewErrorEnvelope
