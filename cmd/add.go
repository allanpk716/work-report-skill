package cmd

import (
	"fmt"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/config"
	"wr/internal/llm"
	"wr/internal/logger"
	"wr/internal/models"
	"wr/internal/storage"

	"github.com/spf13/cobra"
)

var (
	addType           string
	addTitle          string
	addDate           string
	addTime           string
	addImage          string
	addText           string
	addDescription    string
	addTags           string
	addLocation       string
	addRelatedPerson  string
	addPriority       string
	addRemindBefore   string
	addRecurring      string
	addIdempotencyKey string
)

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new work report entry",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		// Parse tags
		var tags []string
		if addTags != "" {
			tags = splitTags(addTags)
		}

		// If image is provided and type is not specified, use vision LLM classification
		usedLLM := false
		if addImage != "" && addType == "" {
			result, err := classifyImageLocal(cfg, addImage, addText)
			if err != nil {
				return writeJSONLError("llm_error", fmt.Sprintf("LLM vision classification failed: %v", err))
			}

			// cancel_or_update is informational — don't create a record
			if result.Type == "cancel_or_update" {
				writeJSONLSuccess(map[string]interface{}{
					"action":         "cancel_or_update",
					"classification": result,
				})
				return nil
			}

			populateFromResult(result)
			logger.Infof("add: source=llm_vision type=%s title=%q image=%s", addType, addTitle, addImage)
			usedLLM = true
		} else if addText != "" && addType == "" {
			// Text classification — may return multiple results for multi-event input
			results, err := classifyTextLocal(cfg, addText)
			if err != nil {
				return writeJSONLError("llm_error", fmt.Sprintf("LLM classification failed: %v", err))
			}

			// Separate cancel_or_update from actionable results
			var actionable []llm.ClassifyResult
			var cancels []llm.ClassifyResult
			for _, r := range results {
				if r.Type == "cancel_or_update" {
					cancels = append(cancels, r)
				} else {
					actionable = append(actionable, r)
				}
			}

			// All results are cancel_or_update — return early
			if len(actionable) == 0 {
				classification := cancels
				if len(classification) == 1 {
					writeJSONLSuccess(map[string]interface{}{
						"action":         "cancel_or_update",
						"classification": classification[0],
					})
				} else {
					writeJSONLSuccess(map[string]interface{}{
						"action":         "cancel_or_update",
						"classification": classification,
					})
				}
				return nil
			}

			// Single actionable result — use existing single-record flow
			if len(actionable) == 1 {
				populateFromResult(&actionable[0])
				logger.Infof("add: source=llm type=%s title=%q", addType, addTitle)
				usedLLM = true
				// Fall through to existing single-record creation below
			} else {
				// Multiple actionable results — batch create records
				return handleBatchAddLocal(store, cfg, actionable, cancels)
			}
		}

		// Default date to today when not provided (covers both manual and LLM paths)
		if addDate == "" {
			addDate = todayInLocation(cfg)
			source := "default_today"
			if usedLLM {
				source = "llm_date_fallback"
			}
			logger.Infof("add: source=%s date=%s", source, addDate)
		}

		// Validate required fields
		if addType == "" {
			return writeJSONLError("invalid_type", "missing required field: type")
		}
		if !models.IsValidType(addType) {
			return writeJSONLError("invalid_type", fmt.Sprintf("invalid type: %q (must be meeting, task, reminder, or done_things)", addType))
		}
		if addTitle == "" {
			return writeJSONLError("invalid_body", "missing required field: title")
		}
		if addDate == "" {
			return writeJSONLError("invalid_body", "missing required field: date")
		}

		// Idempotency check
		if addIdempotencyKey != "" {
			existing, _, err := store.GetByIdempotencyKey(addIdempotencyKey)
			if err != nil {
				logger.Warnf("add: idempotency lookup error (degrading to normal add): key=%s err=%v", addIdempotencyKey, err)
			}
			if existing != nil {
				cf := models.GetCommonFields(existing)
				logger.Infof("add: short_id=%s source=idempotent_hit key=%s", cf.ShortID, addIdempotencyKey)
				writeJSONLSuccess(existing)
				return nil
			}
		}

		// Build and persist the record
		rec := buildRecord(addType, addTitle, addDate, addTime, addDescription,
			tags, addLocation, addRelatedPerson, addPriority, addRemindBefore, addRecurring, addIdempotencyKey)

		result, err := withLockRetryResult(func() (interface{}, error) {
			return store.AddRecord(rec)
		})
		if err != nil {
			logger.Errorf("add error: %v", err)
			return writeStorageError("failed to add record", err)
		}

		cf := models.GetCommonFields(result)
		source := "manual"
		if usedLLM {
			source = "llm"
		}
		logger.Infof("add: short_id=%s type=%s title=%q source=%s", cf.ShortID, cf.Type, cf.Title, source)

		writeJSONLSuccess(result)
		return nil
	},
}

