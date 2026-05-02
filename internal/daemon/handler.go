package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"wr/internal/models"
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
	log.Printf("[daemon] add: short_id=%s type=%s title=%q", cf.ShortID, cf.Type, cf.Title)

	jsonlResponse(w, "success", result, "")
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
		date = time.Now().Format("2006-01-02")
	}

	// List all types for the given date
	var allEntries []map[string]interface{}
	for _, rt := range models.ValidRecordTypes() {
		opts := storage.ListOptions{
			RecordType:       models.RecordType(rt),
			Date:             date,
			IncludeCompleted: true,
		}
		records, err := s.storage.ListRecords(opts)
		if err != nil {
			log.Printf("[daemon] report error: type=%s err=%v", rt, err)
			continue
		}
		for _, lr := range records {
			allEntries = append(allEntries, map[string]interface{}{
				"short_id": lr.ShortID,
				"type":     lr.Type,
				"title":    lr.Title,
				"date":     lr.Date,
				"time":     lr.Time,
				"status":   lr.Status,
			})
		}
	}

	jsonlResponse(w, "success", map[string]interface{}{
		"action":  "report",
		"date":    date,
		"count":   len(allEntries),
		"entries": allEntries,
	}, "")
}

func (s *Server) handleReportToday(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}

	// Delegate to report with today's date
	today := time.Now().Format("2006-01-02")
	var allEntries []map[string]interface{}
	for _, rt := range models.ValidRecordTypes() {
		opts := storage.ListOptions{
			RecordType:       models.RecordType(rt),
			Date:             today,
			IncludeCompleted: true,
		}
		records, err := s.storage.ListRecords(opts)
		if err != nil {
			log.Printf("[daemon] report_today error: type=%s err=%v", rt, err)
			continue
		}
		for _, lr := range records {
			allEntries = append(allEntries, map[string]interface{}{
				"short_id": lr.ShortID,
				"type":     lr.Type,
				"title":    lr.Title,
				"date":     lr.Date,
				"time":     lr.Time,
				"status":   lr.Status,
			})
		}
	}

	jsonlResponse(w, "success", map[string]interface{}{
		"action":  "report_today",
		"date":    today,
		"count":   len(allEntries),
		"entries": allEntries,
	}, "")
}
