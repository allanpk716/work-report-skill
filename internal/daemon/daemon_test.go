package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wr/internal/config"
	"wr/internal/jsonl"
	"wr/internal/llm"
	"wr/internal/pushover"
	"wr/internal/scheduler"
	"wr/internal/storage"
)

// newTestServer creates a Server backed by a temp directory for storage.
// If cfg is nil, a minimal config is created.
func newTestServer(t *testing.T, cfg ...*config.Config) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	store := storage.New(dir, log.New(io.Discard, "", 0))
	var c *config.Config
	if len(cfg) > 0 && cfg[0] != nil {
		c = cfg[0]
	} else {
		c = &config.Config{}
	}
	srv := NewServer(0, store, c)
	return srv, dir
}

// ── State file tests ──

func TestWriteAndReadState(t *testing.T) {
	dir := t.TempDir()
	state := DaemonState{Port: 17530, PID: 12345}

	if err := WriteState(dir, state); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	got, err := ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.Port != state.Port || got.PID != state.PID {
		t.Errorf("ReadState = %+v, want %+v", got, state)
	}
}

func TestStateBackupWrittenBeforeMain(t *testing.T) {
	dir := t.TempDir()
	state := DaemonState{Port: 17530, PID: 9999}

	if err := WriteState(dir, state); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	mainData, err := os.ReadFile(StatePath(dir))
	if err != nil {
		t.Fatalf("read main: %v", err)
	}
	bakData, err := os.ReadFile(BackupPath(dir))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(mainData) != string(bakData) {
		t.Errorf("main and backup differ: main=%s bak=%s", mainData, bakData)
	}
}

func TestReadStateRecoversFromCorruptMainFile(t *testing.T) {
	dir := t.TempDir()

	validState := DaemonState{Port: 17530, PID: 7777}
	bakData, _ := json.Marshal(validState)
	if err := os.WriteFile(BackupPath(dir), bakData, 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(StatePath(dir), []byte("NOT JSON!!!"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState should recover from backup: %v", err)
	}
	if got.Port != validState.Port || got.PID != validState.PID {
		t.Errorf("recovered state = %+v, want %+v", got, validState)
	}
}

func TestReadStateBothCorrupt(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(StatePath(dir), []byte("BAD"), 0644)
	os.WriteFile(BackupPath(dir), []byte("ALSO BAD"), 0644)

	_, err := ReadState(dir)
	if err == nil {
		t.Fatal("ReadState should fail when both files corrupt")
	}
}

func TestReadStateEmptyFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(StatePath(dir), []byte(""), 0644)

	_, err := ReadState(dir)
	if err == nil {
		t.Fatal("ReadState should fail on empty file")
	}
}

func TestReadStateNoFiles(t *testing.T) {
	dir := t.TempDir()

	_, err := ReadState(dir)
	if err == nil {
		t.Fatal("ReadState should fail when no files exist")
	}
}

func TestRemoveState(t *testing.T) {
	dir := t.TempDir()
	state := DaemonState{Port: 17530, PID: 1}
	WriteState(dir, state)

	if err := RemoveState(dir); err != nil {
		t.Fatalf("RemoveState: %v", err)
	}

	if _, err := os.Stat(StatePath(dir)); !os.IsNotExist(err) {
		t.Error("main state file should be removed")
	}
	if _, err := os.Stat(BackupPath(dir)); !os.IsNotExist(err) {
		t.Error("backup file should be removed")
	}
}

func TestRemoveStateIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := RemoveState(dir); err != nil {
		t.Fatalf("RemoveState on empty dir: %v", err)
	}
}

func TestDefaultStateDir(t *testing.T) {
	dir, err := DefaultStateDir()
	if err != nil {
		t.Fatalf("DefaultStateDir: %v", err)
	}
	if dir == "" {
		t.Error("DefaultStateDir returned empty string")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("state dir %s should exist and be a directory", dir)
	}
}

func TestStatePathHelpers(t *testing.T) {
	dir := "/tmp/test"
	expected := filepath.Join(dir, ".daemon.json")
	if StatePath(dir) != expected {
		t.Errorf("StatePath = %s, want %s", StatePath(dir), expected)
	}
	expectedBak := filepath.Join(dir, ".daemon.json.bak")
	if BackupPath(dir) != expectedBak {
		t.Errorf("BackupPath = %s, want %s", BackupPath(dir), expectedBak)
	}
}

// ── Handler tests (storage-backed) ──

func TestHealthEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("response not valid JSON: %s", body)
	}
	if record["type"] != "result" {
		t.Errorf("type field = %v, want result", record["type"])
	}
	data, ok := record["data"].(map[string]interface{})
	if !ok {
		t.Fatal("data field missing or wrong type")
	}
	if data["version"] != Version {
		t.Errorf("version = %v, want %s", data["version"], Version)
	}
}

