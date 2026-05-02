package cmd

import (
	"fmt"
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

var reportDateCmd = &cobra.Command{
	Use:   "date <YYYY-MM-DD>",
	Short: "Generate report for a specific date",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		date := args[0]
		path := fmt.Sprintf("/api/report?date=%s", date)
		return client.CallDaemonGet(os.Stdout, path)
	},
}

func init() {
	reportCmd.AddCommand(reportTodayCmd)
	reportCmd.AddCommand(reportDateCmd)
	rootCmd.AddCommand(reportCmd)
}
