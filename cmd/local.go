package cmd

import (
	"fmt"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/config"
	"wr/internal/logger"
	"wr/internal/models"
	"wr/internal/storage"
)

// loadConfig loads config from the default path (~/.work-report/config.json).
// Returns the loaded config or writes an error envelope and returns nil.
func loadConfig() *config.Config {
	cfg, err := config.LoadDefault()
	if err != nil {
		logger.Errorf("config load failed: %v", err)
		app.JSONL().ErrorWithCode("storage_error", fmt.Sprintf("failed to load config: %v", err))
		return nil
	}
	return cfg
}

// mustStorage creates a Storage instance rooted at cfg.DataDir.
// Exits with an error envelope on failure.
func mustStorage(cfg *config.Config) *storage.Storage {
	if cfg == nil {
		app.JSONL().ErrorWithCode("storage_error", "config is nil")
		return nil
	}
	dir := cfg.DataDir
	if dir == "" {
		dir, _ = config.DefaultDataDir()
	}
	return storage.New(dir)
}

// writeJSONLError writes a JSONL error envelope with the given error code and
// message, and returns an ExitError. The exit code is resolved from the
// registered error code registry.
func writeJSONLError(code string, msg string) error {
	app.JSONL().ErrorWithCode(code, msg)
	exitCode := agentsdk.ExitFatalError
	if reg := app.Registry(); reg != nil {
		exitCode = reg.ToExitCode(code)
	}
	return &agentsdk.ExitError{Code: exitCode, Err: fmt.Errorf("%s", msg)}
}

// writeJSONLErrorWithExit writes a JSONL error envelope with the given error
// code and message, and returns an ExitError with the explicitly provided
// exit code (bypassing registry lookup).
func writeJSONLErrorWithExit(exitCode int, code string, msg string) error {
	app.JSONL().ErrorWithCode(code, msg)
	return &agentsdk.ExitError{Code: exitCode, Err: fmt.Errorf("%s", msg)}
}

// writeExitErrorWithCode writes a JSONL error envelope with a specific error code
// and returns an ExitError. This is used for CLI-side validation errors that should
// carry the same error_code the daemon would use.
func writeExitErrorWithCode(exitCode int, errorCode string, msg string) error {
	app.JSONL().ErrorWithCode(errorCode, msg)
	return &agentsdk.ExitError{Code: exitCode, Err: fmt.Errorf("%s", msg)}
}

// writeJSONLSuccess writes a JSONL success envelope with the given data.
func writeJSONLSuccess(data interface{}) {
	app.JSONL().Success(data)
}

// resolveIDOrLookup resolves a record by short ID, or falls back to
// title+date lookup via storage.FindByContent. Returns the resolved short ID
// or an error written as a JSONL envelope.
func resolveIDOrLookup(store *storage.Storage, id string, title string, date string) (string, error) {
	// If ID is provided directly, return it
	if id != "" {
		return id, nil
	}

	// Fall back to title+date lookup
	if title == "" || date == "" {
		return "", fmt.Errorf("missing entry id or lookup params (title + date required)")
	}

	matches, err := store.FindByContent(title, date)
	if err != nil {
		return "", err
	}

	if len(matches) > 1 {
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.ShortID
		}
		return "", fmt.Errorf("found %d records matching title=%q date=%q: %s",
			len(matches), title, date, joinStrings(ids, ", "))
	}

	return matches[0].ShortID, nil
}

// joinStrings joins a slice of strings with a separator.
func joinStrings(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}

// todayInLocation returns today's date string in YYYY-MM-DD format using the
// configured timezone, or UTC if the config is nil.
func todayInLocation(cfg *config.Config) string {
	loc := time.UTC
	if cfg != nil {
		loc = cfg.Location()
	}
	return time.Now().In(loc).Format("2006-01-02")
}

// nowInLocation returns the current time in the configured timezone.
func nowInLocation(cfg *config.Config) time.Time {
	loc := time.UTC
	if cfg != nil {
		loc = cfg.Location()
	}
	return time.Now().In(loc)
}

// buildRecord creates the correct typed record struct from the given fields.
// This is extracted from daemon/handler.go so CLI commands can construct
// records without going through the daemon HTTP API.
func buildRecord(recType, title, date, tm, description string,
	tags []string, location, relatedPerson, priority, remindBefore, recurring, idempotencyKey string) interface{} {

	cf := models.CommonFields{
		Type:           models.RecordType(recType),
		Title:          title,
		Date:           date,
		Time:           tm,
		Description:    description,
		Tags:           tags,
		Location:       location,
		RelatedPerson:  relatedPerson,
		Priority:       priority,
		RemindBefore:   remindBefore,
		Status:         models.StatusActive,
		IdempotencyKey: idempotencyKey,
	}

	switch models.RecordType(recType) {
	case models.TypeMeeting:
		return &models.MeetingRecord{CommonFields: cf}
	case models.TypeTask:
		return &models.TaskRecord{CommonFields: cf}
	case models.TypeReminder:
		return &models.ReminderRecord{
			CommonFields: cf,
			Recurring:    recurring,
		}
	case models.TypeDoneThings:
		return &models.DoneThingsRecord{CommonFields: cf}
	default:
		return &models.DoneThingsRecord{CommonFields: cf}
	}
}
