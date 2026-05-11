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
	updateTitle         string
	updateDescription   string
	updateDate          string
	updateTime          string
	updateLocation      string
	updateTags          []string
	updatePriority      string
	updateRemindBefore  string
	updateRecurring     string
	updateEndTime       string
	updateRelatedPerson string
	updateParticipants  []string
	updateAgenda        string
	updateNotes         string
	updateProgress      string
	updateNotifyPriority string
)

var updateCmd = &cobra.Command{
	Use:   "update [<id>]",
	Short: "Update fields of an existing work report entry",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		// Build fields map from changed flags
		fields := make(map[string]interface{})
		if cmd.Flags().Changed("time") {
			fields["time"] = updateTime
		}
		if cmd.Flags().Changed("description") {
			fields["description"] = updateDescription
		}
		if cmd.Flags().Changed("location") {
			fields["location"] = updateLocation
		}
		if cmd.Flags().Changed("tags") {
			fields["tags"] = updateTags
		}
		if cmd.Flags().Changed("priority") {
			fields["priority"] = updatePriority
		}
		if cmd.Flags().Changed("remind-before") {
			fields["remind_before"] = updateRemindBefore
		}
		if cmd.Flags().Changed("recurring") {
			fields["recurring"] = updateRecurring
		}
		if cmd.Flags().Changed("end-time") {
			fields["end_time"] = updateEndTime
		}
		if cmd.Flags().Changed("related-person") {
			fields["related_person"] = updateRelatedPerson
		}
		if cmd.Flags().Changed("participants") {
			fields["participants"] = updateParticipants
		}
		if cmd.Flags().Changed("agenda") {
			fields["agenda"] = updateAgenda
		}
		if cmd.Flags().Changed("notes") {
			fields["notes"] = updateNotes
		}
		if cmd.Flags().Changed("progress") {
			fields["progress"] = updateProgress
		}
		if cmd.Flags().Changed("notify-priority") {
			if !models.IsValidNotificationPriority(updateNotifyPriority) {
				return writeJSONLError("invalid_params", fmt.Sprintf("invalid notify-priority: %q (must be normal or high)", updateNotifyPriority))
			}
			fields["notification_priority"] = updateNotifyPriority
		}

		// Resolve record ID
		var id string
		if len(args) > 0 && args[0] != "" {
			// Positional ID mode: --title/--date update fields, not lookup params
			if cmd.Flags().Changed("title") {
				fields["title"] = updateTitle
			}
			if cmd.Flags().Changed("date") {
				fields["date"] = updateDate
			}
			id = args[0]
		} else {
			// Lookup mode: --title and --date are query params for record lookup
			resolved, err := resolveIDOrLookup(store, "", updateTitle, updateDate)
			if err != nil {
				return writeJSONLError("invalid_params", err.Error())
			}
			id = resolved
		}

		if len(fields) == 0 {
			return writeJSONLError("invalid_params", "no fields specified for update")
		}

		// Check if record exists before updating
		rec, _, err := store.GetByID(id)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return writeJSONLError("record_not_found", fmt.Sprintf("record %q not found", id))
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to find record: %v", err))
		}

		// Check if record is completed or cancelled
		cf := models.GetCommonFields(rec)
		if cf != nil && cf.Status == "completed" {
			return writeJSONLError("already_completed", fmt.Sprintf("record %q is completed, cannot update", id))
		}
		if cf != nil && cf.Status == "cancelled" {
			return writeJSONLError("already_cancelled", fmt.Sprintf("record %q is cancelled, cannot update", id))
		}

		// Apply update via storage
		updated, err := withLockRetryResult(func() (interface{}, error) {
			return store.UpdateRecord(id, fields)
		})
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return writeJSONLError("record_not_found", err.Error())
			}
			if strings.Contains(err.Error(), "empty update") {
				return writeJSONLError("invalid_params", err.Error())
			}
			if strings.Contains(err.Error(), "field not allowed") {
				return writeJSONLError("invalid_field", err.Error())
			}
			if strings.Contains(err.Error(), "already completed") {
				return writeJSONLError("already_completed", err.Error())
			}
			if strings.Contains(err.Error(), "already cancelled") {
				return writeJSONLError("already_cancelled", err.Error())
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to update record: %v", err))
		}

		logger.Infof("update: id=%s fields=%v", id, fieldKeys(fields))
		writeJSONLSuccess(map[string]interface{}{
			"action": "update",
			"record": updated,
		})
		return nil
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)

	updateCmd.Flags().StringVar(&updateTitle, "title", "", "When <id> provided: update title. Otherwise: lookup title")
	updateCmd.Flags().StringVar(&updateDescription, "description", "", "Update description")
	updateCmd.Flags().StringVar(&updateDate, "date", "", "When <id> provided: update date. Otherwise: lookup date (YYYY-MM-DD)")
	updateCmd.Flags().StringVar(&updateTime, "time", "", "Update time (HH:MM)")
	updateCmd.Flags().StringVar(&updateLocation, "location", "", "Update location")
	updateCmd.Flags().StringSliceVar(&updateTags, "tags", nil, "Update tags (comma-separated)")
	updateCmd.Flags().StringVar(&updatePriority, "priority", "", "Update priority")
	updateCmd.Flags().StringVar(&updateRemindBefore, "remind-before", "", "Update remind_before (e.g. 15m, 1h)")
	updateCmd.Flags().StringVar(&updateRecurring, "recurring", "", "Update recurring (e.g. daily, weekly)")
	updateCmd.Flags().StringVar(&updateEndTime, "end-time", "", "Update end time (HH:MM)")
	updateCmd.Flags().StringVar(&updateRelatedPerson, "related-person", "", "Update related person")
	updateCmd.Flags().StringSliceVar(&updateParticipants, "participants", nil, "Update participants (comma-separated)")
	updateCmd.Flags().StringVar(&updateAgenda, "agenda", "", "Update agenda")
	updateCmd.Flags().StringVar(&updateNotes, "notes", "", "Update notes")
	updateCmd.Flags().StringVar(&updateProgress, "progress", "", "Update progress")
	updateCmd.Flags().StringVar(&updateNotifyPriority, "notify-priority", "", "Update notification priority (normal, high)")
}

// fieldKeys returns the keys of a fields map as a string slice.
func fieldKeys(fields map[string]interface{}) []string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	return keys
}
