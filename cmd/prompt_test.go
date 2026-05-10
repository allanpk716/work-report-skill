package cmd

import (
	"os"
	"path/filepath"
	"testing"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/digest"
)

// setupDigestStore initializes a digest store at a temp path for testing.
func setupDigestStore(t *testing.T) (*digest.DigestStore, string) {
	t.Helper()
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "digests.json")
	return digest.NewStore(storePath), storePath
}

// setupDigestTestHome sets up a temp home with a config and a digest store.
func setupDigestTestHome(t *testing.T) (*digest.DigestStore, func()) {
	t.Helper()
	tmpHome, cleanup := setupTempHome(t)

	// Create the .work-report directory
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Init config
	executeCmd("config", "init")

	// The digest store will be at the default path under tmpHome
	store := digest.NewStore(filepath.Join(stateDir, "digests.json"))
	return store, cleanup
}

// TestPromptListSuccess verifies that "wr prompt list" returns built-in prompts.
func TestPromptListSuccess(t *testing.T) {
	_, cleanup := setupDigestTestHome(t)
	defer cleanup()

	code, out := executeCmd("prompt", "list")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "result" {
		t.Errorf("expected type=result, got %v", lines[0]["type"])
	}

	// Should have at least 2 built-in prompts (agenda, report)
	data := unwrapData(lines[0])
	if data == nil {
		t.Fatal("expected data in envelope")
	}
	count, _ := data["count"].(float64)
	if count < 2 {
		t.Errorf("expected at least 2 prompts, got %d", int(count))
	}

	validateAllEnvelopes(t, out)
}

// TestPromptShowSuccess verifies that "wr prompt show <name>" returns prompt text.
func TestPromptShowSuccess(t *testing.T) {
	_, cleanup := setupDigestTestHome(t)
	defer cleanup()

	code, out := executeCmd("prompt", "show", "agenda")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "result" {
		t.Errorf("expected type=result, got %v", lines[0]["type"])
	}

	data := unwrapData(lines[0])
	text, _ := data["text"].(string)
	if text == "" {
		t.Errorf("expected non-empty prompt text, got empty string")
	}

	validateAllEnvelopes(t, out)
}

// TestPromptSetWithText verifies that "wr prompt set <name> --text ..." works.
func TestPromptSetWithText(t *testing.T) {
	_, cleanup := setupDigestTestHome(t)
	defer cleanup()

	code, out := executeCmd("prompt", "set", "agenda", "--text", "custom agenda text")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	// Verify the change persisted
	code, out = executeCmd("prompt", "show", "agenda")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	text, _ := data["text"].(string)
	if text != "custom agenda text" {
		t.Errorf("expected text='custom agenda text', got %q", text)
	}

	validateAllEnvelopes(t, out)
}

// TestPromptSetWithFile verifies that "wr prompt set <name> --file ..." reads from file.
func TestPromptSetWithFile(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	// Create temp file with prompt text
	promptFile := filepath.Join(tmpHome, "prompt.txt")
	if err := os.WriteFile(promptFile, []byte("file-based prompt text"), 0644); err != nil {
		t.Fatal(err)
	}

	// Init config
	stateDir := filepath.Join(tmpHome, ".work-report")
	os.MkdirAll(stateDir, 0755)
	executeCmd("config", "init")

	code, out := executeCmd("prompt", "set", "report", "--file", promptFile)
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	validateAllEnvelopes(t, out)
}

