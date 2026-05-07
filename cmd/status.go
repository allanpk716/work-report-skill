package cmd

import (
	"os"

	"wr/internal/client"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	RunE: func(cmd *cobra.Command, args []string) error {
		// CallDaemonGet writes a JSONL error to stdout and returns ExitError
		// when the daemon is unreachable — just propagate the error, no double write.
		return client.CallDaemonGet(os.Stdout, "/api/status")
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
