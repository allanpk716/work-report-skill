package cmd

import (
	"fmt"
	"strings"

	"github.com/allanpk716/agent-cli-sdk"
	"wr/internal/config"
)

// wrConfigProvider adapts internal/config to the SDK's ConfigProvider interface.
// It delegates to config.Load, config.Redacted, config.SetByPath, config.Validate,
// and config.Save, with error messages aligned so the SDK's pattern-matching on
// whitelist violations triggers correctly.
type wrConfigProvider struct{}

// compile-time check
var _ agentsdk.ConfigProvider = (*wrConfigProvider)(nil)

// ListRedacted loads the current config and returns a redacted copy with
// sensitive fields (API keys, tokens) masked.
func (p *wrConfigProvider) ListRedacted() (interface{}, error) {
	cfg, err := config.LoadDefault()
	if err != nil {
		return nil, fmt.Errorf("config load: %w", err)
	}
	return cfg.Redacted(), nil
}

// Set loads the config, validates the path against the whitelist, sets the
// value, validates the result, and saves. Error messages are aligned with the
// SDK's pattern-matching expectations ("not in whitelist", "unknown field").
func (p *wrConfigProvider) Set(jsonPath, value string) error {
	cfg, err := config.LoadDefault()
	if err != nil {
		return fmt.Errorf("config load: %w", err)
	}

	if err := cfg.SetByPath(jsonPath, value); err != nil {
		// Align error messages with SDK pattern-matching.
		// SDK checks for: "not configurable", "not in whitelist", "unknown field".
		// wr config.SetByPath returns "unknown path" — remap to "unknown field".
		errMsg := err.Error()
		if strings.Contains(errMsg, "unknown path") {
			return fmt.Errorf("config: unknown field %q (not in whitelist)", jsonPath)
		}
		return err
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	path, err := config.DefaultConfigPath()
	if err != nil {
		return fmt.Errorf("config path: %w", err)
	}

	return cfg.Save(path)
}

// Whitelist returns the accepted dot-notation paths that may be set.
func (p *wrConfigProvider) Whitelist() []string {
	return config.ValidConfigPaths()
}
