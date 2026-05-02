package cmd

import (
	"wr/internal/jsonl"

	"github.com/spf13/cobra"
)

var cancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Cancel a work report entry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return jsonl.Success(map[string]interface{}{
			"action": "cancel",
			"stub":   true,
			"id":     args[0],
		})
	},
}

func init() {
	rootCmd.AddCommand(cancelCmd)
}
