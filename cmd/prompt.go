package cmd

import (
	"os"

	"wr/internal/client"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
	"github.com/spf13/cobra"
)

var (
	promptText string
	promptFile string
)

// promptCmd is the parent command for prompt management.
var promptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Manage prompt templates",
}

// promptListCmd lists all prompts with their current text and default status.
var promptListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all prompts",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.CallDaemonGet(os.Stdout, "/api/prompt/list")
	},
}

// promptShowCmd shows the effective prompt text for a named prompt.
var promptShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show prompt text",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.CallDaemonGet(os.Stdout, "/api/prompt/show/"+args[0])
	},
}

// promptSetCmd sets a custom prompt text for a named prompt.
var promptSetCmd = &cobra.Command{
	Use:   "set <name> --text <text>|--file <path>",
	Short: "Set prompt text",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		payload := map[string]string{}
		if promptText != "" {
			payload["text"] = promptText
		}
		if promptFile != "" {
			payload["file"] = promptFile
		}
		if payload["text"] == "" && payload["file"] == "" {
			return writeExitError(int(agentsdk.ExitInvalidParams), "--text or --file is required")
		}
		return client.CallDaemonPost(os.Stdout, "/api/prompt/set/"+args[0], payload)
	},
}

// promptResetCmd restores a built-in prompt to its default text.
var promptResetCmd = &cobra.Command{
	Use:   "reset <name>",
	Short: "Reset prompt to default",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.CallDaemonPost(os.Stdout, "/api/prompt/reset/"+args[0], nil)
	},
}

func init() {
	promptCmd.AddCommand(promptListCmd)
	promptCmd.AddCommand(promptShowCmd)
	promptCmd.AddCommand(promptSetCmd)
	promptCmd.AddCommand(promptResetCmd)

	promptSetCmd.Flags().StringVar(&promptText, "text", "", "Prompt text")
	promptSetCmd.Flags().StringVar(&promptFile, "file", "", "Path to file containing prompt text")

	rootCmd.AddCommand(promptCmd)
}
