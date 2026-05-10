package cmd

import (
	"context"
	"fmt"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/digest"
	"wr/internal/logger"
	"wr/internal/pushover"

	"github.com/spf13/cobra"
)

var (
	digestSchedule  string
	digestScope     string
	digestDirection string
	digestPushFlag  bool
)

// digestCmd is the parent command for digest configuration management.
var digestCmd = &cobra.Command{
	Use:   "digest",
	Short: "Manage digest configurations",
}

// mustDigestStore creates a DigestStore from the default path.
func mustDigestStore() *digest.DigestStore {
	path, err := digest.DefaultStorePath()
	if err != nil {
		app.JSONL().ErrorWithCode("storage_error", fmt.Sprintf("failed to resolve digest store path: %v", err))
		return nil
	}
	return digest.NewStore(path)
}

// digestAddCmd creates a new digest configuration.
var digestAddCmd = &cobra.Command{
	Use:   "add --schedule <cron> --scope <scope> --direction <direction>",
	Short: "Create a new digest configuration",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		store := mustDigestStore()
		if store == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to create digest store")
		}

		// Validate scope
		scope, err := digest.ParseScope(digestScope)
		if err != nil {
			return writeJSONLError("invalid_scope", err.Error())
		}

		// Validate direction
		dir := digest.Direction(digestDirection)
		if dir != digest.DirectionAgenda && dir != digest.DirectionSummary {
			return writeJSONLError("invalid_direction", fmt.Sprintf("direction must be %q or %q", digest.DirectionAgenda, digest.DirectionSummary))
		}

		cfg, err := store.Add(digest.DigestConfig{
			Schedule:  digestSchedule,
			Scope:     scope,
			Direction: dir,
		})
		if err != nil {
			return writeJSONLError("invalid_schedule", err.Error())
		}

		writeJSONLSuccess(map[string]interface{}{
			"action": "digest_add",
			"digest": cfg,
		})
		return nil
	},
}

// digestListCmd lists all digest configurations.
var digestListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all digest configurations",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		store := mustDigestStore()
		if store == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to create digest store")
		}

		list, err := store.List()
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to list digests: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action":  "digest_list",
			"digests": list,
			"count":   len(list),
		})
		return nil
	},
}

// digestRemoveCmd removes a digest configuration by ID.
var digestRemoveCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove a digest configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := mustDigestStore()
		if store == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to create digest store")
		}

		if err := store.Remove(args[0]); err != nil {
			if err == digest.ErrNotFound {
				return writeJSONLError("digest_not_found", fmt.Sprintf("digest %q not found", args[0]))
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to remove digest: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action":    "digest_remove",
			"digest_id": args[0],
		})
		return nil
	},
}

// digestEnableCmd enables a digest configuration by ID.
var digestEnableCmd = &cobra.Command{
	Use:   "enable <id>",
	Short: "Enable a digest configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := mustDigestStore()
		if store == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to create digest store")
		}

		if err := store.Enable(args[0]); err != nil {
			if err == digest.ErrNotFound {
				return writeJSONLError("digest_not_found", fmt.Sprintf("digest %q not found", args[0]))
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to enable digest: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action":    "digest_enable",
			"digest_id": args[0],
		})
		return nil
	},
}

// digestDisableCmd disables a digest configuration by ID.
var digestDisableCmd = &cobra.Command{
	Use:   "disable <id>",
	Short: "Disable a digest configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := mustDigestStore()
		if store == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to create digest store")
		}

		if err := store.Disable(args[0]); err != nil {
			if err == digest.ErrNotFound {
				return writeJSONLError("digest_not_found", fmt.Sprintf("digest %q not found", args[0]))
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to disable digest: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action":    "digest_disable",
			"digest_id": args[0],
		})
		return nil
	},
}

// digestPreviewCmd previews an LLM-generated digest summary for a configuration ID.
var digestPreviewCmd = &cobra.Command{
	Use:   "preview <id>",
	Short: "Preview LLM digest summary in terminal",
	Long:  `Generate a digest summary using the stored configuration. If --push is set, also push the summary via Pushover.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)
		dStore := mustDigestStore()
		if dStore == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to create digest store")
		}

		// Look up the digest configuration
		dCfg, err := dStore.Get(args[0])
		if err != nil {
			if err == digest.ErrNotFound {
				return writeJSONLError("digest_not_found", fmt.Sprintf("digest %q not found", args[0]))
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to get digest: %v", err))
		}

		// Build adapters
		storageAdapter := digest.NewStorageAdapter(store, cfg.Location())
		if storageAdapter == nil {
			return writeJSONLError("storage_error", "failed to create storage adapter")
		}

		llmAdapter := digest.NewLLMAdapter(
			cfg.LLM.Text.APIBase,
			cfg.LLM.Text.APIKey,
			cfg.LLM.Text.Model,
			time.Duration(cfg.LLM.Text.Timeout)*time.Second,
		)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := digest.GenerateSummary(ctx, digest.SummarizeInput{
			DigestID:  dCfg.ID,
			Scope:     dCfg.Scope,
			Direction: dCfg.Direction,
			Loc:       cfg.Location(),
			Storage:   storageAdapter,
			LLM:       llmAdapter,
		})
		if err != nil {
			return writeJSONLError("llm_error", fmt.Sprintf("failed to generate summary: %v", err))
		}

		output := map[string]interface{}{
			"action":      "digest_preview",
			"digest_id":   dCfg.ID,
			"summary":     result.Text,
			"record_count": result.RecordCount,
			"llm_status":  result.LLMStatus,
			"title":       result.Title,
		}

		// Optionally push via Pushover
		if digestPushFlag {
			if cfg.Pushover.APIToken == "" || cfg.Pushover.UserKey == "" {
				return writeJSONLError("pushover_not_configured", "Pushover is not configured (set pushover.api_token and pushover.user_key)")
			}

			pushErr := pushover.Send(ctx, pushover.Config{
				APIToken: cfg.Pushover.APIToken,
				UserKey:  cfg.Pushover.UserKey,
			}, result.Text, result.Title, 0)
			if pushErr != nil {
				logger.Errorf("digest preview push failed: %v", pushErr)
				return writeJSONLError("push_error", fmt.Sprintf("Pushover send failed: %v", pushErr))
			}
			output["pushed"] = true
			logger.Infof("digest preview: pushed digest_id=%s", dCfg.ID)
		}

		writeJSONLSuccess(output)
		return nil
	},
}

func init() {
	digestCmd.AddCommand(digestAddCmd)
	digestCmd.AddCommand(digestListCmd)
	digestCmd.AddCommand(digestRemoveCmd)
	digestCmd.AddCommand(digestEnableCmd)
	digestCmd.AddCommand(digestDisableCmd)
	digestCmd.AddCommand(digestPreviewCmd)

	digestAddCmd.Flags().StringVar(&digestSchedule, "schedule", "", "Cron expression (e.g. '0 8 * * *')")
	digestAddCmd.Flags().StringVar(&digestScope, "scope", "", "Digest scope: today, yesterday, week, month, custom")
	digestAddCmd.Flags().StringVar(&digestDirection, "direction", "", "Output direction: agenda or summary")
	digestPreviewCmd.Flags().BoolVar(&digestPushFlag, "push", false, "Push the summary via Pushover after generating")

	digestAddCmd.MarkFlagRequired("schedule")
	digestAddCmd.MarkFlagRequired("scope")
	digestAddCmd.MarkFlagRequired("direction")

	rootCmd.AddCommand(digestCmd)
}
