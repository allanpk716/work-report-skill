package cmd

import (
	"fmt"
	"os"

	"wr/internal/client"

	"github.com/spf13/cobra"
)

var completeCmd = &cobra.Command{
	Use:   "complete <id>",
	Short: "Mark a work report entry as complete",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := fmt.Sprintf("/api/complete/%s", args[0])
		return client.CallDaemonPost(os.Stdout, path, nil)
	},
}

func init() {
	rootCmd.AddCommand(completeCmd)
}
