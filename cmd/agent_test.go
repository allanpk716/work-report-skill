package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/config"
)

// setupAgentTest creates an isolated test environment for agent meta-commands.
// It resets the app to a fresh instance with a temp home dir, registers error
// codes, config provider, and command meta, and returns the temp home dir and
// a combined cleanup function.
func setupAgentTest(t *testing.T) (tmpHome string, cleanup func()) {
	t.Helper()
	tmpHome, homeCleanup := setupTempHome(t)

	// resetAppForTest creates a fresh app with the agent command tree.
	appCleanup := resetAppForTest(t, tmpHome)

	// Register wr-specific registrations on the fresh app.
	registerErrorCodes()
	registerConfigProvider()
	registerCommandMeta()

	return tmpHome, func() {
		appCleanup()
		homeCleanup()
	}
}

// captureAgentOutput redirects JSONL output AND os.Stdout to a buffer, runs f,
// then restores the original writer and stdout and returns the captured output.
// This is necessary because some commands write to os.Stdout directly
// (not through the SDK writer), so both must be captured for complete output.
func captureAgentOutput(f func()) string {
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	// Also capture os.Stdout
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w

	resetConfigFlags()
	f()

	w.Close()
	os.Stdout = oldStdout

	// Read captured stdout and combine with SDK writer output
	stdoutData, _ := io.ReadAll(r)
	return strings.TrimSpace(buf.String() + string(stdoutData))
}

// parseSingleEnvelope parses a single JSONL line into an agentsdk.Envelope
// and validates it. Fatals on parse or validation failure.
func parseSingleEnvelope(t *testing.T, output string) agentsdk.Envelope {
	t.Helper()
	var env agentsdk.Envelope
	if err := json.Unmarshal([]byte(output), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}
	if err := agentsdk.ValidateEnvelope(env); err != nil {
		t.Fatalf("envelope validation failed: %v", err)
	}
	return env
}

// writeTestConfig creates a config file in the temp home with known values
// including sensitive fields that should be redacted.
func writeTestConfig(t *testing.T, tmpHome string, cfg *config.Config) {
	t.Helper()
	wrHome := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(wrHome, 0755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(wrHome, "config.json")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("write test config: %v", err)
	}
}

// --- Schema tests ---

func TestAgentSchema(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "schema"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)

	// Must be a result envelope
	if env.Type != agentsdk.TypeResult {
		t.Fatalf("expected type=result, got %v", env.Type)
	}
	if env.Data == nil {
		t.Fatal("result envelope must have data")
	}

	// Parse data into map for field inspection
	data, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", env.Data)
	}

	// Verify tool and version fields
	if data["tool"] != "wr" {
		t.Errorf("expected tool=wr, got %v", data["tool"])
	}
	if data["version"] == nil || data["version"] == "" {
		t.Error("expected non-empty version field")
	}

	// Verify commands array exists and has entries
	commands, ok := data["commands"].([]interface{})
	if !ok {
		t.Fatalf("expected commands to be an array, got %T", data["commands"])
	}
	if len(commands) == 0 {
		t.Fatal("expected at least one command in schema output")
	}

	// Verify key commands appear in the schema
	cmdNames := make(map[string]bool)
	for _, c := range commands {
		entry, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		cmdNames[name] = true
	}

	for _, expected := range []string{"add", "update", "complete", "cancel", "list", "export"} {
		if !cmdNames[expected] {
			t.Errorf("expected command %q in schema output, not found", expected)
		}
	}

	// Verify agent schema/errors/config commands also appear
	for _, expected := range []string{"agent schema", "agent errors", "agent config list", "agent config set"} {
		if !cmdNames[expected] {
			t.Errorf("expected agent meta-command %q in schema output, not found", expected)
		}
	}
}

