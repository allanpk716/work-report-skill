// Package config loads and validates ~/.work-report/config.json, providing
// typed access to Pushover credentials, LLM text/vision settings, the data
// directory path, daemon port, and timezone.
//
// Config loading is fail-fast on startup: structural problems (missing required
// fields, invalid values) return errors. Optional fields default sensibly.
// Sensitive fields (API keys, tokens) are never logged or included in JSONL
// output.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config represents the full application configuration.
type Config struct {
	Pushover PushoverConfig `json:"pushover"`
	LLM      LLMConfig      `json:"llm"`
	DataDir  string         `json:"data_dir"`
	Daemon   DaemonConfig   `json:"daemon"`
	Timezone string         `json:"timezone"`
}

// PushoverConfig holds Pushover notification credentials.
// These values are sensitive and must never be echoed in JSONL output.
type PushoverConfig struct {
	APIToken string `json:"api_token"`
	UserKey  string `json:"user_key"`
}

// LLMConfig holds settings for the text and vision LLM providers.
type LLMConfig struct {
	Text   LLMProviderConfig `json:"text"`
	Vision LLMProviderConfig `json:"vision"`
}

// LLMProviderConfig holds a single LLM provider's settings.
type LLMProviderConfig struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
	APIBase  string `json:"api_base,omitempty"`
	Model    string `json:"model"`
	Timeout  int    `json:"timeout,omitempty"`
}

// DaemonConfig holds daemon-specific settings.
type DaemonConfig struct {
	Port int `json:"port"`
}

// DefaultConfigPath returns ~/.work-report/config.json.
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: cannot determine home dir: %w", err)
	}
	return filepath.Join(home, ".work-report", "config.json"), nil
}

// DefaultDataDir returns ~/.work-report/work-records, creating the parent
// directory if it does not exist.
func DefaultDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: cannot determine home dir: %w", err)
	}
	dir := filepath.Join(home, ".work-report", "work-records")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("config: cannot create data dir: %w", err)
	}
	return dir, nil
}

// Load reads and validates config from the given file path.
// If the file does not exist, returns a Config with sensible defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Config file missing — return defaults
			cfg := defaultConfig()
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// LoadDefault reads config from the default path (~/.work-report/config.json).
func LoadDefault() (*Config, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	return Load(path)
}

// applyDefaults fills in zero/empty values with sensible defaults.
func (c *Config) applyDefaults() {
	if c.Timezone == "" {
		c.Timezone = "Asia/Shanghai"
	}
	if c.Daemon.Port == 0 {
		c.Daemon.Port = 18080
	}
	if c.DataDir == "" {
		dir, err := DefaultDataDir()
		if err == nil {
			c.DataDir = dir
		}
	}
	if c.LLM.Text.Timeout == 0 {
		c.LLM.Text.Timeout = 30
	}
	if c.LLM.Vision.Timeout == 0 {
		c.LLM.Vision.Timeout = 30
	}
}

// Validate checks that required fields are present and values are in range.
// This is the exported version of validate, for use by CLI commands that
// construct configs without going through Load (e.g. wr config init).
func (c *Config) Validate() error {
	return c.validate()
}

// validate checks that required fields are present and values are in range.
func (c *Config) validate() error {
	if c.Daemon.Port < 1 || c.Daemon.Port > 65535 {
		return fmt.Errorf("config: daemon.port must be 1-65535, got %d", c.Daemon.Port)
	}

	// Validate timezone is parseable
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("config: invalid timezone %q: %w", c.Timezone, err)
	}

	return nil
}

// defaultConfig returns a Config populated with defaults (no file needed).
func defaultConfig() *Config {
	dir, _ := DefaultDataDir()
	return &Config{
		Timezone: "Asia/Shanghai",
		Daemon: DaemonConfig{
			Port: 18080,
		},
		DataDir: dir,
	}
}

// Redacted returns a copy of the config with sensitive fields masked.
// Use this for any logging or JSONL output.
func (c *Config) Redacted() *Config {
	r := *c
	r.Pushover = PushoverConfig{
		APIToken: maskSecret(c.Pushover.APIToken),
		UserKey:  maskSecret(c.Pushover.UserKey),
	}
	r.LLM = LLMConfig{
		Text: LLMProviderConfig{
			Provider: c.LLM.Text.Provider,
			APIKey:   maskSecret(c.LLM.Text.APIKey),
			APIBase:  c.LLM.Text.APIBase,
			Model:    c.LLM.Text.Model,
			Timeout:  c.LLM.Text.Timeout,
		},
		Vision: LLMProviderConfig{
			Provider: c.LLM.Vision.Provider,
			APIKey:   maskSecret(c.LLM.Vision.APIKey),
			APIBase:  c.LLM.Vision.APIBase,
			Model:    c.LLM.Vision.Model,
			Timeout:  c.LLM.Vision.Timeout,
		},
	}
	return &r
}

