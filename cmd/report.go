package cmd

import (
	"fmt"
	"os"

	agentsdk "github.com/allanpk716/agent-cli-sdk"

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

var reportWeekCmd = &cobra.Command{
	Use:   "week",
	Short: "Generate report for the current week (Mon–Sun)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.CallDaemonGet(os.Stdout, "/api/report/week")
	},
}

var reportRangeFrom string
var reportRangeTo string

var reportRangeCmd = &cobra.Command{
	Use:   "range",
	Short: "Generate report for a date range",
	RunE: func(cmd *cobra.Command, args []string) error {
		if reportRangeFrom == "" || reportRangeTo == "" {
			return writeExitError(agentsdk.ExitInvalidParams, "both --from and --to are required")
		}
		path := fmt.Sprintf("/api/report/range?from=%s&to=%s", reportRangeFrom, reportRangeTo)
		return client.CallDaemonGet(os.Stdout, path)
	},
}

var reportPushWeekCmd = &cobra.Command{
	Use:   "week",
	Short: "Push the current week's report via Pushover",
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.CallDaemonPost(os.Stdout, "/api/report/push/week", nil)
	},
}

var reportPushRangeFrom string
var reportPushRangeTo string

var reportPushRangeCmd = &cobra.Command{
	Use:   "range",
	Short: "Push a date range report via Pushover",
	RunE: func(cmd *cobra.Command, args []string) error {
		if reportPushRangeFrom == "" || reportPushRangeTo == "" {
			return writeExitError(agentsdk.ExitInvalidParams, "both --from and --to are required")
		}
		path := fmt.Sprintf("/api/report/push/range?from=%s&to=%s", reportPushRangeFrom, reportPushRangeTo)
		return client.CallDaemonPost(os.Stdout, path, nil)
	},
}

func init() {
	reportRangeCmd.Flags().StringVar(&reportRangeFrom, "from", "", "Start date (YYYY-MM-DD, required)")
	reportRangeCmd.Flags().StringVar(&reportRangeTo, "to", "", "End date (YYYY-MM-DD, required)")

	reportPushRangeCmd.Flags().StringVar(&reportPushRangeFrom, "from", "", "Start date (YYYY-MM-DD, required)")
	reportPushRangeCmd.Flags().StringVar(&reportPushRangeTo, "to", "", "End date (YYYY-MM-DD, required)")

	reportPushCmd.AddCommand(reportPushTodayCmd)
	reportPushCmd.AddCommand(reportPushDateCmd)
	reportPushCmd.AddCommand(reportPushWeekCmd)
	reportPushCmd.AddCommand(reportPushRangeCmd)

	reportCmd.AddCommand(reportTodayCmd)
	reportCmd.AddCommand(reportDateCmd)
	reportCmd.AddCommand(reportWeekCmd)
	reportCmd.AddCommand(reportRangeCmd)
	reportCmd.AddCommand(reportPushCmd)

	rootCmd.AddCommand(reportCmd)
}