func TestAgentSchema_CommandMetaEnriched(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "schema"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)
	data := env.Data.(map[string]interface{})
	commands := data["commands"].([]interface{})

	// Find the "add" command and verify it has CommandMeta enrichment
	for _, c := range commands {
		entry, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		if name == "add" {
			desc, _ := entry["description"].(string)
			if desc == "" {
				t.Error("expected 'add' command to have description from CommandMeta")
			}
			if !strings.Contains(strings.ToLower(desc), "add") {
				t.Errorf("expected 'add' description to mention 'add', got: %s", desc)
			}
			break
		}
	}

	// Verify "list" command has is_idempotent=true (registered as idempotent)
	for _, c := range commands {
		entry, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		if name == "list" {
			idempotent, _ := entry["is_idempotent"].(bool)
			if !idempotent {
				t.Error("expected 'list' command to be marked as idempotent")
			}
			break
		}
	}
}

// --- Errors tests ---

func TestAgentErrors(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "errors"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)

	if env.Type != agentsdk.TypeResult {
		t.Fatalf("expected type=result, got %v", env.Type)
	}

	data, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", env.Data)
	}

	// Verify codes array exists
	codes, ok := data["codes"].([]interface{})
	if !ok {
		t.Fatalf("expected codes to be an array, got %T", data["codes"])
	}

	// Verify count field matches the codes array length
	count, _ := data["count"].(float64)
	if int(count) != len(codes) {
		t.Errorf("expected count=%d to match codes length=%d", int(count), len(codes))
	}

	// Must have at least the built-in codes (5) plus wr-specific codes (16)
	if len(codes) < 5 {
		t.Errorf("expected at least 5 built-in error codes, got %d", len(codes))
	}

	// Verify built-in codes appear
	codeNames := make(map[string]bool)
	for _, c := range codes {
		entry, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		code, _ := entry["code"].(string)
		codeNames[code] = true
	}

	for _, builtIn := range []string{"FATAL_CRASH", "INTERNAL_ERROR", "INPUT_INVALID", "NOT_FOUND", "RESOURCE_LOCKED"} {
		if !codeNames[builtIn] {
			t.Errorf("expected built-in code %q in errors output", builtIn)
		}
	}

	// Verify wr-specific codes appear
	for _, wrCode := range []string{"storage_locked", "invalid_type", "lock_conflict", "storage_error"} {
		if !codeNames[wrCode] {
			t.Errorf("expected wr-specific code %q in errors output", wrCode)
		}
	}
}

func TestAgentErrors_CodesSorted(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "errors"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)
	data := env.Data.(map[string]interface{})
	codes := data["codes"].([]interface{})

	// Verify codes are sorted alphabetically
	var prevCode string
	for i, c := range codes {
		entry, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		code, _ := entry["code"].(string)
		if prevCode != "" && code < prevCode {
			t.Errorf("codes not sorted: %q (index %d) comes after %q", code, i, prevCode)
		}
		prevCode = code
	}
}

func TestAgentErrors_EachCodeHasRequiredFields(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "errors"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)
	data := env.Data.(map[string]interface{})
	codes := data["codes"].([]interface{})

	for i, c := range codes {
		entry, ok := c.(map[string]interface{})
		if !ok {
			t.Errorf("codes[%d] is not a map: %T", i, c)
			continue
		}

		// Each error entry must have code, exit_code, and description
		if _, ok := entry["code"].(string); !ok || entry["code"] == "" {
			t.Errorf("codes[%d] missing or empty 'code' field", i)
		}
		if _, ok := entry["exit_code"].(float64); !ok {
			t.Errorf("codes[%d] missing or non-numeric 'exit_code' field", i)
		}
		if _, ok := entry["description"].(string); !ok || entry["description"] == "" {
			t.Errorf("codes[%d] missing or empty 'description' field", i)
		}
	}
}

// --- Config list tests ---

