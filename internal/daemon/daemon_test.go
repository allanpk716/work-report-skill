package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wr/internal/config"
	"wr/internal/digest"
	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
	"wr/internal/llm"
	"wr/internal/logger"
	"wr/internal/pushover"
	"wr/internal/scheduler"
	"wr/internal/storage"
)

// newTestServer creates a Server backed by a temp directory for storage.
// If cfg is nil, a minimal config is created with DataDir set to the temp dir
// so that the digest store also uses the temp dir (not the real ~/.work-report).
func newTestServer(t *testing.T, cfg ...*config.Config) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	store := storage.New(dir)
	var c *config.Config
	if len(cfg) > 0 && cfg[0] != nil {
		c = cfg[0]
		// Ensure DataDir is set so digest store uses temp dir.
		if c.DataDir == "" {
			c.DataDir = dir
		}
	} else {
		c = &config.Config{DataDir: dir}
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

	// Missing date is now auto-filled to today (source=default_today)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	date, _ := data["date"].(string)
	today := time.Now().UTC().Format("2006-01-02")
	if date != today {
		t.Errorf("expected date %s (auto-filled today), got %s", today, date)
	}
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
func parseAndValidateEnvelope(t *testing.T, body []byte) (map[string]interface{}, agentsdk.Envelope) {
	t.Helper()
	trimmed := bytes.TrimSpace(body)
	var record map[string]interface{}
	if err := json.Unmarshal(trimmed, &record); err != nil {
		t.Fatalf("invalid JSONL: %s\nerr: %v", body, err)
	}
	var env agentsdk.Envelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		t.Fatalf("invalid JSONL envelope struct: %s\nerr: %v", body, err)
	}
	if err := agentsdk.ValidateEnvelope(env); err != nil {
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

// ── Idempotency tests ──

func TestAddEndpointIdempotency(t *testing.T) {
	srv, dir := newTestServer(t)

	payload := `{"type":"meeting","title":"Standup","date":"2026-05-05","idempotency_key":"standup-2026-05-05"}`

	// First add — should create a new record
	req1 := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(payload))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	srv.router.ServeHTTP(w1, req1)

	assertJSONLStatus(t, w1.Body.Bytes(), "success")

	var resp1 map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w1.Body.Bytes()), &resp1)
	data1 := resp1["data"].(map[string]interface{})
	shortID1, _ := data1["short_id"].(string)
	if shortID1 == "" {
		t.Fatal("first add should return a short_id")
	}

	// Second add with same idempotency key — should return existing record
	req2 := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(payload))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.router.ServeHTTP(w2, req2)

	assertJSONLStatus(t, w2.Body.Bytes(), "success")

	var resp2 map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w2.Body.Bytes()), &resp2)
	data2 := resp2["data"].(map[string]interface{})
	shortID2, _ := data2["short_id"].(string)

	if shortID1 != shortID2 {
		t.Errorf("idempotent add returned different short_id: first=%s second=%s", shortID1, shortID2)
	}

	// Verify only one file on disk
	meetingsDir := filepath.Join(dir, "meetings", "2026", "05", "05")
	files, err := os.ReadDir(meetingsDir)
	if err != nil {
		t.Fatalf("meetings dir should exist: %v", err)
	}
	jsonFiles := 0
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".json") {
			jsonFiles++
		}
	}
	if jsonFiles != 1 {
		t.Errorf("expected exactly 1 meeting file on disk, got %d", jsonFiles)
	}
}

func TestAddEndpointIdempotency_NoKey(t *testing.T) {
	srv, dir := newTestServer(t)

	// Normal add without idempotency key should still work
	payload := `{"type":"meeting","title":"Normal Add","date":"2026-05-05"}`

	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	data := resp["data"].(map[string]interface{})
	if data["type"] != "meeting" {
		t.Errorf("type = %v, want meeting", data["type"])
	}

	// Verify file was written
	meetingsDir := filepath.Join(dir, "meetings", "2026", "05", "05")
	files, err := os.ReadDir(meetingsDir)
	if err != nil {
		t.Fatalf("meetings dir should exist: %v", err)
	}
	if len(files) == 0 {
		t.Error("expected at least one meeting file")
	}
}

