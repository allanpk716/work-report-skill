package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"wr/internal/config"
	"wr/internal/llm"
	"wr/internal/models"
	"wr/internal/pushover"
	"wr/internal/report"
	"wr/internal/storage"
)

const Version = "0.1.0"

// jsonlResponse writes a JSONL record as the HTTP response.
func jsonlResponse(w http.ResponseWriter, status string, data interface{}, message string) {
	record := map[string]interface{}{
		"status": status,
	}
	if data != nil {
		record["data"] = data
	}
	if message != "" {
		record["message"] = message
	}
	b, err := json.Marshal(record)
	if err != nil {
		http.Error(w, `{"status":"error","message":"marshal error"}`, 500)
		return
	}
	w.Header().Set("Content-Type", "application/jsonl")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "%s\n", b)
}

// errorResponse writes a JSONL error with an error code for programmatic handling.
func errorResponse(w http.ResponseWriter, code string, message string) {
	record := map[string]interface{}{
		"status":  "error",
		"code":    code,
		"message": message,
	}
	b, _ := json.Marshal(record)
	w.Header().Set("Content-Type", "application/jsonl")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "%s\n", b)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}
	jsonlResponse(w, "ok", map[string]interface{}{
		"version": Version,
	}, "")
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}
	jsonlResponse(w, "success", nil, "daemon shutting down")
	go s.shutdown()
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonlResponse(w, "error", nil, "method not allowed")
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

	// Scheduler info
	response := map[string]interface{}{
		"daemon": daemonInfo,
		"config": configInfo,
	}

	if s.scheduler != nil {
		state := s.scheduler.State()
		response["scheduler"] = map[string]interface{}{
			"running":       true,
			"entries_count": len(state.Entries),
		}
	}

	jsonlResponse(w, "success", response, "")
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
}

func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}

	var req addRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, "invalid_body", "invalid request body")
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
			jsonlResponse(w, "info", map[string]interface{}{
				"action":        "cancel_or_update",
				"classification": result,
			}, "")
			return
		}

		// Populate request fields from classification result
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

		log.Printf("[daemon] add: source=llm type=%s title=%q image=%s", req.Type, req.Title, req.Image)
		usedLLM = true
	} else if req.Text != "" && req.Type == "" {
		// Text classification (existing flow)
		result, err := s.classifyText(w, req.Text)
		if err != nil {
			return // error already written by classifyText
		}

		// cancel_or_update is informational — don't create a record
		if result.Type == "cancel_or_update" {
			jsonlResponse(w, "info", map[string]interface{}{
				"action":     "cancel_or_update",
				"classification": result,
			}, "")
			return
		}

		// Populate request fields from classification result
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

		log.Printf("[daemon] add: source=llm type=%s title=%q", req.Type, req.Title)
		usedLLM = true
	}

	// Validate required fields
	if req.Type == "" {
		errorResponse(w, "invalid_type", "missing required field: type")
		return
	}
	if !models.IsValidType(req.Type) {
		errorResponse(w, "invalid_type", fmt.Sprintf("invalid type: %q (must be meeting, task, reminder, or log)", req.Type))
		return
	}
	if req.Title == "" {
		errorResponse(w, "invalid_body", "missing required field: title")
		return
	}
	if req.Date == "" {
		errorResponse(w, "invalid_body", "missing required field: date")
		return
	}

	// Build the typed record
	rec := buildRecord(req)

	// Persist via storage
	result, err := s.storage.AddRecord(rec)
	if err != nil {
		log.Printf("[daemon] add error: %v", err)
		errorResponse(w, "storage_error", fmt.Sprintf("failed to add record: %v", err))
		return
	}

	cf := models.GetCommonFields(result)
	source := "manual"
	if usedLLM {
		source = "llm"
	}
	log.Printf("[daemon] add: short_id=%s type=%s title=%q source=%s", cf.ShortID, cf.Type, cf.Title, source)

	// Register with scheduler if present
	if s.scheduler != nil {
		if err := s.scheduler.Register(result); err != nil {
			log.Printf("[daemon] scheduler register warning: short_id=%s err=%v", cf.ShortID, err)
		}
	}

	jsonlResponse(w, "success", result, "")
}