func TestAgentConfigList(t *testing.T) {
	tmpHome, cleanup := setupAgentTest(t)
	defer cleanup()

	// Write a config with sensitive fields
	writeTestConfig(t, tmpHome, &config.Config{
		Pushover: config.PushoverConfig{
			APIToken: "secret-token-12345",
			UserKey:  "secret-key-67890",
		},
		LLM: config.LLMConfig{
			Text: config.LLMProviderConfig{
				Provider: "openai",
				APIKey:   "sk-my-api-key-value",
				Model:    "gpt-4",
			},
		},
		Timezone: "UTC",
	})

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "config", "list"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)

	if env.Type != agentsdk.TypeResult {
		t.Fatalf("expected type=result, got %v", env.Type)
	}

	data, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", env.Data)
	}

	// Data should have a "config" key wrapping the redacted config
	configData, ok := data["config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data.config to be a map, got %T", data["config"])
	}

	// Verify pushover section is redacted
	pushover, ok := configData["pushover"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected pushover to be a map, got %T", configData["pushover"])
	}
	apiToken, _ := pushover["api_token"].(string)
	if apiToken == "secret-token-12345" {
		t.Error("api_token should be redacted but contains the raw secret")
	}
	if apiToken == "" {
		t.Error("api_token should not be empty after redaction")
	}

	userKey, _ := pushover["user_key"].(string)
	if userKey == "secret-key-67890" {
		t.Error("user_key should be redacted but contains the raw secret")
	}

	// Verify LLM api_key is redacted
	llm, ok := configData["llm"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected llm to be a map, got %T", configData["llm"])
	}
	text, ok := llm["text"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected llm.text to be a map, got %T", llm["text"])
	}
	apiKey, _ := text["api_key"].(string)
	if apiKey == "sk-my-api-key-value" {
		t.Error("llm.text.api_key should be redacted but contains the raw secret")
	}

	// Verify non-sensitive fields are preserved
	provider, _ := text["provider"].(string)
	if provider != "openai" {
		t.Errorf("expected provider=openai, got %v", provider)
	}
	model, _ := text["model"].(string)
	if model != "gpt-4" {
		t.Errorf("expected model=gpt-4, got %v", model)
	}
}

func TestAgentConfigList_NoSecretsLeaked(t *testing.T) {
	tmpHome, cleanup := setupAgentTest(t)
	defer cleanup()

	// Write config with specific known secrets
	writeTestConfig(t, tmpHome, &config.Config{
		Pushover: config.PushoverConfig{
			APIToken: "tok_abcdef123456",
			UserKey:  "key_xyz789def012",
		},
		LLM: config.LLMConfig{
			Text: config.LLMProviderConfig{
				APIKey: "sk-live-abc123xyz",
			},
			Vision: config.LLMProviderConfig{
				APIKey: "sk-vision-key-999",
			},
		},
	})

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "config", "list"})
		_ = rootCmd.Execute()
	})

	// Raw output must not contain any of the secret values
	for _, secret := range []string{"tok_abcdef123456", "key_xyz789def012", "sk-live-abc123xyz", "sk-vision-key-999"} {
		if strings.Contains(output, secret) {
			t.Errorf("config list output contains leaked secret: %q", secret)
		}
	}
}

// --- Config set tests ---

func TestAgentConfigSet_ValidPath(t *testing.T) {
	tmpHome, cleanup := setupAgentTest(t)
	defer cleanup()

	// Write initial config
	writeTestConfig(t, tmpHome, &config.Config{
		Timezone: "UTC",
	})

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "config", "set", "timezone", "America/New_York"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)

	if env.Type != agentsdk.TypeResult {
		t.Fatalf("expected type=result, got %v", env.Type)
	}

	data, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", env.Data)
	}

	// Verify set confirmation
	setData, ok := data["set"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data.set to be a map, got %T", data["set"])
	}
	if setData["path"] != "timezone" {
		t.Errorf("expected set.path=timezone, got %v", setData["path"])
	}
	if setData["value"] != "America/New_York" {
		t.Errorf("expected set.value=America/New_York, got %v", setData["value"])
	}
}

func TestAgentConfigSet_InvalidPath(t *testing.T) {
	tmpHome, cleanup := setupAgentTest(t)
	defer cleanup()

	// Write initial config
	writeTestConfig(t, tmpHome, &config.Config{
		Timezone: "UTC",
	})

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "config", "set", "nonexistent.field", "value"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)

	// Should be an error envelope with INPUT_INVALID
	if env.Type != agentsdk.TypeError {
		t.Fatalf("expected type=error, got %v", env.Type)
	}
	if env.ErrorCode != "INPUT_INVALID" {
		t.Errorf("expected error_code=INPUT_INVALID, got %v", env.ErrorCode)
	}
	if !strings.Contains(env.Message, "not in whitelist") && !strings.Contains(env.Message, "unknown field") {
		t.Errorf("expected error message to mention whitelist violation, got: %s", env.Message)
	}
}

