package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/allanpk716/agent-cli-sdk"

	"wr/internal/config"
	"wr/internal/daemon"
)

// TestAgentDoctor verifies that "wr agent doctor" outputs a JSONL result
// envelope with a checks array containing daemon, llm, and pushover entries.
func TestAgentDoctor(t *testing.T) {
	tmpHome, homeCleanup := setupTempHome(t)
	defer homeCleanup()

	cleanup := resetAppForTest(t, tmpHome)
	defer cleanup()

	// Write a daemon state file so the daemon check has something to read.
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := daemon.WriteState(stateDir, daemon.DaemonState{Port: 59001, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}

	// Write a config with LLM key and Pushover credentials.
	cfgPath := filepath.Join(stateDir, "config.json")
	cfg := &config.Config{
		Daemon:   config.DaemonConfig{Port: 59001},
		Timezone: "UTC",
	}
	cfg.LLM.Text.APIKey = "test-key-123"
	cfg.Pushover.APIToken = "tok-123"
	cfg.Pushover.UserKey = "user-456"
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	// Capture JSONL output
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	resetConfigFlags()
	rootCmd.SetArgs([]string{"agent", "doctor"})
	err := rootCmd.Execute()

	if err != nil {
		t.Fatalf("agent doctor failed: %v", err)
	}

	// Parse JSONL output
	output := strings.TrimSpace(buf.String())
	if output == "" {
		t.Fatal("expected JSONL output from agent doctor, got empty string")
	}

	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}

	// Verify it's a result envelope
	if envelope["type"] != "result" {
		t.Errorf("expected type=result, got %v", envelope["type"])
	}

	// Extract data.checks
	data, ok := envelope["data"].(map[string]interface{})
	if !ok {
		t.Fatal("envelope.data is not a map")
	}
	checksRaw, ok := data["checks"].([]interface{})
	if !ok {
		t.Fatal("envelope.data.checks is not an array")
	}

	// Build a set of check names that appeared
	checkNames := make(map[string]bool)
	for _, c := range checksRaw {
		check, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := check["name"].(string)
		checkNames[name] = true
	}

	// Verify all expected custom health checks are present
	for _, expected := range []string{"daemon", "llm", "pushover"} {
		if !checkNames[expected] {
			t.Errorf("missing health check: %s (got: %v)", expected, mapKeys(checkNames))
		}
	}

	// Validate full envelope structure
	var env agentsdk.Envelope
	if err := json.Unmarshal([]byte(output), &env); err != nil {
		t.Fatalf("cannot unmarshal into Envelope struct: %v", err)
	}
	if err := agentsdk.ValidateEnvelope(env); err != nil {
		t.Errorf("doctor output envelope validation failed: %v", err)
	}
}

// TestAgentDoctorSandboxChecks verifies that sandbox directory checks appear
// in the doctor output.
func TestAgentDoctorSandboxChecks(t *testing.T) {
	tmpHome, homeCleanup := setupTempHome(t)
	defer homeCleanup()

	cleanup := resetAppForTest(t, tmpHome)
	defer cleanup()

	// Write config so config_file check can pass
	stateDir := filepath.Join(tmpHome, ".work-report")
	cfgPath := filepath.Join(stateDir, "config.json")
	cfg := &config.Config{
		Daemon:   config.DaemonConfig{Port: 59001},
		Timezone: "UTC",
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	// Capture JSONL output
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	resetConfigFlags()
	rootCmd.SetArgs([]string{"agent", "doctor"})
	err := rootCmd.Execute()

	if err != nil {
		t.Fatalf("agent doctor failed: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	data, ok := envelope["data"].(map[string]interface{})
	if !ok {
		t.Fatal("envelope.data is not a map")
	}
	checksRaw, ok := data["checks"].([]interface{})
	if !ok {
		t.Fatal("envelope.data.checks is not an array")
	}

	// Check that sandbox_* entries exist
	sandboxChecks := 0
	for _, c := range checksRaw {
		check, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := check["name"].(string)
		if strings.HasPrefix(name, "sandbox_") {
			sandboxChecks++
			status, _ := check["status"].(string)
			if status != "pass" {
				t.Errorf("sandbox check %s status=%s, expected pass", name, status)
			}
		}
	}

	if sandboxChecks == 0 {
		t.Error("expected at least one sandbox_* check in doctor output, got none")
	}
}

// mapKeys returns the keys of a map[string]bool for error messages.
func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