// populateFromResult fills empty flag variables from the LLM classification result.
// Fields already set (e.g. by the user via flags) are preserved.
func populateFromResult(result *llm.ClassifyResult) {
	if addType == "" {
		addType = result.Type
	}
	if addTitle == "" {
		addTitle = result.Title
	}
	if addDate == "" {
		addDate = result.Date
	}
	if addTime == "" {
		addTime = result.Time
	}
	if addDescription == "" {
		addDescription = result.Description
	}
	if addLocation == "" {
		addLocation = result.Location
	}
	if addRelatedPerson == "" {
		addRelatedPerson = result.RelatedPerson
	}
	if addPriority == "" {
		addPriority = result.Priority
	}
	if addRemindBefore == "" {
		addRemindBefore = result.RemindBefore
	}
	if addRecurring == "" {
		addRecurring = result.Recurring
	}
}

// classifyTextLocal performs LLM batch classification on the given text using
// the config's text LLM settings. Returns the classification results or an error.
func classifyTextLocal(cfg *config.Config, text string) ([]llm.ClassifyResult, error) {
	if cfg.LLM.Text.APIKey == "" {
		return nil, fmt.Errorf("LLM text classification is not configured (missing api_key in llm.text)")
	}

	loc := cfg.Location()
	today := time.Now().In(loc)

	client := llm.NewClient(cfg.LLM.Text.APIBase, cfg.LLM.Text.APIKey, cfg.LLM.Text.Model,
		time.Duration(cfg.LLM.Text.Timeout)*time.Second)
	results, err := llm.ClassifyBatch(client, text, today, loc)
	if err != nil {
		logger.Errorf("classify error: api_base=%s model=%s error=%v",
			cfg.LLM.Text.APIBase, cfg.LLM.Text.Model, err)
		return nil, err
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("LLM classification returned no results")
	}

	return results, nil
}

// classifyImageLocal performs vision LLM classification on the given image path.
func classifyImageLocal(cfg *config.Config, imagePath string, textContext string) (*llm.ClassifyResult, error) {
	if cfg.LLM.Vision.APIKey == "" {
		return nil, fmt.Errorf("LLM vision classification is not configured (missing api_key in llm.vision)")
	}

	loc := cfg.Location()
	today := time.Now().In(loc)

	client := llm.NewClient(cfg.LLM.Vision.APIBase, cfg.LLM.Vision.APIKey, cfg.LLM.Vision.Model,
		time.Duration(cfg.LLM.Vision.Timeout)*time.Second)
	result, err := llm.ClassifyImage(client, imagePath, textContext, today, loc)
	if err != nil {
		logger.Errorf("classify_image error: api_base=%s model=%s error=%v image=%s",
			cfg.LLM.Vision.APIBase, cfg.LLM.Vision.Model, err, imagePath)
		return nil, err
	}

	logger.Infof("classify_image ok: type=%s model=%s image=%s",
		result.Type, cfg.LLM.Vision.Model, imagePath)

	return result, nil
}

