package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
	"github.com/spf13/pflag"

	"wr/internal/config"
)

// parseJSONL parses JSONL output into a slice of maps.
func parseJSONL(data []byte) []map[string]interface{} {
	var results []map[string]interface{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		results = append(results, m)
	}
	return results
}

// assertValidJSONLEnvelope validates that every JSONL line in data has a
// structurally valid envelope (version, tool, type, timestamp).
func assertValidJSONLEnvelope(t *testing.T, data []byte) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var env agentsdk.Envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("invalid JSONL line: %s\nerr: %v", line, err)
		}
		if err := agentsdk.ValidateEnvelope(env); err != nil {
			t.Errorf("envelope validation failed: %v; line=%s", err, line)
		}
	}
}

// setupConfigEnv creates a temp home dir so config commands operate in isolation.
// Returns the temp home dir and a cleanup function.
func setupConfigEnv(t *testing.T) (homeDir string, cleanup func()) {
	t.Helper()
	tmpHome := t.TempDir()

	// Save and override both HOME and USERPROFILE (Windows uses USERPROFILE)
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", tmpHome)
	os.Setenv("USERPROFILE", tmpHome)

	return tmpHome, func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}
}

// withJSONLCapture replaces jsonl.DefaultWriter with one writing to a buffer,
// runs f, then restores the original writer and returns the captured output.
// resetConfigFlags resets the package-level flag variables to zero values
// and clears Cobra's flag-visit state so state doesn't leak between tests.
func resetConfigFlags() {
	configPushoverToken = ""
	configPushoverKey = ""
	configLLMTextKey = ""
	configLLMTextModel = ""
	configLLMTextAPIBase = ""
	configLLMTextProvider = ""
	configLLMVisionKey = ""
	configLLMVisionModel = ""
	configLLMVisionAPIBase = ""
	configLLMVisionProvider = ""
	configTimezone = ""

	promptText = ""
	promptFile = ""
	promptPreviewScope = ""

	// Reset Cobra's "visited" flag tracking so Changed() returns fresh results
	configInitCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
}

func withJSONLCapture(f func()) []byte {
	// Ensure app is initialized (tests run without main.go calling InitApp)
	if app == nil {
		InitApp()
	}
	var buf bytes.Buffer
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()
	resetConfigFlags()
	f()
	return buf.Bytes()
}

// runConfigCmd executes rootCmd with the given args, capturing JSONL output.
func runConfigCmd(args ...string) ([]byte, error) {
	return withJSONLCapture(func() {
		rootCmd.SetArgs(args)
		_ = rootCmd.Execute()
	}), nil
}

// ---------- config init ----------

func TestConfigInitDefaults(t *testing.T) {
	homeDir, cleanup := setupConfigEnv(t)
	defer cleanup()

	out, _ := runConfigCmd("config", "init")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d (output: %s)", len(lines), string(out))
	}
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0]["type"])
	}

	data, ok := lines[0]["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", lines[0]["data"])
	}

	expectedPath := filepath.Join(homeDir, ".work-report", "config.json")
	// Normalize to forward slashes for comparison
	actualPath := filepath.ToSlash(data["path"].(string))
	expectedNorm := filepath.ToSlash(expectedPath)
	if actualPath != expectedNorm {
		t.Errorf("expected path=%s, got %s", expectedNorm, actualPath)
	}

	// Verify the file was actually created
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Fatalf("config file was not created at %s", expectedPath)
	}

	// Verify we can load it and it has defaults
	cfg, err := config.Load(expectedPath)
	if err != nil {
		t.Fatalf("cannot load created config: %v", err)
	}
	if cfg.Timezone != "Asia/Shanghai" {
		t.Errorf("expected default timezone Asia/Shanghai, got %s", cfg.Timezone)
	}
}