// classifyText performs LLM classification on the given text.
// It writes an error response and returns a nil result on failure.
func (s *Server) classifyText(w http.ResponseWriter, text string) (*llm.ClassifyResult, error) {
	cfg := s.config
	if cfg == nil || cfg.LLM.Text.APIKey == "" {
		errorResponse(w, "llm_not_configured", "LLM text classification is not configured (missing api_key in llm.text)")
		return nil, fmt.Errorf("llm not configured")
	}

	loc := cfg.Location()
	today := time.Now().In(loc)

	client := llm.NewClient(cfg.LLM.Text.APIBase, cfg.LLM.Text.APIKey, cfg.LLM.Text.Model)
	result, err := llm.Classify(client, text, today, loc)
	if err != nil {
		log.Printf("[daemon] classify error: api_base=%s model=%s error=%v",
			cfg.LLM.Text.APIBase, cfg.LLM.Text.Model, err)
		errorResponse(w, "llm_error", fmt.Sprintf("LLM classification failed: %v", err))
		return nil, err
	}

	return result, nil
}

// classifyImage performs vision LLM classification on the given image.
// textContext is optional supplementary text from the user.
// It writes an error response and returns a nil result on failure.
func (s *Server) classifyImage(w http.ResponseWriter, imagePath string, textContext string) (*llm.ClassifyResult, error) {
	cfg := s.config
	if cfg == nil || cfg.LLM.Vision.APIKey == "" {
		errorResponse(w, "llm_not_configured", "LLM vision classification is not configured (missing api_key in llm.vision)")
		return nil, fmt.Errorf("llm vision not configured")
	}

	loc := cfg.Location()
	today := time.Now().In(loc)

	client := llm.NewClient(cfg.LLM.Vision.APIBase, cfg.LLM.Vision.APIKey, cfg.LLM.Vision.Model)
	result, err := llm.ClassifyImage(client, imagePath, textContext, today, loc)
	if err != nil {
		log.Printf("[daemon] classify_image error: api_base=%s model=%s error=%v image=%s",
			cfg.LLM.Vision.APIBase, cfg.LLM.Vision.Model, err, imagePath)
		errorResponse(w, "llm_error", fmt.Sprintf("LLM vision classification failed: %v", err))
		return nil, err
	}

	log.Printf("[daemon] classify_image ok: type=%s model=%s image=%s",
		result.Type, cfg.LLM.Vision.Model, imagePath)

	return result, nil
}

