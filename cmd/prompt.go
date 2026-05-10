package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/digest"

	"github.com/spf13/cobra"
)

var (
	promptText string
	promptFile string
)

// promptCmd is the parent command for prompt management.
var promptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Manage prompt templates",
}

// promptListCmd lists all prompts with their current text and default status.
var promptListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all prompts",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		store := mustDigestStore()
		if store == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to create digest store")
		}

		prompts, err := store.ListPrompts()
		if err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to list prompts: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action":  "prompt_list",
			"prompts": prompts,
			"count":   len(prompts),
		})
		return nil
	},
}

// promptShowCmd shows the effective prompt text for a named prompt.
var promptShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show prompt text",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := mustDigestStore()
		if store == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to create digest store")
		}

		name := digest.PromptName(args[0])
		text, err := store.GetPrompt(name)
		if err != nil {
			if err == digest.ErrPromptNotFound {
				return writeJSONLError("prompt_not_found", fmt.Sprintf("prompt %q not found", args[0]))
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to get prompt: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action":      "prompt_show",
			"name":        name,
			"text":        text,
			"is_default":  store != nil,
		})
		return nil
	},
}

// promptSetCmd sets a custom prompt text for a named prompt.
var promptSetCmd = &cobra.Command{
	Use:   "set <name> --text <text>|--file <path>",
	Short: "Set prompt text",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := mustDigestStore()
		if store == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to create digest store")
		}

		var text string
		if promptText != "" {
			text = promptText
		} else if promptFile != "" {
			data, err := os.ReadFile(promptFile)
			if err != nil {
				return writeJSONLError("invalid_body", fmt.Sprintf("cannot read file %q: %v", promptFile, err))
			}
			text = string(data)
		}
		if text == "" {
			return writeJSONLError("invalid_params", "--text or --file is required")
		}

		name := digest.PromptName(args[0])
		if err := store.SetPrompt(name, text); err != nil {
			return writeJSONLError("storage_error", fmt.Sprintf("failed to set prompt: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action": "prompt_set",
			"name":   name,
		})
		return nil
	},
}

// promptResetCmd restores a built-in prompt to its default text.
var promptResetCmd = &cobra.Command{
	Use:   "reset <name>",
	Short: "Reset prompt to default",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store := mustDigestStore()
		if store == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to create digest store")
		}

		name := digest.PromptName(args[0])
		if err := store.ResetPrompt(name); err != nil {
			if err == digest.ErrPromptNotFound {
				return writeJSONLError("prompt_not_found", fmt.Sprintf("prompt %q not found (no default to reset to)", args[0]))
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to reset prompt: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action": "prompt_reset",
			"name":   name,
		})
		return nil
	},
}

var promptPreviewScope string

// promptPreviewCmd previews LLM prompt output using current data.
var promptPreviewCmd = &cobra.Command{
	Use:   "preview <name>",
	Short: "Preview LLM prompt output in terminal",
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

		name := digest.PromptName(args[0])

		// Verify the prompt exists
		_, err := dStore.GetPrompt(name)
		if err != nil {
			if err == digest.ErrPromptNotFound {
				return writeJSONLError("prompt_not_found", fmt.Sprintf("prompt %q not found", args[0]))
			}
			return writeJSONLError("storage_error", fmt.Sprintf("failed to get prompt: %v", err))
		}

		// Resolve scope
		scope := promptPreviewScope
		if scope == "" {
			scope = "today"
		}
		parsedScope, err := digest.ParseScope(scope)
		if err != nil {
			return writeJSONLError("invalid_scope", err.Error())
		}

		// Map prompt name to direction
		dir := digest.DirectionForPrompt(name)

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
			DigestID:  "prompt-preview",
			Scope:     parsedScope,
			Direction: dir,
			Loc:       cfg.Location(),
			Storage:   storageAdapter,
			LLM:       llmAdapter,
		})
		if err != nil {
			return writeJSONLError("llm_error", fmt.Sprintf("failed to generate preview: %v", err))
		}

		writeJSONLSuccess(map[string]interface{}{
			"action":       "prompt_preview",
			"prompt_name":  name,
			"scope":        scope,
			"preview":      result.Text,
			"record_count": result.RecordCount,
			"llm_status":   result.LLMStatus,
			"title":        result.Title,
		})
		return nil
	},
}

func init() {
	promptCmd.AddCommand(promptListCmd)
	promptCmd.AddCommand(promptShowCmd)
	promptCmd.AddCommand(promptSetCmd)
	promptCmd.AddCommand(promptResetCmd)
	promptCmd.AddCommand(promptPreviewCmd)

	promptSetCmd.Flags().StringVar(&promptText, "text", "", "Prompt text")
	promptSetCmd.Flags().StringVar(&promptFile, "file", "", "Path to file containing prompt text")
	promptPreviewCmd.Flags().StringVar(&promptPreviewScope, "scope", "", "Digest scope: today, yesterday, week, month, custom (default: today)")

	rootCmd.AddCommand(promptCmd)
}
