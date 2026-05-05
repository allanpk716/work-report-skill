package cmd

import (
	"fmt"
	"os"

	agentsdk "github.com/allanpk716/agent-cli-sdk"

	"wr/internal/client"

	"github.com/spf13/cobra"
)

var (
	updateTitle        string
	updateDescription  string
	updateDate         string
	updateTime         string
	updateLocation     string
	updateTags         []string
	updatePriority     string
	updateRemindBefore string
	updateRecurring    string
	updateEndTime      string
	updateRelatedPerson string
	updateParticipants []string
	updateAgenda       string
	updateNotes        string
	updateProgress     string
)

var updateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update fields of an existing work report entry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fields := make(map[string]interface{})

		if cmd.Flags().Changed("title") {
			fields["title"] = updateTitle
		}
		if cmd.Flags().Changed("description") {
			fields["description"] = updateDescription
		}
		if cmd.Flags().Changed("date") {
			fields["date"] = updateDate
		}
		if cmd.Flags().Changed("time") {
			fields["time"] = updateTime
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

		if len(fields) == 0 {
			return writeExitError(agentsdk.ExitInvalidParams, "no fields specified for update")
		}

		path := fmt.Sprintf("/api/update/%s", args[0])
		return client.CallDaemonPost(os.Stdout, path, fields)
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)

	updateCmd.Flags().StringVar(&updateTitle, "title", "", "Update title")
	updateCmd.Flags().StringVar(&updateDescription, "description", "", "Update description")
	updateCmd.Flags().StringVar(&updateDate, "date", "", "Update date (YYYY-MM-DD)")
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
}