func TestAgentConfigSet_InvalidPath_ExitCode(t *testing.T) {
	tmpHome, cleanup := setupAgentTest(t)
	defer cleanup()

	writeTestConfig(t, tmpHome, &config.Config{
		Timezone: "UTC",
	})

	resetConfigFlags()
	rootCmd.SetArgs([]string{"agent", "config", "set", "bogus.path", "value"})
	err := rootCmd.Execute()

	if err == nil {
		t.Fatal("expected error for invalid config path, got nil")
	}

	var exitErr *agentsdk.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != agentsdk.ExitInvalidParams {
		t.Errorf("expected exit code %d (ExitInvalidParams), got %d", agentsdk.ExitInvalidParams, exitErr.Code)
	}
}

func TestAgentConfigSet_ReadRoundtrip(t *testing.T) {
	tmpHome, cleanup := setupAgentTest(t)
	defer cleanup()

	// Write initial config
	writeTestConfig(t, tmpHome, &config.Config{
		Timezone: "UTC",
	})

	// Set timezone via agent config set
	_ = captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "config", "set", "timezone", "Europe/London"})
		_ = rootCmd.Execute()
	})

	// Now list config and verify timezone was persisted
	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "config", "list"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)
	data := env.Data.(map[string]interface{})
	configData := data["config"].(map[string]interface{})

	tz, _ := configData["timezone"].(string)
	if tz != "Europe/London" {
		t.Errorf("expected timezone=Europe/London after set+read roundtrip, got %v", tz)
	}
}

func TestAgentConfigSet_InvalidTimezone(t *testing.T) {
	tmpHome, cleanup := setupAgentTest(t)
	defer cleanup()

	writeTestConfig(t, tmpHome, &config.Config{
		Timezone: "UTC",
	})

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "config", "set", "timezone", "Invalid/Timezone"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)

	// Should be an error (invalid timezone value)
	if env.Type != agentsdk.TypeError {
		t.Fatalf("expected type=error for invalid timezone, got %v", env.Type)
	}
}

// --- Envelope validation across all commands ---

func TestAgentSchema_EnvelopeValid(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "schema"})
		_ = rootCmd.Execute()
	})

	// Validate the full envelope structure
	var envelope agentsdk.Envelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("cannot unmarshal into Envelope struct: %v", err)
	}
	if err := agentsdk.ValidateEnvelope(envelope); err != nil {
		t.Errorf("schema envelope validation failed: %v", err)
	}
}

func TestAgentErrors_EnvelopeValid(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "errors"})
		_ = rootCmd.Execute()
	})

	var envelope agentsdk.Envelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("cannot unmarshal into Envelope struct: %v", err)
	}
	if err := agentsdk.ValidateEnvelope(envelope); err != nil {
		t.Errorf("errors envelope validation failed: %v", err)
	}
}

func TestAgentConfigList_EnvelopeValid(t *testing.T) {
	tmpHome, cleanup := setupAgentTest(t)
	defer cleanup()

	writeTestConfig(t, tmpHome, &config.Config{
		Timezone: "UTC",
	})

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "config", "list"})
		_ = rootCmd.Execute()
	})

	var envelope agentsdk.Envelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("cannot unmarshal into Envelope struct: %v", err)
	}
	if err := agentsdk.ValidateEnvelope(envelope); err != nil {
		t.Errorf("config list envelope validation failed: %v", err)
	}
}

func TestAgentConfigSet_EnvelopeValid(t *testing.T) {
	tmpHome, cleanup := setupAgentTest(t)
	defer cleanup()

	writeTestConfig(t, tmpHome, &config.Config{
		Timezone: "UTC",
	})

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "config", "set", "daemon.port", "9090"})
		_ = rootCmd.Execute()
	})

	var envelope agentsdk.Envelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("cannot unmarshal into Envelope struct: %v", err)
	}
	if err := agentsdk.ValidateEnvelope(envelope); err != nil {
		t.Errorf("config set envelope validation failed: %v", err)
	}
}
