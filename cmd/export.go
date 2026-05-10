package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/models"
	"wr/internal/report"
	"wr/internal/storage"

	"github.com/spf13/cobra"
)

var (
	exportFormat string
	exportDate   string
	exportFrom   string
	exportTo     string
	exportType   string
	exportStatus string
	exportQuery  string
	exportFile   string
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export work report entries in JSON or Markdown format",
	RunE: func(cmd *cobra.Command, args []string) error {
		if exportFormat == "" {
			return writeJSONLError("invalid_params", "--format is required (json or markdown)")
		}
		if exportFormat != "json" && exportFormat != "markdown" {
			return writeJSONLError("invalid_params", "--format must be \"json\" or \"markdown\"")
		}

		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		switch exportFormat {
		case "json":
			return exportJSON(store)
		case "markdown":
			return exportMarkdown(store, cfg)
		default:
			return writeJSONLError("invalid_params", fmt.Sprintf("unknown format %q", exportFormat))
		}
	},
}

// exportJSON exports records as JSONL. Uses ListFullRecords for complete data.
func exportJSON(store *storage.Storage) error {
	opts := buildExportListOptions()
	records, err := store.ListFullRecords(opts)
	if err != nil {
		return writeJSONLError("storage_error", fmt.Sprintf("failed to list records: %v", err))
	}

	output := map[string]interface{}{
		"action":  "export",
		"format":  "json",
		"count":   len(records),
		"records": records,
	}

	if exportFile != "" {
		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to marshal export: %v", err))
		}
		if err := os.WriteFile(exportFile, data, 0644); err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("cannot write to %q: %v", exportFile, err))
		}
		writeJSONLSuccess(map[string]interface{}{
			"action":   "export",
			"format":   "json",
			"count":    len(records),
			"output":   exportFile,
		})
		return nil
	}

	writeJSONLSuccess(output)
	return nil
}

// exportMarkdown exports records as Markdown using the report package.
func exportMarkdown(store *storage.Storage, cfg interface{ Location() *time.Location }) error {
	// Determine date range for the report
	loc := cfg.Location()

	if exportDate != "" {
		// Single date export
		rpt, err := report.Generate(store, exportDate)
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to generate report: %v", err))
		}

		return writeMarkdownOutput(rpt.Markdown, 1, exportDate, exportDate)
	}

	// Date range or week export
	from := exportFrom
	to := exportTo

	// If neither from/to specified, default to today
	if from == "" && to == "" {
		today := time.Now().In(loc).Format("2006-01-02")
		from = today
		to = today
	}

	if from != "" && to == "" {
		to = from
	}
	if to != "" && from == "" {
		from = to
	}

	rpt, err := report.GenerateRange(store, from, to, loc)
	if err != nil {
		return writeJSONLError("storage_error", fmt.Sprintf("failed to generate range report: %v", err))
	}

	return writeMarkdownOutput(rpt.Markdown, rpt.DaysCount, from, to)
}

// writeMarkdownOutput writes Markdown content to a file or stdout as JSONL.
func writeMarkdownOutput(markdown string, count int, from, to string) error {
	if exportFile != "" {
		if err := os.WriteFile(exportFile, []byte(markdown), 0644); err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("cannot write to %q: %v", exportFile, err))
		}
		writeJSONLSuccess(map[string]interface{}{
			"action": "export",
			"format": "markdown",
			"count":  count,
			"from":   from,
			"to":     to,
			"output": exportFile,
		})
		return nil
	}

	writeJSONLSuccess(map[string]interface{}{
		"action":   "export",
		"format":   "markdown",
		"count":    count,
		"from":     from,
		"to":       to,
		"markdown": markdown,
	})
	return nil
}

// buildExportListOptions constructs ListOptions from export flags.
func buildExportListOptions() storage.ListOptions {
	opts := storage.ListOptions{}

	if exportType != "" {
		if models.IsValidType(exportType) {
			opts.RecordType = models.RecordType(exportType)
		}
	}
	if exportDate != "" {
		opts.Date = exportDate
	}
	if exportFrom != "" {
		opts.DateFrom = exportFrom
	}
	if exportTo != "" {
		opts.DateTo = exportTo
	}
	if exportStatus != "" {
		opts.Status = exportStatus
	}
	if exportQuery != "" {
		opts.Query = exportQuery
	}

	return opts
}

func init() {
	exportCmd.Flags().StringVar(&exportFormat, "format", "", "Output format: json or markdown (required)")
	exportCmd.Flags().StringVar(&exportDate, "date", "", "Filter by date (YYYY-MM-DD)")
	exportCmd.Flags().StringVar(&exportFrom, "from", "", "Filter from date (YYYY-MM-DD, inclusive)")
	exportCmd.Flags().StringVar(&exportTo, "to", "", "Filter to date (YYYY-MM-DD, inclusive)")
	exportCmd.Flags().StringVar(&exportType, "type", "", "Filter by entry type")
	exportCmd.Flags().StringVar(&exportStatus, "status", "", "Filter by status (active, completed, cancelled, all)")
	exportCmd.Flags().StringVar(&exportQuery, "query", "", "Keyword search in title and description")
	exportCmd.Flags().StringVar(&exportFile, "file", "", "Output file path (default: stdout)")

	rootCmd.AddCommand(exportCmd)
}
