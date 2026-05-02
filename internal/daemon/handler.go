package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"wr/internal/llm"
	"wr/internal/models"
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

	opts := storage.ListOptions{
		RecordType:       recordType,
		Date:             date,
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
