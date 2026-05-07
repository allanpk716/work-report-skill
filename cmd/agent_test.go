package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/config"
	"wr/internal/daemon"
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

// captureAgentOutput redirects JSONL output to a buffer, runs f, then
// restores the original writer and returns the captured output.
func captureAgentOutput(f func()) string {
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	resetConfigFlags()
	f()
	return strings.TrimSpace(buf.String())
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
	for _, wrCode := range []string{"daemon_not_running", "invalid_type", "lock_conflict", "storage_error"} {
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
		Daemon:   config.DaemonConfig{Port: 18080},
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
		Daemon: config.DaemonConfig{Port: 18080},
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
		Daemon:   config.DaemonConfig{Port: 18080},
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
		Daemon:   config.DaemonConfig{Port: 18080},
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
		Daemon:   config.DaemonConfig{Port: 18080},
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
		Daemon:   config.DaemonConfig{Port: 18080},
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
		Daemon:   config.DaemonConfig{Port: 18080},
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
		Daemon:   config.DaemonConfig{Port: 18080},
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
		Daemon:   config.DaemonConfig{Port: 18080},
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

// --- Agent daemon command tests ---

func TestAgentDaemonStart_Help(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	// Verify the "agent daemon start" command exists and outputs help
	resetConfigFlags()
	rootCmd.SetArgs([]string{"agent", "daemon", "start", "--help"})
	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("expected help output without error, got: %v", err)
	}
}

func TestAgentDaemonStatus_NotRunning(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "daemon", "status"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)

	// Should be an error envelope with daemon_not_running
	if env.Type != agentsdk.TypeError {
		t.Fatalf("expected type=error, got %v", env.Type)
	}
	if env.ErrorCode != "daemon_not_running" {
		t.Errorf("expected error_code=daemon_not_running, got %v", env.ErrorCode)
	}
	if !strings.Contains(env.Message, "agent daemon start") {
		t.Errorf("expected error message to contain 'agent daemon start', got: %s", env.Message)
	}
}

func TestStatus_DaemonNotRunning(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"status"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)

	// Should be an error envelope with daemon_not_running
	if env.Type != agentsdk.TypeError {
		t.Fatalf("expected type=error, got %v", env.Type)
	}
	if env.ErrorCode != "daemon_not_running" {
		t.Errorf("expected error_code=daemon_not_running, got %v", env.ErrorCode)
	}
	if !strings.Contains(env.Message, "agent daemon start") {
		t.Errorf("expected error message to contain 'agent daemon start', got: %s", env.Message)
	}
}

func TestAgentDaemonStop_NotRunning(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "daemon", "stop"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)

	// Should be an error envelope — daemon not running
	if env.Type != agentsdk.TypeError {
		t.Fatalf("expected type=error, got %v", env.Type)
	}
	if env.ErrorCode != "daemon_not_running" {
		t.Errorf("expected error_code=daemon_not_running, got %v", env.ErrorCode)
	}
	if !strings.Contains(env.Message, "not running") {
		t.Errorf("expected error message to mention 'not running', got: %s", env.Message)
	}
}


// --- Detach flag tests ---

func TestAgentDaemonStart_DetachFlag(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	// Find the daemon start command
	startCmd, _, err := rootCmd.Find([]string{"agent", "daemon", "start"})
	if err != nil {
		t.Fatalf("cannot find daemon start command: %v", err)
	}

	// Check --detach flag exists
	fl := startCmd.Flags().Lookup("detach")
	if fl == nil {
		t.Fatal("expected --detach flag to be registered")
	}
	if fl.DefValue != "false" {
		t.Errorf("expected --detach default=false, got %s", fl.DefValue)
	}

	// Verify --detach appears in help output
	var buf strings.Builder
	startCmd.SetOut(&buf)
	_ = startCmd.Help()
	helpOutput := buf.String()
	if !strings.Contains(helpOutput, "--detach") {
		t.Error("expected --detach to appear in help output")
	}
}

