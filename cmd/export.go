package cmd

import (
	"bytes"
	"fmt"
	"os"

	"wr/internal/client"
	"wr/internal/exitcode"

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
			return writeExitError(exitcode.ExitInvalidParams, "--format is required (json or markdown)")
		}
		if exportFormat != "json" && exportFormat != "markdown" {
			return writeExitError(exitcode.ExitInvalidParams, "--format must be \"json\" or \"markdown\"")
		}

		path := "/api/export"
		params := []string{fmt.Sprintf("format=%s", exportFormat)}

		if exportDate != "" {
			params = append(params, fmt.Sprintf("date=%s", exportDate))
		}
		if exportFrom != "" {
			params = append(params, fmt.Sprintf("from=%s", exportFrom))
		}
		if exportTo != "" {
			params = append(params, fmt.Sprintf("to=%s", exportTo))
		}
		if exportType != "" {
			params = append(params, fmt.Sprintf("type=%s", exportType))
		}
		if exportStatus != "" {
			params = append(params, fmt.Sprintf("status=%s", exportStatus))
		}
		if exportQuery != "" {
			params = append(params, fmt.Sprintf("query=%s", exportQuery))
		}

		path += "?" + params[0]
		for _, p := range params[1:] {
			path += "&" + p
		}

		if exportFile == "" {
			return client.CallDaemonGet(os.Stdout, path)
		}

		// Capture response into buffer, then write to file
		var buf bytes.Buffer
		err := client.CallDaemonGet(&buf, path)
		if err != nil {
			// CallDaemonGet already wrote JSONL error to buf; forward to stdout
			os.Stdout.Write(buf.Bytes())
			return err
		}
		if err := os.WriteFile(exportFile, buf.Bytes(), 0644); err != nil {
			return writeExitError(exitcode.ExitFatalError, fmt.Sprintf("cannot write to %q: %v", exportFile, err))
		}
		return nil
	},
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