// buildRecord creates the correct typed record struct from an addRequest.
func buildRecord(req addRequest) interface{} {
	cf := models.CommonFields{
		Type:          models.RecordType(req.Type),
		Title:         req.Title,
		Date:          req.Date,
		Time:          req.Time,
		Description:   req.Description,
		Tags:          req.Tags,
		Location:      req.Location,
		RelatedPerson: req.RelatedPerson,
		Priority:      req.Priority,
		RemindBefore:  req.RemindBefore,
		Status:        models.StatusActive,
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
		jsonlResponse(w, "error", nil, "method not allowed")
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
		log.Printf("[daemon] list error: %v", err)
		errorResponse(w, "storage_error", fmt.Sprintf("failed to list records: %v", err))
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

	jsonlResponse(w, "success", map[string]interface{}{
		"action":  "list",
		"count":   len(entries),
		"entries": entries,
	}, "")
}

func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/complete/")
	if id == "" {
		errorResponse(w, "record_not_found", "missing entry id")
		return
	}

	if err := s.storage.CompleteRecord(id); err != nil {
		log.Printf("[daemon] complete error: id=%s err=%v", id, err)
		if strings.Contains(err.Error(), "not found") {
			errorResponse(w, "record_not_found", err.Error())
		} else {
			errorResponse(w, "storage_error", err.Error())
		}
		return
	}

	// Unregister from scheduler if present
	if s.scheduler != nil {
		if err := s.scheduler.Unregister(id); err != nil {
			log.Printf("[daemon] scheduler unregister warning: short_id=%s err=%v", id, err)
		}
	}

	// Read back the completed record for response
	rec, _, err := s.storage.GetByID(id)
	if err != nil {
		// Record was completed but we can't read it back — still return success
		jsonlResponse(w, "success", map[string]interface{}{
			"action": "complete",
			"id":     id,
		}, "")
		return
	}

	log.Printf("[daemon] complete: short_id=%s", id)
	jsonlResponse(w, "success", rec, "")
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/update/")
	if id == "" {
		errorResponse(w, "record_not_found", "missing entry id")
		return
	}

	var fields map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		errorResponse(w, "invalid_body", "invalid request body")
		return
	}

	updated, err := s.storage.UpdateRecord(id, fields)
	if err != nil {
		log.Printf("[daemon] update error: id=%s err=%v", id, err)
		switch {
		case errors.Is(err, storage.ErrRecordNotFound):
			errorResponse(w, "record_not_found", err.Error())
		case errors.Is(err, storage.ErrRecordCompleted):
			errorResponse(w, "already_completed", err.Error())
		case errors.Is(err, storage.ErrRecordCancelled):
			errorResponse(w, "already_cancelled", err.Error())
		case errors.Is(err, storage.ErrEmptyUpdate):
			errorResponse(w, "invalid_body", err.Error())
		case errors.Is(err, storage.ErrFieldNotAllowed):
			errorResponse(w, "invalid_field", err.Error())
		default:
			errorResponse(w, "storage_error", err.Error())
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
			log.Printf("[daemon] scheduler re-register warning: short_id=%s err=%v", id, err)
		}
	}

	cf := models.GetCommonFields(updated)
	log.Printf("[daemon] update: short_id=%s type=%s fields=%v", cf.ShortID, cf.Type, fieldKeys(fields))

	jsonlResponse(w, "success", updated, "")
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
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/cancel/")
	if id == "" {
		errorResponse(w, "record_not_found", "missing entry id")
		return
	}

	if err := s.storage.CancelRecord(id); err != nil {
		log.Printf("[daemon] cancel error: id=%s err=%v", id, err)
		if strings.Contains(err.Error(), "not found") {
			errorResponse(w, "record_not_found", err.Error())
		} else {
			errorResponse(w, "storage_error", err.Error())
		}
		return
	}

	// Unregister from scheduler if present
	if s.scheduler != nil {
		if err := s.scheduler.Unregister(id); err != nil {
			log.Printf("[daemon] scheduler unregister warning: short_id=%s err=%v", id, err)
		}
	}

	// Read back the cancelled record for response
	rec, _, err := s.storage.GetByID(id)
	if err != nil {
		jsonlResponse(w, "success", map[string]interface{}{
			"action": "cancel",
			"id":     id,
		}, "")
		return
	}

	log.Printf("[daemon] cancel: short_id=%s", id)
	jsonlResponse(w, "success", rec, "")
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonlResponse(w, "error", nil, "method not allowed")
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

	rpt, err := report.Generate(s.storage, date, log.Default())
	if err != nil {
		log.Printf("[daemon] report error: date=%s err=%v", date, err)
		errorResponse(w, "storage_error", fmt.Sprintf("failed to generate report: %v", err))
		return
	}

	log.Printf("[daemon] report: date=%s meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		date, rpt.Summary.Meetings, rpt.Summary.Tasks,
		rpt.Summary.Reminders, rpt.Summary.Logs, rpt.Summary.Total)

	jsonlResponse(w, "success", rpt, "")
}

