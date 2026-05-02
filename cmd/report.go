package cmd

import (
	"wr/internal/jsonl"

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
		return jsonl.Success(map[string]interface{}{
			"action":  "report_today",
			"stub":    true,
			"entries": []interface{}{},
		})
	},
}

func init() {
	reportCmd.AddCommand(reportTodayCmd)
	rootCmd.AddCommand(reportCmd)
}
