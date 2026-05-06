package cmd

import (
	"fmt"
	"net/url"
	"os"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/client"

	"github.com/spf13/cobra"
)

var (
	completeTitle string
	completeDate  string
)

var completeCmd = &cobra.Command{
	Use:   "complete [<id>]",
	Short: "Mark a work report entry as complete",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := buildActionPath("complete", args)
		if err != nil {
			return err
		}
		return client.CallDaemonPost(os.Stdout, path, nil)
	},
}

func init() {
	rootCmd.AddCommand(completeCmd)

	completeCmd.Flags().StringVar(&completeTitle, "title", "", "Lookup: exact title of the record to complete")
	completeCmd.Flags().StringVar(&completeDate, "date", "", "Lookup: date of the record to complete (YYYY-MM-DD)")
}

// buildActionPath resolves the daemon API path for update/complete/cancel commands.
// When args contains an <id>, it returns /api/<action>/<id>.
// When args is empty, it requires --title and --date flags and returns /api/<action>/?title=...&date=...
func buildActionPath(action string, args []string) (string, error) {
	if len(args) > 0 && args[0] != "" {
		return fmt.Sprintf("/api/%s/%s", action, args[0]), nil
	}

	// No positional ID — require both --title and --date for lookup
	title := lookupTitle(action)
	date := lookupDate(action)

	if title == "" || date == "" {
		return "", writeExitError(agentsdk.ExitInvalidParams,
			fmt.Sprintf("provide <id> or both --title and --date for lookup"))
	}

	return fmt.Sprintf("/api/%s/?title=%s&date=%s", action,
		url.QueryEscape(title), url.QueryEscape(date)), nil
}

// lookupTitle returns the --title flag value for the given action command.
func lookupTitle(action string) string {
	switch action {
	case "update":
		return updateTitle
	case "complete":
		return completeTitle
	case "cancel":
		return cancelTitle
	default:
		return ""
	}
}

// lookupDate returns the --date flag value for the given action command.
func lookupDate(action string) string {
	switch action {
	case "update":
		return updateDate
	case "complete":
		return completeDate
	case "cancel":
		return cancelDate
	default:
		return ""
	}
}
