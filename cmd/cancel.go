package cmd

import (
	"fmt"
	"strings"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/logger"
	"wr/internal/models"

	"github.com/spf13/cobra"
)

var (
	cancelTitle string
	cancelDate  string
)

var cancelCmd = &cobra.Command{
	Use:   "cancel [<id>]",
	Short: "Cancel a work report entry",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		// Default --date to today when using --title lookup (matches wr add behavior)
		if cancelTitle != "" && cancelDate == "" {
			cancelDate = todayInLocation(cfg)
			logger.Infof("cancel: source=default_today date=%s", cancelDate)
		}

		// Resolve record ID: positional arg or --title/--date lookup
		var id string
		if len(args) > 0 && args[0] != "" {
			id = args[0]
		} else {
			resolved, err := resolveIDOrLookup(store, "", cancelTitle, cancelDate)
			if err != nil {
				return writeJSONLError("invalid_params", err.Error())
			}
			id = resolved
		}

		// Check if record exists before cancelling
		rec, _, err := store.GetByID(id)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return writeJSONLError("record_not_found", fmt.Sprintf("record %q not found", id))
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to find record: %v", err))
		}

		// Check if already cancelled
		cf := models.GetCommonFields(rec)
		if cf != nil && cf.Status == "cancelled" {
			return writeJSONLError("already_cancelled", fmt.Sprintf("record %q is already cancelled", id))
		}

		// Check if already completed
		if cf != nil && cf.Status == "completed" {
			return writeJSONLError("invalid_params", fmt.Sprintf("record %q is completed, cannot cancel", id))
		}

		if err := withLockRetry(func() error { return store.CancelRecord(id) }); err != nil {
			if strings.Contains(err.Error(), "already cancelled") {
				return writeJSONLError("already_cancelled", err.Error())
			}
			if strings.Contains(err.Error(), "completed") {
				return writeJSONLError("invalid_params", err.Error())
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to cancel record: %v", err))
		}

		// Read back cancelled record for output
		cancelled, _, err := store.GetByID(id)
		if err != nil {
			logger.Warnf("cancel: cancelled record %s but could not read back: %v", id, err)
			writeJSONLSuccess(map[string]interface{}{
				"action": "cancel",
				"id":     id,
			})
			return nil
		}

		logger.Infof("cancel: id=%s", id)
		writeJSONLSuccess(map[string]interface{}{
			"action": "cancel",
			"record": cancelled,
		})
		return nil
	},
}

func init() {
	rootCmd.AddCommand(cancelCmd)

	cancelCmd.Flags().StringVar(&cancelTitle, "title", "", "Lookup: exact title of the record to cancel")
	cancelCmd.Flags().StringVar(&cancelDate, "date", "", "Lookup: date of the record to cancel (YYYY-MM-DD)")
}

// ResetCancelFlags resets all cancel command flags to their defaults.
// For test isolation only.
func ResetCancelFlags() {
	cancelTitle = ""
	cancelDate = ""
}