func TestConfigInitWithFlags(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	out, _ := runConfigCmd("config", "init",
		"--timezone", "UTC",
		"--pushover-token", "tok123",
		"--llm-text-key", "sk-text-key",
	)
	assertValidJSONLEnvelope(t, out)

	lines := parseJSONL(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d", len(lines))
	}
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0]["type"])
	}

	// Load and verify flag overrides
	path, _ := config.DefaultConfigPath()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("cannot load config: %v", err)
	}
	if cfg.Timezone != "UTC" {
		t.Errorf("expected timezone UTC, got %s", cfg.Timezone)
	}
	if cfg.Pushover.APIToken != "tok123" {
		t.Errorf("expected pushover token tok123, got %s", cfg.Pushover.APIToken)
	}
	if cfg.LLM.Text.APIKey != "sk-text-key" {
		t.Errorf("expected LLM text key sk-text-key, got %s", cfg.LLM.Text.APIKey)
	}
}

func TestConfigInitInvalidTimezone(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	out, _ := runConfigCmd("config", "init", "--timezone", "Invalid/Zone")
	assertValidJSONLEnvelope(t, out)

	lines := parseJSONL(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0]["type"] != "error" {
		t.Errorf("expected type=error for invalid timezone, got %v", lines[0]["type"])
	}
}

// ---------- config set ----------

func TestConfigSetBasic(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	// Init first
	runConfigCmd("config", "init")

	out, _ := runConfigCmd("config", "set", "timezone", "Europe/London")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d", len(lines))
	}
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0]["type"])
	}
	data, _ := lines[0]["data"].(map[string]interface{})
	if data["key"] != "timezone" {
		t.Errorf("expected key=timezone, got %v", data["key"])
	}
	if data["value"] != "Europe/London" {
		t.Errorf("expected value=Europe/London, got %v", data["value"])
	}

	// Verify roundtrip
	path, _ := config.DefaultConfigPath()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("cannot load config: %v", err)
	}
	if cfg.Timezone != "Europe/London" {
		t.Errorf("expected timezone Europe/London, got %s", cfg.Timezone)
	}
}

func TestConfigSetLLMApiKey(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	runConfigCmd("config", "init")

	out, _ := runConfigCmd("config", "set", "llm.text.api_key", "sk-abc123")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0]["type"])
	}

	path, _ := config.DefaultConfigPath()
	cfg, _ := config.Load(path)
	if cfg.LLM.Text.APIKey != "sk-abc123" {
		t.Errorf("expected LLM text API key sk-abc123, got %s", cfg.LLM.Text.APIKey)
	}
}

func TestConfigSetUnknownPath(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	runConfigCmd("config", "init")

	out, _ := runConfigCmd("config", "set", "nonexistent.path", "value")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if lines[0]["type"] != "error" {
		t.Errorf("expected type=error for unknown path, got %v", lines[0]["type"])
	}
}

func TestConfigSetWrongArgCount(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	// Cobra.ExactArgs(2) should produce an error
	rootCmd.SetArgs([]string{"config", "set", "onlyonearg"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for wrong arg count")
	}
}

// ---------- config show ----------

func TestConfigShow(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	// Init first with a known secret
	runConfigCmd("config", "init", "--llm-text-key", "sk-secret-key-12345")

	out, _ := runConfigCmd("config", "show")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d (len(lines))", len(lines))
	}
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0]["type"])
	}

	data, ok := lines[0]["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", lines[0]["data"])
	}

	// Verify secrets are redacted
	llm, ok := data["llm"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected llm map, got %T", data["llm"])
	}
	text, ok := llm["text"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected text map, got %T", llm["text"])
	}
	apiKey, _ := text["api_key"].(string)
	if strings.Contains(apiKey, "secret") {
		t.Errorf("API key should be redacted, got: %s", apiKey)
	}
	if !strings.Contains(apiKey, "****") {
		t.Errorf("API key should contain **** masking, got: %s", apiKey)
	}
}

func TestConfigShowBeforeInit(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	// Show without init — should still work with defaults
	out, _ := runConfigCmd("config", "show")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0]["type"])
	}
}

