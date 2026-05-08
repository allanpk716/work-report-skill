package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/config"
	"wr/internal/digest"
	"wr/internal/llm"
	"wr/internal/logger"
	"wr/internal/models"
	"wr/internal/pushover"
	"wr/internal/report"
	"wr/internal/storage"
)

const Version = "0.1.0"

// daemonWriter creates an SDK Writer that writes JSONL envelopes to the HTTP response.
// Content-Type and HTTP 200 are set before any body write, matching the prior writeEnvelope contract.
func daemonWriter(w http.ResponseWriter) *agentsdk.Writer {
	w.Header().Set("Content-Type", "application/jsonl")
	w.WriteHeader(http.StatusOK)
	return agentsdk.NewWriter(w, "wr")
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}
	daemonWriter(w).Success(map[string]interface{}{
		"version": Version,
	})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}
	logger.WithField("source", "http").Infof("stop requested")
	daemonWriter(w).Success(map[string]interface{}{"message": "daemon shutting down"})
	go s.shutdown()
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	// Daemon info
	daemonInfo := map[string]interface{}{
		"version": Version,
		"status":  "running",
		"pid":     0,
		"port":    s.port,
	}

	// Read PID and compute uptime from state file
	dir, err := DefaultStateDir()
	if err == nil {
		state, err := ReadState(dir)
		if err == nil {
			daemonInfo["pid"] = state.PID
		}
	}

	// Config completeness
	configInfo := s.buildConfigDiagnostics()

	// Date/time fields using configured timezone
	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}
	now := time.Now().In(loc)

	dateTimeInfo := map[string]interface{}{
		"current_date":     now.Format("2006-01-02"),
		"current_time":     now.Format("15:04:05"),
		"current_datetime": now.Format(time.RFC3339),
		"timezone":         loc.String(),
		"weekday":          now.Weekday().String(),
	}

	// Scheduler info
	response := map[string]interface{}{
		"daemon":    daemonInfo,
		"config":    configInfo,
		"datetime":  dateTimeInfo,
	}

	if s.scheduler != nil {
		state := s.scheduler.State()
		response["scheduler"] = map[string]interface{}{
			"running":       true,
			"entries_count": len(state.Entries),
		}
	}

	// Record counts — omit section on storage failure rather than failing the entire request
	if s.storage != nil {
		activeReminders, _ := s.storage.ListRecords(storage.ListOptions{RecordType: models.TypeReminder, Status: models.StatusActive})
		activeTasks, _ := s.storage.ListRecords(storage.ListOptions{RecordType: models.TypeTask, Status: models.StatusActive})
		activeMeetings, _ := s.storage.ListRecords(storage.ListOptions{RecordType: models.TypeMeeting, Status: models.StatusActive})
		// Note: logs have no active/completed status, but we count them as active by default
		activeLogs, _ := s.storage.ListRecords(storage.ListOptions{RecordType: models.TypeLog})

		rCount := len(activeReminders)
		tCount := len(activeTasks)
		mCount := len(activeMeetings)
		lCount := len(activeLogs)

		response["records"] = map[string]interface{}{
			"active_reminders": rCount,
			"active_tasks":     tCount,
			"active_meetings":  mCount,
			"active_logs":      lCount,
			"total_active":     rCount + tCount + mCount + lCount,
		}
	}

	daemonWriter(w).Success(response)
}

// buildConfigDiagnostics returns a map describing config completeness with secrets redacted.
func (s *Server) buildConfigDiagnostics() map[string]interface{} {
	cfg := s.config
	if cfg == nil {
		return map[string]interface{}{
			"exists": false,
		}
	}

	// Check config file existence
	configPath, _ := config.DefaultConfigPath()
	configExists := false
	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			configExists = true
		}
	}

	// Check data dir accessibility
	dataDirAccessible := false
	if cfg.DataDir != "" {
		if fi, err := os.Stat(cfg.DataDir); err == nil && fi.IsDir() {
			dataDirAccessible = true
		}
	}

	result := map[string]interface{}{
		"exists": configExists,
		"pushover": map[string]interface{}{
			"configured": cfg.Pushover.APIToken != "" && cfg.Pushover.UserKey != "",
		},
		"llm": map[string]interface{}{
			"text": map[string]interface{}{
				"configured": cfg.LLM.Text.APIKey != "",
			},
			"vision": map[string]interface{}{
				"configured": cfg.LLM.Vision.APIKey != "",
			},
		},
		"data_dir": map[string]interface{}{
			"path":       cfg.DataDir,
			"accessible": dataDirAccessible,
		},
		"redacted": cfg.Redacted(),
	}

	return result
}

