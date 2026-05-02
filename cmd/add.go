package cmd

import (
	"wr/internal/jsonl"

	"github.com/spf13/cobra"
)

var (
	addType  string
	addTitle string
	addDate  string
	addTime  string
	addImage string
)

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new work report entry",
	RunE: func(cmd *cobra.Command, args []string) error {
		return jsonl.Success(map[string]interface{}{
			"action": "add",
			"stub":   true,
			"entry": map[string]string{
				"type":  addType,
				"title": addTitle,
				"date":  addDate,
				"time":  addTime,
				"image": addImage,
			},
		})
	},
}

func init() {
	addCmd.Flags().StringVar(&addType, "type", "", "Entry type (e.g. meeting, task, note)")
	addCmd.Flags().StringVar(&addTitle, "title", "", "Entry title")
	addCmd.Flags().StringVar(&addDate, "date", "", "Date (YYYY-MM-DD)")
	addCmd.Flags().StringVar(&addTime, "time", "", "Time (HH:MM)")
	addCmd.Flags().StringVar(&addImage, "image", "", "Image path")

	rootCmd.AddCommand(addCmd)
}
