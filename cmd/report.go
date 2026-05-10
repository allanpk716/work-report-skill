package cmd

import (
	"context"
	"fmt"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/config"
	"wr/internal/logger"
	"wr/internal/pushover"
	"wr/internal/report"

	"github.com/spf13/cobra"
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate work reports",
}

// --- report today ---

var reportTodayCmd = &cobra.Command{
	Use:   "today",
	Short: "Generate today's work report",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		rpt, err := report.GenerateToday(store, cfg.Location())
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to generate report: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action": "report_today",
			"report": rpt,
		})
		return nil
	},
}

// --- report date ---

var reportDateCmd = &cobra.Command{
	Use:   "date <YYYY-MM-DD>",
	Short: "Generate report for a specific date",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		date := args[0]
		rpt, err := report.Generate(store, date)
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to generate report: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action": "report_date",
			"report": rpt,
		})
		return nil
	},
}

// --- report week ---

var reportWeekCmd = &cobra.Command{
	Use:   "week",
	Short: "Generate report for the current week (Mon–Sun)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		rpt, err := report.GenerateWeek(store, cfg.Location())
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to generate week report: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action": "report_week",
			"report": rpt,
		})
		return nil
	},
}

// --- report range ---

var reportRangeFrom string
var reportRangeTo string

var reportRangeCmd = &cobra.Command{
	Use:   "range",
	Short: "Generate report for a date range",
	RunE: func(cmd *cobra.Command, args []string) error {
		if reportRangeFrom == "" || reportRangeTo == "" {
			return writeJSONLError("invalid_params", "both --from and --to are required")
		}

		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		rpt, err := report.GenerateRange(store, reportRangeFrom, reportRangeTo, cfg.Location())
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to generate range report: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action": "report_range",
			"report": rpt,
		})
		return nil
	},
}

// --- report push (sub-command group) ---

var reportPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Generate and push work report via Pushover",
}

// pushReport generates a report and sends it via Pushover.
// This is the shared logic for all push sub-commands.
func pushReport(cfg *config.Config, rptType string, markdown string) error {
	if cfg.Pushover.APIToken == "" || cfg.Pushover.UserKey == "" {
		return writeJSONLError("pushover_not_configured", "Pushover is not configured (set pushover.api_token and pushover.user_key)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	title := fmt.Sprintf("工作日报 - %s", rptType)
	err := pushover.Send(ctx, pushover.Config{
		APIToken: cfg.Pushover.APIToken,
		UserKey:  cfg.Pushover.UserKey,
	}, markdown, title, 0)
	if err != nil {
		if err == pushover.ErrNotConfigured {
			return writeJSONLError("pushover_not_configured", err.Error())
		}
		logger.Errorf("pushover send failed: %v", err)
		return writeJSONLError("push_error", fmt.Sprintf("Pushover send failed: %v", err))
	}

	logger.Infof("report push: type=%s", rptType)
	return nil
}

// --- report push today ---

var reportPushTodayCmd = &cobra.Command{
	Use:   "today",
	Short: "Push today's work report via Pushover",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		rpt, err := report.GenerateToday(store, cfg.Location())
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to generate report: %v", err))
		}

		if err := pushReport(cfg, rpt.Date, rpt.Markdown); err != nil {
			return err
		}

		writeJSONLSuccess(map[string]interface{}{
			"action": "report_push_today",
			"report": rpt,
		})
		return nil
	},
}

// --- report push date ---

var reportPushDateCmd = &cobra.Command{
	Use:   "date <YYYY-MM-DD>",
	Short: "Push report for a specific date via Pushover",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		date := args[0]
		rpt, err := report.Generate(store, date)
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to generate report: %v", err))
		}

		if err := pushReport(cfg, date, rpt.Markdown); err != nil {
			return err
		}

		writeJSONLSuccess(map[string]interface{}{
			"action": "report_push_date",
			"report": rpt,
		})
		return nil
	},
}

// --- report push week ---

var reportPushWeekCmd = &cobra.Command{
	Use:   "week",
	Short: "Push the current week's report via Pushover",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		rpt, err := report.GenerateWeek(store, cfg.Location())
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to generate week report: %v", err))
		}

		if err := pushReport(cfg, fmt.Sprintf("%s ~ %s", rpt.DateFrom, rpt.DateTo), rpt.Markdown); err != nil {
			return err
		}

		writeJSONLSuccess(map[string]interface{}{
			"action": "report_push_week",
			"report": rpt,
		})
		return nil
	},
}

// --- report push range ---

var reportPushRangeFrom string
var reportPushRangeTo string

var reportPushRangeCmd = &cobra.Command{
	Use:   "range",
	Short: "Push a date range report via Pushover",
	RunE: func(cmd *cobra.Command, args []string) error {
		if reportPushRangeFrom == "" || reportPushRangeTo == "" {
			return writeJSONLError("invalid_params", "both --from and --to are required")
		}

		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		rpt, err := report.GenerateRange(store, reportPushRangeFrom, reportPushRangeTo, cfg.Location())
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to generate range report: %v", err))
		}

		if err := pushReport(cfg, fmt.Sprintf("%s ~ %s", rpt.DateFrom, rpt.DateTo), rpt.Markdown); err != nil {
			return err
		}

		writeJSONLSuccess(map[string]interface{}{
			"action": "report_push_range",
			"report": rpt,
		})
		return nil
	},
}

func init() {
	reportRangeCmd.Flags().StringVar(&reportRangeFrom, "from", "", "Start date (YYYY-MM-DD, required)")
	reportRangeCmd.Flags().StringVar(&reportRangeTo, "to", "", "End date (YYYY-MM-DD, required)")

	reportPushRangeCmd.Flags().StringVar(&reportPushRangeFrom, "from", "", "Start date (YYYY-MM-DD, required)")
	reportPushRangeCmd.Flags().StringVar(&reportPushRangeTo, "to", "", "End date (YYYY-MM-DD, required)")

	reportPushCmd.AddCommand(reportPushTodayCmd)
	reportPushCmd.AddCommand(reportPushDateCmd)
	reportPushCmd.AddCommand(reportPushWeekCmd)
	reportPushCmd.AddCommand(reportPushRangeCmd)

	reportCmd.AddCommand(reportTodayCmd)
	reportCmd.AddCommand(reportDateCmd)
	reportCmd.AddCommand(reportWeekCmd)
	reportCmd.AddCommand(reportRangeCmd)
	reportCmd.AddCommand(reportPushCmd)

	rootCmd.AddCommand(reportCmd)
}