// TestPromptSetMissingFlags verifies that "wr prompt set <name>" without
// --text or --file returns exit code 2 (invalid params).
func TestPromptSetMissingFlags(t *testing.T) {
	_, cleanup := setupDigestTestHome(t)
	defer cleanup()

	code, out := executeCmd("prompt", "set", "agenda")
	if code != agentsdk.ExitInvalidParams {
		t.Errorf("expected exit code %d (ExitInvalidParams), got %d; output: %s", agentsdk.ExitInvalidParams, code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL error output")
	}
	if lines[0]["type"] != "error" {
		t.Errorf("expected type=error, got %v", lines[0]["type"])
	}
}

// TestPromptResetSuccess verifies that "wr prompt reset <name>" resets to default.
func TestPromptResetSuccess(t *testing.T) {
	_, cleanup := setupDigestTestHome(t)
	defer cleanup()

	// Set custom text first
	executeCmd("prompt", "set", "agenda", "--text", "custom text")

	// Reset
	code, out := executeCmd("prompt", "reset", "agenda")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	// Verify it's back to default
	code, out = executeCmd("prompt", "show", "agenda")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	text, _ := data["text"].(string)
	if text == "custom text" {
		t.Errorf("expected default prompt text after reset, got %q", text)
	}

	validateAllEnvelopes(t, out)
}

// TestPromptNotFound verifies that prompt_not_found error maps to
// exit code 2 (invalid params, as registered in errors.go).
func TestPromptNotFound(t *testing.T) {
	_, cleanup := setupDigestTestHome(t)
	defer cleanup()

	code, out := executeCmd("prompt", "show", "unknown_prompt_that_does_not_exist")
	if code != agentsdk.ExitInvalidParams {
		t.Errorf("expected exit code %d (ExitInvalidParams), got %d; output: %s", agentsdk.ExitInvalidParams, code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL error output")
	}
	found := false
	for _, line := range lines {
		if line["error_code"] == "prompt_not_found" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=prompt_not_found in output, got %v", lines)
	}

	validateAllEnvelopes(t, out)
}

// TestPromptCommandRegistration verifies that the prompt command and its
// subcommands are properly registered with the root command.
func TestPromptCommandRegistration(t *testing.T) {
	if app == nil {
		InitApp()
	}

	// Verify parent command
	prompt, _, err := rootCmd.Find([]string{"prompt"})
	if err != nil {
		t.Fatalf("prompt command not registered: %v", err)
	}
	if prompt.Use != "prompt" {
		t.Errorf("expected Use='prompt', got %q", prompt.Use)
	}

	// Verify subcommands exist
	for _, sub := range []string{"list", "show", "set", "reset", "preview"} {
		cmd, _, err := rootCmd.Find([]string{"prompt", sub})
		if err != nil {
			t.Errorf("prompt %s subcommand not registered: %v", sub, err)
		}
		if cmd == nil {
			continue
		}
		if cmd.Use != sub {
			if len(cmd.Use) < len(sub) || cmd.Use[:len(sub)] != sub {
				t.Errorf("expected prompt %s Use to start with %q, got %q", sub, sub, cmd.Use)
			}
		}
	}
}

// TestDigestPreviewMissingArg verifies that "wr digest preview" without an ID
// argument fails with a usage error (cobra validation).
func TestDigestPreviewMissingArg(t *testing.T) {
	_, cleanup := setupDigestTestHome(t)
	defer cleanup()

	code, _ := executeCmd("digest", "preview")
	if code == agentsdk.ExitSuccess {
		t.Error("expected non-zero exit code for missing argument")
	}
}

// TestDigestAddListRemove verifies basic digest CRUD without daemon.
func TestDigestAddListRemove(t *testing.T) {
	_, cleanup := setupDigestTestHome(t)
	defer cleanup()

	// Add a digest
	code, out := executeCmd("digest", "add", "--schedule", "0 8 * * *", "--scope", "today", "--direction", "agenda")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0 for digest add, got %d; output: %s", code, string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	digestMap, _ := data["digest"].(map[string]interface{})
	digestID, _ := digestMap["id"].(string)
	if digestID == "" {
		t.Fatal("expected digest ID in output")
	}

	// List
	code, out = executeCmd("digest", "list")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0 for digest list, got %d; output: %s", code, string(out))
	}
	lines = parseJSONLMaps(out)
	data = unwrapData(lines[0])
	count, _ := data["count"].(float64)
	if int(count) < 1 {
		t.Errorf("expected at least 1 digest, got %d", int(count))
	}

	// Remove
	code, out = executeCmd("digest", "remove", digestID)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0 for digest remove, got %d; output: %s", code, string(out))
	}

	// Verify removed
	code, out = executeCmd("digest", "remove", digestID)
	if code != agentsdk.ExitFatalError {
		t.Errorf("expected exit code 1 after removing non-existent digest, got %d; output: %s", code, string(out))
	}

	validateAllEnvelopes(t, out)
}

