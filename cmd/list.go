package cmd

import (
	"fmt"
	"os"

	"wr/internal/client"

	"github.com/spf13/cobra"
)

var (
	listType   string
	listDate   string
	listFrom   string
	listTo     string
	listStatus string
	listQuery  string
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
		if listFrom != "" {
			params = append(params, fmt.Sprintf("from=%s", listFrom))
		}
		if listTo != "" {
			params = append(params, fmt.Sprintf("to=%s", listTo))
		}
		if listStatus != "" {
			params = append(params, fmt.Sprintf("status=%s", listStatus))
		}
		if listQuery != "" {
			params = append(params, fmt.Sprintf("query=%s", listQuery))
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
	listCmd.Flags().StringVar(&listFrom, "from", "", "Filter from date (YYYY-MM-DD, inclusive)")
	listCmd.Flags().StringVar(&listTo, "to", "", "Filter to date (YYYY-MM-DD, inclusive)")
	listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status (active, completed, cancelled, all)")
	listCmd.Flags().StringVar(&listQuery, "query", "", "Keyword search in title and description")

	rootCmd.AddCommand(listCmd)
}
