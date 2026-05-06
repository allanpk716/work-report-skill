package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
)

// TestPanicRecovery verifies that a panic inside a command handler is caught
// by Execute()'s recover() and emitted as a FATAL_CRASH JSONL error envelope.
func TestPanicRecovery(t *testing.T) {
	if app == nil { InitApp() }
	// Save and restore original state
	origRootCmd := rootCmd
	defer func() { rootCmd = origRootCmd }()

	// Create a fresh root command to avoid polluting the real one
	rootCmd = &cobra.Command{
		Use:           "wr",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Add a command that panics
	panicCmd := &cobra.Command{
		Use: "panic-test",
		Run: func(cmd *cobra.Command, args []string) {
			panic("something went terribly wrong")
		},
	}
	rootCmd.AddCommand(panicCmd)
	rootCmd.SetArgs([]string{"panic-test"})

	// Capture JSONL output
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	// Execute and capture exit code
	code := Execute()

	// Verify exit code is 1 (ExitFatalError)
	if code != agentsdk.ExitFatalError {
		t.Errorf("expected exit code %d (ExitFatalError), got %d", agentsdk.ExitFatalError, code)
	}

	// Verify JSONL output contains FATAL_CRASH error envelope
	output := strings.TrimSpace(buf.String())
	if output == "" {
		t.Fatal("expected JSONL output on panic, got empty string")
	}

	var env map[string]interface{}
	if err := json.Unmarshal([]byte(output), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}

	if env["type"] != "error" {
		t.Errorf("expected type=error, got %v", env["type"])
	}
	if env["error_code"] != "FATAL_CRASH" {
		t.Errorf("expected error_code=FATAL_CRASH, got %v", env["error_code"])
	}
	msg, _ := env["message"].(string)
	if !strings.Contains(msg, "something went terribly wrong") {
		t.Errorf("expected message to contain panic value, got: %s", msg)
	}

	// Validate full envelope structure
	var envelope agentsdk.Envelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("cannot unmarshal into Envelope struct: %v", err)
	}
	if err := agentsdk.ValidateEnvelope(envelope); err != nil {
		t.Errorf("FATAL_CRASH envelope validation failed: %v", err)
	}
}

// TestPanicRecoveryNilPanic verifies that a panic(nil) is also caught and
// reported as FATAL_CRASH.
func TestPanicRecoveryNilPanic(t *testing.T) {
	if app == nil { InitApp() }
	origRootCmd := rootCmd
	defer func() { rootCmd = origRootCmd }()

	rootCmd = &cobra.Command{
		Use:           "wr",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	panicCmd := &cobra.Command{
		Use: "panic-nil",
		Run: func(cmd *cobra.Command, args []string) {
			panic(nil)
		},
	}
	rootCmd.AddCommand(panicCmd)
	rootCmd.SetArgs([]string{"panic-nil"})

	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	code := Execute()

	if code != agentsdk.ExitFatalError {
		t.Errorf("expected exit code %d, got %d", agentsdk.ExitFatalError, code)
	}

	var env map[string]interface{}
	output := strings.TrimSpace(buf.String())
	if err := json.Unmarshal([]byte(output), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if env["error_code"] != "FATAL_CRASH" {
		t.Errorf("expected error_code=FATAL_CRASH, got %v", env["error_code"])
	}

	// Validate full envelope structure for nil panic
	var envelope agentsdk.Envelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("cannot unmarshal into Envelope struct: %v", err)
	}
	if err := agentsdk.ValidateEnvelope(envelope); err != nil {
		t.Errorf("nil-panic FATAL_CRASH envelope validation failed: %v", err)
	}
}

// TestStderrZero verifies that normal CLI command execution produces no output
// on stderr — all output should be JSONL on stdout only.
func TestStderrZero(t *testing.T) {
	if app == nil { InitApp() }
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, fakeDaemonListHandler())
	defer srv.Close()

	// Capture stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	oldStderr := os.Stderr
	os.Stderr = w

	resetConfigFlags()
	rootCmd.SetArgs([]string{"list"})

	// Capture JSONL output (executeCmd does this but also captures stdout)
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	execErr := rootCmd.Execute()

	// Restore stderr and read captured output
	w.Close()
	os.Stderr = oldStderr
	stderrData := make([]byte, 1024)
	n, _ := r.Read(stderrData)
	stderrContent := string(stderrData[:n])

	// Verify stderr is empty
	if stderrContent != "" {
		t.Errorf("expected zero stderr output, got: %q", stderrContent)
	}

	// Also verify the command succeeded or at least produced valid JSONL
	_ = execErr // errors are acceptable — we're only checking stderr silence

	// Validate the JSONL envelope structure
	output := strings.TrimSpace(buf.String())
	if output != "" {
		var envelope agentsdk.Envelope
		if err := json.Unmarshal([]byte(output), &envelope); err != nil {
			t.Fatalf("cannot unmarshal JSONL output into Envelope: %v", err)
		}
		if err := agentsdk.ValidateEnvelope(envelope); err != nil {
			t.Errorf("normal output envelope validation failed: %v", err)
		}
	}
}

// fakeDaemonListHandler returns an HTTP handler that responds with a successful
// list result envelope, suitable for TestStderrZero.
func fakeDaemonListHandler() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		env := agentsdk.NewResultEnvelope("wr", map[string]interface{}{"records": []interface{}{}})
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		w.Write(append(b, '\n'))
	}
}