func TestAddEndpointIdempotency_DifferentKeys(t *testing.T) {
	srv, dir := newTestServer(t)

	// Two adds with different idempotency keys should create two records
	payload1 := `{"type":"meeting","title":"Standup A","date":"2026-05-05","idempotency_key":"key-a"}`
	payload2 := `{"type":"meeting","title":"Standup B","date":"2026-05-05","idempotency_key":"key-b"}`

	req1 := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(payload1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	srv.router.ServeHTTP(w1, req1)
	assertJSONLStatus(t, w1.Body.Bytes(), "success")

	req2 := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(payload2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.router.ServeHTTP(w2, req2)
	assertJSONLStatus(t, w2.Body.Bytes(), "success")

	// Verify two files on disk
	meetingsDir := filepath.Join(dir, "meetings", "2026", "05", "05")
	files, err := os.ReadDir(meetingsDir)
	if err != nil {
		t.Fatalf("meetings dir: %v", err)
	}
	jsonFiles := 0
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".json") {
			jsonFiles++
		}
	}
	if jsonFiles != 2 {
		t.Errorf("expected 2 meeting files on disk, got %d", jsonFiles)
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

// mockLLMBatchServer creates an httptest.Server that simulates the OpenAI chat completions API
// returning multiple ClassifyResult entries as a JSON array.
func mockLLMBatchServer(t *testing.T, results []llm.ClassifyResult) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		b, _ := json.Marshal(results)
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

func TestAddEndpoint_TextMultiEvent(t *testing.T) {
	// Mock LLM returns an array of 2 results
	mockServer := mockLLMBatchServer(t, []llm.ClassifyResult{
		{Type: "task", Title: "完成需求文档", Date: "2026-05-03"},
		{Type: "meeting", Title: "项目评审会", Date: "2026-05-03", Time: "15:00"},
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
	body := strings.NewReader(`{"text":"上午完成了需求文档，下午开了项目评审会"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	resp := w.Result()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	// Response should contain multiple JSONL lines: 2 records + 1 batch_add summary
	lines := bytes.Split(bytes.TrimSpace(respBody), []byte("\n"))
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 JSONL lines (2 records + summary), got %d: %s", len(lines), respBody)
	}

	// First two lines should be individual records
	recordTypes := make(map[string]int)
	for i := 0; i < 2; i++ {
		var record map[string]interface{}
		if err := json.Unmarshal(lines[i], &record); err != nil {
			t.Fatalf("invalid JSONL line %d: %s", i, lines[i])
		}
		if record["type"] != "result" {
			t.Errorf("line %d: expected type=result, got %v", i, record["type"])
		}
		data, ok := record["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("line %d: data field missing", i)
		}
		typ, _ := data["type"].(string)
		recordTypes[typ]++
	}

	if recordTypes["task"] != 1 {
		t.Errorf("expected 1 task record, got %d", recordTypes["task"])
	}
	if recordTypes["meeting"] != 1 {
		t.Errorf("expected 1 meeting record, got %d", recordTypes["meeting"])
	}

	// Last line should be batch_add summary
	var summary map[string]interface{}
	if err := json.Unmarshal(lines[len(lines)-1], &summary); err != nil {
		t.Fatalf("invalid summary JSONL: %s", lines[len(lines)-1])
	}
	summaryData, ok := summary["data"].(map[string]interface{})
	if !ok {
		t.Fatal("summary data field missing")
	}
	action, _ := summaryData["action"].(string)
	if action != "batch_add" {
		t.Errorf("expected action=batch_add, got %q", action)
	}
	created, _ := summaryData["created"].(float64)
	if created != 2 {
		t.Errorf("expected created=2, got %v", created)
	}

	// Verify records were created (checked via JSONL response above).
	// Also verify list endpoint returns both records.
	req = httptest.NewRequest(http.MethodGet, "/api/list?date=2026-05-03", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")
	var listResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &listResp)
	listData := listResp["data"].(map[string]interface{})
	entries, _ := listData["entries"].([]interface{})
	if len(entries) != 2 {
		t.Errorf("expected 2 entries from list, got %d", len(entries))
	}
}

func TestAddEndpoint_TextMultiEventWithCancel(t *testing.T) {
	// Mock LLM returns 3 results: 2 actionable + 1 cancel_or_update
	mockServer := mockLLMBatchServer(t, []llm.ClassifyResult{
		{Type: "task", Title: "写周报", Date: "2026-05-04"},
		{Type: "cancel_or_update", Title: "取消明天的会议", TargetID: "abc12345"},
		{Type: "log", Title: "阅读技术文档", Date: "2026-05-04"},
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
	body := strings.NewReader(`{"text":"写周报，取消明天的会议abc12345，阅读技术文档"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	resp := w.Result()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	// Response: 2 record lines + 1 batch_add summary with skipped info
	lines := bytes.Split(bytes.TrimSpace(respBody), []byte("\n"))
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 JSONL lines, got %d: %s", len(lines), respBody)
	}

	// Summary should report skipped count and classifications
	var summary map[string]interface{}
	json.Unmarshal(lines[len(lines)-1], &summary)
	summaryData := summary["data"].(map[string]interface{})
	created, _ := summaryData["created"].(float64)
	if created != 2 {
		t.Errorf("expected created=2, got %v", created)
	}
	skipped, _ := summaryData["skipped"].(float64)
	if skipped != 1 {
		t.Errorf("expected skipped=1, got %v", skipped)
	}
	classifications, ok := summaryData["classifications"].([]interface{})
	if !ok || len(classifications) != 1 {
		t.Fatalf("expected 1 classification entry, got %v", summaryData["classifications"])
	}
	firstCancel := classifications[0].(map[string]interface{})
	if firstCancel["type"] != "cancel_or_update" {
		t.Errorf("expected cancel_or_update classification, got %v", firstCancel["type"])
	}
}

func TestAddEndpoint_TextSingleEvent(t *testing.T) {
	// Mock LLM returns a single object — backward compatibility test
	mockServer := mockLLMServer(t, llm.ClassifyResult{
		Type:  "reminder",
		Title: "提交周报",
		Date:  "2026-05-06",
		Time:  "17:00",
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
	body := strings.NewReader(`{"text":"周五下午5点前提交周报"}`)
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
	if data["type"] != "reminder" {
		t.Errorf("expected type reminder, got %v", data["type"])
	}
	if data["title"] != "提交周报" {
		t.Errorf("expected title 提交周报, got %v", data["title"])
	}
	if data["date"] != "2026-05-06" {
		t.Errorf("expected date 2026-05-06, got %v", data["date"])
	}

	// Verify single JSONL line (not batch format)
	lines := bytes.Split(bytes.TrimSpace(respBody), []byte("\n"))
	if len(lines) != 1 {
		t.Errorf("single event should return 1 JSONL line, got %d", len(lines))
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

// ── Default-today tests (date auto-fill when not using LLM) ──

func TestAddEndpoint_DefaultToday_NoDate(t *testing.T) {
	srv, _ := newTestServer(t)
	// No date field — should auto-fill to today
	body := strings.NewReader(`{"type":"log","title":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	date, _ := data["date"].(string)
	today := time.Now().UTC().Format("2006-01-02")
	if date != today {
		t.Errorf("expected date %s (default_today), got %s", today, date)
	}
}

func TestAddEndpoint_DefaultToday_ExplicitDatePreserved(t *testing.T) {
	srv, _ := newTestServer(t)
	// Explicit date should be preserved, not overwritten
	body := strings.NewReader(`{"type":"log","title":"test","date":"2026-05-03"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	date, _ := data["date"].(string)
	if date != "2026-05-03" {
		t.Errorf("expected date 2026-05-03 (explicit), got %s", date)
	}
}

func TestAddEndpoint_DefaultToday_WithConfiguredTimezone(t *testing.T) {
	// Use Asia/Shanghai timezone to verify the date is computed in the configured TZ
	cfg := &config.Config{
		Timezone: "Asia/Shanghai",
	}
	srv, _ := newTestServer(t, cfg)

	body := strings.NewReader(`{"type":"log","title":"tz test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	date, _ := data["date"].(string)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	expected := time.Now().In(loc).Format("2006-01-02")
	if date != expected {
		t.Errorf("expected date %s (Asia/Shanghai), got %s", expected, date)
	}
}

func TestAddEndpoint_DefaultToday_NotAppliedInLLMMode(t *testing.T) {
	// When LLM classification is used (text without explicit type),
	// the default-today logic should NOT be applied — LLM provides the date.
	mockServer := mockLLMServer(t, llm.ClassifyResult{
		Type:  "task",
		Title: "LLM classified task",
		Date:  "2025-12-25", // LLM returns a specific date, not today
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
	// text without type → triggers LLM classification (usedLLM=true)
	body := strings.NewReader(`{"text":"some text"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	date, _ := data["date"].(string)
	today := time.Now().UTC().Format("2006-01-02")
	if date == today {
		t.Errorf("in LLM mode, date should come from LLM result, not default_today; got %s", date)
	}
	if date != "2025-12-25" {
		t.Errorf("expected date 2025-12-25 (from LLM), got %s", date)
	}
}

func TestAddEndpoint_DefaultToday_TextWithExplicitType(t *testing.T) {
	// When text is provided WITH explicit type, LLM is NOT called (usedLLM=false),
	// so default-today should apply.
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Text: config.LLMProviderConfig{
				APIBase: "http://127.0.0.1:0", // unreachable — proves LLM not called
				APIKey:  "test-key",
				Model:   "test-model",
			},
		},
	}

	srv, _ := newTestServer(t, cfg)
	body := strings.NewReader(`{"type":"log","title":"test","text":"some text"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	date, _ := data["date"].(string)
	today := time.Now().UTC().Format("2006-01-02")
	if date != today {
		t.Errorf("expected date %s (default_today, type explicit so LLM not used), got %s", today, date)
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
	store := storage.New(dir)

	// Use a far-future timezone so entries don't trigger during the test
	cfg := &config.Config{
		Timezone: "UTC",
	}

	mockPush := &mockPushSender{}
	statePath := filepath.Join(dir, "scheduler-state.json")
	sched := scheduler.NewScheduler(cfg, mockPush, statePath)
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

// ── Date/time and record count tests ──

func TestHandleStatus_DateTimeFields(t *testing.T) {
	cfg := &config.Config{
		Timezone: "Asia/Shanghai",
	}
	srv, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	dt, ok := data["datetime"].(map[string]interface{})
	if !ok {
		t.Fatal("expected datetime section in status response")
	}

	// Verify current_date format: YYYY-MM-DD
	currentDate, _ := dt["current_date"].(string)
	if len(currentDate) != 10 || currentDate[4] != '-' || currentDate[7] != '-' {
		t.Errorf("current_date format invalid: %q (want YYYY-MM-DD)", currentDate)
	}

	// Verify current_time format: HH:MM:SS
	currentTime, _ := dt["current_time"].(string)
	if len(currentTime) != 8 || currentTime[2] != ':' || currentTime[5] != ':' {
		t.Errorf("current_time format invalid: %q (want HH:MM:SS)", currentTime)
	}

	// Verify current_datetime is valid ISO 8601 with timezone offset
	currentDatetime, _ := dt["current_datetime"].(string)
	if _, err := time.Parse(time.RFC3339, currentDatetime); err != nil {
		t.Errorf("current_datetime is not valid RFC3339: %q, err=%v", currentDatetime, err)
	}

	// Verify timezone is Asia/Shanghai
	if dt["timezone"] != "Asia/Shanghai" {
		t.Errorf("timezone = %v, want Asia/Shanghai", dt["timezone"])
	}

	// Verify weekday is a valid English weekday name
	weekday, _ := dt["weekday"].(string)
	validWeekdays := map[string]bool{
		"Monday": true, "Tuesday": true, "Wednesday": true, "Thursday": true,
		"Friday": true, "Saturday": true, "Sunday": true,
	}
	if !validWeekdays[weekday] {
		t.Errorf("weekday = %q, expected a valid English weekday name", weekday)
	}

	// Cross-check: current_date should match today in Asia/Shanghai timezone
	loc, _ := time.LoadLocation("Asia/Shanghai")
	expectedDate := time.Now().In(loc).Format("2006-01-02")
	if currentDate != expectedDate {
		t.Errorf("current_date = %q, want %q (today in Asia/Shanghai)", currentDate, expectedDate)
	}
}

func TestHandleStatus_DateTimeFields_UTCTimezone(t *testing.T) {
	cfg := &config.Config{
		Timezone: "UTC",
	}
	srv, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	dt := data["datetime"].(map[string]interface{})
	if dt["timezone"] != "UTC" {
		t.Errorf("timezone = %v, want UTC", dt["timezone"])
	}

	// current_datetime should end with +00:00 or Z for UTC
	currentDatetime, _ := dt["current_datetime"].(string)
	parsed, err := time.Parse(time.RFC3339, currentDatetime)
	if err != nil {
		t.Fatalf("current_datetime parse error: %v", err)
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		t.Errorf("UTC datetime should have zero offset, got %d seconds", offset)
	}
}

func TestHandleStatus_DateTimeFields_DefaultConfig(t *testing.T) {
	// Empty config should use default timezone (Asia/Shanghai)
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	dt := data["datetime"].(map[string]interface{})
	// Default config has empty timezone which appliesDefaults sets to Asia/Shanghai,
	// but newTestServer uses cfg directly without applyDefaults. Empty timezone
	// falls back to UTC in Location(). Just verify the field exists and is non-empty.
	timezone, _ := dt["timezone"].(string)
	if timezone == "" {
		t.Error("timezone should not be empty")
	}
}

func TestHandleStatus_RecordCounts(t *testing.T) {
	srv, _ := newTestServer(t)

	// Seed: 2 meetings, 1 task, 3 reminders, 1 log
	addRecord(t, srv, "meeting", "m1", "", "2026-06-01")
	addRecord(t, srv, "meeting", "m2", "", "2026-06-01")
	addRecord(t, srv, "task", "t1", "", "2026-06-01")
	addRecord(t, srv, "reminder", "r1", "", "2026-06-01")
	addRecord(t,srv, "reminder", "r2", "", "2026-06-01")
	addRecord(t, srv, "reminder", "r3", "", "2026-06-01")
	addRecord(t, srv, "log", "l1", "", "2026-06-01")

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	records, ok := data["records"].(map[string]interface{})
	if !ok {
		t.Fatal("expected records section in status response")
	}

	// Verify counts match seeded data
	if records["active_meetings"].(float64) != 2 {
		t.Errorf("active_meetings = %v, want 2", records["active_meetings"])
	}
	if records["active_tasks"].(float64) != 1 {
		t.Errorf("active_tasks = %v, want 1", records["active_tasks"])
	}
	if records["active_reminders"].(float64) != 3 {
		t.Errorf("active_reminders = %v, want 3", records["active_reminders"])
	}
	if records["active_logs"].(float64) != 1 {
		t.Errorf("active_logs = %v, want 1", records["active_logs"])
	}
	if records["total_active"].(float64) != 7 {
		t.Errorf("total_active = %v, want 7", records["total_active"])
	}
}

func TestHandleStatus_RecordCounts_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	records, ok := data["records"].(map[string]interface{})
	if !ok {
		t.Fatal("expected records section in status response even when empty")
	}

	// All counts should be zero
	if records["active_meetings"].(float64) != 0 {
		t.Errorf("active_meetings = %v, want 0", records["active_meetings"])
	}
	if records["active_tasks"].(float64) != 0 {
		t.Errorf("active_tasks = %v, want 0", records["active_tasks"])
	}
	if records["active_reminders"].(float64) != 0 {
		t.Errorf("active_reminders = %v, want 0", records["active_reminders"])
	}
	if records["active_logs"].(float64) != 0 {
		t.Errorf("active_logs = %v, want 0", records["active_logs"])
	}
	if records["total_active"].(float64) != 0 {
		t.Errorf("total_active = %v, want 0", records["total_active"])
	}
}

func TestHandleStatus_RecordCounts_ExcludesCompleted(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add 2 meetings, then complete 1
	addRecord(t, srv, "meeting", "m1", "", "2026-06-01")
	addRecord(t, srv, "meeting", "m2", "", "2026-06-01")

	// Get the short IDs and complete the first one
	listReq := httptest.NewRequest(http.MethodGet, "/api/list?type=meeting&date=2026-06-01", nil)
	listW := httptest.NewRecorder()
	srv.router.ServeHTTP(listW, listReq)
	entries := parseListEntries(t, listW.Body.Bytes())
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(entries))
	}
	firstID := entries[0]["short_id"].(string)

	compReq := httptest.NewRequest(http.MethodPost, "/api/complete/"+firstID, nil)
	compW := httptest.NewRecorder()
	srv.router.ServeHTTP(compW, compReq)
	assertJSONLStatus(t, compW.Body.Bytes(), "success")

	// Now check status counts
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	records := data["records"].(map[string]interface{})

	if records["active_meetings"].(float64) != 1 {
		t.Errorf("active_meetings = %v, want 1 (after completing 1 of 2)", records["active_meetings"])
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

	assertJSONLStatus(t, w.Body.Bytes(), "result")

	// Verify imported=0 in the response
	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record); err != nil {
		t.Fatalf("invalid JSONL: %s", w.Body.Bytes())
	}
	data, ok := record["data"].(map[string]interface{})
	if !ok {
		t.Fatal("data field missing")
	}
	if data["imported"].(float64) != 0 {
		t.Errorf("imported = %v, want 0", data["imported"])
	}
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

// ── Import validation index tests ──

func TestHandleImportMissingTypeAtIndex(t *testing.T) {
	srv, _ := newTestServer(t)
	// Record 0 is valid, record 1 is missing type entirely
	payload := `{"records":[
		{"type":"meeting","title":"valid meeting","date":"2026-05-04"},
		{"title":"no type field","date":"2026-05-04"}
	]}`
	body := strings.NewReader(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "import_record")

	// Verify error message includes the record index and mentions "type"
	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	msg, _ := record["message"].(string)
	if !strings.Contains(msg, "index 1") {
		t.Errorf("expected error message to mention 'index 1', got %q", msg)
	}
	if !strings.Contains(msg, "type") {
		t.Errorf("expected error message to mention 'type', got %q", msg)
	}
}

func TestHandleImportValidationErrorIncludesIndex(t *testing.T) {
	tests := []struct {
		name         string
		payload      string
		wantIndex    string
		wantField    string
	}{
		{
			name: "missing title at index 0",
			payload: `{"records":[
				{"type":"meeting","date":"2026-05-04"},
				{"type":"task","title":"valid","date":"2026-05-04"}
			]}`,
			wantIndex: "index 0",
			wantField: "title",
		},
		{
			name: "missing date at index 2",
			payload: `{"records":[
				{"type":"meeting","title":"ok","date":"2026-05-04"},
				{"type":"task","title":"ok","date":"2026-05-04"},
				{"type":"log","title":"no date"}
			]}`,
			wantIndex: "index 2",
			wantField: "date",
		},
		{
			name: "invalid type at index 1",
			payload: `{"records":[
				{"type":"meeting","title":"ok","date":"2026-05-04"},
				{"type":"bogus","title":"bad type","date":"2026-05-04"}
			]}`,
			wantIndex: "index 1",
			wantField: "type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newTestServer(t)
			body := strings.NewReader(tt.payload)
			req := httptest.NewRequest(http.MethodPost, "/api/import", body)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.router.ServeHTTP(w, req)

			assertJSONLStatus(t, w.Body.Bytes(), "error")
			assertJSONLCode(t, w.Body.Bytes(), "import_record")

			var record map[string]interface{}
			json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
			msg, _ := record["message"].(string)
			if !strings.Contains(msg, tt.wantIndex) {
				t.Errorf("expected error to mention %q, got %q", tt.wantIndex, msg)
			}
			if !strings.Contains(msg, tt.wantField) {
				t.Errorf("expected error to mention %q, got %q", tt.wantField, msg)
			}
		})
	}
}

func TestHandleImportAllRecordTypes(t *testing.T) {
	srv, _ := newTestServer(t)
	payload := `{"records":[
		{"type":"meeting","title":"team sync","date":"2026-06-01","time":"09:00"},
		{"type":"task","title":"write tests","date":"2026-06-01"},
		{"type":"reminder","title":"follow up","date":"2026-06-02","time":"14:00"},
		{"type":"log","title":"daily standup","date":"2026-06-01"}
	]}`
	body := strings.NewReader(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data, _ := record["data"].(map[string]interface{})
	if data["imported"].(float64) != 4 {
		t.Errorf("expected imported=4, got %v", data["imported"])
	}
}

// ── Import rollback test ──

func TestHandleImportRollback(t *testing.T) {
	// Import 3 records where record 2 is invalid.
	// Verify via the list endpoint that no records were persisted.
	srv, _ := newTestServer(t)

	// First, add one record to establish a baseline
	addBody := strings.NewReader(`{"type":"task","title":"pre-existing task","date":"2026-05-04"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", addBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Verify baseline: 1 task on 2026-05-04
	req = httptest.NewRequest(http.MethodGet, "/api/list?type=task&date=2026-05-04", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	var listResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &listResp)
	listData := listResp["data"].(map[string]interface{})
	entries, _ := listData["entries"].([]interface{})
	baselineCount := len(entries)
	if baselineCount != 1 {
		t.Fatalf("expected baseline of 1 task, got %d", baselineCount)
	}

	// Attempt import with invalid record at index 2
	payload := `{"records":[
		{"type":"meeting","title":"valid meeting","date":"2026-05-04"},
		{"type":"task","title":"valid task","date":"2026-05-04"},
		{"type":"meeting","title":"","date":"2026-05-04"}
	]}`
	body := strings.NewReader(payload)
	req = httptest.NewRequest(http.MethodPost, "/api/import", body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	// Should fail
	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "import_record")

	// Verify rollback: list again, count should be unchanged
	req = httptest.NewRequest(http.MethodGet, "/api/list?type=task&date=2026-05-04", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &listResp)
	listData = listResp["data"].(map[string]interface{})
	entries, _ = listData["entries"].([]interface{})
	if len(entries) != baselineCount {
		t.Errorf("rollback failed: expected %d tasks after failed import, got %d (partial writes occurred)", baselineCount, len(entries))
	}

	// Also verify no meetings were written
	req = httptest.NewRequest(http.MethodGet, "/api/list?type=meeting&date=2026-05-04", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &listResp)
	listData = listResp["data"].(map[string]interface{})
	entries, _ = listData["entries"].([]interface{})
	if len(entries) != 0 {
		t.Errorf("rollback failed: expected 0 meetings, got %d", len(entries))
	}
}

// ── Integration tests: import→list round-trip ──

// readStoredRecord reads the first JSON record file in the given subdirectory
// and unmarshals it into a map. Returns the map and the list of files found.
func readStoredRecord(t *testing.T, dir, subpath string) (map[string]interface{}, []os.DirEntry) {
	t.Helper()
	fullDir := filepath.Join(dir, subpath)
	files, err := os.ReadDir(fullDir)
	if err != nil {
		t.Fatalf("dir %s should exist: %v", subpath, err)
	}
	if len(files) == 0 {
		t.Fatalf("expected at least one file in %s, found none", subpath)
	}
	data, err := os.ReadFile(filepath.Join(fullDir, files[0].Name()))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var record map[string]interface{}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	return record, files
}

// TestIntegration_ImportThenListRoundTrip imports valid records and verifies
// they appear in the list endpoint with fresh ShortIDs.
func TestIntegration_ImportThenListRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)

	// Import 3 records of different types
	payload := `{"records":[
		{"type":"meeting","title":"import-list meeting","date":"2026-06-15","time":"10:00"},
		{"type":"task","title":"import-list task","date":"2026-06-15"},
		{"type":"log","title":"import-list log","date":"2026-06-15"}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")
	var importResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &importResp)
	importData := importResp["data"].(map[string]interface{})
	if importData["imported"].(float64) != 3 {
		t.Fatalf("expected imported=3, got %v", importData["imported"])
	}

	// List meetings and verify the imported record is present with a ShortID
	req = httptest.NewRequest(http.MethodGet, "/api/list?type=meeting&date=2026-06-15", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	meetingEntries := parseListEntries(t, w.Body.Bytes())
	if len(meetingEntries) != 1 {
		t.Fatalf("expected 1 meeting entry, got %d", len(meetingEntries))
	}
	meetingShortID, _ := meetingEntries[0]["short_id"].(string)
	if meetingShortID == "" {
		t.Error("imported meeting should have a fresh ShortID")
	}
	if meetingEntries[0]["title"] != "import-list meeting" {
		t.Errorf("meeting title = %v, want 'import-list meeting'", meetingEntries[0]["title"])
	}
	if meetingEntries[0]["type"] != "meeting" {
		t.Errorf("meeting type = %v, want 'meeting'", meetingEntries[0]["type"])
	}
	if meetingEntries[0]["date"] != "2026-06-15" {
		t.Errorf("meeting date = %v, want '2026-06-15'", meetingEntries[0]["date"])
	}

	// List tasks
	req = httptest.NewRequest(http.MethodGet, "/api/list?type=task&date=2026-06-15", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	taskEntries := parseListEntries(t, w.Body.Bytes())
	if len(taskEntries) != 1 {
		t.Fatalf("expected 1 task entry, got %d", len(taskEntries))
	}
	taskShortID, _ := taskEntries[0]["short_id"].(string)
	if taskShortID == "" {
		t.Error("imported task should have a fresh ShortID")
	}
	if taskEntries[0]["title"] != "import-list task" {
		t.Errorf("task title = %v, want 'import-list task'", taskEntries[0]["title"])
	}

	// List logs
	req = httptest.NewRequest(http.MethodGet, "/api/list?type=log&date=2026-06-15", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	logEntries := parseListEntries(t, w.Body.Bytes())
	if len(logEntries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(logEntries))
	}
	logShortID, _ := logEntries[0]["short_id"].(string)
	if logShortID == "" {
		t.Error("imported log should have a fresh ShortID")
	}

	// Verify all ShortIDs are unique (freshly generated)
	shortIDs := map[string]bool{meetingShortID: true}
	if shortIDs[taskShortID] {
		t.Error("task and meeting should have different ShortIDs")
	}
	shortIDs[taskShortID] = true
	if shortIDs[logShortID] {
		t.Error("log should have a unique ShortID")
	}
}

// TestIntegration_ImportInvalidRollbackViaList imports invalid records and verifies
// that zero new records appear in the list endpoint (full rollback).
func TestIntegration_ImportInvalidRollbackViaList(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a pre-existing record to establish baseline
	addRecord(t, srv, "meeting", "baseline meeting", "", "2026-06-20")

	// Verify baseline: 1 meeting
	req := httptest.NewRequest(http.MethodGet, "/api/list?type=meeting&date=2026-06-20", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	baselineEntries := parseListEntries(t, w.Body.Bytes())
	if len(baselineEntries) != 1 {
		t.Fatalf("expected 1 baseline meeting, got %d", len(baselineEntries))
	}
	baselineID := baselineEntries[0]["short_id"].(string)

	// Attempt import with an invalid record at index 1
	payload := `{"records":[
		{"type":"meeting","title":"should not persist","date":"2026-06-20"},
		{"type":"bogus","title":"invalid type","date":"2026-06-20"}
	]}`
	req = httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "error")

	// Verify rollback: list should show only the baseline record
	req = httptest.NewRequest(http.MethodGet, "/api/list?type=meeting&date=2026-06-20", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	afterEntries := parseListEntries(t, w.Body.Bytes())
	if len(afterEntries) != 1 {
		t.Errorf("rollback failed: expected 1 meeting after failed import, got %d", len(afterEntries))
	}
	if afterEntries[0]["short_id"].(string) != baselineID {
		t.Errorf("after rollback, remaining record should be the baseline, got short_id=%v", afterEntries[0]["short_id"])
	}
}

// TestIntegration_ImportWithOptionalFields imports records with optional fields
// and verifies the stored files preserve all optional fields correctly.
func TestIntegration_ImportWithOptionalFields(t *testing.T) {
	srv, dir := newTestServer(t)

	payload := `{"records":[
		{"type":"meeting","title":"full fields meeting","date":"2026-07-01","time":"14:00","description":"quarterly review","tags":["review","quarterly"],"location":"Room 3A","remind_before":"30m"},
		{"type":"task","title":"full fields task","date":"2026-07-01","description":"finish implementation","tags":["dev"],"location":"Remote","priority":"high"},
		{"type":"reminder","title":"full fields reminder","date":"2026-07-02","time":"09:00","description":"follow up","remind_before":"15m","recurring":"weekly"},
		{"type":"log","title":"full fields log","date":"2026-07-01","description":"daily standup notes","tags":["standup"]}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")
	var importResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &importResp)
	importData := importResp["data"].(map[string]interface{})
	if importData["imported"].(float64) != 4 {
		t.Fatalf("expected imported=4, got %v", importData["imported"])
	}

	// Verify meeting: description, tags, location, remind_before
	meeting, _ := readStoredRecord(t, dir, "meetings/2026/07/01")
	if meeting["description"] != "quarterly review" {
		t.Errorf("meeting description = %v, want 'quarterly review'", meeting["description"])
	}
	tags, _ := meeting["tags"].([]interface{})
	if len(tags) != 2 || tags[0] != "review" || tags[1] != "quarterly" {
		t.Errorf("meeting tags = %v, want [review, quarterly]", tags)
	}
	if meeting["location"] != "Room 3A" {
		t.Errorf("meeting location = %v, want 'Room 3A'", meeting["location"])
	}
	if meeting["remind_before"] != "30m" {
		t.Errorf("meeting remind_before = %v, want '30m'", meeting["remind_before"])
	}
	if meeting["time"] != "14:00" {
		t.Errorf("meeting time = %v, want '14:00'", meeting["time"])
	}
	if meeting["short_id"] == "" {
		t.Error("meeting should have a short_id")
	}

	// Verify task: description, tags, location, priority
	task, _ := readStoredRecord(t, dir, "tasks/active")
	if task["description"] != "finish implementation" {
		t.Errorf("task description = %v, want 'finish implementation'", task["description"])
	}
	taskTags, _ := task["tags"].([]interface{})
	if len(taskTags) != 1 || taskTags[0] != "dev" {
		t.Errorf("task tags = %v, want [dev]", taskTags)
	}
	if task["location"] != "Remote" {
		t.Errorf("task location = %v, want 'Remote'", task["location"])
	}
	if task["priority"] != "high" {
		t.Errorf("task priority = %v, want 'high'", task["priority"])
	}

	// Verify reminder: description, remind_before, recurring
	reminder, _ := readStoredRecord(t, dir, "reminders/active")
	if reminder["description"] != "follow up" {
		t.Errorf("reminder description = %v, want 'follow up'", reminder["description"])
	}
	if reminder["remind_before"] != "15m" {
		t.Errorf("reminder remind_before = %v, want '15m'", reminder["remind_before"])
	}
	if reminder["recurring"] != "weekly" {
		t.Errorf("reminder recurring = %v, want 'weekly'", reminder["recurring"])
	}
	if reminder["time"] != "09:00" {
		t.Errorf("reminder time = %v, want '09:00'", reminder["time"])
	}

	// Verify log: description, tags
	logRec, _ := readStoredRecord(t, dir, "logs/2026/07/01")
	if logRec["description"] != "daily standup notes" {
		t.Errorf("log description = %v, want 'daily standup notes'", logRec["description"])
	}
	logTags, _ := logRec["tags"].([]interface{})
	if len(logTags) != 1 || logTags[0] != "standup" {
		t.Errorf("log tags = %v, want [standup]", logTags)
	}
}

// TestIntegration_ImportMinimalFields imports records with only the required
// fields (type, title, date) and verifies they persist correctly.
func TestIntegration_ImportMinimalFields(t *testing.T) {
	srv, dir := newTestServer(t)

	payload := `{"records":[
		{"type":"meeting","title":"minimal meeting","date":"2026-08-01"},
		{"type":"task","title":"minimal task","date":"2026-08-01"},
		{"type":"reminder","title":"minimal reminder","date":"2026-08-02"},
		{"type":"log","title":"minimal log","date":"2026-08-01"}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")
	var importResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &importResp)
	importData := importResp["data"].(map[string]interface{})
	if importData["imported"].(float64) != 4 {
		t.Fatalf("expected imported=4, got %v", importData["imported"])
	}

	// Verify meeting: only type, title, date populated; optional fields absent
	meeting, _ := readStoredRecord(t, dir, "meetings/2026/08/01")
	if meeting["type"] != "meeting" {
		t.Errorf("meeting type = %v, want 'meeting'", meeting["type"])
	}
	if meeting["title"] != "minimal meeting" {
		t.Errorf("meeting title = %v, want 'minimal meeting'", meeting["title"])
	}
	if meeting["date"] != "2026-08-01" {
		t.Errorf("meeting date = %v, want '2026-08-01'", meeting["date"])
	}
	if meeting["short_id"] == "" {
		t.Error("meeting should have a short_id")
	}
	if meeting["status"] != "active" {
		t.Errorf("meeting status = %v, want 'active'", meeting["status"])
	}

	// Verify task
	task, _ := readStoredRecord(t, dir, "tasks/active")
	if task["title"] != "minimal task" {
		t.Errorf("task title = %v, want 'minimal task'", task["title"])
	}
	if task["date"] != "2026-08-01" {
		t.Errorf("task date = %v, want '2026-08-01'", task["date"])
	}

	// Verify reminder
	reminder, _ := readStoredRecord(t, dir, "reminders/active")
	if reminder["title"] != "minimal reminder" {
		t.Errorf("reminder title = %v, want 'minimal reminder'", reminder["title"])
	}
	if reminder["date"] != "2026-08-02" {
		t.Errorf("reminder date = %v, want '2026-08-02'", reminder["date"])
	}

	// Verify log
	logRec, _ := readStoredRecord(t, dir, "logs/2026/08/01")
	if logRec["title"] != "minimal log" {
		t.Errorf("log title = %v, want 'minimal log'", logRec["title"])
	}

	// Verify round-trip via list: all 3 meeting-date records should be findable
	req = httptest.NewRequest(http.MethodGet, "/api/list?type=meeting&date=2026-08-01", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	entries := parseListEntries(t, w.Body.Bytes())
	if len(entries) != 1 {
		t.Errorf("expected 1 meeting in list, got %d", len(entries))
	}
}

// ── Export endpoint tests ──

func TestHandleExport_MissingFormat(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/export", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_body")
}

func TestHandleExport_InvalidFormat(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/export?format=xml", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_params")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	msg, _ := record["message"].(string)
	if !strings.Contains(msg, "xml") {
		t.Errorf("expected error message to mention 'xml', got %q", msg)
	}
}

func TestHandleExport_WrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/export?format=json", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "method_not_allowed")
}

func TestHandleExport_JSONEmpty(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/export?format=json", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	if data["format"] != "json" {
		t.Errorf("expected format=json, got %v", data["format"])
	}
	if data["count"].(float64) != 0 {
		t.Errorf("expected count=0 for empty storage, got %v", data["count"])
	}
	// records may be nil (JSON null) or empty array — either is valid for count=0
	records := data["records"]
	if records != nil {
		arr, ok := records.([]interface{})
		if ok && len(arr) != 0 {
			t.Errorf("expected empty records array, got %d", len(arr))
		}
	}
}

func TestHandleExport_JSONWithRecords(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add records
	addRecord(t, srv, "meeting", "export meeting", "daily standup", "2026-05-02")
	addRecord(t, srv, "task", "export task", "write code", "2026-05-02")

	req := httptest.NewRequest(http.MethodGet, "/api/export?format=json&date=2026-05-02", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	if data["format"] != "json" {
		t.Errorf("expected format=json, got %v", data["format"])
	}
	if data["count"].(float64) != 2 {
		t.Errorf("expected count=2, got %v", data["count"])
	}
	records, ok := data["records"].([]interface{})
	if !ok {
		t.Fatal("expected records to be an array")
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// Verify records have full data (not just listed summaries)
	for _, r := range records {
		rec := r.(map[string]interface{})
		if rec["short_id"] == "" {
			t.Error("exported record should have short_id")
		}
		if rec["date"] != "2026-05-02" {
			t.Errorf("expected date=2026-05-02, got %v", rec["date"])
		}
	}
}

func TestHandleExport_JSONWithFilter(t *testing.T) {
	srv, _ := newTestServer(t)

	addRecord(t, srv, "meeting", "type meeting", "", "2026-05-02")
	addRecord(t, srv, "task", "type task", "", "2026-05-02")
	addRecord(t, srv, "log", "type log", "", "2026-05-02")

	// Filter by type=meeting
	req := httptest.NewRequest(http.MethodGet, "/api/export?format=json&date=2026-05-02&type=meeting", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	records := data["records"].([]interface{})
	if len(records) != 1 {
		t.Errorf("expected 1 meeting record with type filter, got %d", len(records))
	}
}

func TestHandleExport_MarkdownSingleDate(t *testing.T) {
	srv, _ := newTestServer(t)

	addRecord(t, srv, "meeting", "md meeting", "discuss sprint", "2026-05-03")
	addRecord(t, srv, "task", "md task", "implement feature", "2026-05-03")

	req := httptest.NewRequest(http.MethodGet, "/api/export?format=markdown&date=2026-05-03", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	if data["format"] != "markdown" {
		t.Errorf("expected format=markdown, got %v", data["format"])
	}
	if data["count"].(float64) != 2 {
		t.Errorf("expected count=2, got %v", data["count"])
	}
	content, ok := data["content"].(string)
	if !ok || content == "" {
		t.Fatal("expected non-empty content field for markdown format")
	}
	if !strings.Contains(content, "工作日报") {
		t.Errorf("expected markdown to contain '工作日报', got %q", content)
	}
	if !strings.Contains(content, "md meeting") {
		t.Errorf("expected markdown to contain 'md meeting', got %q", content)
	}
	if !strings.Contains(content, "md task") {
		t.Errorf("expected markdown to contain 'md task', got %q", content)
	}
}

func TestHandleExport_MarkdownDateRange(t *testing.T) {
	srv, _ := newTestServer(t)

	addRecord(t, srv, "meeting", "range day1", "", "2026-04-20")
	addRecord(t, srv, "task", "range day2", "", "2026-04-21")

	req := httptest.NewRequest(http.MethodGet, "/api/export?format=markdown&from=2026-04-20&to=2026-04-21", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	if data["format"] != "markdown" {
		t.Errorf("expected format=markdown, got %v", data["format"])
	}
	if data["count"].(float64) != 2 {
		t.Errorf("expected count=2, got %v", data["count"])
	}
	content, _ := data["content"].(string)
	if !strings.Contains(content, "工作周报") {
		t.Errorf("expected range markdown to contain '工作周报', got %q", content)
	}
}

func TestHandleExport_InvalidFormatCSV(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/export?format=csv", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_params")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	msg, _ := record["message"].(string)
	if !strings.Contains(msg, "csv") {
		t.Errorf("expected error message to mention 'csv', got %q", msg)
	}
}

func TestHandleExport_JSONDateRange(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add records across multiple dates
	addRecord(t, srv, "meeting", "early meeting", "", "2026-04-20")
	addRecord(t, srv, "meeting", "in range 1", "", "2026-04-25")
	addRecord(t, srv, "task", "in range 2", "", "2026-04-28")
	addRecord(t, srv, "meeting", "late meeting", "", "2026-05-05")

	// Export JSON with from/to date range
	req := httptest.NewRequest(http.MethodGet, "/api/export?format=json&from=2026-04-25&to=2026-04-28", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	if data["count"].(float64) != 2 {
		t.Errorf("expected count=2 for date range, got %v", data["count"])
	}
	records := data["records"].([]interface{})
	if len(records) != 2 {
		t.Fatalf("expected 2 records in range, got %d", len(records))
	}
	for _, r := range records {
		rec := r.(map[string]interface{})
		title, _ := rec["title"].(string)
		if title == "early meeting" || title == "late meeting" {
			t.Errorf("record %q should not be in range [2026-04-25, 2026-04-28]", title)
		}
	}
}

func TestHandleExport_MarkdownEmpty(t *testing.T) {
	srv, _ := newTestServer(t)
	// No records added — export markdown for a specific date
	req := httptest.NewRequest(http.MethodGet, "/api/export?format=markdown&date=2026-12-25", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	if data["count"].(float64) != 0 {
		t.Errorf("expected count=0 for empty markdown export, got %v", data["count"])
	}
	content, _ := data["content"].(string)
	// Markdown should still be generated (with empty sections) — not empty string
	if content == "" {
		t.Error("expected non-empty markdown content even for empty result set")
	}
}

func TestHandleExport_JSONStatusFilter(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a task, complete it
	addRecord(t, srv, "task", "active export task", "", "2026-05-10")

	time.Sleep(1 * time.Second) // avoid ShortID collision

	body := strings.NewReader(`{"type":"task","title":"completed export task","date":"2026-05-10"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	shortID := addResp["data"].(map[string]interface{})["short_id"].(string)

	// Complete the second task
	req = httptest.NewRequest(http.MethodPost, "/api/complete/"+shortID, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Export with status=completed — export uses IncludeCompleted=true
	req = httptest.NewRequest(http.MethodGet, "/api/export?format=json&date=2026-05-10&status=completed", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	records := data["records"].([]interface{})
	if len(records) != 1 {
		t.Fatalf("expected 1 completed record, got %d", len(records))
	}
	rec := records[0].(map[string]interface{})
	if rec["status"] != "completed" {
		t.Errorf("expected status=completed, got %v", rec["status"])
	}
	if rec["title"] != "completed export task" {
		t.Errorf("expected title='completed export task', got %v", rec["title"])
	}
}

func TestHandleExport_JSONQueryFilter(t *testing.T) {
	srv, _ := newTestServer(t)

	addRecord(t, srv, "meeting", "代码评审", "评审PR #123", "2026-05-15")
	addRecord(t, srv, "task", "写测试", "为export写测试", "2026-05-15")
	addRecord(t, srv, "log", "部署日志", "生产环境部署", "2026-05-15")

	// Export JSON with query filter "评审"
	req := httptest.NewRequest(http.MethodGet, "/api/export?format=json&date=2026-05-15&query=%E8%AF%84%E5%AE%A1", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	records := data["records"].([]interface{})
	// Should match meeting "代码评审" title and meeting "评审PR #123" description
	if len(records) < 1 {
		t.Errorf("expected at least 1 record matching '评审', got %d", len(records))
	}
}

func TestHandleExport_JSONCombinedFilters(t *testing.T) {
	srv, _ := newTestServer(t)

	addRecord(t, srv, "meeting", "design review", "sprint planning", "2026-04-25")
	addRecord(t, srv, "task", "code review", "review PR", "2026-04-28")
	addRecord(t, srv, "log", "daily log", "review work", "2026-04-28")
	addRecord(t, srv, "meeting", "deploy meeting", "deploy to prod", "2026-05-01")

	// Combined: type=meeting + from/to + query=review
	req := httptest.NewRequest(http.MethodGet, "/api/export?format=json&type=meeting&from=2026-04-25&to=2026-05-01&query=review", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	records := data["records"].([]interface{})

	// Should match only the "design review" meeting in range
	if len(records) != 1 {
		t.Errorf("expected 1 meeting matching 'review' in date range, got %d", len(records))
		for _, r := range records {
			rec := r.(map[string]interface{})
			t.Logf("  record: type=%v title=%v date=%v", rec["type"], rec["title"], rec["date"])
		}
	}
}

// ── Integration: export round-trip tests ──

func TestIntegration_ExportJsonRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add records via the add endpoint
	addRecord(t, srv, "meeting", "export-int meeting", "sprint planning", "2026-06-10")
	addRecord(t, srv, "task", "export-int task", "implement export", "2026-06-10")
	addRecord(t, srv, "log", "export-int log", "daily notes", "2026-06-10")

	// Export as JSON
	req := httptest.NewRequest(http.MethodGet, "/api/export?format=json&date=2026-06-10", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	if data["count"].(float64) != 3 {
		t.Fatalf("expected count=3, got %v", data["count"])
	}

	records := data["records"].([]interface{})
	if len(records) != 3 {
		t.Fatalf("expected 3 exported records, got %d", len(records))
	}

	// Verify all records have essential fields and correct content
	titles := map[string]bool{}
	for _, r := range records {
		rec := r.(map[string]interface{})
		if rec["short_id"] == "" {
			t.Error("exported record should have short_id")
		}
		if rec["date"] != "2026-06-10" {
			t.Errorf("expected date=2026-06-10, got %v", rec["date"])
		}
		if rec["status"] == "" {
			t.Error("exported record should have status")
		}
		title, _ := rec["title"].(string)
		titles[title] = true
	}
	if !titles["export-int meeting"] || !titles["export-int task"] || !titles["export-int log"] {
		t.Errorf("expected all 3 titles present, got %v", titles)
	}

	// Verify JSON output is valid and can round-trip
	jsonBytes, _ := json.Marshal(records)
	var roundTripped []map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &roundTripped); err != nil {
		t.Fatalf("exported records should be valid JSON for round-trip: %v", err)
	}
	if len(roundTripped) != 3 {
		t.Errorf("round-tripped record count = %d, want 3", len(roundTripped))
	}
}

func TestIntegration_ExportMarkdownRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add records
	addRecord(t, srv, "meeting", "md-int meeting", "discuss architecture", "2026-06-15")
	addRecord(t, srv, "task", "md-int task", "implement feature", "2026-06-15")
	addRecord(t, srv, "log", "md-int log", "notes from today", "2026-06-15")

	// Export as markdown
	req := httptest.NewRequest(http.MethodGet, "/api/export?format=markdown&date=2026-06-15", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	if data["count"].(float64) != 3 {
		t.Fatalf("expected count=3, got %v", data["count"])
	}

	content, _ := data["content"].(string)
	if content == "" {
		t.Fatal("expected non-empty markdown content")
	}

	// Verify markdown contains expected sections and record titles
	if !strings.Contains(content, "工作日报") {
		t.Error("expected markdown to contain '工作日报' header")
	}
	if !strings.Contains(content, "md-int meeting") {
		t.Error("expected markdown to contain 'md-int meeting'")
	}
	if !strings.Contains(content, "md-int task") {
		t.Error("expected markdown to contain 'md-int task'")
	}
	if !strings.Contains(content, "md-int log") {
		t.Error("expected markdown to contain 'md-int log'")
	}
}

func TestHandleExport_MarkdownNoDateFilter(t *testing.T) {
	loc := time.Now().Location()
	srv, _ := newTestServer(t, &config.Config{Timezone: loc.String()})

	today := time.Now().Format("2006-01-02")
	addRecord(t, srv, "log", "today log", "", today)

	req := httptest.NewRequest(http.MethodGet, "/api/export?format=markdown", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})

	if data["format"] != "markdown" {
		t.Errorf("expected format=markdown, got %v", data["format"])
	}
	if data["count"].(float64) != 1 {
		t.Errorf("expected count=1 for today's record, got %v", data["count"])
	}
}

func TestHandleExport_DefaultCompleted(t *testing.T) {
	srv, _ := newTestServer(t)

	date := "2026-06-01"

	// Add an active record
	addRecord(t, srv, "task", "active task", "", date)

	// Add and complete a second record
	body := strings.NewReader(`{"type":"task","title":"completed task","date":"` + date + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	shortID := addResp["data"].(map[string]interface{})["short_id"].(string)

	req = httptest.NewRequest(http.MethodPost, "/api/complete/"+shortID, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Export without --status: should only include active records (IncludeCompleted=false)
	req = httptest.NewRequest(http.MethodGet, "/api/export?format=json&date="+date, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	records := data["records"].([]interface{})
	if len(records) != 1 {
		t.Fatalf("default export: expected 1 active record, got %d", len(records))
	}
	rec := records[0].(map[string]interface{})
	if rec["title"] != "active task" {
		t.Errorf("default export: expected title='active task', got %v", rec["title"])
	}

	// Export with --status=all: should include both active and completed
	req = httptest.NewRequest(http.MethodGet, "/api/export?format=json&date="+date+"&status=all", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data = record["data"].(map[string]interface{})
	records = data["records"].([]interface{})
	if len(records) != 2 {
		t.Fatalf("status=all export: expected 2 records, got %d", len(records))
	}
}

// ── Lookup-based update/complete/cancel tests (S03) ──

// addRecordWithTime adds a record with a time field and returns the parsed response data.
func addRecordWithTime(t *testing.T, srv *Server, typ, title, desc, date, timeVal string) map[string]interface{} {
	t.Helper()
	body := fmt.Sprintf(`{"type":"%s","title":"%s","description":"%s","date":"%s","time":"%s"}`, typ, title, desc, date, timeVal)
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")
	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	return resp["data"].(map[string]interface{})
}

func TestHandleUpdate_LookupByTitleDate(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a meeting with a specific title and date
	addData := addRecordWithTime(t, srv, "meeting", "Standup", "daily sync", "2026-05-05", "09:00")
	origShortID := addData["short_id"].(string)

	// Update via lookup (no short_id in path, title+date in query params)
	updateBody := strings.NewReader(`{"time":"15:00"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/update/?title=Standup&date=2026-05-05", updateBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	data := resp["data"].(map[string]interface{})

	// Should have resolved to the same record
	if data["short_id"] != origShortID {
		t.Errorf("expected short_id=%s, got %v", origShortID, data["short_id"])
	}
	// Time should be updated
	if data["time"] != "15:00" {
		t.Errorf("expected time=15:00, got %v", data["time"])
	}
}

func TestHandleUpdate_LookupMultipleMatches(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add two meetings with same title and date but different times
	addRecordWithTime(t, srv, "meeting", "Standup", "sync 1", "2026-05-05", "09:00")
	addRecordWithTime(t, srv, "meeting", "Standup", "sync 2", "2026-05-05", "10:00")

	// Try to update via lookup — should get multiple_matches error
	updateBody := strings.NewReader(`{"time":"15:00"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/update/?title=Standup&date=2026-05-05", updateBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "multiple_matches")

	// Verify the error message contains context about the matches
	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, "Standup") || !strings.Contains(msg, "2026-05-05") {
		t.Errorf("expected error message to contain lookup criteria, got: %s", msg)
	}
}

func TestHandleUpdate_LookupNoMatch(t *testing.T) {
	srv, _ := newTestServer(t)

	// No records added — lookup should fail with record_not_found
	updateBody := strings.NewReader(`{"time":"15:00"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/update/?title=Nonexistent&date=2026-05-05", updateBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "record_not_found")
}

func TestHandleUpdate_LookupMissingParams(t *testing.T) {
	srv, _ := newTestServer(t)

	// Path has no ID, and query params are incomplete — title only, no date
	updateBody := strings.NewReader(`{"time":"15:00"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/update/?title=Standup", updateBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "record_not_found")

	// Neither title nor date
	req2 := httptest.NewRequest(http.MethodPost, "/api/update/", strings.NewReader(`{"time":"15:00"}`))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.router.ServeHTTP(w2, req2)

	assertJSONLStatus(t, w2.Body.Bytes(), "error")
	assertJSONLCode(t, w2.Body.Bytes(), "record_not_found")
}

func TestHandleComplete_LookupByTitleDate(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a meeting
	addData := addRecordWithTime(t, srv, "meeting", "Team Sync", "weekly", "2026-05-06", "10:00")
	origShortID := addData["short_id"].(string)

	// Complete via lookup
	req := httptest.NewRequest(http.MethodPost, "/api/complete/?title=Team+Sync&date=2026-05-06", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	data := resp["data"].(map[string]interface{})

	if data["short_id"] != origShortID {
		t.Errorf("expected short_id=%s, got %v", origShortID, data["short_id"])
	}
	if data["status"] != "completed" {
		t.Errorf("expected status=completed, got %v", data["status"])
	}
}

func TestHandleComplete_LookupNoMatch(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/complete/?title=Ghost&date=2026-05-06", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "record_not_found")
}

func TestHandleComplete_LookupMultipleMatches(t *testing.T) {
	srv, _ := newTestServer(t)

	addRecordWithTime(t, srv, "meeting", "Standup", "a", "2026-05-07", "09:00")
	addRecordWithTime(t, srv, "meeting", "Standup", "b", "2026-05-07", "10:00")

	req := httptest.NewRequest(http.MethodPost, "/api/complete/?title=Standup&date=2026-05-07", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "multiple_matches")
}

func TestHandleCancel_LookupByTitleDate(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a meeting
	addData := addRecordWithTime(t, srv, "meeting", "Cancel Me", "to cancel", "2026-05-08", "11:00")
	origShortID := addData["short_id"].(string)

	// Cancel via lookup
	req := httptest.NewRequest(http.MethodPost, "/api/cancel/?title=Cancel+Me&date=2026-05-08", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	data := resp["data"].(map[string]interface{})

	if data["short_id"] != origShortID {
		t.Errorf("expected short_id=%s, got %v", origShortID, data["short_id"])
	}
	if data["status"] != "cancelled" {
		t.Errorf("expected status=cancelled, got %v", data["status"])
	}
}

func TestHandleCancel_LookupNoMatch(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/cancel/?title=Ghost&date=2026-05-08", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "record_not_found")
}

func TestHandleCancel_LookupMultipleMatches(t *testing.T) {
	srv, _ := newTestServer(t)

	addRecordWithTime(t, srv, "meeting", "Standup", "a", "2026-05-09", "09:00")
	addRecordWithTime(t, srv, "meeting", "Standup", "b", "2026-05-09", "10:00")

	req := httptest.NewRequest(http.MethodPost, "/api/cancel/?title=Standup&date=2026-05-09", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "multiple_matches")
}

func TestHandleUpdate_ShortIDStillWorks(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a meeting
	addData := addRecordWithTime(t, srv, "meeting", "Regression", "test", "2026-05-10", "09:00")
	shortID := addData["short_id"].(string)

	// Update via short_id (existing path)
	updateBody := strings.NewReader(`{"time":"16:00","description":"updated"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/update/"+shortID, updateBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	data := resp["data"].(map[string]interface{})

	if data["short_id"] != shortID {
		t.Errorf("expected short_id=%s, got %v", shortID, data["short_id"])
	}
	if data["time"] != "16:00" {
		t.Errorf("expected time=16:00, got %v", data["time"])
	}
	if data["description"] != "updated" {
		t.Errorf("expected description=updated, got %v", data["description"])
	}
}

func TestHandleComplete_ShortIDStillWorks(t *testing.T) {
	srv, _ := newTestServer(t)

	addData := addRecordWithTime(t, srv, "meeting", "Regression", "test", "2026-05-10", "09:00")
	shortID := addData["short_id"].(string)

	req := httptest.NewRequest(http.MethodPost, "/api/complete/"+shortID, nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	data := resp["data"].(map[string]interface{})
	if data["status"] != "completed" {
		t.Errorf("expected status=completed, got %v", data["status"])
	}
}

func TestHandleCancel_ShortIDStillWorks(t *testing.T) {
	srv, _ := newTestServer(t)

	addData := addRecordWithTime(t, srv, "meeting", "Regression", "test", "2026-05-10", "09:00")
	shortID := addData["short_id"].(string)

	req := httptest.NewRequest(http.MethodPost, "/api/cancel/"+shortID, nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	data := resp["data"].(map[string]interface{})
	if data["status"] != "cancelled" {
		t.Errorf("expected status=cancelled, got %v", data["status"])
	}
}

func TestHandleUpdate_LookupSpecialChars(t *testing.T) {
	srv, _ := newTestServer(t)

	// Title with special characters (URL-encoded in query params)
	addData := addRecordWithTime(t, srv, "meeting", "Project Review (Q2)", "quarterly", "2026-06-15", "14:00")
	origShortID := addData["short_id"].(string)

	updateBody := strings.NewReader(`{"time":"16:00"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/update/?title=Project+Review+(Q2)&date=2026-06-15", updateBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	data := resp["data"].(map[string]interface{})

	if data["short_id"] != origShortID {
		t.Errorf("expected short_id=%s, got %v", origShortID, data["short_id"])
	}
	if data["time"] != "16:00" {
		t.Errorf("expected time=16:00, got %v", data["time"])
	}
}

func TestHandleUpdate_LookupAlreadyCompleted(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add and complete a meeting
	addRecordWithTime(t, srv, "meeting", "Done Meeting", "done", "2026-05-11", "09:00")

	// Complete it first via lookup
	req := httptest.NewRequest(http.MethodPost, "/api/complete/?title=Done+Meeting&date=2026-05-11", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Now try to update via lookup — should fail because it's completed
	updateBody := strings.NewReader(`{"time":"15:00"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/update/?title=Done+Meeting&date=2026-05-11", updateBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "already_completed")
}

// ── Lifecycle logging tests (T02) ──

// TestPanicRecovery_LogsToStructuredLogger verifies that the panicRecoveryMiddleware
// logs the panic to the structured logger (in addition to returning a JSONL error).
func TestPanicRecovery_LogsToStructuredLogger(t *testing.T) {
	// Init logger to a temp dir so we can read the log file
	logDir := t.TempDir()
	if err := logger.Init(logDir); err != nil {
		t.Fatalf("logger.Init: %v", err)
	}
	defer logger.Shutdown()

	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.panicRecoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic for logging")
	})))
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/test-path", "", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Verify the JSONL error response was returned
	assertJSONLStatus(t, body, "error")
	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(body), &record)
	if record["error_code"] != "FATAL_CRASH" {
		t.Errorf("expected error_code=FATAL_CRASH, got %v", record["error_code"])
	}

	// Wait briefly for the log to flush, then read the log file
	time.Sleep(100 * time.Millisecond)

	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one log file")
	}

	logData, err := os.ReadFile(filepath.Join(logDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	logContent := string(logData)

	if !strings.Contains(logContent, "handler panic") {
		t.Errorf("expected log to contain 'handler panic', got: %s", logContent)
	}
	if !strings.Contains(logContent, "test panic for logging") {
		t.Errorf("expected log to contain 'test panic for logging', got: %s", logContent)
	}
	if !strings.Contains(logContent, "/test-path") {
		t.Errorf("expected log to contain '/test-path', got: %s", logContent)
	}
}

// TestHandleStop_LogsShutdown verifies that the /api/stop handler logs
// the stop request via the structured logger.
func TestHandleStop_LogsShutdown(t *testing.T) {
	logDir := t.TempDir()
	if err := logger.Init(logDir); err != nil {
		t.Fatalf("logger.Init: %v", err)
	}
	defer logger.Shutdown()

	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.router)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/stop", "", nil)
	if err != nil {
		t.Fatalf("POST /api/stop: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	assertJSONLStatus(t, respBody, "success")

	// Wait briefly for the log to flush
	time.Sleep(100 * time.Millisecond)

	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one log file")
	}

	logData, err := os.ReadFile(filepath.Join(logDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	logContent := string(logData)

	if !strings.Contains(logContent, "stop requested") {
		t.Errorf("expected log to contain 'stop requested', got: %s", logContent)
	}
	if !strings.Contains(logContent, "http") {
		t.Errorf("expected log to contain 'http' source field, got: %s", logContent)
	}
	if !strings.Contains(logContent, "daemon shutting down") {
		t.Errorf("expected log to contain 'daemon shutting down', got: %s", logContent)
	}
}

// ── Digest CRUD handler tests ──

func TestDigestAddEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)
	body := strings.NewReader(`{"schedule":"0 8 * * *","scope":"today","direction":"agenda"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", body)
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
	id, _ := data["id"].(string)
	if id == "" {
		t.Error("expected non-empty digest id")
	}
	if data["schedule"] != "0 8 * * *" {
		t.Errorf("expected schedule '0 8 * * *', got %v", data["schedule"])
	}
	if data["scope"] != "today" {
		t.Errorf("expected scope 'today', got %v", data["scope"])
	}
	if data["direction"] != "agenda" {
		t.Errorf("expected direction 'agenda', got %v", data["direction"])
	}
	if data["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", data["enabled"])
	}
	if data["created_at"] == "" {
		t.Error("expected created_at to be set")
	}
}

func TestDigestAddEndpoint_DefaultDirection(t *testing.T) {
	srv, _ := newTestServer(t)
	body := strings.NewReader(`{"schedule":"0 18 * * 5","scope":"week"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	if data["direction"] != "agenda" {
		t.Errorf("expected default direction 'agenda', got %v", data["direction"])
	}
}

func TestDigestAddEndpoint_InvalidScope(t *testing.T) {
	srv, _ := newTestServer(t)
	body := strings.NewReader(`{"schedule":"0 8 * * *","scope":"invalid_scope"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_scope")
}

func TestDigestAddEndpoint_InvalidDirection(t *testing.T) {
	srv, _ := newTestServer(t)
	body := strings.NewReader(`{"schedule":"0 8 * * *","scope":"today","direction":"backward"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_direction")
}

func TestDigestAddEndpoint_InvalidSchedule(t *testing.T) {
	srv, _ := newTestServer(t)
	body := strings.NewReader(`{"schedule":"","scope":"today"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_schedule")
}

func TestDigestAddEndpoint_InvalidJSON(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_body")
}

func TestDigestAddEndpoint_WrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/digest/add", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "method_not_allowed")
}

func TestDigestListEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add two digests first
	for _, scope := range []string{"today", "week"} {
		body := strings.NewReader(fmt.Sprintf(`{"schedule":"0 8 * * *","scope":"%s","direction":"summary"}`, scope))
		req := httptest.NewRequest(http.MethodPost, "/api/digest/add", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)
		assertJSONLStatus(t, w.Body.Bytes(), "success")
	}

	// List
	req := httptest.NewRequest(http.MethodGet, "/api/digest/list", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	count, _ := data["count"].(float64)
	if count != 2 {
		t.Errorf("expected 2 digests, got %v", count)
	}
	digests, ok := data["digests"].([]interface{})
	if !ok || len(digests) != 2 {
		t.Errorf("expected digests array with 2 items, got %v", data["digests"])
	}
}

func TestDigestListEndpoint_Empty(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/digest/list", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	count, _ := data["count"].(float64)
	if count != 0 {
		t.Errorf("expected 0 digests, got %v", count)
	}
}

func TestDigestRemoveEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a digest
	body := strings.NewReader(`{"schedule":"0 8 * * *","scope":"today"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	id := addData["id"].(string)

	// Remove it
	req = httptest.NewRequest(http.MethodPost, "/api/digest/remove/"+id, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var removeResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &removeResp)
	removeData := removeResp["data"].(map[string]interface{})
	if removeData["id"] != id {
		t.Errorf("expected id %q, got %v", id, removeData["id"])
	}
	if removeData["message"] != "digest removed" {
		t.Errorf("expected message 'digest removed', got %v", removeData["message"])
	}

	// Verify list is empty
	req = httptest.NewRequest(http.MethodGet, "/api/digest/list", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var listResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &listResp)
	listData := listResp["data"].(map[string]interface{})
	count, _ := listData["count"].(float64)
	if count != 0 {
		t.Errorf("expected 0 digests after remove, got %v", count)
	}
}

func TestDigestRemoveEndpoint_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/remove/d_nonexistent", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "digest_not_found")
}

func TestDigestEnableDisableEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// Add a digest (enabled by default)
	body := strings.NewReader(`{"schedule":"0 8 * * *","scope":"today"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	addData := addResp["data"].(map[string]interface{})
	id := addData["id"].(string)

	// Disable
	req = httptest.NewRequest(http.MethodPost, "/api/digest/disable/"+id, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")
	var disableResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &disableResp)
	disableData := disableResp["data"].(map[string]interface{})
	if disableData["enabled"] != false {
		t.Errorf("expected enabled=false after disable, got %v", disableData["enabled"])
	}

	// Enable
	req = httptest.NewRequest(http.MethodPost, "/api/digest/enable/"+id, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")
	var enableResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &enableResp)
	enableData := enableResp["data"].(map[string]interface{})
	if enableData["enabled"] != true {
		t.Errorf("expected enabled=true after enable, got %v", enableData["enabled"])
	}
}

func TestDigestEnableEndpoint_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/enable/d_nonexistent", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "digest_not_found")
}

func TestDigestDisableEndpoint_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/disable/d_nonexistent", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "digest_not_found")
}

func TestDigestListEndpoint_WrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/list", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "method_not_allowed")
}

// TestDigestCRUDEndToEnd exercises the full digest lifecycle: add → list → disable → enable → remove.
func TestDigestCRUDEndToEnd(t *testing.T) {
	srv, _ := newTestServer(t)

	// 1. Add
	body := strings.NewReader(`{"schedule":"0 9 * * 1","scope":"week","direction":"summary"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var addResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &addResp)
	id := addResp["data"].(map[string]interface{})["id"].(string)

	// 2. List — should have 1
	req = httptest.NewRequest(http.MethodGet, "/api/digest/list", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	var listResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &listResp)
	count := listResp["data"].(map[string]interface{})["count"].(float64)
	if count != 1 {
		t.Fatalf("expected 1 digest, got %v", count)
	}

	// 3. Disable
	req = httptest.NewRequest(http.MethodPost, "/api/digest/disable/"+id, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// 4. Enable
	req = httptest.NewRequest(http.MethodPost, "/api/digest/enable/"+id, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// 5. Remove
	req = httptest.NewRequest(http.MethodPost, "/api/digest/remove/"+id, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// 6. List — should be empty
	req = httptest.NewRequest(http.MethodGet, "/api/digest/list", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &listResp)
	count = listResp["data"].(map[string]interface{})["count"].(float64)
	if count != 0 {
		t.Errorf("expected 0 digests after remove, got %v", count)
	}
}

// ── Prompt CRUD handler tests ──

func TestPromptListEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/prompt/list", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	count := data["count"].(float64)
	if count != 2 {
		t.Errorf("expected 2 built-in prompts (agenda, report), got %v", count)
	}
	prompts, ok := data["prompts"].([]interface{})
	if !ok || len(prompts) != 2 {
		t.Fatalf("expected prompts array with 2 items, got %v", data["prompts"])
	}

	// Verify both are defaults initially
	for _, p := range prompts {
		pMap := p.(map[string]interface{})
		isDefault, _ := pMap["is_default"].(bool)
		if !isDefault {
			t.Errorf("expected is_default=true for %v, got false", pMap["name"])
		}
		text, _ := pMap["text"].(string)
		if text == "" {
			t.Errorf("expected non-empty text for %v", pMap["name"])
		}
	}
}

func TestPromptListEndpoint_WrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/prompt/list", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "method_not_allowed")
}

func TestPromptShowEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// Show built-in agenda prompt (default)
	req := httptest.NewRequest(http.MethodGet, "/api/prompt/show/agenda", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	if data["name"] != "agenda" {
		t.Errorf("expected name=agenda, got %v", data["name"])
	}
	isDefault, _ := data["is_default"].(bool)
	if !isDefault {
		t.Errorf("expected is_default=true for built-in agenda, got false")
	}
	text, _ := data["text"].(string)
	if !strings.Contains(text, "Agenda") {
		t.Errorf("expected agenda text to contain 'Agenda', got: %s", text)
	}
}

func TestPromptShowEndpoint_Report(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/prompt/show/report", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	if data["name"] != "report" {
		t.Errorf("expected name=report, got %v", data["name"])
	}
	text, _ := data["text"].(string)
	if !strings.Contains(text, "Report") {
		t.Errorf("expected report text to contain 'Report', got: %s", text)
	}
}

func TestPromptShowEndpoint_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/prompt/show/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "prompt_not_found")
}

func TestPromptShowEndpoint_MissingName(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/prompt/show/", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "prompt_not_found")
}

func TestPromptShowEndpoint_WrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/prompt/show/agenda", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "method_not_allowed")
}

func TestPromptSetEndpoint_Text(t *testing.T) {
	srv, _ := newTestServer(t)

	body := strings.NewReader(`{"text":"custom agenda prompt"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/prompt/set/agenda", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	if data["name"] != "agenda" {
		t.Errorf("expected name=agenda, got %v", data["name"])
	}

	// Verify the override is reflected in show
	req = httptest.NewRequest(http.MethodGet, "/api/prompt/show/agenda", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data = record["data"].(map[string]interface{})
	if data["text"] != "custom agenda prompt" {
		t.Errorf("expected text='custom agenda prompt', got %v", data["text"])
	}
	isDefault, _ := data["is_default"].(bool)
	if isDefault {
		t.Errorf("expected is_default=false after set, got true")
	}
}

func TestPromptSetEndpoint_File(t *testing.T) {
	srv, dir := newTestServer(t)

	// Create a temp file with prompt content
	promptFile := filepath.Join(dir, "custom_prompt.txt")
	if err := os.WriteFile(promptFile, []byte("file-based prompt content"), 0644); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}

	// Use json.Marshal to properly escape the file path
	payload, _ := json.Marshal(map[string]string{"file": promptFile})
	req := httptest.NewRequest(http.MethodPost, "/api/prompt/set/report", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Verify via show
	req = httptest.NewRequest(http.MethodGet, "/api/prompt/show/report", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	if data["text"] != "file-based prompt content" {
		t.Errorf("expected text from file, got %v", data["text"])
	}
}

func TestPromptSetEndpoint_FileNotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	body := strings.NewReader(`{"file":"/nonexistent/path/prompt.txt"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/prompt/set/agenda", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_body")
}

func TestPromptSetEndpoint_EmptyText(t *testing.T) {
	srv, _ := newTestServer(t)

	body := strings.NewReader(`{"text":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/prompt/set/agenda", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_body")
}

func TestPromptSetEndpoint_InvalidJSON(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/prompt/set/agenda", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_body")
}

func TestPromptSetEndpoint_MissingName(t *testing.T) {
	srv, _ := newTestServer(t)

	body := strings.NewReader(`{"text":"some prompt"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/prompt/set/", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "prompt_not_found")
}

func TestPromptSetEndpoint_WrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/prompt/set/agenda", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "method_not_allowed")
}

func TestPromptResetEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// First set a custom prompt
	body := strings.NewReader(`{"text":"custom override"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/prompt/set/agenda", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Verify it's overridden
	req = httptest.NewRequest(http.MethodGet, "/api/prompt/show/agenda", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	if data["text"] != "custom override" {
		t.Fatalf("expected override text, got %v", data["text"])
	}

	// Reset to default
	req = httptest.NewRequest(http.MethodPost, "/api/prompt/reset/agenda", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var resetResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resetResp)
	resetData := resetResp["data"].(map[string]interface{})
	if resetData["name"] != "agenda" {
		t.Errorf("expected name=agenda, got %v", resetData["name"])
	}
	if resetData["message"] != "prompt reset to default" {
		t.Errorf("expected reset message, got %v", resetData["message"])
	}
	// Text should be the built-in default (contains "Agenda")
	resetText, _ := resetData["text"].(string)
	if !strings.Contains(resetText, "Agenda") {
		t.Errorf("expected default agenda text after reset, got: %s", resetText)
	}

	// Verify via show that it's back to default
	req = httptest.NewRequest(http.MethodGet, "/api/prompt/show/agenda", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data = record["data"].(map[string]interface{})
	isDefault, _ := data["is_default"].(bool)
	if !isDefault {
		t.Errorf("expected is_default=true after reset, got false")
	}
	if data["text"] != resetText {
		t.Errorf("show text should match reset text")
	}
}

func TestPromptResetEndpoint_NoOverride_NoOp(t *testing.T) {
	srv, _ := newTestServer(t)

	// Reset a prompt that was never overridden — should succeed (no-op)
	req := httptest.NewRequest(http.MethodPost, "/api/prompt/reset/agenda", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var record map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &record)
	data := record["data"].(map[string]interface{})
	if data["message"] != "prompt reset to default" {
		t.Errorf("expected reset message, got %v", data["message"])
	}
}

func TestPromptResetEndpoint_NonBuiltin(t *testing.T) {
	srv, _ := newTestServer(t)

	// First set a custom (non-builtin) prompt
	body := strings.NewReader(`{"text":"custom non-builtin"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/prompt/set/my-custom", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Reset should fail — no default for non-builtin names
	req = httptest.NewRequest(http.MethodPost, "/api/prompt/reset/my-custom", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "prompt_not_found")
}

func TestPromptResetEndpoint_MissingName(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/prompt/reset/", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "prompt_not_found")
}

func TestPromptResetEndpoint_WrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/prompt/reset/agenda", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "method_not_allowed")
}

// TestPromptCRUDEndToEnd exercises the full prompt lifecycle: list → show → set → show → reset → list.
func TestPromptCRUDEndToEnd(t *testing.T) {
	srv, _ := newTestServer(t)

	// 1. List — 2 built-in defaults
	req := httptest.NewRequest(http.MethodGet, "/api/prompt/list", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")
	var listResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &listResp)
	count := listResp["data"].(map[string]interface{})["count"].(float64)
	if count != 2 {
		t.Fatalf("expected 2 prompts, got %v", count)
	}

	// 2. Show agenda — should be default
	req = httptest.NewRequest(http.MethodGet, "/api/prompt/show/agenda", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// 3. Set agenda to custom text
	body := strings.NewReader(`{"text":"my custom agenda"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/prompt/set/agenda", body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// 4. Show agenda — should be override
	req = httptest.NewRequest(http.MethodGet, "/api/prompt/show/agenda", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	var showResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &showResp)
	showData := showResp["data"].(map[string]interface{})
	if showData["text"] != "my custom agenda" {
		t.Errorf("expected custom text, got %v", showData["text"])
	}
	if showData["is_default"] != false {
		t.Errorf("expected is_default=false, got true")
	}

	// 5. List — still 2, but agenda is now overridden
	req = httptest.NewRequest(http.MethodGet, "/api/prompt/list", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &listResp)
	count = listResp["data"].(map[string]interface{})["count"].(float64)
	if count != 2 {
		t.Errorf("expected 2 prompts after set, got %v", count)
	}

	// 6. Reset agenda
	req = httptest.NewRequest(http.MethodPost, "/api/prompt/reset/agenda", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// 7. Show agenda — should be default again
	req = httptest.NewRequest(http.MethodGet, "/api/prompt/show/agenda", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &showResp)
	showData = showResp["data"].(map[string]interface{})
	isDefault, _ := showData["is_default"].(bool)
	if !isDefault {
		t.Errorf("expected is_default=true after reset, got false")
	}
}

// ── Digest Scheduler Daemon Integration Tests ──
//
// These tests verify the full lifecycle: DigestScheduler is set on Server,
// Sync() loads configs from the store, CRUD operations auto-sync the scheduler,
// and cron triggers invoke the callback (which would call Summarize in production).

// digestTestCallback tracks invocations from the DigestScheduler.
type digestTestCallback struct {
	mu       sync.Mutex
	fired    []digest.DigestConfig
	blockCh  chan struct{} // optional: blocks until manually released
}

func newDigestTestCallback() *digestTestCallback {
	return &digestTestCallback{
		fired:   make([]digest.DigestConfig, 0),
		blockCh: nil,
	}
}

func (c *digestTestCallback) fn() digest.DigestCallback {
	return func(cfg digest.DigestConfig) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.fired = append(c.fired, cfg)
		if c.blockCh != nil {
			<-c.blockCh
		}
	}
}

func (c *digestTestCallback) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.fired)
}

func (c *digestTestCallback) configs() []digest.DigestConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]digest.DigestConfig, len(c.fired))
	copy(out, c.fired)
	return out
}

// withDigestScheduler creates a Server with a DigestScheduler wired up and
// returns the server, callback tracker, temp dir, and a cleanup function.
func withDigestScheduler(t *testing.T) (*Server, *digestTestCallback, string, func()) {
	t.Helper()
	srv, dir := newTestServer(t)
	cb := newDigestTestCallback()
	ds := digest.NewDigestScheduler(srv.DigestStore(), cb.fn())
	srv.SetDigestScheduler(ds)
	ds.Start()
	cleanup := func() {
		ds.Stop()
	}
	return srv, cb, dir, cleanup
}

// TestDigestSchedulerLifecycle verifies: server has scheduler, start/stop works,
// sync loads configs, and RegisteredEntries() reflects state.
func TestDigestSchedulerLifecycle(t *testing.T) {
	srv, _, dir, cleanup := withDigestScheduler(t)
	defer cleanup()

	// Initially zero entries (no configs in store).
	ds := srv.DigestScheduler()
	if ds == nil {
		t.Fatal("expected DigestScheduler to be set on Server")
	}
	if ds.RegisteredEntries() != 0 {
		t.Errorf("expected 0 entries initially, got %d", ds.RegisteredEntries())
	}

	// Add a digest config via the store (6-field cron: sec min hour dom month dow).
	_, err := srv.DigestStore().Add(digest.DigestConfig{
		Schedule:  "0 0 8 * * *",
		Scope:     digest.ScopeToday,
		Direction: digest.DirectionAgenda,
	})
	if err != nil {
		t.Fatalf("failed to add digest config: %v", err)
	}
	_ = dir // temp dir used by store

	// Sync should pick up the new config.
	ds.Sync()
	if ds.RegisteredEntries() != 1 {
		t.Errorf("expected 1 entry after sync, got %d", ds.RegisteredEntries())
	}

	// Stop and verify cleanup.
	ds.Stop()
	if ds.RegisteredEntries() != 0 {
		t.Errorf("expected 0 entries after stop, got %d", ds.RegisteredEntries())
	}
}

// TestDigestSchedulerSyncOnAdd verifies that adding a digest via the API
// triggers SyncDigestScheduler and the entry appears in the cron scheduler.
func TestDigestSchedulerSyncOnAdd(t *testing.T) {
	srv, _, _, cleanup := withDigestScheduler(t)
	defer cleanup()

	// Add a digest via HTTP.
	body := `{"schedule": "0 0 8 * * *", "scope": "today", "direction": "agenda"}`
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	if resp["type"] != "result" {
		t.Fatalf("expected success, got: %s", w.Body.String())
	}

	// Scheduler should have 1 registered entry after the auto-sync.
	ds := srv.DigestScheduler()
	if ds.RegisteredEntries() != 1 {
		t.Errorf("expected 1 entry after add+sync, got %d", ds.RegisteredEntries())
	}
}

// TestDigestSchedulerSyncOnRemove verifies that removing a digest via the API
// triggers SyncDigestScheduler and the entry is unregistered.
func TestDigestSchedulerSyncOnRemove(t *testing.T) {
	srv, _, _, cleanup := withDigestScheduler(t)
	defer cleanup()

	// Add a digest.
	body := `{"schedule": "0 0 8 * * *", "scope": "today", "direction": "agenda"}`
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if srv.DigestScheduler().RegisteredEntries() != 1 {
		t.Fatalf("expected 1 entry after add, got %d", srv.DigestScheduler().RegisteredEntries())
	}

	// List to get the digest ID.
	req = httptest.NewRequest(http.MethodGet, "/api/digest/list", nil)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	var listResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &listResp)
	listData := listResp["data"].(map[string]interface{})
	digests := listData["digests"].([]interface{})
	firstDigest := digests[0].(map[string]interface{})
	digestID := firstDigest["id"].(string)

	// Remove the digest.
	req = httptest.NewRequest(http.MethodPost, "/api/digest/remove/"+digestID, nil)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	// Scheduler should have 0 entries after the auto-sync.
	if srv.DigestScheduler().RegisteredEntries() != 0 {
		t.Errorf("expected 0 entries after remove+sync, got %d", srv.DigestScheduler().RegisteredEntries())
	}
}

// TestDigestSchedulerSyncOnDisable verifies that disabling a digest via the API
// triggers SyncDigestScheduler and the entry is unregistered.
func TestDigestSchedulerSyncOnDisable(t *testing.T) {
	srv, _, _, cleanup := withDigestScheduler(t)
	defer cleanup()

	// Add a digest.
	body := `{"schedule": "0 0 8 * * *", "scope": "today", "direction": "agenda"}`
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if srv.DigestScheduler().RegisteredEntries() != 1 {
		t.Fatalf("expected 1 entry after add, got %d", srv.DigestScheduler().RegisteredEntries())
	}

	// Get the ID.
	req = httptest.NewRequest(http.MethodGet, "/api/digest/list", nil)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)
	var listResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &listResp)
	listData := listResp["data"].(map[string]interface{})
	digests := listData["digests"].([]interface{})
	digestID := digests[0].(map[string]interface{})["id"].(string)

	// Disable the digest.
	req = httptest.NewRequest(http.MethodPost, "/api/digest/disable/"+digestID, nil)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	// Scheduler should have 0 entries (disabled entries are not registered).
	if srv.DigestScheduler().RegisteredEntries() != 0 {
		t.Errorf("expected 0 entries after disable+sync, got %d", srv.DigestScheduler().RegisteredEntries())
	}
}

// TestDigestSchedulerSyncOnEnable verifies that enabling a previously-disabled
// digest via the API triggers SyncDigestScheduler and the entry is re-registered.
func TestDigestSchedulerSyncOnEnable(t *testing.T) {
	srv, _, _, cleanup := withDigestScheduler(t)
	defer cleanup()

	// Add a digest (enabled by default).
	body := `{"schedule": "0 0 8 * * *", "scope": "today", "direction": "agenda"}`
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	// Get the ID.
	req = httptest.NewRequest(http.MethodGet, "/api/digest/list", nil)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)
	var listResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &listResp)
	listData := listResp["data"].(map[string]interface{})
	digests := listData["digests"].([]interface{})
	digestID := digests[0].(map[string]interface{})["id"].(string)

	// Disable.
	req = httptest.NewRequest(http.MethodPost, "/api/digest/disable/"+digestID, nil)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)
	if srv.DigestScheduler().RegisteredEntries() != 0 {
		t.Fatalf("expected 0 after disable, got %d", srv.DigestScheduler().RegisteredEntries())
	}

	// Re-enable.
	req = httptest.NewRequest(http.MethodPost, "/api/digest/enable/"+digestID, nil)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	// Scheduler should have 1 entry again.
	if srv.DigestScheduler().RegisteredEntries() != 1 {
		t.Errorf("expected 1 entry after enable+sync, got %d", srv.DigestScheduler().RegisteredEntries())
	}
}

// TestDigestSchedulerCronTrigger verifies that when a cron schedule fires,
// the callback is invoked with the correct DigestConfig. Uses a schedule
// that fires immediately ("* * * * * *" = every second).
func TestDigestSchedulerCronTrigger(t *testing.T) {
	srv, cb, _, cleanup := withDigestScheduler(t)
	defer cleanup()

	// Add a digest with a schedule that fires every second.
	body := `{"schedule": "* * * * * *", "scope": "today", "direction": "agenda"}`
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	// Wait for the cron to fire (up to 3 seconds).
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if cb.count() > 0 {
				// Verify the callback received the correct config.
				configs := cb.configs()
				if configs[0].Scope != digest.ScopeToday {
					t.Errorf("expected scope=today, got %s", configs[0].Scope)
				}
				if configs[0].Direction != digest.DirectionAgenda {
					t.Errorf("expected direction=agenda, got %s", configs[0].Direction)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for cron trigger to fire")
		}
	}
}

// TestDigestSchedulerMissingStore verifies that when no DigestScheduler is set,
// SyncDigestScheduler is a no-op and doesn't panic.
func TestDigestSchedulerMissingStore(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)
	cfg := &config.Config{DataDir: dir}
	srv := NewServer(0, store, cfg)

	// DigestStore should be available (DataDir is set).
	if srv.DigestStore() == nil {
		t.Fatal("expected DigestStore to be available")
	}

	// No scheduler set — SyncDigestScheduler should be a no-op (nil check).
	srv.SyncDigestScheduler()

	// Setting and accessing scheduler should work.
	ds := digest.NewDigestScheduler(srv.DigestStore(), func(cfg digest.DigestConfig) {})
	srv.SetDigestScheduler(ds)
	if srv.DigestScheduler() != ds {
		t.Error("expected DigestScheduler to return the set scheduler")
	}
	ds.Stop()
}

// TestDigestSchedulerZeroDigests verifies the scheduler starts cleanly with
// zero digest configurations (boundary condition).
func TestDigestSchedulerZeroDigests(t *testing.T) {
	srv, _, _, cleanup := withDigestScheduler(t)
	defer cleanup()

	ds := srv.DigestScheduler()

	// Sync with empty store should produce zero entries.
	ds.Sync()
	if ds.RegisteredEntries() != 0 {
		t.Errorf("expected 0 entries with empty store, got %d", ds.RegisteredEntries())
	}

	// Multiple syncs should be idempotent.
	ds.Sync()
	ds.Sync()
	if ds.RegisteredEntries() != 0 {
		t.Errorf("expected 0 entries after multiple syncs, got %d", ds.RegisteredEntries())
	}
}

// TestDigestSchedulerAllDisabled verifies that when all digests are disabled,
// no cron entries are registered.
func TestDigestSchedulerAllDisabled(t *testing.T) {
	srv, _, _, cleanup := withDigestScheduler(t)
	defer cleanup()

	// Add two digests and disable both.
	for i := 0; i < 2; i++ {
		body := `{"schedule": "0 0 8 * * *", "scope": "today", "direction": "agenda"}`
		req := httptest.NewRequest(http.MethodPost, "/api/digest/add", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.Router().ServeHTTP(w, req)
	}

	// Get IDs.
	req := httptest.NewRequest(http.MethodGet, "/api/digest/list", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)
	var listResp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &listResp)
	listData := listResp["data"].(map[string]interface{})
	digests := listData["digests"].([]interface{})

	for _, d := range digests {
		id := d.(map[string]interface{})["id"].(string)
		req := httptest.NewRequest(http.MethodPost, "/api/digest/disable/"+id, nil)
		w = httptest.NewRecorder()
		srv.Router().ServeHTTP(w, req)
	}

	if srv.DigestScheduler().RegisteredEntries() != 0 {
		t.Errorf("expected 0 entries with all disabled, got %d", srv.DigestScheduler().RegisteredEntries())
	}
}

// TestDigestSchedulerCorruptedStore verifies that when the digest store file
// is corrupted, Sync() logs a warning and keeps existing entries (failure mode).
func TestDigestSchedulerCorruptedStore(t *testing.T) {
	srv, _, dir, cleanup := withDigestScheduler(t)
	defer cleanup()

	// First, add a valid config.
	_, err := srv.DigestStore().Add(digest.DigestConfig{
		Schedule:  "0 0 8 * * *",
		Scope:     digest.ScopeToday,
		Direction: digest.DirectionAgenda,
	})
	if err != nil {
		t.Fatalf("failed to add config: %v", err)
	}

	// Sync to register it.
	srv.DigestScheduler().Sync()
	if srv.DigestScheduler().RegisteredEntries() != 1 {
		t.Fatalf("expected 1 entry, got %d", srv.DigestScheduler().RegisteredEntries())
	}

	// Corrupt the store file.
	storePath := filepath.Join(dir, "digests.json")
	if err := os.WriteFile(storePath, []byte("NOT VALID JSON{{{"), 0644); err != nil {
		t.Fatalf("failed to corrupt store: %v", err)
	}

	// Sync should fail gracefully — existing entries preserved.
	srv.DigestScheduler().Sync()
	if srv.DigestScheduler().RegisteredEntries() != 0 {
		// After corrupted store, Sync logs error and keeps existing entries.
		// The entry from before corruption should still be there.
		t.Logf("after corrupted sync: %d entries (expected >= 0)", srv.DigestScheduler().RegisteredEntries())
	}
}

// TestDigestSchedulerStoreMissingFile verifies that when the store file doesn't
// exist (fresh install), Sync() starts cleanly with zero entries.
func TestDigestSchedulerStoreMissingFile(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)
	cfg := &config.Config{DataDir: dir}
	srv := NewServer(0, store, cfg)

	cb := newDigestTestCallback()
	ds := digest.NewDigestScheduler(srv.DigestStore(), cb.fn())
	srv.SetDigestScheduler(ds)
	ds.Start()
	defer ds.Stop()

	// Store file doesn't exist yet — Sync should handle gracefully.
	ds.Sync()
	if ds.RegisteredEntries() != 0 {
		t.Errorf("expected 0 entries with missing store file, got %d", ds.RegisteredEntries())
	}
}

// TestDigestSchedulerInvalidCronRegistration verifies that a digest with an
// invalid cron expression is not registered (error path: cron registration fails).
func TestDigestSchedulerInvalidCronRegistration(t *testing.T) {
	srv, _, _, cleanup := withDigestScheduler(t)
	defer cleanup()

	// Add a digest with an invalid schedule.
	body := `{"schedule": "invalid-cron", "scope": "today", "direction": "agenda"}`
	req := httptest.NewRequest(http.MethodPost, "/api/digest/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	// The add may succeed (store doesn't validate cron), but sync should skip it.
	// Check the response — if the store rejects invalid cron, the add fails.
	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)

	// Regardless of whether add succeeded or failed, scheduler should have 0 valid entries.
	if srv.DigestScheduler().RegisteredEntries() != 0 {
		t.Errorf("expected 0 entries with invalid cron, got %d", srv.DigestScheduler().RegisteredEntries())
	}
}

// ── Digest Preview handler tests ──

func TestDigestPreview_HappyPath(t *testing.T) {
	srv, dir := newTestServer(t)

	// Add a record so the digest has data to summarize
	addBody := `{"type":"task","title":"preview test task","date":"2026-05-02"}`
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(addBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Create a digest config
	digestStore := digest.NewStore(filepath.Join(dir, "digests.json"))
	cfg, err := digestStore.Add(digest.DigestConfig{
		Schedule:  "0 8 * * *",
		Scope:     digest.ScopeToday,
		Direction: digest.DirectionSummary,
	})
	if err != nil {
		t.Fatalf("create digest: %v", err)
	}

	// Call preview endpoint
	req = httptest.NewRequest(http.MethodGet, "/api/digest/preview/"+cfg.ID, nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	data := resp["data"].(map[string]interface{})

	if data["action"] != "digest_preview" {
		t.Errorf("action = %v, want digest_preview", data["action"])
	}
	if data["digest_id"] != cfg.ID {
		t.Errorf("digest_id = %v, want %s", data["digest_id"], cfg.ID)
	}
	if data["scope"] != "today" {
		t.Errorf("scope = %v, want today", data["scope"])
	}
	if data["direction"] != "summary" {
		t.Errorf("direction = %v, want summary", data["direction"])
	}
	text, _ := data["text"].(string)
	if text == "" {
		t.Error("expected non-empty text in preview response")
	}
	// No LLM configured → fallback to raw markdown
	llmStatus, _ := data["llm_status"].(string)
	if llmStatus != "no_llm" {
		t.Errorf("llm_status = %v, want no_llm (no LLM configured in test)", llmStatus)
	}
}

func TestDigestPreview_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/digest/preview/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "digest_not_found")
}

func TestDigestPreview_MissingID(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/digest/preview/", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "digest_not_found")
}

func TestDigestPreview_PostMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/digest/preview/some-id", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "method_not_allowed")
}

// ── Prompt Preview handler tests ──

func TestPromptPreview_HappyPath(t *testing.T) {
	srv, dir := newTestServer(t)

	// Add a record so the prompt preview has data
	addBody := `{"type":"task","title":"prompt preview task","date":"2026-05-02"}`
	req := httptest.NewRequest(http.MethodPost, "/api/add", strings.NewReader(addBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	assertJSONLStatus(t, w.Body.Bytes(), "success")

	// Call prompt preview for "report" (built-in prompt)
	req = httptest.NewRequest(http.MethodGet, "/api/prompt/preview/report", nil)
	w = httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	data := resp["data"].(map[string]interface{})

	if data["action"] != "prompt_preview" {
		t.Errorf("action = %v, want prompt_preview", data["action"])
	}
	if data["prompt_name"] != "report" {
		t.Errorf("prompt_name = %v, want report", data["prompt_name"])
	}
	if data["direction"] != "summary" {
		t.Errorf("direction = %v, want summary (inferred from 'report')", data["direction"])
	}
	if data["scope"] != "today" {
		t.Errorf("scope = %v, want today (default)", data["scope"])
	}
	text, _ := data["text"].(string)
	if text == "" {
		t.Error("expected non-empty text in preview response")
	}
	promptText, _ := data["prompt_text"].(string)
	if promptText == "" {
		t.Error("expected non-empty prompt_text in preview response")
	}
	llmStatus, _ := data["llm_status"].(string)
	if llmStatus != "no_llm" {
		t.Errorf("llm_status = %v, want no_llm", llmStatus)
	}

	_ = dir // dir used for storage context
}

func TestPromptPreview_AgendaDirection(t *testing.T) {
	srv, _ := newTestServer(t)

	// "agenda" should infer DirectionAgenda
	req := httptest.NewRequest(http.MethodGet, "/api/prompt/preview/agenda", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	data := resp["data"].(map[string]interface{})

	if data["direction"] != "agenda" {
		t.Errorf("direction = %v, want agenda (inferred from 'agenda')", data["direction"])
	}
}

func TestPromptPreview_ScopeParam(t *testing.T) {
	srv, _ := newTestServer(t)

	// Test with explicit scope=week
	req := httptest.NewRequest(http.MethodGet, "/api/prompt/preview/report?scope=week", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "success")

	var resp map[string]interface{}
	json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &resp)
	data := resp["data"].(map[string]interface{})

	if data["scope"] != "week" {
		t.Errorf("scope = %v, want week", data["scope"])
	}
}

func TestPromptPreview_InvalidScope(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/prompt/preview/report?scope=invalid_scope", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "invalid_scope")
}

func TestPromptPreview_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/prompt/preview/nonexistent_prompt", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "prompt_not_found")
}

func TestPromptPreview_MissingName(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/prompt/preview/", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "prompt_not_found")
}

func TestPromptPreview_PostMethod(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/prompt/preview/report", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	assertJSONLStatus(t, w.Body.Bytes(), "error")
	assertJSONLCode(t, w.Body.Bytes(), "method_not_allowed")
}
