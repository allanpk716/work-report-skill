package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTestConfigFile creates a temp config file with the given JSON content.
func writeTestConfigFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return path
}

func TestLoadFullConfig(t *testing.T) {
	path := writeTestConfigFile(t, `{
		"pushover": {
			"api_token": "a4dy8a411xm4z96in1qvx41k714wq3",
			"user_key": "uw3b9cbopa5jn843xqxwknzcbjzoe5"
		},
		"llm": {
			"text": {
				"provider": "aihubmix",
				"api_key": "sk-test-key-12345678",
				"model": "deepseek-v4-pro"
			},
			"vision": {
				"provider": "zhipu",
				"api_key": "be4e8eb5452f438ebe09b9b1e5c433f3",
				"api_base": "https://open.bigmodel.cn/api/paas/v4/",
				"model": "glm-4.6v"
			}
		},
		"data_dir": "/tmp/test-work-records",
		"daemon": {
			"port": 18092
		},
		"timezone": "Asia/Shanghai"
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Pushover
	if cfg.Pushover.APIToken != "a4dy8a411xm4z96in1qvx41k714wq3" {
		t.Errorf("Pushover.APIToken = %q", cfg.Pushover.APIToken)
	}
	if cfg.Pushover.UserKey != "uw3b9cbopa5jn843xqxwknzcbjzoe5" {
		t.Errorf("Pushover.UserKey = %q", cfg.Pushover.UserKey)
	}

	// LLM text
	if cfg.LLM.Text.Provider != "aihubmix" {
		t.Errorf("LLM.Text.Provider = %q", cfg.LLM.Text.Provider)
	}
	if cfg.LLM.Text.Model != "deepseek-v4-pro" {
		t.Errorf("LLM.Text.Model = %q", cfg.LLM.Text.Model)
	}

	// LLM vision
	if cfg.LLM.Vision.Provider != "zhipu" {
		t.Errorf("LLM.Vision.Provider = %q", cfg.LLM.Vision.Provider)
	}
	if cfg.LLM.Vision.APIBase != "https://open.bigmodel.cn/api/paas/v4/" {
		t.Errorf("LLM.Vision.APIBase = %q", cfg.LLM.Vision.APIBase)
	}

	// Daemon
	if cfg.Daemon.Port != 18092 {
		t.Errorf("Daemon.Port = %d, want 18092", cfg.Daemon.Port)
	}

	// Timezone
	if cfg.Timezone != "Asia/Shanghai" {
		t.Errorf("Timezone = %q", cfg.Timezone)
	}

	// DataDir
	if cfg.DataDir != "/tmp/test-work-records" {
		t.Errorf("DataDir = %q", cfg.DataDir)
	}
}

func TestLoadDefaultsOnMissingFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}

	if cfg.Timezone != "Asia/Shanghai" {
		t.Errorf("default Timezone = %q, want Asia/Shanghai", cfg.Timezone)
	}
	if cfg.Daemon.Port != 18080 {
		t.Errorf("default Daemon.Port = %d, want 18080", cfg.Daemon.Port)
	}
	if cfg.DataDir == "" {
		t.Error("default DataDir should not be empty")
	}
}

func TestLoadDefaultsOnEmptyObject(t *testing.T) {
	path := writeTestConfigFile(t, `{}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load empty config: %v", err)
	}

	if cfg.Daemon.Port != 18080 {
		t.Errorf("Daemon.Port = %d, want 18080", cfg.Daemon.Port)
	}
	if cfg.Timezone != "Asia/Shanghai" {
		t.Errorf("Timezone = %q", cfg.Timezone)
	}
}