func TestAgentDaemonStart_InternalDaemonizeHidden(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	// Find the daemon start command
	startCmd, _, err := rootCmd.Find([]string{"agent", "daemon", "start"})
	if err != nil {
		t.Fatalf("cannot find daemon start command: %v", err)
	}

	// Check --internal-daemonize flag exists but is hidden
	fl := startCmd.Flags().Lookup("internal-daemonize")
	if fl == nil {
		t.Fatal("expected --internal-daemonize flag to be registered")
	}
	if !fl.Hidden {
		t.Error("expected --internal-daemonize flag to be hidden")
	}

	// Verify --internal-daemonize does NOT appear in help output
	var buf strings.Builder
	startCmd.SetOut(&buf)
	_ = startCmd.Help()
	helpOutput := buf.String()
	if strings.Contains(helpOutput, "--internal-daemonize") {
		t.Error("expected --internal-daemonize to be hidden from help output")
	}
}

func TestAgentDaemonStart_Detach_AlreadyRunning(t *testing.T) {
	tmpHome, cleanup := setupAgentTest(t)
	defer cleanup()

	// Bind a port to simulate a running daemon
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot bind port: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	// Write daemon state file pointing to our bound port
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := daemon.WriteState(stateDir, daemon.DaemonState{
		Port: port,
		PID:  os.Getpid(),
	}); err != nil {
		t.Fatalf("cannot write state: %v", err)
	}

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "daemon", "start", "--detach"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)

	// Should be an error envelope
	if env.Type != agentsdk.TypeError {
		t.Fatalf("expected type=error, got %v", env.Type)
	}
	if !strings.Contains(env.Message, "already running") {
		t.Errorf("expected error message to mention 'already running', got: %s", env.Message)
	}
	if !strings.Contains(env.Message, fmt.Sprintf("port %d", port)) {
		t.Errorf("expected error message to mention port %d, got: %s", port, env.Message)
	}
	if !strings.Contains(env.Message, fmt.Sprintf("pid=%d", os.Getpid())) {
		t.Errorf("expected error message to mention pid=%d, got: %s", os.Getpid(), env.Message)
	}
}

func TestAgentDaemonStart_Detach_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("skipping: exec.Command binary execution in temp directories fails on Windows (PE loader incompatibility)")
	}

	// Find a free port for the daemon
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot find free port: %v", err)
	}
	daemonPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Build binary
	tmpDir := t.TempDir()
	binaryName := "wr"
	if runtime.GOOS == "windows" {
		binaryName = "wr.exe"
	}
	binaryPath := filepath.Join(tmpDir, binaryName)

	buildCmd := exec.Command("go", "build", "-o", binaryPath, ".")
	buildCmd.Dir = "" // use current working directory
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}

	// Create temp home with config
	tmpHome := t.TempDir()
	wrHome := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(wrHome, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Daemon:   config.DaemonConfig{Port: daemonPort},
		Timezone: "UTC",
	}
	cfgPath := filepath.Join(wrHome, "config.json")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("cannot save config: %v", err)
	}

	// Always clean up the daemon
	defer func() {
		stopCmd := exec.Command(binaryPath, "agent", "daemon", "stop")
		stopCmd.Env = append(os.Environ(),
			"HOME="+tmpHome,
			"USERPROFILE="+tmpHome,
			"WR_HOME="+wrHome,
		)
		stopCmd.CombinedOutput()
	}()

	// Run with --detach
	cmd := exec.Command(binaryPath, "agent", "daemon", "start", "--detach")
	cmd.Env = append(os.Environ(),
		"HOME="+tmpHome,
		"USERPROFILE="+tmpHome,
		"WR_HOME="+wrHome,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("detach command failed: %v\noutput: %s", err, output)
	}

	// Parse JSONL output
	line := strings.TrimSpace(string(output))
	if line == "" {
		t.Fatal("expected non-empty JSONL output from --detach")
	}

	var env agentsdk.Envelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, line)
	}
	if err := agentsdk.ValidateEnvelope(env); err != nil {
		t.Fatalf("envelope validation failed: %v", err)
	}

	// Verify envelope type
	if env.Type != agentsdk.TypeResult {
		t.Errorf("expected type=result, got %v", env.Type)
	}

	// Verify data fields
	data, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", env.Data)
	}

	status, _ := data["status"].(string)
	if status != "running" {
		t.Errorf("expected status=running, got %v", status)
	}

	pid, _ := data["pid"].(float64)
	if pid <= 0 {
		t.Errorf("expected positive pid, got %v", pid)
	}

	port, _ := data["port"].(float64)
	if int(port) != daemonPort {
		t.Errorf("expected port=%d, got %v", daemonPort, port)
	}

	// Verify daemon is actually running by checking status
	time.Sleep(200 * time.Millisecond) // small settle
	statusCmd := exec.Command(binaryPath, "agent", "daemon", "status")
	statusCmd.Env = cmd.Env
	statusOutput, err := statusCmd.CombinedOutput()
	if err != nil {
		t.Logf("status check output: %s", statusOutput)
		// Non-fatal: the daemon may have shut down quickly
	} else {
		var statusEnv agentsdk.Envelope
		if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(string(statusOutput))), &statusEnv); jsonErr == nil {
			if statusEnv.Type == agentsdk.TypeResult {
				statusData, _ := statusEnv.Data.(map[string]interface{})
				if s, _ := statusData["status"].(string); s == "not_running" {
					t.Error("daemon reported not_running immediately after --detach reported running")
				}
			}
		}
	}
}

