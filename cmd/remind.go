package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/logger"
	"wr/internal/pushover"
	"wr/internal/remind"

	"github.com/spf13/cobra"
)

var remindCmd = &cobra.Command{
	Use:   "remind",
	Short: "Manage reminders",
}

// --- remind due ---

var remindDueWindow string
var remindDueIncludeStale bool

var remindDueCmd = &cobra.Command{
	Use:   "due",
	Short: "List all due reminders",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		window, err := parseWindowDuration(remindDueWindow)
		if err != nil {
			return writeJSONLError("invalid_params", fmt.Sprintf("invalid --window value: %v", err))
		}

		now := nowInLocation(cfg)
		due, err := remind.ListDue(store, now, window, remindDueIncludeStale)
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to list due reminders: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action":  "remind_due",
			"count":   len(due),
			"entries": due,
		})
		return nil
	},
}

// --- remind push ---

var (
	remindPushDueFlag        bool
	remindPushWindow         string
	remindPushIncludeStale   bool
)

var remindPushCmd = &cobra.Command{
	Use:   "push [<short_id>]",
	Short: "Push reminders via Pushover",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		if cfg.Pushover.APIToken == "" || cfg.Pushover.UserKey == "" {
			return writeJSONLError("pushover_not_configured", "Pushover is not configured (set pushover.api_token and pushover.user_key)")
		}

		pCfg := pushover.Config{
			APIToken: cfg.Pushover.APIToken,
			UserKey:  cfg.Pushover.UserKey,
		}

		// Mode 1: --due flag → push all due reminders
		if remindPushDueFlag {
			window, err := parseWindowDuration(remindPushWindow)
			if err != nil {
				return writeJSONLError("invalid_params", fmt.Sprintf("invalid --window value: %v", err))
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			now := nowInLocation(cfg)
			result, err := remind.PushDue(ctx, store, pCfg, now, window, remindPushIncludeStale)
			if err != nil {
				logger.Errorf("remind push due failed: %v", err)
				return writeJSONLError("storage_error", fmt.Sprintf("failed to push due reminders: %v", err))
			}

			writeJSONLSuccess(map[string]interface{}{
				"action": "remind_push_due",
				"pushed": result.Pushed,
				"failed": result.Failed,
				"total":  len(result.Pushed) + len(result.Failed),
			})
			return nil
		}

		// Mode 2: positional short_id → push single reminder
		if len(args) == 0 {
			return writeJSONLError("invalid_params", "either --due or a <short_id> is required")
		}

		shortID := args[0]

		// Check if record exists before pushing
		_, _, err := store.GetByID(shortID)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return writeJSONLError("record_not_found", fmt.Sprintf("record %q not found", shortID))
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to find record: %v", err))
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		item, err := remind.PushSingle(ctx, store, pCfg, shortID)
		if err != nil {
			logger.Errorf("remind push single failed: %v", err)
			return writeJSONLError("push_error", fmt.Sprintf("failed to push reminder %s: %v", shortID, err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action": "remind_push_single",
			"pushed": []interface{}{item},
		})
		return nil
	},
}

// parseWindowDuration parses a duration string like "30m", "1h", "0" into time.Duration.
// Empty or "0" returns 0 (no window).
func parseWindowDuration(s string) (time.Duration, error) {
	if s == "" || s == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	return d, nil
}

// ResetRemindFlags resets all remind command flags to their defaults.
// For test isolation only.
func ResetRemindFlags() {
	remindDueWindow = ""
	remindDueIncludeStale = false
	remindPushDueFlag = false
	remindPushWindow = ""
	remindPushIncludeStale = false
}

func init() {
	remindDueCmd.Flags().StringVar(&remindDueWindow, "window", "0", "Look-ahead window (e.g. \"30m\", \"1h\"); 0 = no window")
	remindDueCmd.Flags().BoolVar(&remindDueIncludeStale, "include-stale", false, "Include stale reminders (>24h overdue)")

	remindPushCmd.Flags().BoolVar(&remindPushDueFlag, "due", false, "Push all due reminders")
	remindPushCmd.Flags().StringVar(&remindPushWindow, "window", "0", "Look-ahead window (e.g. \"30m\", \"1h\"); 0 = no window")
	remindPushCmd.Flags().BoolVar(&remindPushIncludeStale, "include-stale", false, "Include stale reminders (>24h overdue)")

	remindCmd.AddCommand(remindDueCmd)
	remindCmd.AddCommand(remindPushCmd)

	rootCmd.AddCommand(remindCmd)
}