func TestLoadInvalidPort(t *testing.T) {
	path := writeTestConfigFile(t, `{"daemon": {"port": 99999}}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
}

func TestLoadInvalidTimezone(t *testing.T) {
	path := writeTestConfigFile(t, `{"timezone": "Invalid/Zone"}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid timezone")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	path := writeTestConfigFile(t, `{invalid json`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestRedactedMasksSensitiveFields(t *testing.T) {
	cfg := &Config{
		Pushover: PushoverConfig{
			APIToken: "a4dy8a411xm4z96in1qvx41k714wq3",
			UserKey:  "uw3b9cbopa5jn843xqxwknzcbjzoe5",
		},
		LLM: LLMConfig{
			Text: LLMProviderConfig{
				Provider: "aihubmix",
				APIKey:   "sk-test-key-12345678",
				Model:    "deepseek-v4-pro",
			},
			Vision: LLMProviderConfig{
				Provider: "zhipu",
				APIKey:   "be4e8eb5452f438",
				Model:    "glm-4.6v",
			},
		},
	}

	r := cfg.Redacted()

	// Pushover tokens should be masked
	if r.Pushover.APIToken == cfg.Pushover.APIToken {
		t.Error("Pushover.APIToken not masked")
	}
	if r.Pushover.UserKey == cfg.Pushover.UserKey {
		t.Error("Pushover.UserKey not masked")
	}
	// Should start with first 4 chars
	if r.Pushover.APIToken[:4] != "a4dy" {
		t.Errorf("masked token prefix = %q, want a4dy****", r.Pushover.APIToken[:4])
	}

	// LLM keys should be masked
	if r.LLM.Text.APIKey == cfg.LLM.Text.APIKey {
		t.Error("LLM.Text.APIKey not masked")
	}
	if r.LLM.Vision.APIKey == cfg.LLM.Vision.APIKey {
		t.Error("LLM.Vision.APIKey not masked")
	}

	// Non-sensitive fields should be preserved
	if r.LLM.Text.Provider != "aihubmix" {
		t.Errorf("LLM.Text.Provider masked but shouldn't be: %q", r.LLM.Text.Provider)
	}
	if r.LLM.Text.Model != "deepseek-v4-pro" {
		t.Errorf("LLM.Text.Model masked but shouldn't be: %q", r.LLM.Text.Model)
	}

	// Original should be unchanged
	if cfg.Pushover.APIToken != "a4dy8a411xm4z96in1qvx41k714wq3" {
		t.Error("Redacted() modified the original config")
	}
}

func TestMaskSecret(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"ab", "****"},
		{"abcd", "****"},
		{"abcdefgh", "abcd****"},
		{"a4dy8a411xm4z96in1qvx41k714wq3", "a4dy****"},
	}
	for _, tt := range tests {
		got := maskSecret(tt.input)
		if got != tt.want {
			t.Errorf("maskSecret(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRedactedJSONOutput(t *testing.T) {
	cfg := &Config{
		Pushover: PushoverConfig{
			APIToken: "secret-token-12345",
			UserKey:  "secret-user-67890",
		},
		LLM: LLMConfig{
			Text: LLMProviderConfig{
				Provider: "test-provider",
				APIKey:   "secret-llm-key-abc",
				Model:    "test-model",
			},
		},
	}

	r := cfg.Redacted()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal redacted: %v", err)
	}

	s := string(data)

	// Verify secrets do NOT appear in JSON output
	for _, secret := range []string{
		"secret-token-12345",
		"secret-user-67890",
		"secret-llm-key-abc",
	} {
		if contains(s, secret) {
			t.Errorf("secret %q found in redacted JSON output", secret)
		}
	}

	// Verify non-sensitive data is present
	if !contains(s, "test-provider") {
		t.Error("provider name missing from redacted output")
	}
	if !contains(s, "test-model") {
		t.Error("model name missing from redacted output")
	}
}

func TestLocation(t *testing.T) {
	cfg := &Config{Timezone: "Asia/Shanghai"}
	loc := cfg.Location()
	if loc.String() != "Asia/Shanghai" {
		t.Errorf("Location() = %q, want Asia/Shanghai", loc)
	}
}

func TestLocationInvalidFallsBackToUTC(t *testing.T) {
	// Manually set invalid timezone (bypassing validation)
	cfg := &Config{Timezone: "Invalid/Zone"}
	loc := cfg.Location()
	if loc != time.UTC {
		t.Errorf("Location() for invalid tz = %v, want UTC", loc)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	path, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath: %v", err)
	}
	if !contains(path, ".work-report") {
		t.Errorf("DefaultConfigPath = %q, expected to contain .work-report", path)
	}
	if !contains(path, "config.json") {
		t.Errorf("DefaultConfigPath = %q, expected to end with config.json", path)
	}
}

func TestSaveCreatesDirsAndWritesJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "config.json")

	cfg := &Config{
		Pushover: PushoverConfig{
			APIToken: "tok123",
			UserKey:  "key456",
		},
		Timezone: "Asia/Shanghai",
		Daemon:   DaemonConfig{Port: 18080},
		DataDir:  "/tmp/data",
	}

	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File should exist
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat saved file: %v", err)
	}

	// Should roundtrip through Load
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if loaded.Pushover.APIToken != "tok123" {
		t.Errorf("roundtrip APIToken = %q, want tok123", loaded.Pushover.APIToken)
	}
	if loaded.Timezone != "Asia/Shanghai" {
		t.Errorf("roundtrip Timezone = %q", loaded.Timezone)
	}
	if loaded.Daemon.Port != 18080 {
		t.Errorf("roundtrip Port = %d, want 18080", loaded.Daemon.Port)
	}
}

func TestSaveOverwriteExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg1 := &Config{Timezone: "UTC", Daemon: DaemonConfig{Port: 9090}}
	if err := cfg1.Save(path); err != nil {
		t.Fatalf("Save first: %v", err)
	}

	cfg2 := &Config{Timezone: "Asia/Tokyo", Daemon: DaemonConfig{Port: 8080}}
	if err := cfg2.Save(path); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Timezone != "Asia/Tokyo" {
		t.Errorf("Timezone = %q, want Asia/Tokyo", loaded.Timezone)
	}
	if loaded.Daemon.Port != 8080 {
		t.Errorf("Port = %d, want 8080", loaded.Daemon.Port)
	}
}