// ---------- integration ----------

func TestConfigInitSetShowRoundtrip(t *testing.T) {
	homeDir, cleanup := setupConfigEnv(t)
	defer cleanup()

	// Step 1: init with some flags
	out, _ := runConfigCmd("config", "init", "--timezone", "UTC", "--pushover-token", "tok-abcdef")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if lines[0]["type"] != "result" {
		t.Fatalf("init failed: %v", lines[0])
	}

	configPath := filepath.Join(homeDir, ".work-report", "config.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatalf("config file not created at %s", configPath)
	}

	// Step 2: set a value
	out, _ = runConfigCmd("config", "set", "llm.vision.model", "gpt-4o")
	assertValidJSONLEnvelope(t, out)
	lines = parseJSONL(out)
	if lines[0]["type"] != "result" {
		t.Fatalf("set failed: %v", lines[0])
	}

	// Step 3: show and verify
	out, _ = runConfigCmd("config", "show")
	assertValidJSONLEnvelope(t, out)
	lines = parseJSONL(out)
	if lines[0]["type"] != "result" {
		t.Fatalf("show failed: %v", lines[0])
	}

	data, _ := lines[0]["data"].(map[string]interface{})

	// Timezone should be UTC from init
	if data["timezone"] != "UTC" {
		t.Errorf("expected timezone UTC, got %v", data["timezone"])
	}

	// Vision model should be gpt-4o from set
	llm, _ := data["llm"].(map[string]interface{})
	vision, _ := llm["vision"].(map[string]interface{})
	if vision["model"] != "gpt-4o" {
		t.Errorf("expected vision model gpt-4o, got %v", vision["model"])
	}

	// Pushover token should be redacted
	pushover, _ := data["pushover"].(map[string]interface{})
	token, _ := pushover["api_token"].(string)
	if strings.Contains(token, "abcdef") {
		t.Errorf("pushover token should be redacted, got: %s", token)
	}
}

func TestConfigInitIdempotent(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	// Init twice — second should overwrite
	runConfigCmd("config", "init", "--timezone", "UTC")

	out, _ := runConfigCmd("config", "init", "--timezone", "Asia/Tokyo")
	assertValidJSONLEnvelope(t, out)
	lines := parseJSONL(out)
	if lines[0]["type"] != "result" {
		t.Fatalf("expected type=result, got %v", lines[0]["type"])
	}

	path, _ := config.DefaultConfigPath()
	cfg, _ := config.Load(path)
	if cfg.Timezone != "Asia/Tokyo" {
		t.Errorf("expected timezone Asia/Tokyo after second init, got %s", cfg.Timezone)
	}
}

func TestConfigAllSetByPathPaths(t *testing.T) {
	_, cleanup := setupConfigEnv(t)
	defer cleanup()

	runConfigCmd("config", "init")

	tests := []struct {
		path  string
		value string
	}{
		{"pushover.api_token", "token-123"},
		{"pushover.user_key", "key-456"},
		{"llm.text.provider", "openai"},
		{"llm.text.api_key", "sk-text"},
		{"llm.text.api_base", "https://api.openai.com/v1/"},
		{"llm.text.model", "gpt-4"},
		{"llm.vision.provider", "zhipu"},
		{"llm.vision.api_key", "sk-vision"},
		{"llm.vision.api_base", "https://api.zhipu.ai/"},
		{"llm.vision.model", "glm-4v"},
		{"data_dir", "/tmp/work-records"},
		{"timezone", "Europe/London"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			out, _ := runConfigCmd("config", "set", tc.path, tc.value)
			assertValidJSONLEnvelope(t, out)
			lines := parseJSONL(out)
			if len(lines) != 1 {
				t.Fatalf("expected 1 line for %s, got %d (output: %s)", tc.path, len(lines), string(out))
			}
			if lines[0]["type"] != "result" {
				t.Fatalf("expected success for %s, got %v", tc.path, lines[0])
			}
		})
	}
}