// TestDigestInvalidScope verifies validation of invalid scope.
func TestDigestInvalidScope(t *testing.T) {
	_, cleanup := setupDigestTestHome(t)
	defer cleanup()

	code, out := executeCmd("digest", "add", "--schedule", "0 8 * * *", "--scope", "invalid", "--direction", "agenda")
	if code != agentsdk.ExitInvalidParams {
		t.Errorf("expected exit code 2 for invalid scope, got %d; output: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["error_code"] != "invalid_scope" {
		t.Errorf("expected error_code=invalid_scope, got %v", lines[0]["error_code"])
	}

	validateAllEnvelopes(t, out)
}

// TestDigestInvalidDirection verifies validation of invalid direction.
func TestDigestInvalidDirection(t *testing.T) {
	_, cleanup := setupDigestTestHome(t)
	defer cleanup()

	code, out := executeCmd("digest", "add", "--schedule", "0 8 * * *", "--scope", "today", "--direction", "invalid")
	if code != agentsdk.ExitInvalidParams {
		t.Errorf("expected exit code 2 for invalid direction, got %d; output: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["error_code"] != "invalid_direction" {
		t.Errorf("expected error_code=invalid_direction, got %v", lines[0]["error_code"])
	}

	validateAllEnvelopes(t, out)
}

// TestDigestEnableDisable verifies enable/disable operations.
func TestDigestEnableDisable(t *testing.T) {
	_, cleanup := setupDigestTestHome(t)
	defer cleanup()

	// Add
	code, out := executeCmd("digest", "add", "--schedule", "0 8 * * *", "--scope", "today", "--direction", "summary")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0 for digest add, got %d; output: %s", code, string(out))
	}
	lines := parseJSONLMaps(out)
	data := unwrapData(lines[0])
	digestMap, _ := data["digest"].(map[string]interface{})
	digestID, _ := digestMap["id"].(string)

	// Disable
	code, out = executeCmd("digest", "disable", digestID)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0 for digest disable, got %d; output: %s", code, string(out))
	}

	// Enable
	code, out = executeCmd("digest", "enable", digestID)
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0 for digest enable, got %d; output: %s", code, string(out))
	}

	validateAllEnvelopes(t, out)
}

// TestDigestPreviewRegistration verifies that the digest preview command is
// properly registered.
func TestDigestPreviewRegistration(t *testing.T) {
	if app == nil {
		InitApp()
	}

	cmd, _, err := rootCmd.Find([]string{"digest", "preview"})
	if err != nil {
		t.Fatalf("digest preview subcommand not registered: %v", err)
	}
	if cmd == nil {
		t.Fatal("digest preview command is nil")
	}
	if len(cmd.Use) < 7 || cmd.Use[:7] != "preview" {
		t.Errorf("expected digest preview Use to start with 'preview', got %q", cmd.Use)
	}
}

// TestPromptPreviewMissingArg verifies that "wr prompt preview" without a name
// argument fails with a usage error.
func TestPromptPreviewMissingArg(t *testing.T) {
	_, cleanup := setupDigestTestHome(t)
	defer cleanup()

	code, _ := executeCmd("prompt", "preview")
	if code == agentsdk.ExitSuccess {
		t.Error("expected non-zero exit code for missing argument")
	}
}
