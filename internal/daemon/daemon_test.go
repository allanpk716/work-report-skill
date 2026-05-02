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
	"wr/internal/llm"
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
	if record["status"] != "ok" {
		t.Errorf("status field = %v, want ok", record["status"])
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
	if record["status"] != "ok" {
		t.Errorf("status = %v, want ok", record["status"])
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
	if addResp["status"] != "success" {
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
	if compResp["status"] != "success" {
		t.Fatalf("complete failed: %s", body)
	}
}

// ── Helpers ──

func assertJSONLStatus(t *testing.T, body []byte, wantStatus string) {
	t.Helper()
	trimmed := bytes.TrimSpace(body)
	var record map[string]interface{}
	if err := json.Unmarshal(trimmed, &record); err != nil {
		t.Fatalf("invalid JSONL: %s\nerr: %v", body, err)
	}
	if record["status"] != wantStatus {
		t.Errorf("status = %v, want %s; body=%s", record["status"], wantStatus, body)
	}
}

func assertJSONLCode(t *testing.T, body []byte, wantCode string) {
	t.Helper()
	trimmed := bytes.TrimSpace(body)
	var record map[string]interface{}
	if err := json.Unmarshal(trimmed, &record); err != nil {
		t.Fatalf("invalid JSONL: %s\nerr: %v", body, err)
	}
	if record["code"] != wantCode {
		t.Errorf("code = %v, want %s; body=%s", record["code"], wantCode, body)
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
