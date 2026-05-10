package cmd

import (
	"fmt"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
	"github.com/spf13/cobra"

	"wr/internal/config"
)

var (
	configPushoverToken   string
	configPushoverKey     string
	configLLMTextKey      string
	configLLMTextModel    string
	configLLMTextAPIBase  string
	configLLMTextProvider string
	configLLMVisionKey    string
	configLLMVisionModel  string
	configLLMVisionAPIBase  string
	configLLMVisionProvider string
	configTimezone        string
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage wr configuration",
	Long:  `Manage ~/.work-report/config.json programmatically. Subcommands: init, set, show.`,
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a config file with defaults",
	Long: `Create ~/.work-report/config.json with sensible defaults. Any provided flags override the defaults. Validates and saves. Outputs JSONL success with the config file path.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.DefaultConfigPath()
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("config: cannot determine config path: %v", err))
		}

		// Load existing or start with defaults
		cfg, err := config.Load(path)
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("config: load: %v", err))
		}

		// Override with any flags that were explicitly set
		if cmd.Flags().Changed("pushover-token") {
			cfg.Pushover.APIToken = configPushoverToken
		}
		if cmd.Flags().Changed("pushover-key") {
			cfg.Pushover.UserKey = configPushoverKey
		}
		if cmd.Flags().Changed("llm-text-key") {
			cfg.LLM.Text.APIKey = configLLMTextKey
		}
		if cmd.Flags().Changed("llm-text-model") {
			cfg.LLM.Text.Model = configLLMTextModel
		}
		if cmd.Flags().Changed("llm-text-api-base") {
			cfg.LLM.Text.APIBase = configLLMTextAPIBase
		}
		if cmd.Flags().Changed("llm-text-provider") {
			cfg.LLM.Text.Provider = configLLMTextProvider
		}
		if cmd.Flags().Changed("llm-vision-key") {
			cfg.LLM.Vision.APIKey = configLLMVisionKey
		}
		if cmd.Flags().Changed("llm-vision-model") {
			cfg.LLM.Vision.Model = configLLMVisionModel
		}
		if cmd.Flags().Changed("llm-vision-api-base") {
			cfg.LLM.Vision.APIBase = configLLMVisionAPIBase
		}
		if cmd.Flags().Changed("llm-vision-provider") {
			cfg.LLM.Vision.Provider = configLLMVisionProvider
		}
		if cmd.Flags().Changed("timezone") {
			cfg.Timezone = configTimezone
		}

		// Validate before saving
		if err := cfg.Validate(); err != nil {
			return writeExitError(agentsdk.ExitInvalidParams, fmt.Sprintf("config: validation failed: %v", err))
		}

		if err := cfg.Save(path); err != nil {
			return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("config: save failed: %v", err))
		}

		return app.JSONL().Success(map[string]interface{}{
			"path": path,
		})
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config value",
	Long: `Set a config value by dot-notation key (e.g. "llm.text.api_key"). Loads config, applies the change, validates, and saves.`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		value := args[1]

		path, err := config.DefaultConfigPath()
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("config: cannot determine config path: %v", err))
		}

		cfg, err := config.Load(path)
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("config: load: %v", err))
		}

		if err := cfg.SetByPath(key, value); err != nil {
			return writeExitError(agentsdk.ExitInvalidParams, fmt.Sprintf("config: set: %v", err))
		}

		// Validate before saving
		if err := cfg.Validate(); err != nil {
			return writeExitError(agentsdk.ExitInvalidParams, fmt.Sprintf("config: validation failed: %v", err))
		}

		if err := cfg.Save(path); err != nil {
			return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("config: save failed: %v", err))
		}

		return app.JSONL().Success(map[string]interface{}{
			"key":   key,
			"value": value,
		})
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display current config (secrets redacted)",
	Long:  `Load the current config and display it as JSONL with all sensitive fields masked.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.DefaultConfigPath()
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("config: cannot determine config path: %v", err))
		}

		cfg, err := config.Load(path)
		if err != nil {
			return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("config: load: %v", err))
		}

		return app.JSONL().Success(cfg.Redacted())
	},
}

func init() {
	// Init flags
	configInitCmd.Flags().StringVar(&configPushoverToken, "pushover-token", "", "Pushover API token")
	configInitCmd.Flags().StringVar(&configPushoverKey, "pushover-key", "", "Pushover user key")
	configInitCmd.Flags().StringVar(&configLLMTextKey, "llm-text-key", "", "LLM text provider API key")
	configInitCmd.Flags().StringVar(&configLLMTextModel, "llm-text-model", "", "LLM text model name")
	configInitCmd.Flags().StringVar(&configLLMTextAPIBase, "llm-text-api-base", "", "LLM text API base URL")
	configInitCmd.Flags().StringVar(&configLLMTextProvider, "llm-text-provider", "", "LLM text provider name")
	configInitCmd.Flags().StringVar(&configLLMVisionKey, "llm-vision-key", "", "LLM vision provider API key")
	configInitCmd.Flags().StringVar(&configLLMVisionModel, "llm-vision-model", "", "LLM vision model name")
	configInitCmd.Flags().StringVar(&configLLMVisionAPIBase, "llm-vision-api-base", "", "LLM vision API base URL")
	configInitCmd.Flags().StringVar(&configLLMVisionProvider, "llm-vision-provider", "", "LLM vision provider name")
	configInitCmd.Flags().StringVar(&configTimezone, "timezone", "", "Timezone (e.g. Asia/Shanghai)")

	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configShowCmd)
	rootCmd.AddCommand(configCmd)
}
