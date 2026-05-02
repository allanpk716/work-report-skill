package cmd

import (
	"os"

	"wr/internal/client"

	"github.com/spf13/cobra"
)

var (
	addType          string
	addTitle         string
	addDate          string
	addTime          string
	addImage         string
	addText          string
	addDescription   string
	addTags          string
	addLocation      string
	addRelatedPerson string
	addPriority      string
	addRemindBefore  string
	addRecurring     string
)

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new work report entry",
	RunE: func(cmd *cobra.Command, args []string) error {
		payload := map[string]interface{}{
			"type":  addType,
			"title": addTitle,
			"date":  addDate,
		}
		if addTime != "" {
			payload["time"] = addTime
		}
		if addImage != "" {
			payload["image"] = addImage
		}
		if addText != "" {
			payload["text"] = addText
		}
		if addDescription != "" {
			payload["description"] = addDescription
		}
		if addTags != "" {
			payload["tags"] = splitTags(addTags)
		}
		if addLocation != "" {
			payload["location"] = addLocation
		}
		if addRelatedPerson != "" {
			payload["related_person"] = addRelatedPerson
		}
		if addPriority != "" {
			payload["priority"] = addPriority
		}
		if addRemindBefore != "" {
			payload["remind_before"] = addRemindBefore
		}
		if addRecurring != "" {
			payload["recurring"] = addRecurring
		}
		return client.CallDaemonPost(os.Stdout, "/api/add", payload)
	},
}

// splitTags splits a comma-separated tag string into a slice.
func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	// Simple comma split — no need for regex
	result := make([]string, 0)
	for _, tag := range splitByComma(s) {
		if tag != "" {
			result = append(result, tag)
		}
	}
	return result
}

func splitByComma(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func init() {
	addCmd.Flags().StringVar(&addType, "type", "", "Entry type (meeting, task, reminder, log)")
	addCmd.Flags().StringVar(&addTitle, "title", "", "Entry title")
	addCmd.Flags().StringVar(&addDate, "date", "", "Date (YYYY-MM-DD)")
	addCmd.Flags().StringVar(&addTime, "time", "", "Time (HH:MM)")
	addCmd.Flags().StringVar(&addText, "text", "", "Natural language text for LLM classification (type/title/date become optional)")
	addCmd.Flags().StringVar(&addImage, "image", "", "Image path")
	addCmd.Flags().StringVar(&addDescription, "description", "", "Description")
	addCmd.Flags().StringVar(&addTags, "tags", "", "Comma-separated tags")
	addCmd.Flags().StringVar(&addLocation, "location", "", "Location")
	addCmd.Flags().StringVar(&addRelatedPerson, "related-person", "", "Related person")
	addCmd.Flags().StringVar(&addPriority, "priority", "", "Priority (normal, high, medium)")
	addCmd.Flags().StringVar(&addRemindBefore, "remind-before", "", "Remind before (e.g. 15m, 30m)")
	addCmd.Flags().StringVar(&addRecurring, "recurring", "", "Recurring pattern (e.g. daily, weekly)")

	rootCmd.AddCommand(addCmd)
}