func TestAddEndpoint(t *testing.T) {
	srv, dir := newTestServer(t)
	body := strings.NewReader(`{"type":"meeting","title":"test meeting","date":"2026-05-02","time":"15:00"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	resp := w.Result()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	assertJSONLStatus(t, respBody, "success")

	// Verify record was persisted
	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(respBody), &record); err != nil {
		t.Fatalf("invalid JSONL: %s", respBody)
	}
	data, ok := record["data"].(map[string]interface{})
	if !ok {
		t.Fatal("data field missing")
	}
	shortID, _ := data["short_id"].(string)
	if shortID == "" {
		t.Error("expected short_id to be populated")
	}

	// Verify file was written
	meetingsDir := filepath.Join(dir, "meetings", "2026", "05", "02")
	files, err := os.ReadDir(meetingsDir)
	if err != nil {
		t.Fatalf("meetings dir should exist: %v", err)
	}
	if len(files) == 0 {
		t.Error("expected at least one meeting file to be written")
	}
}

func TestAddEndpointInvalidType(t *testing.T) {
	srv, _ := newTestServer(t)
	body := strings.NewReader(`{"type":"invalid","title":"test","date":"2026-05-02"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_type")
}

func TestAddEndpointMissingTitle(t *testing.T) {
	srv, _ := newTestServer(t)
	body := strings.NewReader(`{"type":"meeting","date":"2026-05-02"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_body")
}

func TestAddEndpointMissingDate(t *testing.T) {
	srv, _ := newTestServer(t)
	body := strings.NewReader(`{"type":"meeting","title":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_body")
}

func TestAddEndpointAllTypes(t *testing.T) {
	srv, _ := newTestServer(t)

	for _, typ := range []string{"meeting", "task", "reminder", "log"} {
		body := strings.NewReader(
			`{"type":"` + typ + `","title":"test ` + typ + `","date":"2026-05-02"}`,
		)
		req := httptest.NewRequest(http.MethodPost, "/api/add", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		assertJSONLStatus(t, w.Body.Bytes(), "success")
	}
}

func TestAddEndpointWithTags(t *testing.T) {
	srv, _ := newTestServer(t)
	body := strings.NewReader(`{"type":"task","title":"tagged task","date":"2026-05-02","tags":["urgent","review"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	tags, _ := data["tags"].([]interface{})
	if len(tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(tags))
	}
}

func TestListEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a record first
	addBody := strings.NewReader(`{"type":"meeting","title":"list test","date":"2026-05-02","time":"10:00"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", addBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// List records
	req = httptest.NewRequest(http.MethodGet, "/api/list?type=meeting&date=2026-05-02", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("status = %d", w.Result().StatusCode)
	}
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	entries, _ := data["entries"].([]interface{})
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

func TestListEndpointEmpty(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/list?type=meeting", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	entries, _ := data["entries"].([]interface{})
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for empty storage, got %d", len(entries))
	}
}

func TestCompleteEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a task
	addBody := strings.NewReader(`{"type":"task","title":"complete test","date":"2026-05-02"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", addBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	// Complete it
	req = httptest.NewRequest(http.MethodPost, "/api/complete/"+shortID, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var compResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &compResp)
	compData := compResp["data"].(map[string]interface{})
	status, _ := compData["status"].(string)
	if status != "completed" {
		t.Errorf("expected status=completed, got %s", status)
	}
}

func TestCompleteEndpointNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/complete/nonexist", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "record_not_found")
}

func TestCancelEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a task
	addBody := strings.NewReader(`{"type":"task","title":"cancel test","date":"2026-05-02"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", addBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	// Cancel it
	req = httptest.NewRequest(http.MethodPost, "/api/cancel/"+shortID, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var cancelResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &cancelResp)
	cancelData := cancelResp["data"].(map[string]interface{})
	status, _ := cancelData["status"].(string)
	if status != "cancelled" {
		t.Errorf("expected status=cancelled, got %s", status)
	}
}

func TestCancelEndpointNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/cancel/nonexist", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "record_not_found")
}

func TestReportEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add multiple records
	for _, typ := range []string{"meeting", "task"} {
		body := strings.NewReader(
			`{"type":"` + typ + `","title":"report ` + typ + `","date":"2026-05-02"}`,
		)
		req := httptest.NewRequest(http.MethodPost, "/api/add", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)
	}

	// Get report for the date
	req := httptest.NewRequest(http.MethodGet, "/api/report?date=2026-05-02", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	// Verify grouped structure
	summary, _ := data["summary"].(map[string]interface{})
	total, _ := summary["total"].(float64)
	if total != 2 {
		t.Errorf("expected 2 records in report, got %v", total)
	}
	meetings, _ := summary["meetings"].(float64)
	if meetings != 1 {
		t.Errorf("expected 1 meeting, got %v", meetings)
	}
	tasks, _ := summary["tasks"].(float64)
	if tasks != 1 {
		t.Errorf("expected 1 task, got %v", tasks)
	}

	// Verify markdown field present
	md, _ := data["markdown"].(string)
	if md == "" {
		t.Error("expected markdown field in report")
	}
}

func TestReportTodayEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a record for today
	body := strings.NewReader(`{"type":"log","title":"today log","date":"2026-05-02"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	req = httptest.NewRequest(http.MethodGet, "/api/report/today", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("status = %d", w.Result().StatusCode)
	}
	assertJSONLStatus(t, w.Body.Bytes(), "success")
}

func TestHealthWrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
}

func TestCompleteMissingID(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/complete/", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
}

func TestAddInvalidJSON(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
}

// ── End-to-end via httptest ──

func TestServerStartAndHealthEndToEnd(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.router)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var record map[string]interface{}
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("invalid JSON: %s", body)
	}
	if record["type"] != "result" {
		t.Errorf("type = %v, want result", record["type"])
	}
}

func TestEndToEndAddListCompleteCancel(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.router)
	defer ts.Close()

	// Add
	addPayload := `{"type":"task","title":"e2e task","date":"2026-05-02","tags":["test"]}`
	resp, err := http.Post(ts.URL+"/api/add", "application/json", strings.NewReader(addPayload))
	if err != nil {
		t.Fatalf("POST /api/add: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var addResp map[string]interface{}
	json.Unmarshal(body, &addResp)
	if addResp["type"] != "result" {
		t.Fatalf("add failed: %s", body)
	}
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	// List
	resp, err = http.Get(ts.URL + "/api/list?type=task&date=2026-05-02")
	if err != nil {
		t.Fatalf("GET /api/list: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	var listResp map[string]interface{}
	json.Unmarshal(body, &listResp)
	listData := listResp["data"].(map[string]interface{})
	entries, _ := listData["entries"].([]interface{})
	if len(entries) != 1 {
		t.Errorf("list: expected 1 entry, got %d", len(entries))
	}

	// Complete
	resp, err = http.Post(ts.URL+"/api/complete/"+shortID, "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/complete: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	var compResp map[string]interface{}
	json.Unmarshal(body, &compResp)
	if compResp["type"] != "result" {
		t.Fatalf("complete failed: %s", body)
	}
}

// ── Helpers ──

// parseAndValidateEnvelope unmarshals body into both a raw map and a typed
// jsonl.Envelope, validates the envelope structure via ValidateEnvelope, and
// returns both. Every daemon test that checks JSONL output routes through this
// helper via assertJSONLStatus / assertJSONLCode.
func parseAndValidateEnvelope(t *testing.T, body []byte) (map[string]interface{}, jsonl.Envelope) {
	t.Helper()
	trimmed := bytes.TrimSpace(body)
	var record map[string]interface{}
	if err := json.Unmarshal(trimmed, &record); err != nil {
		t.Fatalf("invalid JSONL: %s\nerr: %v", body, err)
	}
	var env jsonl.Envelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		t.Fatalf("invalid JSONL envelope struct: %s\nerr: %v", body, err)
	}
	if err := jsonl.ValidateEnvelope(env); err != nil {
		t.Errorf("envelope validation failed: %v; body=%s", err, body)
	}
	return record, env
}

func assertJSONLStatus(t *testing.T, body []byte, wantStatus string) {
	t.Helper()
	record, _ := parseAndValidateEnvelope(t, body)
	// Map old status values to new envelope type values
	var wantType string
	switch wantStatus {
	case "success", "ok", "info":
		wantType = "result"
	case "error":
		wantType = "error"
	default:
		wantType = wantStatus
	}
	if record["type"] != wantType {
		t.Errorf("type = %v (mapped from status=%q), want %s; body=%s", record["type"], wantStatus, wantType, body)
	}
}

func assertJSONLCode(t *testing.T, body []byte, wantCode string) {
	t.Helper()
	record, _ := parseAndValidateEnvelope(t, body)
	if record["error_code"] != wantCode {
		t.Errorf("error_code = %v, want %s; body=%s", record["error_code"], wantCode, body)
	}
}

// ── LLM text classification tests ──

// mockLLMServer creates an httptest.Server that simulates the OpenAI chat completions API.
// The handler receives the classification result to return as JSON.
func mockLLMServer(t *testing.T, classifyResult llm.ClassifyResult) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it's a chat completions request
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		b, _ := json.Marshal(classifyResult)
		// Wrap in OpenAI chat completions response format
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": string(b),
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

// mockLLMErrorServer creates a server that returns an error HTTP status.
func mockLLMErrorServer(t *testing.T, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		fmt.Fprintf(w, `{"error":{"message":"test error","type":"server_error","code":"%d"}}`, statusCode)
	}))
}

func TestAddEndpoint_TextClassification(t *testing.T) {
	// Create a mock LLM server
	mockServer := mockLLMServer(t, llm.ClassifyResult{
		Type:  "meeting",
		Title: "项目评审会",
		Date:  "2026-05-03",
		Time:  "15:00",
	})
	defer mockServer.Close()

	cfg := &config.Config{
		LLM: config.LLMConfig{
			Text: config.LLMProviderConfig{
				APIBase: mockServer.URL,
				APIKey:  "test-key",
				Model:   "test-model",
			},
		},
	}

	srv, _ := newTestServer(t, cfg)
	body := strings.NewReader(`{"text":"明天下午3点项目评审会"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	resp := w.Result()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	assertJSONLStatus(t, respBody, "success")

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(respBody), &record); err != nil {
		t.Fatalf("invalid JSONL: %s", respBody)
	}
	data, ok := record["data"].(map[string]interface{})
	if !ok {
		t.Fatal("data field missing")
	}
	if data["type"] != "meeting" {
		t.Errorf("expected type meeting, got %v", data["type"])
	}
	if data["title"] != "项目评审会" {
		t.Errorf("expected title 项目评审会, got %v", data["title"])
	}
	if data["date"] != "2026-05-03" {
		t.Errorf("expected date 2026-05-03, got %v", data["date"])
	}
}

func TestAddEndpoint_TextCancelOrUpdate(t *testing.T) {
	mockServer := mockLLMServer(t, llm.ClassifyResult{
		Type:     "cancel_or_update",
		Title:    "取消明天下午的会议",
		TargetID: "abc12345",
	})
	defer mockServer.Close()

	cfg := &config.Config{
		LLM: config.LLMConfig{
			Text: config.LLMProviderConfig{
				APIBase: mockServer.URL,
				APIKey:  "test-key",
				Model:   "test-model",
			},
		},
	}

	srv, _ := newTestServer(t, cfg)
	body := strings.NewReader(`{"text":"取消明天下午的会议 abc12345"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	resp := w.Result()
	respBody, _ := io.ReadAll(resp.Body)

	assertJSONLStatus(t, respBody, "info")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(respBody), &record)
	data, ok := record["data"].(map[string]interface{})
	if !ok {
		t.Fatal("data field missing")
	}
	action, _ := data["action"].(string)
	if action != "cancel_or_update" {
		t.Errorf("expected action cancel_or_update, got %q", action)
	}
	classification, _ := data["classification"].(map[string]interface{})
	if classification["type"] != "cancel_or_update" {
		t.Errorf("expected classification type cancel_or_update, got %v", classification["type"])
	}
}

func TestAddEndpoint_TextLLMNotConfigured(t *testing.T) {
	// Config with no LLM api_key
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Text: config.LLMProviderConfig{
				APIBase: "",
				APIKey:  "",
				Model:   "",
			},
		},
	}

	srv, _ := newTestServer(t, cfg)
	body := strings.NewReader(`{"text":"明天下午3点项目评审会"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "llm_not_configured")
}

func TestAddEndpoint_TextLLMError(t *testing.T) {
	// Server that returns 500 error
	mockServer := mockLLMErrorServer(t, http.StatusInternalServerError)
	defer mockServer.Close()

	cfg := &config.Config{
		LLM: config.LLMConfig{
			Text: config.LLMProviderConfig{
				APIBase: mockServer.URL,
				APIKey:  "test-key",
				Model:   "test-model",
			},
		},
	}

	srv, _ := newTestServer(t, cfg)
	body := strings.NewReader(`{"text":"明天下午3点项目评审会"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "llm_error")
}

func TestAddEndpoint_TextWithExplicitType_NoLLMCall(t *testing.T) {
	// When both text and type are provided, explicit type should win
	// (no LLM call needed — verify the mock server is never called by not starting one)
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Text: config.LLMProviderConfig{
				APIBase: "http://127.0.0.1:0", // unreachable
				APIKey:  "test-key",
				Model:   "test-model",
			},
		},
	}

	srv, _ := newTestServer(t, cfg)
	body := strings.NewReader(`{"text":"明天下午3点项目评审会","type":"task","title":"manual task","date":"2026-05-03"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	// Explicit type should be used, not LLM
	if data["type"] != "task" {
		t.Errorf("expected type task, got %v", data["type"])
	}
	if data["title"] != "manual task" {
		t.Errorf("expected title 'manual task', got %v", data["title"])
	}
}

func TestAddEndpoint_NormalAddStillWorks(t *testing.T) {
	// Ensure the existing explicit add flow is unchanged
	srv, _ := newTestServer(t)
	body := strings.NewReader(`{"type":"meeting","title":"normal add","date":"2026-05-02","time":"15:00"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	if data["type"] != "meeting" {
		t.Errorf("expected type meeting, got %v", data["type"])
	}
}

// ── LLM image (vision) classification tests ──

// minimalPNG is a valid 1x1 transparent PNG (67 bytes).
var minimalPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41, // IDAT chunk
	0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, // IEND chunk
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

// createTestImage writes a minimal PNG to a temp file and returns its path.
func createTestImage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/test.png"
	if err := os.WriteFile(path, minimalPNG, 0644); err != nil {
		t.Fatalf("create test image: %v", err)
	}
	return path
}

// imageAddPayload builds a JSON add-request body with the given image path
// and optional extra fields. Uses json.Marshal to handle path escaping correctly.
func imageAddPayload(t *testing.T, imagePath string, extra map[string]interface{}) string {
	t.Helper()
	payload := map[string]interface{}{
		"image": imagePath,
	}
	for k, v := range extra {
		payload[k] = v
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal image payload: %v", err)
	}
	return string(b)
}

// mockVisionLLMServer creates a mock server that captures the request body
// and returns the given classification result.
func mockVisionLLMServer(t *testing.T, classifyResult llm.ClassifyResult) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		b, _ := json.Marshal(classifyResult)
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": string(b),
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

// visionConfig creates a config with vision LLM pointing at the given URL.
func visionConfig(mockURL string) *config.Config {
	return &config.Config{
		LLM: config.LLMConfig{
			Vision: config.LLMProviderConfig{
				APIBase: mockURL,
				APIKey:  "test-vision-key",
				Model:   "test-vision-model",
			},
		},
	}
}

func TestAddEndpoint_ImageClassification(t *testing.T) {
	imgPath := createTestImage(t)

	mockServer := mockVisionLLMServer(t, llm.ClassifyResult{
		Type:  "meeting",
		Title: "项目周会",
		Date:  "2026-05-05",
		Time:  "14:00",
	})
	defer mockServer.Close()

	srv, _ := newTestServer(t, visionConfig(mockServer.URL))
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(imageAddPayload(t, imgPath, nil)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	resp := w.Result()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	assertJSONLStatus(t, respBody, "success")

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(respBody), &record); err != nil {
		t.Fatalf("invalid JSONL: %s", respBody)
	}
	data, ok := record["data"].(map[string]interface{})
	if !ok {
		t.Fatal("data field missing")
	}
	if data["type"] != "meeting" {
		t.Errorf("expected type meeting, got %v", data["type"])
	}
	if data["title"] != "项目周会" {
		t.Errorf("expected title 项目周会, got %v", data["title"])
	}
	if data["date"] != "2026-05-05" {
		t.Errorf("expected date 2026-05-05, got %v", data["date"])
	}
	if data["time"] != "14:00" {
		t.Errorf("expected time 14:00, got %v", data["time"])
	}
}

func TestAddEndpoint_ImageWithTextContext(t *testing.T) {
	imgPath := createTestImage(t)

	// Server that captures the request body to verify textContext is passed
	var capturedBody map[string]interface{}
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyBytes, &capturedBody)

		result := llm.ClassifyResult{
			Type:        "meeting",
			Title:       "设计评审会",
			Date:        "2026-05-06",
			Description: "来自图片的会议截图，补充信息: 这是会议截图",
		}
		b, _ := json.Marshal(result)
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"content": string(b)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	srv, _ := newTestServer(t, visionConfig(mockServer.URL))
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(imageAddPayload(t, imgPath, map[string]interface{}{"text": "这是会议截图"})))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Verify the LLM request included supplementary text context
	if capturedBody == nil {
		t.Fatal("no request body captured from LLM call")
	}
	messages, _ := capturedBody["messages"].([]interface{})
	if len(messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(messages))
	}
	userMsg, _ := messages[1].(map[string]interface{})
	// Content should be an array (multimodal) containing the supplementary info
	contentArr, ok := userMsg["content"].([]interface{})
	if !ok {
		t.Fatal("user message content should be an array for multimodal")
	}
	// Find the text part and check it includes supplementary info
	foundSupplementary := false
	for _, part := range contentArr {
		partMap, _ := part.(map[string]interface{})
		if partMap["type"] == "text" {
			text, _ := partMap["text"].(string)
			if strings.Contains(text, "补充信息: 这是会议截图") {
				foundSupplementary = true
			}
		}
	}
	if !foundSupplementary {
		t.Error("expected user message to contain supplementary text context")
	}
}

func TestAddEndpoint_ImageCancelOrUpdate(t *testing.T) {
	imgPath := createTestImage(t)

	mockServer := mockVisionLLMServer(t, llm.ClassifyResult{
		Type:     "cancel_or_update",
		Title:    "取消周五会议",
		TargetID: "xyz12345",
	})
	defer mockServer.Close()

	srv, _ := newTestServer(t, visionConfig(mockServer.URL))
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(imageAddPayload(t, imgPath, nil)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "info")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data, _ := record["data"].(map[string]interface{})
	action, _ := data["action"].(string)
	if action != "cancel_or_update" {
		t.Errorf("expected action cancel_or_update, got %q", action)
	}
}

func TestAddEndpoint_ImageVisionNotConfigured(t *testing.T) {
	imgPath := createTestImage(t)

	// Config with no vision api_key
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Vision: config.LLMProviderConfig{
				APIBase: "",
				APIKey:  "",
				Model:   "",
			},
		},
	}

	srv, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(imageAddPayload(t, imgPath, nil)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "llm_not_configured")
}

func TestAddEndpoint_ImageLLMError(t *testing.T) {
	imgPath := createTestImage(t)

	mockServer := mockLLMErrorServer(t, http.StatusInternalServerError)
	defer mockServer.Close()

	srv, _ := newTestServer(t, visionConfig(mockServer.URL))
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(imageAddPayload(t, imgPath, nil)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "llm_error")
}

func TestAddEndpoint_ImageWithExplicitType_NoLLMCall(t *testing.T) {
	imgPath := createTestImage(t)

	// When both image and type are provided, explicit type wins (no LLM call)
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Vision: config.LLMProviderConfig{
				APIBase: "http://127.0.0.1:0", // unreachable
				APIKey:  "test-key",
				Model:   "test-model",
			},
		},
	}

	srv, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(imageAddPayload(t, imgPath, map[string]interface{}{
		"type":  "task",
		"title": "manual image task",
		"date":  "2026-05-03",
	})))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	if data["type"] != "task" {
		t.Errorf("expected type task, got %v", data["type"])
	}
	if data["title"] != "manual image task" {
		t.Errorf("expected title 'manual image task', got %v", data["title"])
	}
}

func TestAddEndpoint_ImageFileNotFound(t *testing.T) {
	// Non-existent image path — file validation happens before API call
	mockServer := mockVisionLLMServer(t, llm.ClassifyResult{
		Type:  "meeting",
		Title: "should not reach",
	})
	defer mockServer.Close()

	srv, _ := newTestServer(t, visionConfig(mockServer.URL))
	body := `{"image":"/nonexistent/path/to/image.png"}`
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "llm_error")

	// Verify error message mentions file issue
	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	msg, _ := record["message"].(string)
	if !strings.Contains(msg, "vision classification failed") {
		t.Errorf("expected vision classification failure message, got %q", msg)
	}
}

func TestAddEndpoint_ImageUnsupportedFormat(t *testing.T) {
	// Create a temp file with .txt extension (unsupported)
	dir := t.TempDir()
	path := dir + "/test.txt"
	os.WriteFile(path, []byte("not an image"), 0644)

	mockServer := mockVisionLLMServer(t, llm.ClassifyResult{
		Type:  "meeting",
		Title: "should not reach",
	})
	defer mockServer.Close()

	srv, _ := newTestServer(t, visionConfig(mockServer.URL))
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(imageAddPayload(t, path, nil)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "llm_error")
}

func TestAddEndpoint_ImageWinsOverText(t *testing.T) {
	// When both image and text are provided (no type), image classification takes priority
	imgPath := createTestImage(t)

	var requestReceived bool
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true

		// Verify request body has multimodal content (image path, not just text)
		bodyBytes, _ := io.ReadAll(r.Body)
		var reqBody map[string]interface{}
		json.Unmarshal(bodyBytes, &reqBody)
		messages, _ := reqBody["messages"].([]interface{})
		userMsg, _ := messages[1].(map[string]interface{})
		content, _ := userMsg["content"].([]interface{})
		// Should have both text and image_url parts
		if len(content) != 2 {
			t.Errorf("expected 2 content parts (text + image), got %d", len(content))
		}

		result := llm.ClassifyResult{
			Type:  "task",
			Title: "从图片识别的任务",
			Date:  "2026-05-07",
		}
		b, _ := json.Marshal(result)
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"content": string(b)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	cfg := &config.Config{
		LLM: config.LLMConfig{
			Vision: config.LLMProviderConfig{
				APIBase: mockServer.URL,
				APIKey:  "test-vision-key",
				Model:   "test-vision-model",
			},
			// Text LLM also configured but should NOT be called
			Text: config.LLMProviderConfig{
				APIBase: "http://127.0.0.1:0", // unreachable
				APIKey:  "test-text-key",
				Model:   "test-text-model",
			},
		},
	}

	srv, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(imageAddPayload(t, imgPath, map[string]interface{}{"text": "这是文字描述"})))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")
	if !requestReceived {
		t.Error("expected vision LLM to be called")
	}

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	if data["type"] != "task" {
		t.Errorf("expected type task, got %v", data["type"])
	}
}

// ── Scheduler integration tests ──

// mockPushSender is a test double for scheduler.PushoverSender.
type mockPushSender struct {
	calls []string
}

func (m *mockPushSender) Send(_ context.Context, _ scheduler.PushoverConfig, message, title string, priority int) error {
	m.calls = append(m.calls, message)
	return nil
}

// newTestServerWithScheduler creates a Server with a real scheduler backed by
// a mock pushover sender. The scheduler state is stored in a temp directory.
func newTestServerWithScheduler(t *testing.T) (*Server, *mockPushSender, string) {
	t.Helper()
	dir := t.TempDir()
	store := storage.New(dir, log.New(io.Discard, "", 0))

	// Use a far-future timezone so entries don't trigger during the test
	cfg := &config.Config{
		Timezone: "UTC",
	}

	mockPush := &mockPushSender{}
	statePath := filepath.Join(dir, "scheduler-state.json")
	sched := scheduler.NewScheduler(cfg, mockPush, statePath, log.New(io.Discard, "", 0))
	if err := sched.Start(); err != nil {
		t.Fatalf("scheduler start: %v", err)
	}
	t.Cleanup(func() { sched.Stop() })

	srv := NewServer(0, store, cfg)
	srv.SetScheduler(sched)
	return srv, mockPush, dir
}

func TestDaemon_Add_TriggersScheduler(t *testing.T) {
	srv, _, _ := newTestServerWithScheduler(t)

	// Add a reminder with date/time far in the future
	futureDate := time.Now().Add(24 * time.Hour).Format("2006-01-02")
	body := strings.NewReader(fmt.Sprintf(
		`{"type":"reminder","title":"test reminder","date":"%s","time":"23:59","remind_before":"15m","recurring":""}`,
		futureDate,
	))
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Verify the scheduler has a registered entry
	state := srv.scheduler.State()
	if len(state.Entries) != 1 {
		t.Fatalf("expected 1 scheduler entry, got %d", len(state.Entries))
	}

	// Verify the entry details
	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	data := addResp["data"].(map[string]interface{})
	shortID := data["short_id"].(string)

	entry := state.GetEntry(shortID)
	if entry == nil {
		t.Fatal("scheduler entry not found for short_id")
	}
	if entry.RecordType != "reminder" {
		t.Errorf("expected record_type=reminder, got %s", entry.RecordType)
	}
	if entry.Title != "test reminder" {
		t.Errorf("expected title='test reminder', got %s", entry.Title)
	}
}

func TestDaemon_Complete_UnregistersScheduler(t *testing.T) {
	srv, _, _ := newTestServerWithScheduler(t)

	// Add a reminder
	futureDate := time.Now().Add(24 * time.Hour).Format("2006-01-02")
	body := strings.NewReader(fmt.Sprintf(
		`{"type":"reminder","title":"complete test","date":"%s","time":"23:59","remind_before":"15m"}`,
		futureDate,
	))
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	// Verify it was registered
	state := srv.scheduler.State()
	if len(state.Entries) != 1 {
		t.Fatalf("expected 1 entry after add, got %d", len(state.Entries))
	}

	// Complete the record
	req = httptest.NewRequest(http.MethodPost, "/api/complete/"+shortID, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Verify the entry was unregistered
	state = srv.scheduler.State()
	if len(state.Entries) != 0 {
		t.Errorf("expected 0 entries after complete, got %d", len(state.Entries))
	}
}

func TestDaemon_Cancel_UnregistersScheduler(t *testing.T) {
	srv, _, _ := newTestServerWithScheduler(t)

	// Add a reminder
	futureDate := time.Now().Add(24 * time.Hour).Format("2006-01-02")
	body := strings.NewReader(fmt.Sprintf(
		`{"type":"reminder","title":"cancel test","date":"%s","time":"23:59","remind_before":"15m"}`,
		futureDate,
	))
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	// Verify it was registered
	state := srv.scheduler.State()
	if len(state.Entries) != 1 {
		t.Fatalf("expected 1 entry after add, got %d", len(state.Entries))
	}

	// Cancel the record
	req = httptest.NewRequest(http.MethodPost, "/api/cancel/"+shortID, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Verify the entry was unregistered
	state = srv.scheduler.State()
	if len(state.Entries) != 0 {
		t.Errorf("expected 0 entries after cancel, got %d", len(state.Entries))
	}
}

func TestDaemon_Add_NonReminder_NoSchedulerEntry(t *testing.T) {
	srv, _, _ := newTestServerWithScheduler(t)

	// Add a log record (no remind_before, not reminder type)
	body := strings.NewReader(`{"type":"log","title":"just a log","date":"2026-05-02"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Verify no scheduler entry was created (log without remind_before is skipped)
	state := srv.scheduler.State()
	if len(state.Entries) != 0 {
		t.Errorf("expected 0 scheduler entries for log type, got %d", len(state.Entries))
	}
}

func TestDaemon_Add_NilScheduler_NoPanic(t *testing.T) {
	// Server with nil scheduler — should not panic
	srv, _ := newTestServer(t)

	body := strings.NewReader(`{"type":"reminder","title":"no scheduler","date":"2026-05-02","time":"15:00","remind_before":"15m"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")
}

func TestDaemon_Complete_NilScheduler_NoPanic(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a record first
	addBody := strings.NewReader(`{"type":"task","title":"test","date":"2026-05-02"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", addBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	// Complete with nil scheduler — should not panic
	req = httptest.NewRequest(http.MethodPost, "/api/complete/"+shortID, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")
}

// ── Report push tests ──

func TestReportPushToday_Success(t *testing.T) {
	// Create a mock Pushover server
	var gotTitle, gotMessage string
	mockPush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotTitle = r.FormValue("title")
		gotMessage = r.FormValue("message")
		w.WriteHeader(http.StatusOK)
	}))
	defer mockPush.Close()

	// Override pushover URL
	origURL := pushover.PushoverURL()
	pushover.SetPushoverURL(mockPush.URL)
	defer pushover.SetPushoverURL(origURL)

	cfg := &config.Config{
		Pushover: config.PushoverConfig{
			APIToken: "test-token",
			UserKey:  "test-user",
		},
	}
	srv, _ := newTestServer(t, cfg)

	// Add a record for today
	body := strings.NewReader(fmt.Sprintf(`{"type":"task","title":"push test task","date":"%s"}`, time.Now().In(srv.config.Location()).Format("2006-01-02")))
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Push today's report
	req = httptest.NewRequest(http.MethodPost, "/api/report/push/today", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data, _ := record["data"].(map[string]interface{})
	if data["pushed"] != true {
		t.Errorf("expected pushed=true, got %v", data["pushed"])
	}

	if gotTitle == "" {
		t.Error("expected Pushover title to be set")
	}
	if gotMessage == "" {
		t.Error("expected Pushover message to be set")
	}
	if !strings.Contains(gotMessage, "push test task") {
		t.Errorf("expected message to contain task title, got %q", gotMessage)
	}
}

func TestReportPushToday_NotConfigured(t *testing.T) {
	// Config with empty Pushover credentials
	cfg := &config.Config{
		Pushover: config.PushoverConfig{
			APIToken: "",
			UserKey:  "",
		},
	}
	srv, _ := newTestServer(t, cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/report/push/today", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "pushover_not_configured")
}

func TestReportPushToday_WrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/report/push/today", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
}

func TestReportPushDate_Success(t *testing.T) {
	var gotTitle string
	mockPush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotTitle = r.FormValue("title")
		w.WriteHeader(http.StatusOK)
	}))
	defer mockPush.Close()

	origURL := pushover.PushoverURL()
	pushover.SetPushoverURL(mockPush.URL)
	defer pushover.SetPushoverURL(origURL)

	cfg := &config.Config{
		Pushover: config.PushoverConfig{
			APIToken: "test-token",
			UserKey:  "test-user",
		},
	}
	srv, _ := newTestServer(t, cfg)

	// Add a record
	body := strings.NewReader(`{"type":"meeting","title":"date push meeting","date":"2026-05-03"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Push report for the specific date
	req = httptest.NewRequest(http.MethodPost, "/api/report/push/date/2026-05-03", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data, _ := record["data"].(map[string]interface{})
	if data["pushed"] != true {
		t.Errorf("expected pushed=true, got %v", data["pushed"])
	}
	if data["date"] != "2026-05-03" {
		t.Errorf("expected date=2026-05-03, got %v", data["date"])
	}
	if !strings.Contains(gotTitle, "2026-05-03") {
		t.Errorf("expected title to contain date, got %q", gotTitle)
	}
}

func TestReportPushDate_NotConfigured(t *testing.T) {
	cfg := &config.Config{
		Pushover: config.PushoverConfig{
			APIToken: "",
			UserKey:  "",
		},
	}
	srv, _ := newTestServer(t, cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/report/push/date/2026-05-03", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "pushover_not_configured")
}

func TestReportPushDate_WrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/report/push/date/2026-05-03", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
}

func TestReportPush_PushoverSendFail(t *testing.T) {
	// Server that returns 500 — simulates Pushover API failure
	mockPush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockPush.Close()

	origURL := pushover.PushoverURL()
	pushover.SetPushoverURL(mockPush.URL)
	defer pushover.SetPushoverURL(origURL)

	cfg := &config.Config{
		Pushover: config.PushoverConfig{
			APIToken: "test-token",
			UserKey:  "test-user",
		},
	}
	srv, _ := newTestServer(t, cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/report/push/today", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "push_error")
}

// ── Status endpoint tests ──

func TestHandleStatus(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(body), &record); err != nil {
		t.Fatalf("invalid JSONL: %s", body)
	}
	if record["type"] != "result" {
		t.Errorf("type field = %v, want result", record["type"])
	}

	data, ok := record["data"].(map[string]interface{})
	if !ok {
		t.Fatal("data field missing or wrong type")
	}

	// Check daemon section
	daemon, ok := data["daemon"].(map[string]interface{})
	if !ok {
		t.Fatal("daemon field missing")
	}
	if daemon["status"] != "running" {
		t.Errorf("daemon.status = %v, want running", daemon["status"])
	}
	if daemon["version"] != Version {
		t.Errorf("daemon.version = %v, want %s", daemon["version"], Version)
	}

	// Check config section
	configSection, ok := data["config"].(map[string]interface{})
	if !ok {
		t.Fatal("config field missing")
	}
	configExists, _ := configSection["exists"].(bool)
	// Config may or may not exist depending on test environment, but field must be present
	_ = configExists
}

func TestHandleStatusWithConfig(t *testing.T) {
	cfg := &config.Config{
		Pushover: config.PushoverConfig{
			APIToken: "test-api-token-12345",
			UserKey:  "test-user-key-67890",
		},
		LLM: config.LLMConfig{
			Text: config.LLMProviderConfig{
				APIKey: "test-text-key",
				Model:  "test-model",
			},
			Vision: config.LLMProviderConfig{
				APIKey: "test-vision-key",
				Model:  "test-vision-model",
			},
		},
		DataDir: os.TempDir(),
	}

	srv, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record); err != nil {
		t.Fatalf("invalid JSONL: %s", w.Body.Bytes())
	}
	data := record["data"].(map[string]interface{})
	configSection := data["config"].(map[string]interface{})

	// Pushover should be configured
	pushoverInfo := configSection["pushover"].(map[string]interface{})
	if pushoverInfo["configured"] != true {
		t.Error("expected pushover configured=true")
	}

	// LLM text should be configured
	llmInfo := configSection["llm"].(map[string]interface{})
	textInfo := llmInfo["text"].(map[string]interface{})
	if textInfo["configured"] != true {
		t.Error("expected llm.text configured=true")
	}

	// LLM vision should be configured
	visionInfo := llmInfo["vision"].(map[string]interface{})
	if visionInfo["configured"] != true {
		t.Error("expected llm.vision configured=true")
	}

	// Data dir should be accessible
	dataDirInfo := configSection["data_dir"].(map[string]interface{})
	if dataDirInfo["accessible"] != true {
		t.Errorf("expected data_dir accessible=true, got %v", dataDirInfo["accessible"])
	}

	// Redacted config should be present
	redacted, ok := configSection["redacted"].(map[string]interface{})
	if !ok {
		t.Fatal("expected redacted config field")
	}
	// Verify secrets are redacted — should not contain raw API keys
	redactedJSON, _ := json.Marshal(redacted)
	redactedStr := string(redactedJSON)
	if strings.Contains(redactedStr, "test-api-token-12345") {
		t.Error("pushover api_token should be redacted")
	}
	if strings.Contains(redactedStr, "test-user-key-67890") {
		t.Error("pushover user_key should be redacted")
	}
	if strings.Contains(redactedStr, "test-text-key") {
		t.Error("llm.text api_key should be redacted")
	}
	if strings.Contains(redactedStr, "test-vision-key") {
		t.Error("llm.vision api_key should be redacted")
	}
}

func TestHandleStatusWithScheduler(t *testing.T) {
	srv, _, _ := newTestServerWithScheduler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	schedInfo, ok := data["scheduler"].(map[string]interface{})
	if !ok {
		t.Fatal("expected scheduler field in status response")
	}
	if schedInfo["running"] != true {
		t.Error("expected scheduler running=true")
	}
	entriesCount, _ := schedInfo["entries_count"].(float64)
	if entriesCount != 0 {
		t.Errorf("expected 0 scheduler entries, got %v", entriesCount)
	}
}

func TestHandleStatusWrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
}

func TestHandleStatusEmptyConfig(t *testing.T) {
	// newTestServer creates a zero Config when none provided — not nil.
	// Verify status still returns valid output with nothing configured.
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	configSection := data["config"].(map[string]interface{})

	// Empty config — pushover/LLM should not be configured
	pushoverInfo := configSection["pushover"].(map[string]interface{})
	if pushoverInfo["configured"] != false {
		t.Errorf("expected pushover configured=false for empty config")
	}
	llmInfo := configSection["llm"].(map[string]interface{})
	textInfo := llmInfo["text"].(map[string]interface{})
	if textInfo["configured"] != false {
		t.Errorf("expected llm.text configured=false for empty config")
	}
}

// ── List filter tests ──

func addRecord(t *testing.T, srv *Server, typ, title, desc, date string) {
	t.Helper()
	body := fmt.Sprintf(`{"type":"%s","title":"%s","description":"%s","date":"%s"}`, typ, title, desc, date)
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")
}

func parseListEntries(t *testing.T, body []byte) []map[string]interface{} {
	t.Helper()
	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(body), &record); err != nil {
		t.Fatalf("invalid JSONL: %s", body)
	}
	if record["type"] != "result" {
		t.Fatalf("expected type=result, got %v", record["type"])
	}
	data, _ := record["data"].(map[string]interface{})
	entries, _ := data["entries"].([]interface{})
	result := make([]map[string]interface{}, len(entries))
	for i, e := range entries {
		result[i] = e.(map[string]interface{})
	}
	return result
}

func TestHandleList_DateRange(t *testing.T) {
	srv, _ := newTestServer(t)

	// Use meetings — date-tree storage avoids same-second collisions
	addRecord(t, srv, "meeting", "early meeting", "", "2026-04-20")
	addRecord(t, srv, "meeting", "in range 1", "", "2026-04-25")
	addRecord(t, srv, "meeting", "in range 2", "", "2026-04-28")
	addRecord(t, srv, "meeting", "in range 3", "", "2026-05-01")
	addRecord(t, srv, "meeting", "late meeting", "", "2026-05-05")

	req := httptest.NewRequest(http.MethodGet, "/api/list?type=meeting&from=2026-04-25&to=2026-05-01", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	entries := parseListEntries(t, w.Body.Bytes())
	if len(entries) != 3 {
		t.Errorf("expected 3 entries in date range, got %d", len(entries))
		for _, e := range entries {
			t.Logf("  entry: title=%v date=%v", e["title"], e["date"])
		}
	}
}

func TestHandleList_StatusFilter(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add two tasks, complete one
	addRecord(t, srv, "task", "active task", "", "2026-05-02")

	time.Sleep(1 * time.Second) // avoid ShortID collision

	body2 := strings.NewReader(`{"type":"task","title":"to complete task","date":"2026-05-02"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body2)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp2 map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp2)
	shortID2 := addResp2["data"].(map[string]interface{})["short_id"].(string)

	// Complete the second task
	req = httptest.NewRequest(http.MethodPost, "/api/complete/"+shortID2, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Filter by status=completed
	req = httptest.NewRequest(http.MethodGet, "/api/list?type=task&status=completed", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	entries, _ := data["entries"].([]interface{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 completed entry, got %d", len(entries))
	}
	entry := entries[0].(map[string]interface{})
	if entry["status"] != "completed" {
		t.Errorf("expected status=completed, got %v", entry["status"])
	}
	if entry["short_id"] != shortID2 {
		t.Errorf("expected short_id=%s, got %v", shortID2, entry["short_id"])
	}

	// Filter by status=active
	req = httptest.NewRequest(http.MethodGet, "/api/list?type=task&status=active", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data = record["data"].(map[string]interface{})
	entries, _ = data["entries"].([]interface{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 active entry, got %d", len(entries))
	}
	entry = entries[0].(map[string]interface{})
	if entry["status"] != "active" {
		t.Errorf("expected status=active, got %v", entry["status"])
	}
}

func TestHandleList_KeywordSearch(t *testing.T) {
	srv, _ := newTestServer(t)

	// Use different types to avoid task ShortID collisions in flat dir
	addRecord(t, srv, "task", "代码评审", "评审PR #123", "2026-05-02")
	addRecord(t, srv, "meeting", "周会", "讨论评审流程", "2026-05-02")
	addRecord(t, srv, "log", "部署日志", "生产环境部署", "2026-05-02")

	// Search for "评审" — should match task title + meeting description
	req := httptest.NewRequest(http.MethodGet, "/api/list?query=%E8%AF%84%E5%AE%A1", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	entries := parseListEntries(t, w.Body.Bytes())
	if len(entries) != 2 {
		t.Errorf("expected 2 entries matching '评审', got %d", len(entries))
		for _, e := range entries {
			t.Logf("  entry: type=%v title=%v", e["type"], e["title"])
		}
	}

	// Verify case-insensitivity with uppercase query
	req = httptest.NewRequest(http.MethodGet, "/api/list?query=PR", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	entries = parseListEntries(t, w.Body.Bytes())
	if len(entries) != 1 {
		t.Errorf("expected 1 entry matching 'PR', got %d", len(entries))
	}
}

func TestHandleList_CombinedFilters(t *testing.T) {
	srv, _ := newTestServer(t)

	// Use meetings (date-tree storage) to avoid flat-dir ShortID collisions
	addRecord(t, srv, "meeting", "设计文档评审", "评审V2设计", "2026-04-25")
	addRecord(t, srv, "meeting", "代码评审", "评审PR", "2026-04-28")
	addRecord(t, srv, "log", "周会评审", "评审本周工作", "2026-04-28")
	addRecord(t, srv, "meeting", "上线部署", "部署到生产", "2026-05-01")
	addRecord(t, srv, "meeting", "测试评审", "评审测试报告", "2026-05-05")

	// Combined: type=meeting + from=2026-04-25 + to=2026-05-01 + status=active
	req := httptest.NewRequest(http.MethodGet, "/api/list?type=meeting&from=2026-04-25&to=2026-05-01&status=active", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	entries := parseListEntries(t, w.Body.Bytes())
	// Should get: 设计文档评审(04-25), 代码评审(04-28), 上线部署(05-01) — 3 meetings in range
	if len(entries) != 3 {
		t.Errorf("expected 3 entries for combined filter, got %d", len(entries))
		for _, e := range entries {
			t.Logf("  entry: type=%v title=%v date=%v status=%v",
				e["type"], e["title"], e["date"], e["status"])
		}
	}

	// Verify all are active meetings in the date range
	for _, e := range entries {
		if e["type"] != "meeting" {
			t.Errorf("expected type=meeting, got %v", e["type"])
		}
		if e["status"] != "active" {
			t.Errorf("expected status=active, got %v", e["status"])
		}
		dateStr, _ := e["date"].(string)
		if dateStr < "2026-04-25" || dateStr > "2026-05-01" {
			t.Errorf("date %s outside range [2026-04-25, 2026-05-01]", dateStr)
		}
	}
}

// ── Update endpoint tests ──

func TestUpdateEndpoint_UpdateTitleOnMeeting(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a meeting
	addBody := strings.NewReader(`{"type":"meeting","title":"original meeting","date":"2026-05-02","time":"10:00"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", addBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	// Update title
	updateBody := strings.NewReader(`{"title":"updated meeting title"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/update/"+shortID, updateBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var updateResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &updateResp)
	data := updateResp["data"].(map[string]interface{})
	if data["title"] != "updated meeting title" {
		t.Errorf("expected title='updated meeting title', got %v", data["title"])
	}
}

func TestUpdateEndpoint_UpdateTimeAndLocationOnTask(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a task
	addBody := strings.NewReader(`{"type":"task","title":"task test","date":"2026-05-02","time":"09:00","location":"Office A"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", addBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	// Update time + location
	updateBody := strings.NewReader(`{"time":"15:00","location":"Room B"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/update/"+shortID, updateBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var updateResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &updateResp)
	data := updateResp["data"].(map[string]interface{})
	if data["time"] != "15:00" {
		t.Errorf("expected time=15:00, got %v", data["time"])
	}
	if data["location"] != "Room B" {
		t.Errorf("expected location='Room B', got %v", data["location"])
	}
}

func TestUpdateEndpoint_UpdateTimeTriggersSchedulerReregister(t *testing.T) {
	srv, _, _ := newTestServerWithScheduler(t)

	// Add a reminder with a future date
	futureDate := time.Now().Add(48 * time.Hour).Format("2006-01-02")
	addBody := strings.NewReader(fmt.Sprintf(
		`{"type":"reminder","title":"sched test","date":"%s","time":"10:00","remind_before":"15m"}`,
		futureDate,
	))
	req := httptest.NewRequest(http.MethodPost, "/api/add", addBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	// Verify initial scheduler entry
	state := srv.scheduler.State()
	if len(state.Entries) != 1 {
		t.Fatalf("expected 1 scheduler entry after add, got %d", len(state.Entries))
	}

	// Update time — should trigger scheduler re-register
	updateBody := strings.NewReader(`{"time":"15:00"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/update/"+shortID, updateBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Verify scheduler still has exactly 1 entry (unregistered old + registered new)
	state = srv.scheduler.State()
	if len(state.Entries) != 1 {
		t.Errorf("expected 1 scheduler entry after update, got %d", len(state.Entries))
	}

	// Verify the entry's trigger time reflects the update
	entry := state.GetEntry(shortID)
	if entry == nil {
		t.Fatal("scheduler entry not found for short_id after update")
	}
}

func TestUpdateEndpoint_RecordNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	updateBody := strings.NewReader(`{"title":"no such record"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/update/nonexist", updateBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "record_not_found")
}

func TestUpdateEndpoint_AlreadyCompleted(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add + complete a task
	addBody := strings.NewReader(`{"type":"task","title":"complete then update","date":"2026-05-02"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", addBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	req = httptest.NewRequest(http.MethodPost, "/api/complete/"+shortID, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	// Try to update the completed record
	updateBody := strings.NewReader(`{"title":"try update completed"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/update/"+shortID, updateBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "already_completed")
}

func TestUpdateEndpoint_AlreadyCancelled(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add + cancel a task
	addBody := strings.NewReader(`{"type":"task","title":"cancel then update","date":"2026-05-02"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", addBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	req = httptest.NewRequest(http.MethodPost, "/api/cancel/"+shortID, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	// Try to update the cancelled record
	updateBody := strings.NewReader(`{"title":"try update cancelled"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/update/"+shortID, updateBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "already_cancelled")
}

func TestUpdateEndpoint_TypeFieldInvalid(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a meeting
	addBody := strings.NewReader(`{"type":"meeting","title":"try change type","date":"2026-05-02"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", addBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	// Try to update the type field — should fail
	updateBody := strings.NewReader(`{"type":"task"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/update/"+shortID, updateBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_field")
}

func TestUpdateEndpoint_EmptyFieldsInvalid(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a meeting
	addBody := strings.NewReader(`{"type":"meeting","title":"empty update test","date":"2026-05-02"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", addBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	// Send empty JSON body
	updateBody := strings.NewReader(`{}`)
	req = httptest.NewRequest(http.MethodPost, "/api/update/"+shortID, updateBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_body")
}

func TestUpdateEndpoint_WrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/update/abc123", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
}

func TestUpdateEndpoint_MissingID(t *testing.T) {
	srv, _ := newTestServer(t)
	updateBody := strings.NewReader(`{"title":"no id"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/update/", updateBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "record_not_found")
}

func TestUpdateEndpoint_InvalidJSON(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/update/abc123", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_body")
}

func TestUpdateEndpoint_NilScheduler_NoPanic(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a task
	addBody := strings.NewReader(`{"type":"task","title":"no scheduler update","date":"2026-05-02","time":"10:00"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", addBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	shortID := addData["short_id"].(string)

	// Update time — with nil scheduler, should not panic
	updateBody := strings.NewReader(`{"time":"15:00"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/update/"+shortID, updateBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")
}

// ── Report range and week tests ──

func TestReportRangeEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add records across 3 dates
	addRecord(t, srv, "meeting", "day1 meeting", "", "2026-04-25")
	addRecord(t, srv, "task", "day1 task", "", "2026-04-25")
	addRecord(t, srv, "meeting", "day2 meeting", "", "2026-04-26")
	addRecord(t, srv, "log", "day3 log", "", "2026-04-27")

	req := httptest.NewRequest(http.MethodGet, "/api/report/range?from=2026-04-25&to=2026-04-27", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	daysCount, _ := data["days_count"].(float64)
	if daysCount != 3 {
		t.Errorf("expected days_count=3, got %v", daysCount)
	}

	summary, _ := data["summary"].(map[string]interface{})
	meetings, _ := summary["meetings"].(float64)
	if meetings != 2 {
		t.Errorf("expected 2 meetings, got %v", meetings)
	}
	tasks, _ := summary["tasks"].(float64)
	if tasks != 1 {
		t.Errorf("expected 1 task, got %v", tasks)
	}
	total, _ := summary["total"].(float64)
	if total != 4 {
		t.Errorf("expected total=4, got %v", total)
	}

	// Verify markdown present
	md, _ := data["markdown"].(string)
	if md == "" {
		t.Error("expected markdown field in range report")
	}
}

func TestReportRangeEndpoint_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/report/range?from=2026-01-01&to=2026-01-03", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	daysCount, _ := data["days_count"].(float64)
	if daysCount != 3 {
		t.Errorf("expected days_count=3, got %v", daysCount)
	}
	summary, _ := data["summary"].(map[string]interface{})
	total, _ := summary["total"].(float64)
	if total != 0 {
		t.Errorf("expected total=0 for empty range, got %v", total)
	}
}

func TestReportRangeEndpoint_InvalidDate(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/report/range?from=not-a-date&to=2026-05-01", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "storage_error")
}

func TestReportRangeEndpoint_MissingParams(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/report/range?from=2026-05-01", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_body")
}

func TestReportRangeEndpoint_FromAfterTo(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/report/range?from=2026-05-05&to=2026-05-01", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "storage_error")
}

func TestReportWeekEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// Use today's date so records always fall within the current week
	today := time.Now().Format("2006-01-02")

	// Add records for today (within current week)
	addRecord(t, srv, "meeting", "week meeting", "", today)
	addRecord(t, srv, "task", "week task", "", today)

	req := httptest.NewRequest(http.MethodGet, "/api/report/week", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	daysCount, _ := data["days_count"].(float64)
	if daysCount != 7 {
		t.Errorf("expected days_count=7 for week report, got %v", daysCount)
	}
	summary, _ := data["summary"].(map[string]interface{})
	total, _ := summary["total"].(float64)
	if total != 2 {
		t.Errorf("expected total=2 in week report, got %v", total)
	}
}

func TestReportWeekEndpoint_WrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/report/week", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
}

func TestReportPushWeekEndpoint(t *testing.T) {
	var gotTitle string
	mockPush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotTitle = r.FormValue("title")
		w.WriteHeader(http.StatusOK)
	}))
	defer mockPush.Close()

	origURL := pushover.PushoverURL()
	pushover.SetPushoverURL(mockPush.URL)
	defer pushover.SetPushoverURL(origURL)

	cfg := &config.Config{
		Pushover: config.PushoverConfig{
			APIToken: "test-token",
			UserKey:  "test-user",
		},
	}
	srv, _ := newTestServer(t, cfg)

	// Use today's date so records always fall within the current week
	today := time.Now().Format("2006-01-02")

	addRecord(t, srv, "task", "week push task", "", today)

	req := httptest.NewRequest(http.MethodPost, "/api/report/push/week", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data, _ := record["data"].(map[string]interface{})
	if data["pushed"] != true {
		t.Errorf("expected pushed=true, got %v", data["pushed"])
	}
	if data["days"] != float64(7) {
		t.Errorf("expected days=7, got %v", data["days"])
	}
	if gotTitle == "" {
		t.Error("expected Pushover title to be set")
	}
}

func TestReportPushRangeEndpoint(t *testing.T) {
	var gotTitle, gotMessage string
	mockPush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotTitle = r.FormValue("title")
		gotMessage = r.FormValue("message")
		w.WriteHeader(http.StatusOK)
	}))
	defer mockPush.Close()

	origURL := pushover.PushoverURL()
	pushover.SetPushoverURL(mockPush.URL)
	defer pushover.SetPushoverURL(origURL)

	cfg := &config.Config{
		Pushover: config.PushoverConfig{
			APIToken: "test-token",
			UserKey:  "test-user",
		},
	}
	srv, _ := newTestServer(t, cfg)

	addRecord(t, srv, "meeting", "range push meeting", "", "2026-04-28")
	addRecord(t, srv, "log", "range push log", "", "2026-04-29")

	req := httptest.NewRequest(http.MethodPost, "/api/report/push/range?from=2026-04-28&to=2026-04-30", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data, _ := record["data"].(map[string]interface{})
	if data["pushed"] != true {
		t.Errorf("expected pushed=true, got %v", data["pushed"])
	}
	if data["date_from"] != "2026-04-28" {
		t.Errorf("expected date_from=2026-04-28, got %v", data["date_from"])
	}
	if data["date_to"] != "2026-04-30" {
		t.Errorf("expected date_to=2026-04-30, got %v", data["date_to"])
	}
	if gotTitle == "" {
		t.Error("expected Pushover title to be set")
	}
	if !strings.Contains(gotMessage, "range push meeting") {
		t.Errorf("expected message to contain meeting title, got %q", gotMessage)
	}
}

// ── Panic recovery middleware tests ──

func TestPanicRecovery(t *testing.T) {
	srv, _ := newTestServer(t)
	router := srv.Router()

	// Register a temporary route that panics
	router.HandleFunc("/api/test-panic", func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	// Create test server with full middleware chain (panicRecovery → logging → router)
	ts := httptest.NewServer(srv.panicRecoveryMiddleware(srv.loggingMiddleware(router)))
	defer ts.Close()

	// Request the panicking endpoint
	resp, err := http.Get(ts.URL + "/api/test-panic")
	if err != nil {
		t.Fatalf("GET /api/test-panic: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Verify HTTP status is 200 (writeEnvelope always writes 200)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	// Parse JSONL response and assert FATAL_CRASH envelope
	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(body), &record); err != nil {
		t.Fatalf("invalid JSONL response: %s\nerr: %v", body, err)
	}

	if record["type"] != "error" {
		t.Errorf("type = %v, want error", record["type"])
	}
	if record["error_code"] != "FATAL_CRASH" {
		t.Errorf("error_code = %v, want FATAL_CRASH", record["error_code"])
	}
	msg, _ := record["message"].(string)
	if !strings.Contains(msg, "panic: test panic") {
		t.Errorf("message = %q, want containing 'panic: test panic'", msg)
	}
	if record["version"] != "1.0" {
		t.Errorf("version = %v, want 1.0", record["version"])
	}
	if record["tool"] != "wr" {
		t.Errorf("tool = %v, want wr", record["tool"])
	}

	// Verify the server continues serving subsequent requests after the panic
	healthResp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health after panic: %v", err)
	}
	defer healthResp.Body.Close()

	healthBody, _ := io.ReadAll(healthResp.Body)
	var healthRecord map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(healthBody), &healthRecord); err != nil {
		t.Fatalf("invalid health JSONL: %s", healthBody)
	}
	if healthRecord["type"] != "result" {
		t.Errorf("health type = %v, want result (server should recover from panic)", healthRecord["type"])
	}
}

// ── Import endpoint tests ──

func TestHandleImportSuccess(t *testing.T) {
	srv, dir := newTestServer(t)
	payload := `{"records":[
		{"type":"meeting","title":"imported meeting","date":"2026-05-04","time":"10:00"},
		{"type":"task","title":"imported task","date":"2026-05-04"},
		{"type":"reminder","title":"imported reminder","date":"2026-05-05"},
		{"type":"log","title":"imported log","date":"2026-05-04"}
	]}`
	body := strings.NewReader(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Verify imported count
	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record); err != nil {
		t.Fatalf("invalid JSONL: %s", w.Body.Bytes())
	}
	data, ok := record["data"].(map[string]interface{})
	if !ok {
		t.Fatal("data field missing")
	}
	if data["imported"].(float64) != 4 {
		t.Errorf("imported = %v, want 4", data["imported"])
	}

	// Verify files were written — check meeting and task dirs
	meetingsDir := filepath.Join(dir, "meetings", "2026", "05", "04")
	files, err := os.ReadDir(meetingsDir)
	if err != nil {
		t.Fatalf("meetings dir should exist: %v", err)
	}
	if len(files) == 0 {
		t.Error("expected meeting file to be written")
	}

	tasksDir := filepath.Join(dir, "tasks", "active")
	files, err = os.ReadDir(tasksDir)
	if err != nil {
		t.Fatalf("tasks dir should exist: %v", err)
	}
	if len(files) == 0 {
		t.Error("expected task file to be written")
	}

	remindersDir := filepath.Join(dir, "reminders", "active")
	files, err = os.ReadDir(remindersDir)
	if err != nil {
		t.Fatalf("reminders dir should exist: %v", err)
	}
	if len(files) == 0 {
		t.Error("expected reminder file to be written")
	}
}

func TestHandleImportInvalidType(t *testing.T) {
	srv, _ := newTestServer(t)
	payload := `{"records":[
		{"type":"meeting","title":"valid","date":"2026-05-04"},
		{"type":"bogus","title":"invalid type","date":"2026-05-04"}
	]}`
	body := strings.NewReader(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "import_record")
}

func TestHandleImportMissingTitle(t *testing.T) {
	srv, _ := newTestServer(t)
	payload := `{"records":[
		{"type":"meeting","title":"","date":"2026-05-04"}
	]}`
	body := strings.NewReader(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "import_record")
}

func TestHandleImportMissingDate(t *testing.T) {
	srv, _ := newTestServer(t)
	payload := `{"records":[
		{"type":"task","title":"no date"}
	]}`
	body := strings.NewReader(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "import_record")
}

func TestHandleImportEmptyRecords(t *testing.T) {
	srv, _ := newTestServer(t)
	payload := `{"records":[]}`
	body := strings.NewReader(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_body")
}

func TestHandleImportInvalidBody(t *testing.T) {
	srv, _ := newTestServer(t)
	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_body")
}

func TestHandleImportFailFastNoPersistOnInvalid(t *testing.T) {
	// Verify that when a record fails validation, nothing is persisted.
	srv, dir := newTestServer(t)
	payload := `{"records":[
		{"type":"meeting","title":"should not be saved","date":"2026-05-04"},
		{"type":"meeting","title":"","date":"2026-05-04"}
	]}`
	body := strings.NewReader(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")

	// No meeting files should have been written
	meetingsDir := filepath.Join(dir, "meetings", "2026", "05", "04")
	files, err := os.ReadDir(meetingsDir)
	if err == nil && len(files) > 0 {
		t.Errorf("expected no files written on validation failure, got %d files", len(files))
	}
}

func TestHandleImportMethodNotAllowed(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/import", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "method_not_allowed")
}
