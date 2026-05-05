package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	agentsdk "github.com/allanpk716/agent-cli-sdk"
)

// app is the SDK App instance shared by all commands.
// Initialized by InitApp() in main.go before any command runs.
var app *agentsdk.App

// InitApp creates the SDK App instance. Must be called before Execute().
// Sets WR_HOME so the SDK Sandbox uses ~/.work-report/ (D024).
func InitApp() {
	// Set WR_HOME before creating the app so SDK Sandbox picks it up.
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		os.Setenv("WR_HOME", filepath.Join(home, ".work-report"))
	}

	app = agentsdk.New("wr", version)

	// Ensure sandbox directories exist before any command runs.
	// This guarantees crash_dumps/ is ready for the signal handler
	// installed by app.Execute().
	if err := app.Sandbox().Ensure(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: sandbox ensure failed: %v\n", err)
	}

	// Register custom health checks (daemon, llm, pushover).
	registerHealthChecks()

	// Add agent command tree (schema, errors, config, doctor, debug, cache).
	// Guard against double-registration in tests that call InitApp() repeatedly.
	if _, _, err := rootCmd.Find([]string{"agent"}); err != nil {
		rootCmd.AddCommand(app.AgentCommands())
	}

	registerErrorCodes()
	registerConfigProvider()
	registerCommandMeta()

	// Register daemon commands under the agent command tree.
	registerAgentDaemonCommands()
}

// App returns the shared SDK App instance for use by downstream slices.
func App() *agentsdk.App {
	return app
}

// registerConfigProvider creates the wrConfigProvider adapter and registers it
// with the SDK so agent config list/set commands can operate on wr's config.
func registerConfigProvider() {
	app.RegisterConfig("wr", &wrConfigProvider{})
}

// registerCommandMeta registers metadata for key wr commands to enrich the
// agent schema output with descriptions and idempotency flags.
func registerCommandMeta() {
	meta := map[string]agentsdk.CommandMeta{
		"add": {
			Description:  "Add a new work report entry",
			IsIdempotent: false,
		},
		"update": {
			Description:  "Update fields of an existing work report entry",
			IsIdempotent: false,
		},
		"complete": {
			Description:  "Mark a work report entry as complete",
			IsIdempotent: true,
		},
		"cancel": {
			Description:  "Cancel a work report entry",
			IsIdempotent: true,
		},
		"list": {
			Description:  "List work report entries",
			IsIdempotent: true,
		},
		"export": {
			Description:  "Export work report entries in JSON or Markdown format",
			IsIdempotent: true,
		},
		"import": {
			Description:  "Bulk import work report entries from a JSON file",
			IsIdempotent: false,
		},
		"report": {
			Description:  "Generate work reports",
			IsIdempotent: true,
		},
		"report today": {
			Description:  "Generate today's work report",
			IsIdempotent: true,
		},
		"report date": {
			Description:  "Generate report for a specific date",
			IsIdempotent: true,
		},
		"report push": {
			Description:  "Generate and push work report via Pushover",
			IsIdempotent: false,
		},
		"report week": {
			Description:  "Generate report for the current week (Mon–Sun)",
			IsIdempotent: true,
		},
		"status": {
			Description:  "Show daemon status and config diagnostics",
			IsIdempotent: true,
		},
		"config": {
			Description:  "Manage wr configuration",
			IsIdempotent: false,
		},
		"config init": {
			Description:  "Create a config file with defaults",
			IsIdempotent: true,
		},
		"config set": {
			Description:  "Set a configuration value",
			IsIdempotent: false,
		},
		"config show": {
			Description:  "Display current config (secrets redacted)",
			IsIdempotent: true,
		},
		"agent daemon": {
			Description:  "Manage the wr daemon process",
			IsIdempotent: false,
		},
		"agent daemon start": {
			Description:  "Start the wr daemon",
			IsIdempotent: false,
		},
		"agent daemon stop": {
			Description:  "Stop the wr daemon",
			IsIdempotent: true,
		},
		"agent daemon status": {
			Description:  "Show daemon status",
			IsIdempotent: true,
		},
		"agent daemon ensure-running": {
			Description:  "Ensure the daemon is running (start if needed) and return status",
			IsIdempotent: true,
		},
	}

	for cmdPath, m := range meta {
		app.RegisterCommandMeta(cmdPath, m)
	}
}
