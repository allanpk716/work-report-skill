package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/logger"
	"wr/internal/models"

	"github.com/spf13/cobra"
)

var importFilePath string

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Bulk import work report entries from a JSON file",
	RunE: func(cmd *cobra.Command, args []string) error {
		if importFilePath == "" {
			return writeJSONLError("invalid_params", "--file is required")
		}

		cfg := loadConfig()
		if cfg == nil {
			return writeJSONLErrorWithExit(agentsdk.ExitFatalError, "storage_error", "failed to load config")
		}
		store := mustStorage(cfg)

		data, err := os.ReadFile(importFilePath)
		if err != nil {
			return writeJSONLError("invalid_body", fmt.Sprintf("cannot read file %q: %v", importFilePath, err))
		}

		// Parse and normalize the import payload
		records, err := parseImportRecords(data)
		if err != nil {
			return writeJSONLError("invalid_body", fmt.Sprintf("invalid JSON in %q: %v", importFilePath, err))
		}

		if len(records) == 0 {
			return writeJSONLError("invalid_body", "no records found in import file")
		}

		// Phase 1: validate all records before persisting any
		for i, rawRec := range records {
			if err := validateImportRecord(rawRec, i); err != nil {
				return writeJSONLError("import_record", err.Error())
			}
		}

		// Phase 2: persist each record
		imported := 0
		var importErrors []map[string]interface{}

		for _, rawRec := range records {
			rec := buildRecordFromMap(rawRec)

			result, err := withLockRetryResult(func() (interface{}, error) {
				return store.AddRecord(rec)
			})
			if err != nil {
				logger.Warnf("import: failed to add record: %v", err)
				importErrors = append(importErrors, map[string]interface{}{
					"record": rawRec,
					"error":  err.Error(),
				})
				continue
			}

			cf := models.GetCommonFields(result)
			logger.Infof("import: short_id=%s type=%s title=%q", cf.ShortID, cf.Type, cf.Title)
			writeJSONLSuccess(result)
			imported++
		}

		// Write summary
		summary := map[string]interface{}{
			"action":        "import",
			"imported":      imported,
			"failed":        len(importErrors),
			"total":         len(records),
		}
		if len(importErrors) > 0 {
			summary["errors"] = importErrors
		}
		writeJSONLSuccess(summary)

		logger.Infof("import complete: imported=%d failed=%d total=%d", imported, len(importErrors), len(records))
		return nil
	},
}

// parseImportRecords reads JSON data and returns a slice of raw record maps.
// Accepts either a top-level array or an object with a "records" key.
func parseImportRecords(data []byte) ([]map[string]interface{}, error) {
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	switch v := raw.(type) {
	case []interface{}:
		records := make([]map[string]interface{}, 0, len(v))
		for i, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("item %d is not a JSON object", i)
			}
			records = append(records, m)
		}
		return records, nil
	case map[string]interface{}:
		arr, ok := v["records"]
		if !ok {
			return nil, fmt.Errorf("object must contain a \"records\" key")
		}
		items, ok := arr.([]interface{})
		if !ok {
			return nil, fmt.Errorf("\"records\" must be an array")
		}
		records := make([]map[string]interface{}, 0, len(items))
		for i, item := range items {
			m, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("records[%d] is not a JSON object", i)
			}
			records = append(records, m)
		}
		return records, nil
	default:
		return nil, fmt.Errorf("expected JSON array or object, got %T", raw)
	}
}

// validateImportRecord checks that a raw record map has the minimum required fields.
func validateImportRecord(rec map[string]interface{}, index int) error {
	recType, _ := rec["type"].(string)
	if recType == "" {
		return fmt.Errorf("record %d: missing required field \"type\"", index)
	}
	if !models.IsValidType(recType) {
		return fmt.Errorf("record %d: invalid type %q (must be meeting, task, reminder, or log)", index, recType)
	}
	title, _ := rec["title"].(string)
	if title == "" {
		return fmt.Errorf("record %d: missing required field \"title\"", index)
	}
	return nil
}

// buildRecordFromMap constructs a typed record from a raw map, extracting
// known fields and passing them to buildRecord in cmd/local.go.
func buildRecordFromMap(rec map[string]interface{}) interface{} {
	recType := getStringField(rec, "type")
	title := getStringField(rec, "title")
	date := getStringField(rec, "date")
	tm := getStringField(rec, "time")
	description := getStringField(rec, "description")
	location := getStringField(rec, "location")
	relatedPerson := getStringField(rec, "related_person")
	priority := getStringField(rec, "priority")
	remindBefore := getStringField(rec, "remind_before")
	recurring := getStringField(rec, "recurring")
	idempotencyKey := getStringField(rec, "idempotency_key")

	var tags []string
	if v, ok := rec["tags"]; ok {
		switch arr := v.(type) {
		case []interface{}:
			for _, t := range arr {
				if s, ok := t.(string); ok && s != "" {
					tags = append(tags, s)
				}
			}
		case []string:
			tags = arr
		}
	}

	notifyPriority := getStringField(rec, "notification_priority")

	return buildRecord(recType, title, date, tm, description,
		tags, location, relatedPerson, priority, remindBefore, recurring, idempotencyKey, notifyPriority)
}

// getStringField extracts a string field from a map, returning "" if missing or wrong type.
func getStringField(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func init() {
	importCmd.Flags().StringVar(&importFilePath, "file", "", "Path to JSON file containing records to import")

	rootCmd.AddCommand(importCmd)
}
