package cmd

import (
	"fmt"
	"os"

	agentsdk "github.com/allanpk716/agent-cli-sdk"

	"wr/internal/client"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Try calling the daemon's /api/status endpoint
		err := client.CallDaemonGet(os.Stdout, "/api/status")
		if err == nil {
			return nil
		}

		// Daemon is not running — return error envelope
		return writeExitErrorWithCode(agentsdk.ExitNetworkError, "daemon_not_running",
			fmt.Sprintf("daemon is not running: %v. Run 'wr agent daemon start' to start the daemon.", err))
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
