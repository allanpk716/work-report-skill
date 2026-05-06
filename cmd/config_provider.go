package cmd

import (
	"fmt"
	"strings"

	"github.com/allanpk716/ai-agent-cli-rules/sdks/go"
	"wr/internal/config"
)

// wrConfigProvider adapts internal/config to the SDK's ConfigProvider interface.
// It delegates to config.Load, config.Redacted, config.SetByPath, config.Validate,
// and config.Save. Error types are aligned with the SDK's errors.As-based
// classification so agentConfigSetCmd correctly maps to INPUT_INVALID.
type wrConfigProvider struct{}

// unknownFieldErr satisfies agentsdk.UnknownFieldError so the SDK's
// agentConfigSetCmd classifies unknown-path errors as INPUT_INVALID.
type unknownFieldErr struct {
	field string
}

func (e *unknownFieldErr) Error() string        { return "config: unknown field " + e.field + " (not in whitelist)" }
func (e *unknownFieldErr) Field() string        { return e.field }
func (e *unknownFieldErr) IsUnknownFieldError() bool { return true }

// compile-time checks
var _ agentsdk.ConfigProvider = (*wrConfigProvider)(nil)
var _ agentsdk.UnknownFieldError = (*unknownFieldErr)(nil)

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
		// Return SDK-compatible UnknownFieldError so agentConfigSetCmd
		// classifies this as INPUT_INVALID via errors.As.
		errMsg := err.Error()
		if strings.Contains(errMsg, "unknown path") || strings.Contains(errMsg, "unhandled path") {
			return &unknownFieldErr{field: jsonPath}
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
