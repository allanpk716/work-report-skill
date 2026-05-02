package cmd

import (
	"wr/internal/jsonl"

	"github.com/spf13/cobra"
)

var (
	listType string
	listDate string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List work report entries",
	RunE: func(cmd *cobra.Command, args []string) error {
		return jsonl.Success(map[string]interface{}{
			"action": "list",
			"stub":   true,
			"filters": map[string]string{
				"type": listType,
				"date": listDate,
			},
			"entries": []interface{}{},
		})
	},
}

func init() {
	listCmd.Flags().StringVar(&listType, "type", "", "Filter by entry type")
	listCmd.Flags().StringVar(&listDate, "date", "", "Filter by date (YYYY-MM-DD)")

	rootCmd.AddCommand(listCmd)
}