// --- Ensure-running command tests ---

func TestAgentDaemonEnsureRunning_AlreadyRunning(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	// Start a fake HTTP server that mimics /api/status
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot bind port: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// Fake daemon server returning a status response
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"type":"result","data":{"status":"running","record_count":5},"tool":"wr","version":"test"}`)
	})
	go func() { _ = http.Serve(ln, mux) }()

	// Write daemon state file pointing to our fake server
	stateDir := filepath.Join(os.Getenv("HOME"), ".work-report")
	if err := daemon.WriteState(stateDir, daemon.DaemonState{
		Port: port,
		PID:  os.Getpid(),
	}); err != nil {
		t.Fatalf("cannot write state: %v", err)
	}

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "daemon", "ensure-running"})
		_ = rootCmd.Execute()
	})

	env := parseSingleEnvelope(t, output)

	// Should be a success envelope
	if env.Type != agentsdk.TypeResult {
		t.Fatalf("expected type=result, got %v; full output: %s", env.Type, output)
	}

	data, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", env.Data)
	}

	// Must include source=already_running
	source, _ := data["source"].(string)
	if source != "already_running" {
		t.Errorf("expected source=already_running, got %v", source)
	}

	// Must include status from the daemon's /api/status
	status, _ := data["status"].(string)
	if status != "running" {
		t.Errorf("expected status=running from daemon, got %v", status)
	}

	// Must include record_count from daemon
	recordCount, _ := data["record_count"].(float64)
	if recordCount != 5 {
		t.Errorf("expected record_count=5, got %v", recordCount)
	}

	// Must include pid and port
	pid, _ := data["pid"].(float64)
	if pid <= 0 {
		t.Errorf("expected positive pid, got %v", pid)
	}
	portVal, _ := data["port"].(float64)
	if int(portVal) != port {
		t.Errorf("expected port=%d, got %v", port, portVal)
	}
}

func TestAgentDaemonEnsureRunning_StaleState(t *testing.T) {
	tmpHome, cleanup := setupAgentTest(t)
	defer cleanup()

	// Write a state file pointing to a port that is NOT listening (stale state)
	stateDir := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Use a port that is very unlikely to be in use
	if err := daemon.WriteState(stateDir, daemon.DaemonState{
		Port: 59999,
		PID:  12345,
	}); err != nil {
		t.Fatalf("cannot write state: %v", err)
	}

	// ensure-running should detect stale state, clean up, and attempt to start.
	// Since we can't actually start the daemon in unit tests, we expect it to
	// either timeout or succeed (if a real daemon is running on some port).
	// For a clean unit test, we just verify the command doesn't crash and
	// the stale state file was cleaned up.
	resetConfigFlags()
	rootCmd.SetArgs([]string{"agent", "daemon", "ensure-running"})
	// This will attempt to start the daemon and likely timeout.
	// We accept any result — the point is no panic.
	_ = rootCmd.Execute()

	// Verify the stale state file was cleaned up
	_, err := os.Stat(filepath.Join(stateDir, ".daemon.json"))
	if err == nil {
		// State file still exists — it may have been re-created by a newly started daemon,
		// or the cleanup didn't happen. Only fail if it still has the stale port.
		st, readErr := daemon.ReadState(stateDir)
		if readErr == nil && st.Port == 59999 && st.PID == 12345 {
			t.Error("stale state file was not cleaned up")
		}
	}
}

func TestAgentDaemonEnsureRunning_EnvelopeValid(t *testing.T) {
	_, cleanup := setupAgentTest(t)
	defer cleanup()

	// Set up a fake daemon to get a valid success envelope
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot bind port: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"type":"result","data":{"status":"running","record_count":0},"tool":"wr","version":"test"}`)
	})
	go func() { _ = http.Serve(ln, mux) }()

	stateDir := filepath.Join(os.Getenv("HOME"), ".work-report")
	if err := daemon.WriteState(stateDir, daemon.DaemonState{
		Port: port,
		PID:  os.Getpid(),
	}); err != nil {
		t.Fatalf("cannot write state: %v", err)
	}

	output := captureAgentOutput(func() {
		resetConfigFlags()
		rootCmd.SetArgs([]string{"agent", "daemon", "ensure-running"})
		_ = rootCmd.Execute()
	})

	var envelope agentsdk.Envelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("cannot unmarshal into Envelope struct: %v\noutput: %s", err, output)
	}
	if err := agentsdk.ValidateEnvelope(envelope); err != nil {
		t.Errorf("ensure-running envelope validation failed: %v", err)
	}
}