// maskSecret replaces all but the first 4 characters of a secret with asterisks.
// Empty strings remain empty.
func maskSecret(s string) string {
	if len(s) <= 4 {
		if s == "" {
			return ""
		}
		return "****"
	}
	return s[:4] + "****"
}

// ValidConfigPaths returns the whitelist of accepted dot-notation paths that
// SetByPath and the wr config set command may modify. This prevents typos
// from silently creating bogus keys in the config file.
func ValidConfigPaths() []string {
	return []string{
		"pushover.api_token",
		"pushover.user_key",
		"llm.text.provider",
		"llm.text.api_key",
		"llm.text.api_base",
		"llm.text.model",
		"llm.text.timeout",
		"llm.vision.provider",
		"llm.vision.api_key",
		"llm.vision.api_base",
		"llm.vision.model",
		"llm.vision.timeout",
		"data_dir",
		"daemon.port",
		"timezone",
	}
}

// Save writes the config as pretty-printed JSON to the given path, creating
// parent directories as needed.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("config: create dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}

	return nil
}

// SetByPath sets a config value identified by a dot-notation path (e.g.
// "llm.text.api_key"). Returns an error if the path is not in the
// ValidConfigPaths whitelist or if the value type does not match the existing
// field type.
func (c *Config) SetByPath(path string, value string) error {
	// Validate the path is in the whitelist
	if !isValidPath(path) {
		return fmt.Errorf("config: unknown path %q (valid paths: %v)", path, ValidConfigPaths())
	}

	switch path {
	case "pushover.api_token":
		c.Pushover.APIToken = value
	case "pushover.user_key":
		c.Pushover.UserKey = value
	case "llm.text.provider":
		c.LLM.Text.Provider = value
	case "llm.text.api_key":
		c.LLM.Text.APIKey = value
	case "llm.text.api_base":
		c.LLM.Text.APIBase = value
	case "llm.text.model":
		c.LLM.Text.Model = value
	case "llm.text.timeout":
		var timeout int
		if _, err := fmt.Sscanf(value, "%d", &timeout); err != nil {
			return fmt.Errorf("config: llm.text.timeout must be an integer, got %q", value)
		}
		if timeout < 1 {
			return fmt.Errorf("config: llm.text.timeout must be >= 1, got %d", timeout)
		}
		c.LLM.Text.Timeout = timeout
	case "llm.vision.provider":
		c.LLM.Vision.Provider = value
	case "llm.vision.api_key":
		c.LLM.Vision.APIKey = value
	case "llm.vision.api_base":
		c.LLM.Vision.APIBase = value
	case "llm.vision.model":
		c.LLM.Vision.Model = value
	case "llm.vision.timeout":
		var timeout int
		if _, err := fmt.Sscanf(value, "%d", &timeout); err != nil {
			return fmt.Errorf("config: llm.vision.timeout must be an integer, got %q", value)
		}
		if timeout < 1 {
			return fmt.Errorf("config: llm.vision.timeout must be >= 1, got %d", timeout)
		}
		c.LLM.Vision.Timeout = timeout
	case "data_dir":
		c.DataDir = value
	case "daemon.port":
		var port int
		if _, err := fmt.Sscanf(value, "%d", &port); err != nil {
			return fmt.Errorf("config: daemon.port must be an integer, got %q", value)
		}
		c.Daemon.Port = port
	case "timezone":
		if _, err := time.LoadLocation(value); err != nil {
			return fmt.Errorf("config: invalid timezone %q: %w", value, err)
		}
		c.Timezone = value
	default:
		return fmt.Errorf("config: unhandled path %q", path)
	}

	return nil
}

// isValidPath checks whether the given dot-notation path is in the whitelist.
func isValidPath(path string) bool {
	for _, p := range ValidConfigPaths() {
		if p == path {
			return true
		}
	}
	return false
}

// Location returns the parsed timezone location for this config.
func (c *Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}
