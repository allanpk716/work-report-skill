package cmd

import (
	"os"

	"wr/internal/client"

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
		path, err := buildActionPath("cancel", args)
		if err != nil {
			return err
		}
		return client.CallDaemonPost(os.Stdout, path, nil)
	},
}

func init() {
	rootCmd.AddCommand(cancelCmd)

	cancelCmd.Flags().StringVar(&cancelTitle, "title", "", "Lookup: exact title of the record to cancel")
	cancelCmd.Flags().StringVar(&cancelDate, "date", "", "Lookup: date of the record to cancel (YYYY-MM-DD)")
}
