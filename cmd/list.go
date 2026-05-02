package cmd

import (
	"fmt"
	"os"

	"wr/internal/client"

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
		path := "/api/list"
		params := []string{}
		if listType != "" {
			params = append(params, fmt.Sprintf("type=%s", listType))
		}
		if listDate != "" {
			params = append(params, fmt.Sprintf("date=%s", listDate))
		}
		if len(params) > 0 {
			path += "?" + params[0]
			for _, p := range params[1:] {
				path += "&" + p
			}
		}
		return client.CallDaemonGet(os.Stdout, path)
	},
}

func init() {
	listCmd.Flags().StringVar(&listType, "type", "", "Filter by entry type")
	listCmd.Flags().StringVar(&listDate, "date", "", "Filter by date (YYYY-MM-DD)")

	rootCmd.AddCommand(listCmd)
}
