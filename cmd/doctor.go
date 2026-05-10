package cmd

import (
	"fmt"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/config"
)

// registerHealthChecks registers LLM and Pushover health checks
// with the SDK App. Called once from InitApp(). Each check loads config fresh
// (not cached) — doctor is a diagnostic tool, not a hot path.
func registerHealthChecks() {
	app.RegisterHealthCheck("llm", checkLLM)
	app.RegisterHealthCheck("pushover", checkPushover)
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
