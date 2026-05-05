package cmd

import (
	"fmt"

	agentsdk "github.com/allanpk716/agent-cli-sdk"

	"wr/internal/config"
	"wr/internal/daemon"
)

// registerHealthChecks registers daemon, LLM, and Pushover health checks
// with the SDK App. Called once from InitApp(). Each check loads config fresh
// (not cached) — doctor is a diagnostic tool, not a hot path.
func registerHealthChecks() {
	app.RegisterHealthCheck("daemon", checkDaemon)
	app.RegisterHealthCheck("llm", checkLLM)
	app.RegisterHealthCheck("pushover", checkPushover)
}

// checkDaemon verifies the daemon process is running by reading its state
// file and checking if the recorded port is listening.
func checkDaemon() agentsdk.HealthCheckResult {
	dir, err := daemon.DefaultStateDir()
	if err != nil {
		return agentsdk.HealthCheckResult{
			Name:    "daemon",
			Status:  agentsdk.HealthCheckFail,
			Message: fmt.Sprintf("cannot determine state dir: %v", err),
		}
	}

	state, err := daemon.ReadState(dir)
	if err != nil {
		return agentsdk.HealthCheckResult{
			Name:    "daemon",
			Status:  agentsdk.HealthCheckFail,
			Message: "daemon is not running (no state file found)",
		}
	}

	if !daemon.IsPortInUse(state.Port) {
		return agentsdk.HealthCheckResult{
			Name:    "daemon",
			Status:  agentsdk.HealthCheckFail,
			Message: fmt.Sprintf("daemon not listening on port %d (stale state file, pid=%d)", state.Port, state.PID),
			Details: map[string]interface{}{
				"port": state.Port,
				"pid":  state.PID,
			},
		}
	}

	return agentsdk.HealthCheckResult{
		Name:    "daemon",
		Status:  agentsdk.HealthCheckPass,
		Message: fmt.Sprintf("daemon running on port %d (pid=%d)", state.Port, state.PID),
		Details: map[string]interface{}{
			"port": state.Port,
			"pid":  state.PID,
		},
	}
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