func TestSetByPathStringFields(t *testing.T) {
	cfg := defaultConfig()

	tests := []struct {
		path  string
		value string
		want  string
	}{
		{"pushover.api_token", "new-token", "new-token"},
		{"pushover.user_key", "new-key", "new-key"},
		{"llm.text.provider", "openai", "openai"},
		{"llm.text.api_key", "sk-abc", "sk-abc"},
		{"llm.text.api_base", "https://api.openai.com", "https://api.openai.com"},
		{"llm.text.model", "gpt-4o", "gpt-4o"},
		{"llm.vision.provider", "zhipu", "zhipu"},
		{"llm.vision.api_key", "viz-key", "viz-key"},
		{"llm.vision.api_base", "https://viz.api", "https://viz.api"},
		{"llm.vision.model", "glm-4v", "glm-4v"},
		{"data_dir", "/custom/dir", "/custom/dir"},
		{"timezone", "America/New_York", "America/New_York"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if err := cfg.SetByPath(tt.path, tt.value); err != nil {
				t.Fatalf("SetByPath(%q, %q): %v", tt.path, tt.value, err)
			}
		})
	}

	// Verify the last values stuck
	if cfg.LLM.Text.Provider != "openai" {
		t.Errorf("llm.text.provider = %q", cfg.LLM.Text.Provider)
	}
	if cfg.DataDir != "/custom/dir" {
		t.Errorf("data_dir = %q", cfg.DataDir)
	}
	if cfg.Timezone != "America/New_York" {
		t.Errorf("timezone = %q", cfg.Timezone)
	}
}

func TestSetByPathDaemonPort(t *testing.T) {
	cfg := defaultConfig()
	if err := cfg.SetByPath("daemon.port", "9999"); err != nil {
		t.Fatalf("SetByPath daemon.port: %v", err)
	}
	if cfg.Daemon.Port != 9999 {
		t.Errorf("daemon.port = %d, want 9999", cfg.Daemon.Port)
	}
}

func TestSetByPathDaemonPortNonInteger(t *testing.T) {
	cfg := defaultConfig()
	err := cfg.SetByPath("daemon.port", "not-a-number")
	if err == nil {
		t.Fatal("expected error for non-integer port")
	}
}

func TestSetByPathInvalidTimezone(t *testing.T) {
	cfg := defaultConfig()
	err := cfg.SetByPath("timezone", "Invalid/Zone")
	if err == nil {
		t.Fatal("expected error for invalid timezone")
	}
}

func TestSetByPathUnknownPath(t *testing.T) {
	cfg := defaultConfig()
	err := cfg.SetByPath("nonexistent.field", "value")
	if err == nil {
		t.Fatal("expected error for unknown path")
	}
}

func TestSetByPathTypoPath(t *testing.T) {
	cfg := defaultConfig()
	err := cfg.SetByPath("llm.text.apikey", "sk-xxx")
	if err == nil {
		t.Fatal("expected error for typo path llm.text.apikey")
	}
}

func TestSetByPathRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := defaultConfig()
	_ = cfg.SetByPath("pushover.api_token", "roundtrip-token")
	_ = cfg.SetByPath("llm.text.api_key", "sk-roundtrip")
	_ = cfg.SetByPath("daemon.port", "7777")

	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Pushover.APIToken != "roundtrip-token" {
		t.Errorf("APIToken = %q, want roundtrip-token", loaded.Pushover.APIToken)
	}
	if loaded.LLM.Text.APIKey != "sk-roundtrip" {
		t.Errorf("LLM.Text.APIKey = %q, want sk-roundtrip", loaded.LLM.Text.APIKey)
	}
	if loaded.Daemon.Port != 7777 {
		t.Errorf("Port = %d, want 7777", loaded.Daemon.Port)
	}
}

func TestValidConfigPaths(t *testing.T) {
	paths := ValidConfigPaths()

	// Must contain essential paths
	expected := []string{
		"pushover.api_token",
		"pushover.user_key",
		"llm.text.api_key",
		"llm.text.model",
		"llm.vision.api_key",
		"data_dir",
		"daemon.port",
		"timezone",
	}
	for _, p := range expected {
		found := false
		for _, vp := range paths {
			if vp == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ValidConfigPaths missing %q", p)
		}
	}
}

func TestSaveProducesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := &Config{
		Pushover: PushoverConfig{
			APIToken: "token",
			UserKey:  "user",
		},
		LLM: LLMConfig{
			Text: LLMProviderConfig{
				Provider: "test",
				APIKey:   "key",
				Model:    "model",
			},
		},
		Daemon:   DaemonConfig{Port: 8080},
		Timezone: "UTC",
		DataDir:  "/data",
	}

	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Must be valid JSON
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}

	// Must be pretty-printed (contain newlines)
	if !contains(string(data), "\n") {
		t.Error("saved JSON is not pretty-printed (no newlines)")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
