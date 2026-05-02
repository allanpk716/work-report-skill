package cmd

import (
	"wr/internal/jsonl"

	"github.com/spf13/cobra"
)

var completeCmd = &cobra.Command{
	Use:   "complete <id>",
	Short: "Mark a work report entry as complete",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return jsonl.Success(map[string]interface{}{
			"action": "complete",
			"stub":   true,
			"id":     args[0],
		})
	},
}

func init() {
	rootCmd.AddCommand(completeCmd)
}
