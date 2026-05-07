package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
)

// resetAppForTest creates a fresh app with WR_HOME pointing to tmpHome,
// removes any existing agent command tree, and re-registers everything.
// Returns a cleanup function to restore the original state.
func resetAppForTest(t *testing.T, tmpHome string) func() {
	t.Helper()
	wrHome := filepath.Join(tmpHome, ".work-report")
	os.Setenv("WR_HOME", wrHome)

	// Remove existing agent command tree so we can re-add with new app
	agentCmd, _, _ := rootCmd.Find([]string{"agent"})
	if agentCmd != nil {
		rootCmd.RemoveCommand(agentCmd)
	}

	// Save original app
	origApp := app

	// Create fresh app pointing to temp WR_HOME
	app = agentsdk.New("wr", version)
	registerHealthChecks()
	rootCmd.AddCommand(app.AgentCommands(newDaemonGroupCmd()))
	app.Sandbox().Ensure()

	return func() {
		// Remove the test agent commands
		agentCmd, _, _ := rootCmd.Find([]string{"agent"})
		if agentCmd != nil {
			rootCmd.RemoveCommand(agentCmd)
		}
		// Restore original app
		app = origApp
		if app != nil {
			registerHealthChecks()
			rootCmd.AddCommand(app.AgentCommands(newDaemonGroupCmd()))
		}
	}
}

// TestAgentDebugLastCrash_NoDumps verifies that "wr agent debug last-crash"
// returns a NOT_FOUND error when no crash dumps exist.
func TestAgentDebugLastCrash_NoDumps(t *testing.T) {
	tmpHome, homeCleanup := setupTempHome(t)
	defer homeCleanup()

	cleanup := resetAppForTest(t, tmpHome)
	defer cleanup()

	// Capture JSONL output
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	resetConfigFlags()
	rootCmd.SetArgs([]string{"agent", "debug", "last-crash"})
	err := rootCmd.Execute()

	// Should return an error (NOT_FOUND exit code)
	if err == nil {
		t.Fatal("expected error when no crash dumps exist, got nil")
	}

	// Verify exit code is ExitNotFound
	var exitErr *agentsdk.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != agentsdk.ExitNotFound {
		t.Errorf("expected exit code %d (ExitNotFound), got %d", agentsdk.ExitNotFound, exitErr.Code)
	}

	// Verify JSONL output contains NOT_FOUND error code
	output := strings.TrimSpace(buf.String())
	if output == "" {
		t.Fatal("expected JSONL error output, got empty string")
	}

	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if envelope["error_code"] != "NOT_FOUND" {
		t.Errorf("expected error_code=NOT_FOUND, got %v", envelope["error_code"])
	}
}

// TestAgentDebugLastCrash_WithDump verifies that "wr agent debug last-crash"
// reads and returns the most recent crash dump file.
func TestAgentDebugLastCrash_WithDump(t *testing.T) {
	tmpHome, homeCleanup := setupTempHome(t)
	defer homeCleanup()

	cleanup := resetAppForTest(t, tmpHome)
	defer cleanup()

	// Create a crash dump file manually in the sandbox crash_dumps dir
	crashDump := map[string]interface{}{
		"timestamp":    time.Now().Format(time.RFC3339),
		"app_name":     "wr",
		"app_version":  "test",
		"crash_type":   "panic",
		"panic_value":  "test panic for debug command",
		"stack_trace":  "goroutine 1 [running]:\nmain.main()\n\t/main.go:42",
		"flight_context": map[string]interface{}{
			"command": "list",
		},
	}
	dumpData, err := json.MarshalIndent(crashDump, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	crashDumpsDir := app.Sandbox().CrashDumpsDir()
	dumpPath := filepath.Join(crashDumpsDir, "crash-20260105-120000.json")
	if err := os.WriteFile(dumpPath, dumpData, 0644); err != nil {
		t.Fatalf("failed to write test crash dump: %v", err)
	}

	// Capture JSONL output
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	resetConfigFlags()
	rootCmd.SetArgs([]string{"agent", "debug", "last-crash"})
	err = rootCmd.Execute()

	if err != nil {
		t.Fatalf("agent debug last-crash failed: %v", err)
	}

	// Parse JSONL output
	output := strings.TrimSpace(buf.String())
	if output == "" {
		t.Fatal("expected JSONL output from agent debug last-crash, got empty string")
	}

	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	// Verify it's a result envelope
	if envelope["type"] != "result" {
		t.Errorf("expected type=result, got %v", envelope["type"])
	}

	// Verify data contains crash dump fields
	data, ok := envelope["data"].(map[string]interface{})
	if !ok {
		t.Fatal("envelope.data is not a map")
	}

	// Check key crash dump fields are present
	if data["crash_type"] != "panic" {
		t.Errorf("expected crash_type=panic, got %v", data["crash_type"])
	}
	if data["app_name"] != "wr" {
		t.Errorf("expected app_name=wr, got %v", data["app_name"])
	}
	panicVal, _ := data["panic_value"].(string)
	if !strings.Contains(panicVal, "test panic") {
		t.Errorf("expected panic_value to contain 'test panic', got: %s", panicVal)
	}

	// Validate full envelope structure
	var env agentsdk.Envelope
	if err := json.Unmarshal([]byte(output), &env); err != nil {
		t.Fatalf("cannot unmarshal into Envelope struct: %v", err)
	}
	if err := agentsdk.ValidateEnvelope(env); err != nil {
		t.Errorf("debug last-crash output envelope validation failed: %v", err)
	}
}
