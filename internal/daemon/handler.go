package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}
	jsonlResponse(w, "ok", map[string]interface{}{
		"version": Version,
	}, "")
}

func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonlResponse(w, "error", nil, "invalid request body")
		return
	}
	jsonlResponse(w, "success", map[string]interface{}{
		"action": "add",
		"stub":   true,
		"entry":  body,
	}, "")
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}
	query := r.URL.Query()
	jsonlResponse(w, "success", map[string]interface{}{
		"action": "list",
		"stub":   true,
		"filters": map[string]string{
			"type": query.Get("type"),
			"date": query.Get("date"),
		},
		"entries": []interface{}{},
	}, "")
}

func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/complete/")
	if id == "" {
		jsonlResponse(w, "error", nil, "missing entry id")
		return
	}
	jsonlResponse(w, "success", map[string]interface{}{
		"action": "complete",
		"stub":   true,
		"id":     id,
	}, "")
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/cancel/")
	if id == "" {
		jsonlResponse(w, "error", nil, "missing entry id")
		return
	}
	jsonlResponse(w, "success", map[string]interface{}{
		"action": "cancel",
		"stub":   true,
		"id":     id,
	}, "")
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}
	jsonlResponse(w, "success", map[string]interface{}{
		"action":  "report",
		"stub":    true,
		"entries": []interface{}{},
	}, "")
}

func (s *Server) handleReportToday(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonlResponse(w, "error", nil, "method not allowed")
		return
	}
	jsonlResponse(w, "success", map[string]interface{}{
		"action":  "report_today",
		"stub":    true,
		"entries": []interface{}{},
	}, "")
}