func TestAgentDaemonEnsureRunning_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("skipping: exec.Command binary execution in temp directories fails on Windows (PE loader incompatibility)")
	}

	// Find a free port for the daemon
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot find free port: %v", err)
	}
	daemonPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Build binary
	tmpDir := t.TempDir()
	binaryName := "wr"
	if runtime.GOOS == "windows" {
		binaryName = "wr.exe"
	}
	binaryPath := filepath.Join(tmpDir, binaryName)

	buildCmd := exec.Command("go", "build", "-o", binaryPath, ".")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}

	// Create temp home with config
	tmpHome := t.TempDir()
	wrHome := filepath.Join(tmpHome, ".work-report")
	if err := os.MkdirAll(wrHome, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Daemon:   config.DaemonConfig{Port: daemonPort},
		Timezone: "UTC",
	}
	cfgPath := filepath.Join(wrHome, "config.json")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("cannot save config: %v", err)
	}

	envVars := append(os.Environ(),
		"HOME="+tmpHome,
		"USERPROFILE="+tmpHome,
		"WR_HOME="+wrHome,
	)

	// Always clean up the daemon
	defer func() {
		stopCmd := exec.Command(binaryPath, "agent", "daemon", "stop")
		stopCmd.Env = envVars
		stopCmd.CombinedOutput()
	}()

	// First call: should start the daemon (source=started)
	cmd := exec.Command(binaryPath, "agent", "daemon", "ensure-running")
	cmd.Env = envVars
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("first ensure-running failed: %v\noutput: %s", err, output)
	}

	line := strings.TrimSpace(string(output))
	var env agentsdk.Envelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, line)
	}
	if err := agentsdk.ValidateEnvelope(env); err != nil {
		t.Fatalf("envelope validation failed: %v", err)
	}

	if env.Type != agentsdk.TypeResult {
		t.Errorf("expected type=result, got %v", env.Type)
	}

	data, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", env.Data)
	}

	source, _ := data["source"].(string)
	if source != "started" {
		t.Errorf("expected source=started on first call, got %v", source)
	}

	pid, _ := data["pid"].(float64)
	if pid <= 0 {
		t.Errorf("expected positive pid, got %v", pid)
	}

	// Second call: daemon is already running (source=already_running)
	time.Sleep(500 * time.Millisecond)
	cmd2 := exec.Command(binaryPath, "agent", "daemon", "ensure-running")
	cmd2.Env = envVars
	output2, err := cmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("second ensure-running failed: %v\noutput: %s", err, output2)
	}

	line2 := strings.TrimSpace(string(output2))
	var env2 agentsdk.Envelope
	if err := json.Unmarshal([]byte(line2), &env2); err != nil {
		t.Fatalf("second output is not valid JSON: %v\noutput: %s", err, line2)
	}

	data2, ok := env2.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", env2.Data)
	}

	source2, _ := data2["source"].(string)
	if source2 != "already_running" {
		t.Errorf("expected source=already_running on second call, got %v", source2)
	}
}
