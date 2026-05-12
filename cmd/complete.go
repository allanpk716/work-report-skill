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
	completeTitle string
	completeDate  string
)

var completeCmd = &cobra.Command{
	Use:   "complete [<id>]",
	Short: "Mark a work report entry as complete",
	Long: `Mark a work report entry as complete.

Note: done_things records cannot be completed — they are factual records of work already done.
Only task, meeting, reminder, and personal types support this action.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		// Default --date to today when using --title lookup (matches wr add behavior)
		if completeTitle != "" && completeDate == "" {
			completeDate = todayInLocation(cfg)
			logger.Infof("complete: source=default_today date=%s", completeDate)
		}

		// Resolve record ID: positional arg or --title/--date lookup
		var id string
		if len(args) > 0 && args[0] != "" {
			id = args[0]
		} else {
			resolved, err := resolveIDOrLookup(store, "", completeTitle, completeDate)
			if err != nil {
				return writeJSONLError("invalid_params", err.Error())
			}
			id = resolved
		}

		// Check if record exists before completing
		rec, _, err := store.GetByID(id)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return writeJSONLError("record_not_found", fmt.Sprintf("record %q not found", id))
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to find record: %v", err))
		}

		// Check if already completed
		cf := models.GetCommonFields(rec)
		if cf != nil && cf.Status == "completed" {
			return writeJSONLError("already_completed", fmt.Sprintf("record %q is already completed", id))
		}

		// Check if already cancelled
		if cf != nil && cf.Status == "cancelled" {
			return writeJSONLError("already_cancelled", fmt.Sprintf("record %q is cancelled, cannot complete", id))
		}

		// Check if record type supports completion
		if cf != nil && !models.IsActionableType(cf.Type) {
			return writeJSONLError("type_not_completable", fmt.Sprintf("%s records cannot be completed: they are factual records of work already done. Only task, meeting, reminder, and personal types support the complete action.", cf.Type))
		}

		if err := withLockRetry(func() error { return store.CompleteRecord(id) }); err != nil {
			if strings.Contains(err.Error(), "already completed") {
				return writeJSONLError("already_completed", err.Error())
			}
			if strings.Contains(err.Error(), "cannot be completed") {
				return writeJSONLError("invalid_params", err.Error())
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to complete record: %v", err))
		}

		// Read back completed record for output
		completed, _, err := store.GetByID(id)
		if err != nil {
			logger.Warnf("complete: completed record %s but could not read back: %v", id, err)
			writeJSONLSuccess(map[string]interface{}{
				"action": "complete",
				"id":     id,
			})
			return nil
		}

		logger.Infof("complete: id=%s", id)
		writeJSONLSuccess(map[string]interface{}{
			"action": "complete",
			"record": completed,
		})
		return nil
	},
}

func init() {
	rootCmd.AddCommand(completeCmd)

	completeCmd.Flags().StringVar(&completeTitle, "title", "", "Lookup: exact title of the record to complete")
	completeCmd.Flags().StringVar(&completeDate, "date", "", "Lookup: date of the record to complete (YYYY-MM-DD)")
}

// ResetCompleteFlags resets all complete command flags to their defaults.
// For test isolation only.
func ResetCompleteFlags() {
	completeTitle = ""
	completeDate = ""
}
