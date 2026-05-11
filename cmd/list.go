package cmd

import (
	"fmt"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/models"
	"wr/internal/storage"

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
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		// Build ListOptions from flags
		opts := storage.ListOptions{}

		if listType != "" {
			if !models.IsValidType(listType) {
				return writeJSONLError("invalid_type", fmt.Sprintf("invalid type: %q (must be meeting, task, reminder, or done_things)", listType))
			}
			opts.RecordType = models.RecordType(listType)
		}
		if listDate != "" {
			opts.Date = listDate
		}
		if listFrom != "" {
			opts.DateFrom = listFrom
		}
		if listTo != "" {
			opts.DateTo = listTo
		}
		if listStatus != "" {
			opts.Status = listStatus
		}
		if listQuery != "" {
			opts.Query = listQuery
		}

		records, err := store.ListRecords(opts)
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to list records: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action": "list",
			"count":  len(records),
			"entries": records,
		})
		return nil
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
