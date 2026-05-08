package cmd

import (
	"os"

	"wr/internal/client"

	"github.com/spf13/cobra"
)

var (
	digestSchedule  string
	digestScope     string
	digestDirection string
)

// digestCmd is the parent command for digest configuration management.
var digestCmd = &cobra.Command{
	Use:   "digest",
	Short: "Manage digest configurations",
}

// digestAddCmd creates a new digest configuration.
var digestAddCmd = &cobra.Command{
	Use:   "add --schedule <cron> --scope <scope> --direction <direction>",
	Short: "Create a new digest configuration",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		payload := map[string]string{
			"schedule":  digestSchedule,
			"scope":     digestScope,
			"direction": digestDirection,
		}
		return client.CallDaemonPost(os.Stdout, "/api/digest/add", payload)
	},
}

// digestListCmd lists all digest configurations.
var digestListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all digest configurations",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.CallDaemonGet(os.Stdout, "/api/digest/list")
	},
}

// digestRemoveCmd removes a digest configuration by ID.
var digestRemoveCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove a digest configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.CallDaemonPost(os.Stdout, "/api/digest/remove/"+args[0], nil)
	},
}

// digestEnableCmd enables a digest configuration by ID.
var digestEnableCmd = &cobra.Command{
	Use:   "enable <id>",
	Short: "Enable a digest configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.CallDaemonPost(os.Stdout, "/api/digest/enable/"+args[0], nil)
	},
}

// digestDisableCmd disables a digest configuration by ID.
var digestDisableCmd = &cobra.Command{
	Use:   "disable <id>",
	Short: "Disable a digest configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.CallDaemonPost(os.Stdout, "/api/digest/disable/"+args[0], nil)
	},
}

// digestPreviewCmd previews an LLM-generated digest summary for a configuration ID.
var digestPreviewCmd = &cobra.Command{
	Use:   "preview <id>",
	Short: "Preview LLM digest summary in terminal",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.CallDaemonGet(os.Stdout, "/api/digest/preview/"+args[0])
	},
}

func init() {
	digestCmd.AddCommand(digestAddCmd)
	digestCmd.AddCommand(digestListCmd)
	digestCmd.AddCommand(digestRemoveCmd)
	digestCmd.AddCommand(digestEnableCmd)
	digestCmd.AddCommand(digestDisableCmd)
	digestCmd.AddCommand(digestPreviewCmd)

	digestAddCmd.Flags().StringVar(&digestSchedule, "schedule", "", "Cron expression (e.g. '0 8 * * *')")
	digestAddCmd.Flags().StringVar(&digestScope, "scope", "", "Digest scope: today, yesterday, week, month, custom")
	digestAddCmd.Flags().StringVar(&digestDirection, "direction", "", "Output direction: agenda or summary")

	digestAddCmd.MarkFlagRequired("schedule")
	digestAddCmd.MarkFlagRequired("scope")
	digestAddCmd.MarkFlagRequired("direction")

	rootCmd.AddCommand(digestCmd)
}
