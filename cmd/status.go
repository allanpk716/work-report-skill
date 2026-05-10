package cmd

import (
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Daemon has been removed — status command stubbed.
		// S03 will rewrite this with new observability surfaces.
		return app.JSONL().Error("daemon commands have been removed; use 'wr agent doctor' for diagnostics")
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
