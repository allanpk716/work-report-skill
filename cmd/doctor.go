package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/config"
)

// registerHealthChecks registers LLM, Pushover, and data-dir health checks
// with the SDK App. Called once from InitApp(). Each check loads config fresh
// (not cached) — doctor is a diagnostic tool, not a hot path.
func registerHealthChecks() {
	app.RegisterHealthCheck("llm", checkLLM)
	app.RegisterHealthCheck("pushover", checkPushover)
	app.RegisterHealthCheck("data_dir", checkDataDir)
}

// checkLLM verifies the LLM text API key is configured.
// Warns (rather than fails) if missing, since not all workflows require LLM.
func checkLLM() agentsdk.HealthCheckResult {
	cfg, err := config.LoadDefault()
	if err != nil {
		return agentsdk.HealthCheckResult{
			Name:    "llm",
			Status:  agentsdk.HealthCheckFail,
			Message: fmt.Sprintf("cannot load config: %v", err),
		}
	}

	if cfg.LLM.Text.APIKey == "" {
		return agentsdk.HealthCheckResult{
			Name:    "llm",
			Status:  agentsdk.HealthCheckWarning,
			Message: "llm.text.api_key is not set — LLM features will be unavailable",
		}
	}

	return agentsdk.HealthCheckResult{
		Name:    "llm",
		Status:  agentsdk.HealthCheckPass,
		Message: "llm.text.api_key is configured",
	}
}

// checkPushover verifies both Pushover API token and user key are set.
// Fails if either is missing, since partial configuration is unusable.
func checkPushover() agentsdk.HealthCheckResult {
	cfg, err := config.LoadDefault()
	if err != nil {
		return agentsdk.HealthCheckResult{
			Name:    "pushover",
			Status:  agentsdk.HealthCheckFail,
			Message: fmt.Sprintf("cannot load config: %v", err),
		}
	}

	hasToken := cfg.Pushover.APIToken != ""
	hasKey := cfg.Pushover.UserKey != ""

	if !hasToken && !hasKey {
		return agentsdk.HealthCheckResult{
			Name:    "pushover",
			Status:  agentsdk.HealthCheckFail,
			Message: "pushover.api_token and pushover.user_key are both missing",
		}
	}
	if !hasToken {
		return agentsdk.HealthCheckResult{
			Name:    "pushover",
			Status:  agentsdk.HealthCheckFail,
			Message: "pushover.api_token is missing",
		}
	}
	if !hasKey {
		return agentsdk.HealthCheckResult{
			Name:    "pushover",
			Status:  agentsdk.HealthCheckFail,
			Message: "pushover.user_key is missing",
		}
	}

	return agentsdk.HealthCheckResult{
		Name:    "pushover",
		Status:  agentsdk.HealthCheckPass,
		Message: "pushover credentials are configured",
	}
}

// checkDataDir verifies the data directory exists and is writable.
// Pass if directory exists and is writable, warn if not present but creatable,
// fail if not present and not creatable.
func checkDataDir() agentsdk.HealthCheckResult {
	cfg, err := config.LoadDefault()
	if err != nil {
		return agentsdk.HealthCheckResult{
			Name:    "data_dir",
			Status:  agentsdk.HealthCheckFail,
			Message: fmt.Sprintf("cannot load config: %v", err),
		}
	}

	dir := cfg.DataDir
	if dir == "" {
		dir, _ = config.DefaultDataDir()
	}

	// Check if directory exists
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// Try to create it
			if mkdirErr := os.MkdirAll(dir, 0755); mkdirErr != nil {
				return agentsdk.HealthCheckResult{
					Name:    "data_dir",
					Status:  agentsdk.HealthCheckFail,
					Message: fmt.Sprintf("data_dir %q does not exist and cannot be created: %v", dir, mkdirErr),
				}
			}
			return agentsdk.HealthCheckResult{
				Name:    "data_dir",
				Status:  agentsdk.HealthCheckWarning,
				Message: fmt.Sprintf("data_dir %q was missing — created automatically", dir),
			}
		}
		return agentsdk.HealthCheckResult{
			Name:    "data_dir",
			Status:  agentsdk.HealthCheckFail,
			Message: fmt.Sprintf("cannot stat data_dir %q: %v", dir, err),
		}
	}

	if !info.IsDir() {
		return agentsdk.HealthCheckResult{
			Name:    "data_dir",
			Status:  agentsdk.HealthCheckFail,
			Message: fmt.Sprintf("data_dir %q exists but is not a directory", dir),
		}
	}

	// Check writability
	probe := filepath.Join(dir, ".wr-doctor-write-test")
	if err := os.WriteFile(probe, []byte("probe"), 0644); err != nil {
		return agentsdk.HealthCheckResult{
			Name:    "data_dir",
			Status:  agentsdk.HealthCheckFail,
			Message: fmt.Sprintf("data_dir %q is not writable: %v", dir, err),
		}
	}
	os.Remove(probe)

	return agentsdk.HealthCheckResult{
		Name:    "data_dir",
		Status:  agentsdk.HealthCheckPass,
		Message: fmt.Sprintf("data_dir %q exists and is writable", dir),
	}
}