func (s *Server) handleReportToday(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}

	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}

	rpt, err := report.GenerateToday(s.storage, loc, log.Default())
	if err != nil {
		log.Printf("[daemon] report_today error: err=%v", err)
		errorResponse(w, "storage_error", fmt.Sprintf("failed to generate today report: %v", err))
		return
	}

	log.Printf("[daemon] report_today: date=%s meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		rpt.Date, rpt.Summary.Meetings, rpt.Summary.Tasks,
		rpt.Summary.Reminders, rpt.Summary.Logs, rpt.Summary.Total)

	jsonlResponse(w, "success", rpt, "")
}

func (s *Server) handleReportPushToday(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}

	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}

	rpt, err := report.GenerateToday(s.storage, loc, log.Default())
	if err != nil {
		log.Printf("[daemon] report_push_today error: err=%v", err)
		errorResponse(w, "storage_error", fmt.Sprintf("failed to generate report: %v", err))
		return
	}

	s.sendReportPush(w, rpt)
}

func (s *Server) handleReportPushDate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}

	date := strings.TrimPrefix(r.URL.Path, "/api/report/push/date/")
	if date == "" {
		errorResponse(w, "invalid_body", "missing date in path")
		return
	}

	rpt, err := report.Generate(s.storage, date, log.Default())
	if err != nil {
		log.Printf("[daemon] report_push_date error: date=%s err=%v", date, err)
		errorResponse(w, "storage_error", fmt.Sprintf("failed to generate report: %v", err))
		return
	}

	s.sendReportPush(w, rpt)
}

func (s *Server) handleReportRange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}

	query := r.URL.Query()
	from := query.Get("from")
	to := query.Get("to")
	if from == "" || to == "" {
		errorResponse(w, "invalid_body", "missing required query params: from and to")
		return
	}

	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}

	rpt, err := report.GenerateRange(s.storage, from, to, loc, log.Default())
	if err != nil {
		log.Printf("[daemon] report_range error: from=%s to=%s err=%v", from, to, err)
		errorResponse(w, "storage_error", fmt.Sprintf("failed to generate range report: %v", err))
		return
	}

	log.Printf("[daemon] report_range: from=%s to=%s days=%d meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		from, to, rpt.DaysCount, rpt.Summary.Meetings, rpt.Summary.Tasks,
		rpt.Summary.Reminders, rpt.Summary.Logs, rpt.Summary.Total)

	jsonlResponse(w, "success", rpt, "")
}

func (s *Server) handleReportWeek(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}

	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}

	rpt, err := report.GenerateWeek(s.storage, loc, log.Default())
	if err != nil {
		log.Printf("[daemon] report_week error: err=%v", err)
		errorResponse(w, "storage_error", fmt.Sprintf("failed to generate week report: %v", err))
		return
	}

	log.Printf("[daemon] report_week: from=%s to=%s days=%d meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		rpt.DateFrom, rpt.DateTo, rpt.DaysCount, rpt.Summary.Meetings, rpt.Summary.Tasks,
		rpt.Summary.Reminders, rpt.Summary.Logs, rpt.Summary.Total)

	jsonlResponse(w, "success", rpt, "")
}

func (s *Server) handleReportPushRange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}

	query := r.URL.Query()
	from := query.Get("from")
	to := query.Get("to")
	if from == "" || to == "" {
		errorResponse(w, "invalid_body", "missing required query params: from and to")
		return
	}

	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}

	rpt, err := report.GenerateRange(s.storage, from, to, loc, log.Default())
	if err != nil {
		log.Printf("[daemon] report_push_range error: from=%s to=%s err=%v", from, to, err)
		errorResponse(w, "storage_error", fmt.Sprintf("failed to generate range report: %v", err))
		return
	}

	s.sendRangeReportPush(w, rpt)
}

