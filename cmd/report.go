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

var reportPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Generate and push work report via Pushover",
}

var reportPushTodayCmd = &cobra.Command{
	Use:   "today",
	Short: "Push today's work report via Pushover",
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.CallDaemonPost(os.Stdout, "/api/report/push/today", nil)
	},
}

var reportPushDateCmd = &cobra.Command{
	Use:   "date <YYYY-MM-DD>",
	Short: "Push report for a specific date via Pushover",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		date := args[0]
		path := fmt.Sprintf("/api/report/push/date/%s", date)
		return client.CallDaemonPost(os.Stdout, path, nil)
	},
}

func init() {
	reportPushCmd.AddCommand(reportPushTodayCmd)
	reportPushCmd.AddCommand(reportPushDateCmd)

	reportCmd.AddCommand(reportTodayCmd)
	reportCmd.AddCommand(reportDateCmd)
	reportCmd.AddCommand(reportPushCmd)

	rootCmd.AddCommand(reportCmd)
}