// handleBatchAddLocal creates multiple records from LLM classification results.
// Each actionable result becomes an independent record. cancel_or_update results
// are reported in a summary line appended after all records.
func handleBatchAddLocal(store *storage.Storage, cfg *config.Config, actionable []llm.ClassifyResult, cancels []llm.ClassifyResult) error {
	created := 0

	for _, result := range actionable {
		recType := result.Type
		title := result.Title
		date := result.Date
		if date == "" {
			date = todayInLocation(cfg)
		}

		// Validate required fields
		if recType == "" || !models.IsValidType(recType) {
			logger.Warnf("add batch: skipping result with invalid type %q", recType)
			continue
		}
		if title == "" {
			logger.Warnf("add batch: skipping result with empty title")
			continue
		}

		rec := buildRecord(recType, title, date, result.Time, result.Description,
			nil, result.Location, result.RelatedPerson, result.Priority,
			result.RemindBefore, result.Recurring, "")

		persisted, err := withLockRetryResult(func() (interface{}, error) {
			return store.AddRecord(rec)
		})
		if err != nil {
			logger.Errorf("add batch: storage error: %v", err)
			continue
		}

		cf := models.GetCommonFields(persisted)
		logger.Infof("add batch: short_id=%s type=%s title=%q source=llm_batch", cf.ShortID, cf.Type, cf.Title)
		writeJSONLSuccess(persisted)
		created++
	}

	// Append summary if there were cancel_or_update entries
	if len(cancels) > 0 {
		writeJSONLSuccess(map[string]interface{}{
			"action":          "batch_add",
			"created":         created,
			"skipped":         len(cancels),
			"classifications": cancels,
		})
	} else {
		writeJSONLSuccess(map[string]interface{}{
			"action":  "batch_add",
			"created": created,
		})
	}

	logger.Infof("add batch complete: created=%d cancelled=%d", created, len(cancels))
	return nil
}

// splitTags splits a comma-separated tag string into a slice.
func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	result := make([]string, 0)
	for _, tag := range splitByComma(s) {
		if tag != "" {
			result = append(result, tag)
		}
	}
	return result
}

func splitByComma(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func init() {
	addCmd.Flags().StringVar(&addType, "type", "", "Entry type (meeting, task, reminder, done_things)")
	addCmd.Flags().StringVar(&addTitle, "title", "", "Entry title")
	addCmd.Flags().StringVar(&addDate, "date", "", "Date (YYYY-MM-DD, defaults to today if omitted)")
	addCmd.Flags().StringVar(&addTime, "time", "", "Time (HH:MM)")
	addCmd.Flags().StringVar(&addText, "text", "", "Natural language text for LLM classification (type/title/date become optional)")
	addCmd.Flags().StringVar(&addImage, "image", "", "Image path for LLM vision classification (type/title/date become optional)")
	addCmd.Flags().StringVar(&addDescription, "description", "", "Description")
	addCmd.Flags().StringVar(&addTags, "tags", "", "Comma-separated tags")
	addCmd.Flags().StringVar(&addLocation, "location", "", "Location")
	addCmd.Flags().StringVar(&addRelatedPerson, "related-person", "", "Related person")
	addCmd.Flags().StringVar(&addPriority, "priority", "", "Priority (normal, high, medium)")
	addCmd.Flags().StringVar(&addRemindBefore, "remind-before", "", "Remind before (e.g. 15m, 30m)")
	addCmd.Flags().StringVar(&addRecurring, "recurring", "", "Recurring pattern (e.g. daily, weekly)")
	addCmd.Flags().StringVar(&addIdempotencyKey, "idempotency-key", "", "Idempotency key: retry with same key returns existing record")

	rootCmd.AddCommand(addCmd)
}