func (s *Server) handleReportPushWeek(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}

	loc := time.UTC
	if s.config != nil {
		loc = s.config.Location()
	}

	rpt, err := report.GenerateWeek(s.storage, loc, log.Default())
	if err != nil {
		log.Printf("[daemon] report_push_week error: err=%v", err)
		errorResponse(w, "storage_error", fmt.Sprintf("failed to generate week report: %v", err))
		return
	}

	s.sendRangeReportPush(w, rpt)
}

// sendRangeReportPush generates a Pushover notification from a range report.
// Returns pushover_not_configured error code if Pushover credentials are empty.
func (s *Server) sendRangeReportPush(w http.ResponseWriter, rpt *report.RangeReport) {
	if s.config == nil || s.config.Pushover.APIToken == "" || s.config.Pushover.UserKey == "" {
		log.Printf("[daemon] report_push_range: pushover_not_configured from=%s to=%s", rpt.DateFrom, rpt.DateTo)
		errorResponse(w, "pushover_not_configured", "Pushover is not configured (api_token or user_key is empty)")
		return
	}

	cfg := pushover.Config{
		APIToken: s.config.Pushover.APIToken,
		UserKey:  s.config.Pushover.UserKey,
	}

	title := fmt.Sprintf("工作报告 %s ~ %s", rpt.DateFrom, rpt.DateTo)
	if err := pushover.Send(context.Background(), cfg, rpt.Markdown, title, 0); err != nil {
		log.Printf("[daemon] report_push_range: send failed from=%s to=%s err=%v", rpt.DateFrom, rpt.DateTo, err)
		errorResponse(w, "push_error", fmt.Sprintf("Pushover send failed: %v", err))
		return
	}

	log.Printf("[daemon] report_push_range: sent from=%s to=%s days=%d meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		rpt.DateFrom, rpt.DateTo, rpt.DaysCount, rpt.Summary.Meetings, rpt.Summary.Tasks,
		rpt.Summary.Reminders, rpt.Summary.Logs, rpt.Summary.Total)

	jsonlResponse(w, "success", map[string]interface{}{
		"date_from": rpt.DateFrom,
		"date_to":   rpt.DateTo,
		"days":      rpt.DaysCount,
		"total":     rpt.Summary.Total,
		"pushed":    true,
		"summary":   rpt.Summary,
	}, "")
}

// sendReportPush generates a Pushover notification from the daily report.
// Returns pushover_not_configured error code if Pushover credentials are empty.
func (s *Server) sendReportPush(w http.ResponseWriter, rpt *report.DailyReport) {
	// Check Pushover configuration
	if s.config == nil || s.config.Pushover.APIToken == "" || s.config.Pushover.UserKey == "" {
		log.Printf("[daemon] report_push: pushover_not_configured date=%s", rpt.Date)
		errorResponse(w, "pushover_not_configured", "Pushover is not configured (api_token or user_key is empty)")
		return
	}

	cfg := pushover.Config{
		APIToken: s.config.Pushover.APIToken,
		UserKey:  s.config.Pushover.UserKey,
	}

	title := fmt.Sprintf("工作日报 %s", rpt.Date)
	if err := pushover.Send(context.Background(), cfg, rpt.Markdown, title, 0); err != nil {
		log.Printf("[daemon] report_push: send failed date=%s err=%v", rpt.Date, err)
		errorResponse(w, "push_error", fmt.Sprintf("Pushover send failed: %v", err))
		return
	}

	log.Printf("[daemon] report_push: sent date=%s meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		rpt.Date, rpt.Summary.Meetings, rpt.Summary.Tasks,
		rpt.Summary.Reminders, rpt.Summary.Logs, rpt.Summary.Total)

	jsonlResponse(w, "success", map[string]interface{}{
		"date":    rpt.Date,
		"total":   rpt.Summary.Total,
		"pushed":  true,
		"summary": rpt.Summary,
	}, "")
}
