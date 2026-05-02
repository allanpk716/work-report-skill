package cmd

import (
	"os"

	"wr/internal/client"

	"github.com/spf13/cobra"
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate work reports",
}

var reportTodayCmd = &cobra.Command{
	Use:   "today",
	Short: "Generate today's work report",
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.CallDaemonGet(os.Stdout, "/api/report/today")
	},
}

func init() {
	reportCmd.AddCommand(reportTodayCmd)
	rootCmd.AddCommand(reportCmd)
}
