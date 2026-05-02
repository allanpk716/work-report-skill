package cmd

import (
	"fmt"
	"os"

	"wr/internal/client"

	"github.com/spf13/cobra"
)

var cancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Cancel a work report entry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := fmt.Sprintf("/api/cancel/%s", args[0])
		return client.CallDaemonPost(os.Stdout, path, nil)
	},
}

func init() {
	rootCmd.AddCommand(cancelCmd)
}