// addRequest is the JSON body expected by the add endpoint.
type addRequest struct {
	Type          string   `json:"type"`
	Title         string   `json:"title"`
	Date          string   `json:"date"`
	Time          string   `json:"time,omitempty"`
	Description   string   `json:"description,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Location      string   `json:"location,omitempty"`
	RelatedPerson string   `json:"related_person,omitempty"`
	Priority      string   `json:"priority,omitempty"`
	RemindBefore  string   `json:"remind_before,omitempty"`
	Recurring     string   `json:"recurring,omitempty"`
	Text          string   `json:"text,omitempty"`
	Image         string   `json:"image,omitempty"`
	IdempotencyKey string  `json:"idempotency_key,omitempty"`
}

func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	var req addRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		daemonWriter(w).ErrorWithCode("invalid_body", "invalid request body")
		return
	}

	// If image is provided and type is not specified, use vision LLM classification
	usedLLM := false
	if req.Image != "" && req.Type == "" {
		result, err := s.classifyImage(w, req.Image, req.Text)
		if err != nil {
			return // error already written by classifyImage
		}

		// cancel_or_update is informational — don't create a record
		if result.Type == "cancel_or_update" {
			daemonWriter(w).Success(map[string]interface{}{
				"action":         "cancel_or_update",
				"classification": result,
			})
			return
		}

		populateRequestFromResult(&req, *result)
		logger.Infof("add: source=llm type=%s title=%q image=%s", req.Type, req.Title, req.Image)
		usedLLM = true
	} else if req.Text != "" && req.Type == "" {
		// Text classification — may return multiple results for multi-event input
		results, err := s.classifyText(w, req.Text)
		if err != nil {
			return // error already written by classifyText
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

		// All results are cancel_or_update — return early (backward compat)
		if len(actionable) == 0 {
			classification := cancels
			if len(classification) == 1 {
				daemonWriter(w).Success(map[string]interface{}{
					"action":         "cancel_or_update",
					"classification": classification[0],
				})
			} else {
				daemonWriter(w).Success(map[string]interface{}{
					"action":         "cancel_or_update",
					"classification": classification,
				})
			}
			return
		}

		// Single actionable result — use existing single-record flow
		if len(actionable) == 1 {
			populateRequestFromResult(&req, actionable[0])
			logger.Infof("add: source=llm type=%s title=%q", req.Type, req.Title)
			usedLLM = true
			// Fall through to existing single-record creation below
		} else {
			// Multiple actionable results — batch create records
			s.handleBatchAdd(w, actionable, cancels)
			return
		}
	}

	// Default date to today when not using LLM classification.
	// When LLM is used (text/image classification), the LLM result provides the date.
	if !usedLLM && req.Date == "" {
		loc := time.UTC
		if s.config != nil {
			loc = s.config.Location()
		}
		req.Date = time.Now().In(loc).Format("2006-01-02")
		logger.Infof("add: source=default_today date=%s", req.Date)
	}

	// Validate required fields
	if req.Type == "" {
		daemonWriter(w).ErrorWithCode("invalid_type", "missing required field: type")
		return
	}
	if !models.IsValidType(req.Type) {
		daemonWriter(w).ErrorWithCode("invalid_type", fmt.Sprintf("invalid type: %q (must be meeting, task, reminder, or log)", req.Type))
		return
	}
	if req.Title == "" {
		daemonWriter(w).ErrorWithCode("invalid_body", "missing required field: title")
		return
	}
	if req.Date == "" {
		daemonWriter(w).ErrorWithCode("invalid_body", "missing required field: date")
		return
	}

	// Idempotency check: if key provided, look up existing record
	if req.IdempotencyKey != "" {
		existing, _, err := s.storage.GetByIdempotencyKey(req.IdempotencyKey)
		if err != nil {
			logger.Warnf("add: idempotency lookup error (degrading to normal add): key=%s err=%v", req.IdempotencyKey, err)
		}
		if existing != nil {
			cf := models.GetCommonFields(existing)
			logger.Infof("add: short_id=%s source=idempotent_hit key=%s", cf.ShortID, req.IdempotencyKey)
			daemonWriter(w).Success(existing)
			return
		}
	}

	// Build the typed record
	rec := buildRecord(req)

	// Persist via storage
	result, err := s.storage.AddRecord(rec)
	if err != nil {
		logger.Errorf("add error: %v", err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to add record: %v", err))
		return
	}

	cf := models.GetCommonFields(result)
	source := "manual"
	if usedLLM {
		source = "llm"
	}
	logger.Infof("add: short_id=%s type=%s title=%q source=%s", cf.ShortID, cf.Type, cf.Title, source)

	// Register with scheduler if present
	if s.scheduler != nil {
		if err := s.scheduler.Register(result); err != nil {
			logger.Warnf("scheduler register warning: short_id=%s err=%v", cf.ShortID, err)
		}
	}

	daemonWriter(w).Success(result)
}

// classifyText performs LLM batch classification on the given text.
// It writes an error response and returns nil on failure.
func (s *Server) classifyText(w http.ResponseWriter, text string) ([]llm.ClassifyResult, error) {
	cfg := s.config
	if cfg == nil || cfg.LLM.Text.APIKey == "" {
		daemonWriter(w).ErrorWithCode("llm_not_configured", "LLM text classification is not configured (missing api_key in llm.text)")
		return nil, fmt.Errorf("llm not configured")
	}

	loc := cfg.Location()
	today := time.Now().In(loc)

	client := llm.NewClient(cfg.LLM.Text.APIBase, cfg.LLM.Text.APIKey, cfg.LLM.Text.Model,
		time.Duration(cfg.LLM.Text.Timeout)*time.Second)
	results, err := llm.ClassifyBatch(client, text, today, loc)
	if err != nil {
		logger.Errorf("classify error: api_base=%s model=%s error=%v",
			cfg.LLM.Text.APIBase, cfg.LLM.Text.Model, err)
		daemonWriter(w).ErrorWithCode("llm_error", fmt.Sprintf("LLM classification failed: %v", err))
		return nil, err
	}

	if len(results) == 0 {
		daemonWriter(w).ErrorWithCode("llm_error", "LLM classification returned no results")
		return nil, fmt.Errorf("llm returned empty results")
	}

	return results, nil
}

// populateRequestFromResult fills empty fields in req from the LLM classification result.
// Fields already set in the request (e.g. by the user) are preserved.
func populateRequestFromResult(req *addRequest, result llm.ClassifyResult) {
	req.Type = result.Type
	if req.Title == "" {
		req.Title = result.Title
	}
	if req.Date == "" {
		req.Date = result.Date
	}
	if req.Time == "" {
		req.Time = result.Time
	}
	if req.Description == "" {
		req.Description = result.Description
	}
	if req.Location == "" {
		req.Location = result.Location
	}
	if req.RelatedPerson == "" {
		req.RelatedPerson = result.RelatedPerson
	}
	if req.Priority == "" {
		req.Priority = result.Priority
	}
	if req.RemindBefore == "" {
		req.RemindBefore = result.RemindBefore
	}
	if req.Recurring == "" {
		req.Recurring = result.Recurring
	}
}

// handleBatchAdd creates multiple records from LLM classification results.
// Each actionable result becomes an independent record. cancel_or_update results
// are reported in a summary line appended after all records.
func (s *Server) handleBatchAdd(w http.ResponseWriter, actionable []llm.ClassifyResult, cancels []llm.ClassifyResult) {
	dw := daemonWriter(w)
	created := 0

	for _, result := range actionable {
		addReq := addRequest{
			Type:          result.Type,
			Title:         result.Title,
			Date:          result.Date,
			Time:          result.Time,
			Description:   result.Description,
			Location:      result.Location,
			RelatedPerson: result.RelatedPerson,
			Priority:      result.Priority,
			RemindBefore:  result.RemindBefore,
			Recurring:     result.Recurring,
		}

		// Validate required fields
		if addReq.Type == "" || !models.IsValidType(addReq.Type) {
			logger.Warnf("add batch: skipping result with invalid type %q", addReq.Type)
			continue
		}
		if addReq.Title == "" {
			logger.Warnf("add batch: skipping result with empty title")
			continue
		}
		if addReq.Date == "" {
			loc := time.UTC
			if s.config != nil {
				loc = s.config.Location()
			}
			addReq.Date = time.Now().In(loc).Format("2006-01-02")
		}

		rec := buildRecord(addReq)
		persisted, err := s.storage.AddRecord(rec)
		if err != nil {
			logger.Errorf("add batch: storage error: %v", err)
			continue
		}

		cf := models.GetCommonFields(persisted)
		logger.Infof("add batch: short_id=%s type=%s title=%q source=llm_batch", cf.ShortID, cf.Type, cf.Title)

		if s.scheduler != nil {
			if err := s.scheduler.Register(persisted); err != nil {
				logger.Warnf("add batch: scheduler register warning: short_id=%s err=%v", cf.ShortID, err)
			}
		}

		dw.Success(persisted)
		created++
	}

	// Append summary if there were cancel_or_update entries
	if len(cancels) > 0 {
		dw.Success(map[string]interface{}{
			"action":          "batch_add",
			"created":         created,
			"skipped":         len(cancels),
			"classifications": cancels,
		})
	} else {
		dw.Success(map[string]interface{}{
			"action":  "batch_add",
			"created": created,
		})
	}

	logger.Infof("add batch complete: created=%d cancelled=%d", created, len(cancels))
}

// classifyImage performs vision LLM classification on the given image.
// textContext is optional supplementary text from the user.
// It writes an error response and returns a nil result on failure.
func (s *Server) classifyImage(w http.ResponseWriter, imagePath string, textContext string) (*llm.ClassifyResult, error) {
	cfg := s.config
	if cfg == nil || cfg.LLM.Vision.APIKey == "" {
		daemonWriter(w).ErrorWithCode("llm_not_configured", "LLM vision classification is not configured (missing api_key in llm.vision)")
		return nil, fmt.Errorf("llm vision not configured")
	}

	loc := cfg.Location()
	today := time.Now().In(loc)

	client := llm.NewClient(cfg.LLM.Vision.APIBase, cfg.LLM.Vision.APIKey, cfg.LLM.Vision.Model,
		time.Duration(cfg.LLM.Vision.Timeout)*time.Second)
	result, err := llm.ClassifyImage(client, imagePath, textContext, today, loc)
	if err != nil {
		logger.Errorf("classify_image error: api_base=%s model=%s error=%v image=%s",
			cfg.LLM.Vision.APIBase, cfg.LLM.Vision.Model, err, imagePath)
		daemonWriter(w).ErrorWithCode("llm_error", fmt.Sprintf("LLM vision classification failed: %v", err))
		return nil, err
	}

	logger.Infof("classify_image ok: type=%s model=%s image=%s",
		result.Type, cfg.LLM.Vision.Model, imagePath)

	return result, nil
}

// importRequest is the JSON body expected by the import endpoint.
type importRequest struct {
	Records []addRequest `json:"records"`
}

// handleImport handles POST /api/import — bulk-add records with fail-fast validation.
// All records are validated before any are persisted. On first invalid record,
// the request is rejected with an error identifying the offending index.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	var req importRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		daemonWriter(w).ErrorWithCode("invalid_body", "invalid request body")
		return
	}

	if len(req.Records) == 0 {
		daemonWriter(w).Success(map[string]interface{}{"imported": 0})
		return
	}

	// Phase 1: Validate ALL records before persisting any.
	for i, rec := range req.Records {
		if rec.Type == "" {
			daemonWriter(w).ErrorWithCode("import_record", fmt.Sprintf("record at index %d: missing required field: type", i))
			return
		}
		if !models.IsValidType(rec.Type) {
			daemonWriter(w).ErrorWithCode("import_record", fmt.Sprintf("record at index %d: invalid type: %q (must be meeting, task, reminder, or log)", i, rec.Type))
			return
		}
		if rec.Title == "" {
			daemonWriter(w).ErrorWithCode("import_record", fmt.Sprintf("record at index %d: missing required field: title", i))
			return
		}
		if rec.Date == "" {
			daemonWriter(w).ErrorWithCode("import_record", fmt.Sprintf("record at index %d: missing required field: date", i))
			return
		}
	}

	// Phase 2: Persist all records.
	imported := 0
	for _, rec := range req.Records {
		built := buildRecord(rec)
		result, err := s.storage.AddRecord(built)
		if err != nil {
			logger.Errorf("import: storage error at record index %d: %v", imported, err)
			daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to add record at index %d: %v", imported, err))
			return
		}

		// Register with scheduler if present (log warning only — never fail primary operation)
		if s.scheduler != nil {
			if err := s.scheduler.Register(result); err != nil {
				logger.Warnf("import: scheduler register warning at index %d: %v", imported, err)
			}
		}

		imported++
	}

	logger.Infof("import: imported=%d requested=%d", imported, len(req.Records))
	daemonWriter(w).Success(map[string]interface{}{"imported": imported})
}

// buildRecord creates the correct typed record struct from an addRequest.
func buildRecord(req addRequest) interface{} {
	cf := models.CommonFields{
		Type:           models.RecordType(req.Type),
		Title:          req.Title,
		Date:           req.Date,
		Time:           req.Time,
		Description:    req.Description,
		Tags:           req.Tags,
		Location:       req.Location,
		RelatedPerson:  req.RelatedPerson,
		Priority:       req.Priority,
		RemindBefore:   req.RemindBefore,
		Status:         models.StatusActive,
		IdempotencyKey: req.IdempotencyKey,
	}

	switch models.RecordType(req.Type) {
	case models.TypeMeeting:
		return &models.MeetingRecord{CommonFields: cf}
	case models.TypeTask:
		return &models.TaskRecord{CommonFields: cf}
	case models.TypeReminder:
		return &models.ReminderRecord{
			CommonFields: cf,
			Recurring:    req.Recurring,
		}
	case models.TypeLog:
		return &models.LogRecord{CommonFields: cf}
	default:
		// Should not happen after validation, but fallback to generic
		return &models.LogRecord{CommonFields: cf}
	}
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	query := r.URL.Query()
	recordType := models.RecordType(query.Get("type"))
	date := query.Get("date")
	from := query.Get("from")
	to := query.Get("to")
	status := query.Get("status")
	q := query.Get("query")

	opts := storage.ListOptions{
		RecordType:       recordType,
		Date:             date,
		DateFrom:         from,
		DateTo:           to,
		Status:           status,
		Query:            q,
		IncludeCompleted: false,
	}

	records, err := s.storage.ListRecords(opts)
	if err != nil {
		logger.Errorf("list error: %v", err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to list records: %v", err))
		return
	}

	// Convert ListedRecord to map for JSONL output
	entries := make([]map[string]interface{}, 0, len(records))
	for _, lr := range records {
		entries = append(entries, map[string]interface{}{
			"short_id": lr.ShortID,
			"type":     lr.Type,
			"title":    lr.Title,
			"date":     lr.Date,
			"time":     lr.Time,
			"status":   lr.Status,
		})
	}

	daemonWriter(w).Success(map[string]interface{}{
		"action":  "list",
		"count":   len(entries),
		"entries": entries,
	})
}

func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}
	id, ok := s.resolveRecordID(w, r, "/api/complete/")
	if !ok {
		return
	}

	if err := s.storage.CompleteRecord(id); err != nil {
		logger.Errorf("complete error: id=%s err=%v", id, err)
		if strings.Contains(err.Error(), "not found") {
			daemonWriter(w).ErrorWithCode("record_not_found", err.Error())
		} else {
			daemonWriter(w).ErrorWithCode("storage_error", err.Error())
		}
		return
	}

	// Unregister from scheduler if present
	if s.scheduler != nil {
		if err := s.scheduler.Unregister(id); err != nil {
			logger.Warnf("scheduler unregister warning: short_id=%s err=%v", id, err)
		}
	}

	// Read back the completed record for response
	rec, _, err := s.storage.GetByID(id)
	if err != nil {
		// Record was completed but we can't read it back — still return success
		daemonWriter(w).Success(map[string]interface{}{
			"action": "complete",
			"id":     id,
		})
		return
	}

	logger.Infof("complete: short_id=%s", id)
	daemonWriter(w).Success(rec)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}
	id, ok := s.resolveRecordID(w, r, "/api/update/")
	if !ok {
		return
	}

	var fields map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		daemonWriter(w).ErrorWithCode("invalid_body", "invalid request body")
		return
	}

	updated, err := s.storage.UpdateRecord(id, fields)
	if err != nil {
		logger.Errorf("update error: id=%s err=%v", id, err)
		switch {
		case errors.Is(err, storage.ErrRecordNotFound):
			daemonWriter(w).ErrorWithCode("record_not_found", err.Error())
		case errors.Is(err, storage.ErrRecordCompleted):
			daemonWriter(w).ErrorWithCode("already_completed", err.Error())
		case errors.Is(err, storage.ErrRecordCancelled):
			daemonWriter(w).ErrorWithCode("already_cancelled", err.Error())
		case errors.Is(err, storage.ErrEmptyUpdate):
			daemonWriter(w).ErrorWithCode("invalid_body", err.Error())
		case errors.Is(err, storage.ErrFieldNotAllowed):
			daemonWriter(w).ErrorWithCode("invalid_field", err.Error())
		default:
			daemonWriter(w).ErrorWithCode("storage_error", err.Error())
		}
		return
	}

	// Check if time-related fields changed — if so, re-register scheduler entry
	schedulerFields := map[string]bool{
		"time": true, "date": true, "remind_before": true, "recurring": true,
	}
	needsSchedulerUpdate := false
	for field := range fields {
		if schedulerFields[field] {
			needsSchedulerUpdate = true
			break
		}
	}

	if s.scheduler != nil && needsSchedulerUpdate {
		// Unregister old entry (ignore error — may not have been scheduled)
		_ = s.scheduler.Unregister(id)
		// Register with updated record
		if err := s.scheduler.Register(updated); err != nil {
			logger.Warnf("scheduler re-register warning: short_id=%s err=%v", id, err)
		}
	}

	cf := models.GetCommonFields(updated)
	logger.Infof("update: short_id=%s type=%s fields=%v", cf.ShortID, cf.Type, fieldKeys(fields))

	daemonWriter(w).Success(updated)
}

// resolveRecordID resolves the record short_id from either a path parameter or
// query-parameter-based lookup. If the path contains a non-empty ID after the
// prefix (e.g. /api/update/abc123), it is returned directly. Otherwise, it
// reads "title" and "date" from query params and calls FindByContent.
//
// Returns the resolved short_id, or an empty string with an error written to w
// on failure (multiple_matches, record_not_found, or missing params).
func (s *Server) resolveRecordID(w http.ResponseWriter, r *http.Request, pathPrefix string) (string, bool) {
	id := strings.TrimPrefix(r.URL.Path, pathPrefix)
	if id != "" {
		return id, true
	}

	// Path ID missing — try query-param lookup
	title := r.URL.Query().Get("title")
	date := r.URL.Query().Get("date")

	if title == "" || date == "" {
		daemonWriter(w).ErrorWithCode("record_not_found", "missing entry id or lookup params (title + date required)")
		return "", false
	}

	matches, err := s.storage.FindByContent(title, date)
	if err != nil {
		if errors.Is(err, storage.ErrRecordNotFound) {
			daemonWriter(w).ErrorWithCode("record_not_found", fmt.Sprintf("no active record found with title=%q date=%q", title, date))
			return "", false
		}
		logger.Errorf("lookup error: title=%q date=%q err=%v", title, date, err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("lookup failed: %v", err))
		return "", false
	}

	if len(matches) > 1 {
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.ShortID
		}
		logger.Infof("lookup: multiple_matches title=%q date=%q count=%d ids=%v", title, date, len(matches), ids)
		daemonWriter(w).ErrorWithCode("multiple_matches", fmt.Sprintf("found %d records matching title=%q date=%q: %s", len(matches), title, date, strings.Join(ids, ", ")))
		return "", false
	}

	resolved := matches[0].ShortID
	logger.Infof("lookup: source=lookup title=%q date=%q resolved=%s", title, date, resolved)
	return resolved, true
}

// fieldKeys returns the keys of a map for logging.
func fieldKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}
	id, ok := s.resolveRecordID(w, r, "/api/cancel/")
	if !ok {
		return
	}

	if err := s.storage.CancelRecord(id); err != nil {
		logger.Errorf("cancel error: id=%s err=%v", id, err)
		if strings.Contains(err.Error(), "not found") {
			daemonWriter(w).ErrorWithCode("record_not_found", err.Error())
		} else {
			daemonWriter(w).ErrorWithCode("storage_error", err.Error())
		}
		return
	}

	// Unregister from scheduler if present
	if s.scheduler != nil {
		if err := s.scheduler.Unregister(id); err != nil {
			logger.Warnf("scheduler unregister warning: short_id=%s err=%v", id, err)
		}
	}

	// Read back the cancelled record for response
	rec, _, err := s.storage.GetByID(id)
	if err != nil {
		daemonWriter(w).Success(map[string]interface{}{
			"action": "cancel",
			"id":     id,
		})
		return
	}

	logger.Infof("cancel: short_id=%s", id)
	daemonWriter(w).Success(rec)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	query := r.URL.Query()
	format := query.Get("format")
	if format == "" {
		daemonWriter(w).ErrorWithCode("invalid_body", "missing required query param: format")
		return
	}
	if format != "json" && format != "markdown" {
		daemonWriter(w).ErrorWithCode("invalid_params", fmt.Sprintf("invalid format: %q (must be json or markdown)", format))
		return
	}

	// Build filter options — same pattern as handleList
	date := query.Get("date")
	to := query.Get("to")
	status := query.Get("status")
	q := query.Get("query")

	opts := storage.ListOptions{
		RecordType:       models.RecordType(query.Get("type")),
		Date:             date,
		DateFrom:         query.Get("from"),
		DateTo:           to,
		Status:           status,
		Query:            q,
		IncludeCompleted: false, // default to active-only for consistency with list
	}

	if format == "json" {
		records, err := s.storage.ListFullRecords(opts)
		if err != nil {
			logger.Errorf("export json error: %v", err)
			daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to list records: %v", err))
			return
		}
		logger.Infof("export: format=json count=%d", len(records))
		daemonWriter(w).Success(map[string]interface{}{
			"format":  "json",
			"count":   len(records),
			"records": records,
		})
		return
	}

	// Markdown format — use report generation for rich rendering
	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}

	var markdown string
	var count int

	if date != "" {
		// Single date
		rpt, err := report.Generate(s.storage, date)
		if err != nil {
			logger.Errorf("export markdown error: date=%s err=%v", date, err)
			daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to generate report: %v", err))
			return
		}
		markdown = rpt.Markdown
		count = rpt.Summary.Total
	} else if opts.DateFrom != "" && opts.DateTo != "" {
		// Date range
		rpt, err := report.GenerateRange(s.storage, opts.DateFrom, opts.DateTo, loc)
		if err != nil {
			logger.Errorf("export markdown error: from=%s to=%s err=%v", opts.DateFrom, opts.DateTo, err)
			daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to generate range report: %v", err))
			return
		}
		markdown = rpt.Markdown
		count = rpt.Summary.Total
	} else {
		// No date filter — use today's date
		today := time.Now().In(loc).Format("2006-01-02")
		rpt, err := report.Generate(s.storage, today)
		if err != nil {
			logger.Errorf("export markdown error: date=%s err=%v", today, err)
			daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to generate report: %v", err))
			return
		}
		markdown = rpt.Markdown
		count = rpt.Summary.Total
	}

	logger.Infof("export: format=markdown count=%d", count)
	daemonWriter(w).Success(map[string]interface{}{
		"format":  "markdown",
		"count":   count,
		"content": markdown,
	})
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	query := r.URL.Query()
	date := query.Get("date")
	if date == "" {
		loc := time.UTC
		if s.config != nil {
			loc = s.config.Location()
		}
		date = time.Now().In(loc).Format("2006-01-02")
	}

	rpt, err := report.Generate(s.storage, date)
	if err != nil {
		logger.Errorf("report error: date=%s err=%v", date, err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to generate report: %v", err))
		return
	}

	logger.Infof("report: date=%s meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		date, rpt.Summary.Meetings, rpt.Summary.Tasks,
		rpt.Summary.Reminders, rpt.Summary.Logs, rpt.Summary.Total)

	daemonWriter(w).Success(rpt)
}

func (s *Server) handleReportToday(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}

	rpt, err := report.GenerateToday(s.storage, loc)
	if err != nil {
		logger.Errorf("report_today error: err=%v", err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to generate today report: %v", err))
		return
	}

	logger.Infof("report_today: date=%s meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		rpt.Date, rpt.Summary.Meetings, rpt.Summary.Tasks,
		rpt.Summary.Reminders, rpt.Summary.Logs, rpt.Summary.Total)

	daemonWriter(w).Success(rpt)
}

func (s *Server) handleReportPushToday(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}

	rpt, err := report.GenerateToday(s.storage, loc)
	if err != nil {
		logger.Errorf("report_push_today error: err=%v", err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to generate report: %v", err))
		return
	}

	s.sendReportPush(w, rpt)
}

func (s *Server) handleReportPushDate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	date := strings.TrimPrefix(r.URL.Path, "/api/report/push/date/")
	if date == "" {
		daemonWriter(w).ErrorWithCode("invalid_body", "missing date in path")
		return
	}

	rpt, err := report.Generate(s.storage, date)
	if err != nil {
		logger.Errorf("report_push_date error: date=%s err=%v", date, err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to generate report: %v", err))
		return
	}

	s.sendReportPush(w, rpt)
}

func (s *Server) handleReportRange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	query := r.URL.Query()
	from := query.Get("from")
	to := query.Get("to")
	if from == "" || to == "" {
		daemonWriter(w).ErrorWithCode("invalid_body", "missing required query params: from and to")
		return
	}

	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}

	rpt, err := report.GenerateRange(s.storage, from, to, loc)
	if err != nil {
		logger.Errorf("report_range error: from=%s to=%s err=%v", from, to, err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to generate range report: %v", err))
		return
	}

	logger.Infof("report_range: from=%s to=%s days=%d meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		from, to, rpt.DaysCount, rpt.Summary.Meetings, rpt.Summary.Tasks,
		rpt.Summary.Reminders, rpt.Summary.Logs, rpt.Summary.Total)

	daemonWriter(w).Success(rpt)
}

func (s *Server) handleReportWeek(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}

	rpt, err := report.GenerateWeek(s.storage, loc)
	if err != nil {
		logger.Errorf("report_week error: err=%v", err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to generate week report: %v", err))
		return
	}

	logger.Infof("report_week: from=%s to=%s days=%d meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		rpt.DateFrom, rpt.DateTo, rpt.DaysCount, rpt.Summary.Meetings, rpt.Summary.Tasks,
		rpt.Summary.Reminders, rpt.Summary.Logs, rpt.Summary.Total)

	daemonWriter(w).Success(rpt)
}

func (s *Server) handleReportPushRange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	query := r.URL.Query()
	from := query.Get("from")
	to := query.Get("to")
	if from == "" || to == "" {
		daemonWriter(w).ErrorWithCode("invalid_body", "missing required query params: from and to")
		return
	}

	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}

	rpt, err := report.GenerateRange(s.storage, from, to, loc)
	if err != nil {
		logger.Errorf("report_push_range error: from=%s to=%s err=%v", from, to, err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to generate range report: %v", err))
		return
	}

	s.sendRangeReportPush(w, rpt)
}

func (s *Server) handleReportPushWeek(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}

	rpt, err := report.GenerateWeek(s.storage, loc)
	if err != nil {
		logger.Errorf("report_push_week error: err=%v", err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to generate week report: %v", err))
		return
	}

	s.sendRangeReportPush(w, rpt)
}

// sendRangeReportPush generates a Pushover notification from a range report.
// Returns pushover_not_configured error code if Pushover credentials are empty.
func (s *Server) sendRangeReportPush(w http.ResponseWriter, rpt *report.RangeReport) {
	if s.config == nil || s.config.Pushover.APIToken == "" || s.config.Pushover.UserKey == "" {
		logger.Warnf("report_push_range: pushover_not_configured from=%s to=%s", rpt.DateFrom, rpt.DateTo)
		daemonWriter(w).ErrorWithCode("pushover_not_configured", "Pushover is not configured (api_token or user_key is empty)")
		return
	}

	cfg := pushover.Config{
		APIToken: s.config.Pushover.APIToken,
		UserKey:  s.config.Pushover.UserKey,
	}

	title := fmt.Sprintf("工作报告 %s ~ %s", rpt.DateFrom, rpt.DateTo)
	if err := pushover.Send(context.Background(), cfg, rpt.Markdown, title, 0); err != nil {
		logger.Infof("report_push_range: send failed from=%s to=%s err=%v", rpt.DateFrom, rpt.DateTo, err)
		daemonWriter(w).ErrorWithCode("push_error", fmt.Sprintf("Pushover send failed: %v", err))
		return
	}

	logger.Infof("report_push_range: sent from=%s to=%s days=%d meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		rpt.DateFrom, rpt.DateTo, rpt.DaysCount, rpt.Summary.Meetings, rpt.Summary.Tasks,
		rpt.Summary.Reminders, rpt.Summary.Logs, rpt.Summary.Total)

	daemonWriter(w).Success(map[string]interface{}{
		"date_from": rpt.DateFrom,
		"date_to":   rpt.DateTo,
		"days":      rpt.DaysCount,
		"total":     rpt.Summary.Total,
		"pushed":    true,
		"summary":   rpt.Summary,
	})
}

// sendReportPush generates a Pushover notification from the daily report.
// Returns pushover_not_configured error code if Pushover credentials are empty.
func (s *Server) sendReportPush(w http.ResponseWriter, rpt *report.DailyReport) {
	// Check Pushover configuration
	if s.config == nil || s.config.Pushover.APIToken == "" || s.config.Pushover.UserKey == "" {
		logger.Warnf("report_push: pushover_not_configured date=%s", rpt.Date)
		daemonWriter(w).ErrorWithCode("pushover_not_configured", "Pushover is not configured (api_token or user_key is empty)")
		return
	}

	cfg := pushover.Config{
		APIToken: s.config.Pushover.APIToken,
		UserKey:  s.config.Pushover.UserKey,
	}

	title := fmt.Sprintf("工作日报 %s", rpt.Date)
	if err := pushover.Send(context.Background(), cfg, rpt.Markdown, title, 0); err != nil {
		logger.Errorf("report_push: send failed date=%s err=%v", rpt.Date, err)
		daemonWriter(w).ErrorWithCode("push_error", fmt.Sprintf("Pushover send failed: %v", err))
		return
	}

	logger.Infof("report_push: sent date=%s meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		rpt.Date, rpt.Summary.Meetings, rpt.Summary.Tasks,
		rpt.Summary.Reminders, rpt.Summary.Logs, rpt.Summary.Total)

	daemonWriter(w).Success(map[string]interface{}{
		"date":    rpt.Date,
		"total":   rpt.Summary.Total,
		"pushed":  true,
		"summary": rpt.Summary,
	})
}

// ── Digest CRUD handlers ──

// digestAddRequest is the JSON body expected by POST /api/digest/add.
type digestAddRequest struct {
	Schedule  string `json:"schedule"`
	Scope     string `json:"scope"`
	Direction string `json:"direction"`
}

// handleDigestAdd handles POST /api/digest/add — creates a new digest configuration.
// Body: {"schedule": "0 8 * * *", "scope": "today", "direction": "agenda"}
// Success: returns the created DigestConfig.
func (s *Server) handleDigestAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	if s.digestStore == nil {
		daemonWriter(w).ErrorWithCode("storage_error", "digest store not initialized")
		return
	}

	var req digestAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		daemonWriter(w).ErrorWithCode("invalid_body", "invalid request body")
		return
	}

	// Validate scope
	scope, err := digest.ParseScope(req.Scope)
	if err != nil {
		daemonWriter(w).ErrorWithCode("invalid_scope", err.Error())
		return
	}

	// Validate direction — empty defaults to "agenda"
	dir := digest.Direction(req.Direction)
	if dir == "" {
		dir = digest.DirectionAgenda
	}
	if dir != digest.DirectionAgenda && dir != digest.DirectionSummary {
		daemonWriter(w).ErrorWithCode("invalid_direction", fmt.Sprintf("invalid direction %q (must be agenda or summary)", req.Direction))
		return
	}

	cfg := digest.DigestConfig{
		Schedule:  req.Schedule,
		Scope:     scope,
		Direction: dir,
	}

	added, err := s.digestStore.Add(cfg)
	if err != nil {
		logger.WithField("schedule", req.Schedule).Errorf("digest add error: %v", err)
		daemonWriter(w).ErrorWithCode("invalid_schedule", err.Error())
		return
	}

	logger.WithField("digest_id", added.ID).WithField("scope", string(added.Scope)).WithField("schedule", added.Schedule).Info("digest added via API")

	// Sync digest scheduler so the new entry gets a cron registration.
	s.SyncDigestScheduler()

	daemonWriter(w).Success(added)
}

// handleDigestList handles GET /api/digest/list — returns all digest configurations.
func (s *Server) handleDigestList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	if s.digestStore == nil {
		daemonWriter(w).ErrorWithCode("storage_error", "digest store not initialized")
		return
	}

	digests, err := s.digestStore.List()
	if err != nil {
		logger.Errorf("digest list error: %v", err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to list digests: %v", err))
		return
	}

	logger.WithField("count", len(digests)).Info("digest list via API")
	daemonWriter(w).Success(map[string]interface{}{
		"action": "list",
		"count":  len(digests),
		"digests": digests,
	})
}

// handleDigestRemove handles POST /api/digest/remove/{id} — removes a digest configuration.
func (s *Server) handleDigestRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	if s.digestStore == nil {
		daemonWriter(w).ErrorWithCode("storage_error", "digest store not initialized")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/digest/remove/")
	if id == "" {
		daemonWriter(w).ErrorWithCode("digest_not_found", "missing digest id")
		return
	}

	if err := s.digestStore.Remove(id); err != nil {
		logger.WithField("digest_id", id).Errorf("digest remove error: %v", err)
		daemonWriter(w).ErrorWithCode("digest_not_found", fmt.Sprintf("digest %q not found", id))
		return
	}

	logger.WithField("digest_id", id).Info("digest removed via API")

	// Sync digest scheduler so the removed entry gets unregistered.
	s.SyncDigestScheduler()

	daemonWriter(w).Success(map[string]interface{}{
		"action":  "remove",
		"id":      id,
		"message": "digest removed",
	})
}

// handleDigestEnable handles POST /api/digest/enable/{id} — enables a digest configuration.
func (s *Server) handleDigestEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	if s.digestStore == nil {
		daemonWriter(w).ErrorWithCode("storage_error", "digest store not initialized")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/digest/enable/")
	if id == "" {
		daemonWriter(w).ErrorWithCode("digest_not_found", "missing digest id")
		return
	}

	if err := s.digestStore.Enable(id); err != nil {
		logger.WithField("digest_id", id).Errorf("digest enable error: %v", err)
		daemonWriter(w).ErrorWithCode("digest_not_found", fmt.Sprintf("digest %q not found", id))
		return
	}

	// Read back and return updated config
	updated, err := s.digestStore.Get(id)
	if err != nil {
		daemonWriter(w).Success(map[string]interface{}{
			"action": "enable",
			"id":     id,
		})
		return
	}

	logger.WithField("digest_id", id).Info("digest enabled via API")

	// Sync digest scheduler so the newly-enabled entry gets registered.
	s.SyncDigestScheduler()

	daemonWriter(w).Success(updated)
}

// handleDigestDisable handles POST /api/digest/disable/{id} — disables a digest configuration.
func (s *Server) handleDigestDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	if s.digestStore == nil {
		daemonWriter(w).ErrorWithCode("storage_error", "digest store not initialized")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/digest/disable/")
	if id == "" {
		daemonWriter(w).ErrorWithCode("digest_not_found", "missing digest id")
		return
	}

	if err := s.digestStore.Disable(id); err != nil {
		logger.WithField("digest_id", id).Errorf("digest disable error: %v", err)
		daemonWriter(w).ErrorWithCode("digest_not_found", fmt.Sprintf("digest %q not found", id))
		return
	}

	// Read back and return updated config
	updated, err := s.digestStore.Get(id)
	if err != nil {
		daemonWriter(w).Success(map[string]interface{}{
			"action": "disable",
			"id":     id,
		})
		return
	}

	logger.WithField("digest_id", id).Info("digest disabled via API")

	// Sync digest scheduler so the disabled entry gets unregistered.
	s.SyncDigestScheduler()

	daemonWriter(w).Success(updated)
}

// ── Prompt CRUD handlers ──

// handlePromptList handles GET /api/prompt/list — lists all prompts with their current text and default status.
func (s *Server) handlePromptList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	if s.digestStore == nil {
		daemonWriter(w).ErrorWithCode("storage_error", "digest store not initialized")
		return
	}

	prompts, err := s.digestStore.ListPrompts()
	if err != nil {
		logger.Errorf("prompt list error: %v", err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to list prompts: %v", err))
		return
	}

	// Convert map to sorted array for deterministic output
	entries := make([]map[string]interface{}, 0, len(prompts))
	for _, info := range prompts {
		entry := map[string]interface{}{
			"name":       string(info.Name),
			"text":       info.Text,
			"is_default": info.IsDefault,
		}
		if info.UpdatedAt != "" {
			entry["updated_at"] = info.UpdatedAt
		}
		entries = append(entries, entry)
	}

	logger.WithField("count", len(entries)).Info("prompt list via API")
	daemonWriter(w).Success(map[string]interface{}{
		"action":  "list",
		"count":   len(entries),
		"prompts": entries,
	})
}

// handlePromptShow handles GET /api/prompt/show/{name} — returns the effective prompt text for a named prompt.
func (s *Server) handlePromptShow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	if s.digestStore == nil {
		daemonWriter(w).ErrorWithCode("storage_error", "digest store not initialized")
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/prompt/show/")
	if name == "" {
		daemonWriter(w).ErrorWithCode("prompt_not_found", "missing prompt name")
		return
	}

	text, err := s.digestStore.GetPrompt(digest.PromptName(name))
	if err != nil {
		if errors.Is(err, digest.ErrPromptNotFound) {
			daemonWriter(w).ErrorWithCode("prompt_not_found", fmt.Sprintf("prompt %q not found (no default or override)", name))
		} else {
			logger.Errorf("prompt show error: name=%s err=%v", name, err)
			daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to get prompt: %v", err))
		}
		return
	}

	// Determine if it's a default or override
	info, _ := s.digestStore.ListPrompts()
	promptInfo, exists := info[digest.PromptName(name)]
	isDefault := !exists || promptInfo.IsDefault

	daemonWriter(w).Success(map[string]interface{}{
		"action":     "show",
		"name":       name,
		"text":       text,
		"is_default": isDefault,
	})
}

// promptSetRequest is the JSON body expected by POST /api/prompt/set/{name}.
type promptSetRequest struct {
	Text string `json:"text"`
	File string `json:"file"`
}

// handlePromptSet handles POST /api/prompt/set/{name} — sets a custom prompt text.
// Body: {"text": "..."} or {"file": "path/to/file.txt"}
func (s *Server) handlePromptSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	if s.digestStore == nil {
		daemonWriter(w).ErrorWithCode("storage_error", "digest store not initialized")
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/prompt/set/")
	if name == "" {
		daemonWriter(w).ErrorWithCode("prompt_not_found", "missing prompt name")
		return
	}

	var req promptSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		daemonWriter(w).ErrorWithCode("invalid_body", "invalid request body")
		return
	}

	// Determine text source: explicit text or file content
	text := req.Text
	if text == "" && req.File != "" {
		data, err := os.ReadFile(req.File)
		if err != nil {
			daemonWriter(w).ErrorWithCode("invalid_body", fmt.Sprintf("failed to read file %q: %v", req.File, err))
			return
		}
		text = string(data)
	}

	if text == "" {
		daemonWriter(w).ErrorWithCode("invalid_body", "text or file must be provided (and non-empty)")
		return
	}

	if err := s.digestStore.SetPrompt(digest.PromptName(name), text); err != nil {
		logger.Errorf("prompt set error: name=%s err=%v", name, err)
		daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to set prompt: %v", err))
		return
	}

	logger.WithField("prompt_name", name).Info("prompt set via API")
	daemonWriter(w).Success(map[string]interface{}{
		"action": "set",
		"name":   name,
		"message": "prompt updated",
	})
}

// handlePromptReset handles POST /api/prompt/reset/{name} — restores a built-in prompt to its default text.
func (s *Server) handlePromptReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		daemonWriter(w).ErrorWithCode("method_not_allowed", "method not allowed")
		return
	}

	if s.digestStore == nil {
		daemonWriter(w).ErrorWithCode("storage_error", "digest store not initialized")
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/prompt/reset/")
	if name == "" {
		daemonWriter(w).ErrorWithCode("prompt_not_found", "missing prompt name")
		return
	}

	if err := s.digestStore.ResetPrompt(digest.PromptName(name)); err != nil {
		if errors.Is(err, digest.ErrPromptNotFound) {
			daemonWriter(w).ErrorWithCode("prompt_not_found", fmt.Sprintf("prompt %q has no default to reset to", name))
		} else {
			logger.Errorf("prompt reset error: name=%s err=%v", name, err)
			daemonWriter(w).ErrorWithCode("storage_error", fmt.Sprintf("failed to reset prompt: %v", err))
		}
		return
	}

	// Read back the default text for the response
	text, err := s.digestStore.GetPrompt(digest.PromptName(name))
	if err != nil {
		daemonWriter(w).Success(map[string]interface{}{
			"action":  "reset",
			"name":    name,
			"message": "prompt reset to default",
		})
		return
	}

	logger.WithField("prompt_name", name).Info("prompt reset via API")
	daemonWriter(w).Success(map[string]interface{}{
		"action":  "reset",
		"name":    name,
		"text":    text,
		"message": "prompt reset to default",
	})
}

