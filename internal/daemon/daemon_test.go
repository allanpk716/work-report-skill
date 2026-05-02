package daemon

import (
	"bytes"
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

	"wr/internal/config"
	"wr/internal/llm"
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
	count, _ := data["count"].(float64)
	if count != 2 {
		t.Errorf("expected 2 records in report, got %v", count)
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
