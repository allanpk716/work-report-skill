package storage

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wr/internal/models"
)

// newTestStorage creates a Storage backed by a temp directory.
func newTestStorage(t *testing.T) (*Storage, string) {
	t.Helper()
	dir := t.TempDir()
	s := New(dir, log.New(os.Stderr, "[test-storage] ", log.LstdFlags))
	return s, dir
}

// --- helpers for creating test records ---

func newTestMeeting(title, date, timeStr string) *models.MeetingRecord {
	return &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:     models.TypeMeeting,
			Title:    title,
			Date:     date,
			Time:     timeStr,
			Status:   models.StatusActive,
			Location: "会议室A",
			Tags:     []string{"test"},
		},
		Agenda: "测试议程",
	}
}

func newTestTask(title, date string) *models.TaskRecord {
	return &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:   models.TypeTask,
			Title:  title,
			Date:   date,
			Status: models.StatusPending,
			Tags:   []string{"test"},
		},
	}
}

func newTestReminder(title, date, timeStr string) *models.ReminderRecord {
	return &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:   models.TypeReminder,
			Title:  title,
			Date:   date,
			Time:   timeStr,
			Status: models.StatusActive,
		},
	}
}

func newTestLog(title, date, timeStr string) *models.LogRecord {
	return &models.LogRecord{
		CommonFields: models.CommonFields{
			Type:   models.TypeLog,
			Title:  title,
			Date:   date,
			Time:   timeStr,
		},
	}
}

// --- Tests ---

func TestAddRecord_Meeting(t *testing.T) {
	s, dir := newTestStorage(t)

	rec := newTestMeeting("项目评审会", "2026-05-02", "15:00")
	result, err := s.AddRecord(rec)
	if err != nil {
		t.Fatalf("AddRecord: %v", err)
	}

	cf := models.GetCommonFields(result)
	if cf == nil {
		t.Fatal("GetCommonFields returned nil")
	}
	if cf.ShortID == "" {
		t.Error("ShortID should be populated after add")
	}
	if cf.SavedAt == "" {
		t.Error("SavedAt should be populated after add")
	}
	if cf.Type != models.TypeMeeting {
		t.Errorf("Type = %q, want meeting", cf.Type)
	}

	// Verify file exists in meetings/YYYY/MM/DD/
	expectedDir := filepath.Join(dir, "meetings", "2026", "05", "02")
	files, err := os.ReadDir(expectedDir)
	if err != nil {
		t.Fatalf("reading meetings dir: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if !strings.HasSuffix(files[0].Name(), ".json") {
		t.Errorf("file %q should have .json extension", files[0].Name())
	}
}

func TestAddRecord_Task(t *testing.T) {
	s, dir := newTestStorage(t)

	rec := newTestTask("编写文档", "2026-05-02")
	result, err := s.AddRecord(rec)
	if err != nil {
		t.Fatalf("AddRecord: %v", err)
	}

	cf := models.GetCommonFields(result)
	if cf.ShortID == "" {
		t.Error("ShortID should be populated")
	}

	// Task should be in tasks/active/
	activeDir := filepath.Join(dir, "tasks", "active")
	files, err := os.ReadDir(activeDir)
	if err != nil {
		t.Fatalf("reading tasks/active: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file in active, got %d", len(files))
	}
}

func TestAddRecord_Log(t *testing.T) {
	s, dir := newTestStorage(t)

	rec := newTestLog("完成代码审查", "2026-05-02", "09:30")
	result, err := s.AddRecord(rec)
	if err != nil {
		t.Fatalf("AddRecord: %v", err)
	}

	cf := models.GetCommonFields(result)
	if cf.ShortID == "" {
		t.Error("ShortID should be populated")
	}

	// Log should be in logs/YYYY/MM/DD/
	logDir := filepath.Join(dir, "logs", "2026", "05", "02")
	files, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("reading logs dir: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
}

func TestAddRecord_Reminder(t *testing.T) {
	s, dir := newTestStorage(t)

	rec := newTestReminder("下午会议提醒", "2026-05-02", "14:50")
	result, err := s.AddRecord(rec)
	if err != nil {
		t.Fatalf("AddRecord: %v", err)
	}

	cf := models.GetCommonFields(result)
	if cf.ShortID == "" {
		t.Error("ShortID should be populated")
	}

	activeDir := filepath.Join(dir, "reminders", "active")
	files, err := os.ReadDir(activeDir)
	if err != nil {
		t.Fatalf("reading reminders/active: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file in active, got %d", len(files))
	}
}

func TestAddAndRead_RoundTrip(t *testing.T) {
	s, _ := newTestStorage(t)

	rec := newTestMeeting("需求评审", "2026-05-03", "10:00")
	result, err := s.AddRecord(rec)
	if err != nil {
		t.Fatalf("AddRecord: %v", err)
	}

	cf := models.GetCommonFields(result)
	shortID := cf.ShortID

	// Retrieve by short ID
	found, path, err := s.GetByID(shortID)
	if err != nil {
		t.Fatalf("GetByID(%s): %v", shortID, err)
	}
	if path == "" {
		t.Error("path should not be empty")
	}

	foundCF := models.GetCommonFields(found)
	if foundCF == nil {
		t.Fatal("found record has nil common fields")
	}
	if foundCF.Title != "需求评审" {
		t.Errorf("Title = %q, want 需求评审", foundCF.Title)
	}
	if foundCF.ShortID != shortID {
		t.Errorf("ShortID = %q, want %q", foundCF.ShortID, shortID)
	}
}

func TestListRecords_ByType(t *testing.T) {
	s, _ := newTestStorage(t)

	// Add a meeting and a task
	_, err := s.AddRecord(newTestMeeting("会议1", "2026-05-02", "10:00"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddRecord(newTestTask("任务1", "2026-05-02"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddRecord(newTestMeeting("会议2", "2026-05-03", "14:00"))
	if err != nil {
		t.Fatal(err)
	}

	// List only meetings
	results, err := s.ListRecords(ListOptions{RecordType: models.TypeMeeting})
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 meetings, got %d", len(results))
	}

	for _, r := range results {
		if r.Type != models.TypeMeeting {
			t.Errorf("got type %q, want meeting", r.Type)
		}
	}

	// List only tasks
	taskResults, err := s.ListRecords(ListOptions{RecordType: models.TypeTask})
	if err != nil {
		t.Fatalf("ListRecords tasks: %v", err)
	}
	if len(taskResults) != 1 {
		t.Fatalf("expected 1 task, got %d", len(taskResults))
	}
}

func TestListRecords_WithDateFilter(t *testing.T) {
	s, _ := newTestStorage(t)

	_, err := s.AddRecord(newTestMeeting("会议A", "2026-05-02", "10:00"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddRecord(newTestMeeting("会议B", "2026-05-03", "14:00"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddRecord(newTestMeeting("会议C", "2026-05-02", "16:00"))
	if err != nil {
		t.Fatal(err)
	}

	results, err := s.ListRecords(ListOptions{
		RecordType: models.TypeMeeting,
		Date:       "2026-05-02",
	})
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 records on 2026-05-02, got %d", len(results))
	}

	for _, r := range results {
		if r.Date != "2026-05-02" {
			t.Errorf("date = %q, want 2026-05-02", r.Date)
		}
	}
}

func TestListRecords_SortedNewestFirst(t *testing.T) {
	s, _ := newTestStorage(t)

	_, _ = s.AddRecord(newTestLog("旧记录", "2026-05-01", "09:00"))
	_, _ = s.AddRecord(newTestLog("新记录", "2026-05-03", "15:00"))
	_, _ = s.AddRecord(newTestLog("中记录", "2026-05-02", "10:00"))

	results, err := s.ListRecords(ListOptions{RecordType: models.TypeLog})
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 records, got %d", len(results))
	}

	// Newest first
	if results[0].Title != "新记录" {
		t.Errorf("first record = %q, want 新记录", results[0].Title)
	}
	if results[2].Title != "旧记录" {
		t.Errorf("last record = %q, want 旧记录", results[2].Title)
	}
}

func TestCompleteRecord_Task(t *testing.T) {
	s, dir := newTestStorage(t)

	rec := newTestTask("待办事项", "2026-05-02")
	result, err := s.AddRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	shortID := models.GetCommonFields(result).ShortID

	// Complete it
	err = s.CompleteRecord(shortID)
	if err != nil {
		t.Fatalf("CompleteRecord: %v", err)
	}

	// Verify old file is removed from active
	activeDir := filepath.Join(dir, "tasks", "active")
	files, _ := os.ReadDir(activeDir)
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".json") {
			t.Errorf("active dir should be empty, found %s", f.Name())
		}
	}

	// Verify file exists in completed dir
	// The completed file is under tasks/completed/YYYY/MM/DD/
	found, path, err := s.GetByID(shortID)
	if err != nil {
		t.Fatalf("GetByID after complete: %v", err)
	}

	foundCF := models.GetCommonFields(found)
	if foundCF.Status != models.StatusCompleted {
		t.Errorf("Status = %q, want completed", foundCF.Status)
	}
	if !strings.Contains(path, "completed") {
		t.Errorf("path %q should contain 'completed'", path)
	}
}

func TestCompleteRecord_Reminder(t *testing.T) {
	s, _ := newTestStorage(t)

	rec := newTestReminder("提醒测试", "2026-05-02", "09:00")
	result, err := s.AddRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	shortID := models.GetCommonFields(result).ShortID

	err = s.CompleteRecord(shortID)
	if err != nil {
		t.Fatalf("CompleteRecord: %v", err)
	}

	found, _, err := s.GetByID(shortID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	foundCF := models.GetCommonFields(found)
	if foundCF.Status != models.StatusCompleted {
		t.Errorf("Status = %q, want completed", foundCF.Status)
	}
}

func TestCompleteRecord_LogReturnsError(t *testing.T) {
	s, _ := newTestStorage(t)

	rec := newTestLog("日志测试", "2026-05-02", "09:00")
	result, err := s.AddRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	shortID := models.GetCommonFields(result).ShortID

	err = s.CompleteRecord(shortID)
	if err == nil {
		t.Fatal("expected error when completing a log record")
	}
	if !strings.Contains(err.Error(), "logs cannot be completed") {
		t.Errorf("error = %q, should mention logs cannot be completed", err)
	}
}

func TestCancelRecord(t *testing.T) {
	s, _ := newTestStorage(t)

	rec := newTestTask("取消任务", "2026-05-02")
	result, err := s.AddRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	shortID := models.GetCommonFields(result).ShortID

	err = s.CancelRecord(shortID)
	if err != nil {
		t.Fatalf("CancelRecord: %v", err)
	}

	found, _, err := s.GetByID(shortID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	foundCF := models.GetCommonFields(found)
	if foundCF.Status != models.StatusCancelled {
		t.Errorf("Status = %q, want cancelled", foundCF.Status)
	}
	if foundCF.UpdatedAt == "" {
		t.Error("UpdatedAt should be set")
	}
}

func TestCancelRecord_AlreadyCancelled(t *testing.T) {
	s, _ := newTestStorage(t)

	rec := newTestTask("重复取消", "2026-05-02")
	result, err := s.AddRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	shortID := models.GetCommonFields(result).ShortID

	err = s.CancelRecord(shortID)
	if err != nil {
		t.Fatal(err)
	}

	err = s.CancelRecord(shortID)
	if err == nil {
		t.Fatal("expected error when cancelling already cancelled record")
	}
}

func TestCancelRecord_AlreadyCompleted(t *testing.T) {
	s, _ := newTestStorage(t)

	rec := newTestTask("已完成", "2026-05-02")
	result, err := s.AddRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	shortID := models.GetCommonFields(result).ShortID

	err = s.CompleteRecord(shortID)
	if err != nil {
		t.Fatal(err)
	}

	err = s.CancelRecord(shortID)
	if err == nil {
		t.Fatal("expected error when cancelling completed record")
	}
}

func TestGetByID_NotFound(t *testing.T) {
	s, _ := newTestStorage(t)

	_, _, err := s.GetByID("nonexist")
	if err == nil {
		t.Fatal("expected error for nonexistent ID")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, should contain 'not found'", err)
	}
}

func TestGetByID_EmptyID(t *testing.T) {
	s, _ := newTestStorage(t)

	_, _, err := s.GetByID("")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestCorruptJSON_Handling(t *testing.T) {
	s, dir := newTestStorage(t)

	// Manually create a corrupt JSON file
	corruptDir := filepath.Join(dir, "meetings", "2026", "05", "02")
	if err := os.MkdirAll(corruptDir, 0755); err != nil {
		t.Fatal(err)
	}
	corruptPath := filepath.Join(corruptDir, "20260502_100000.json")
	if err := os.WriteFile(corruptPath, []byte("{invalid json"), 0644); err != nil {
		t.Fatal(err)
	}

	// Also add a valid record
	_, err := s.AddRecord(newTestMeeting("正常会议", "2026-05-02", "10:00"))
	if err != nil {
		t.Fatal(err)
	}

	// List should skip the corrupt file and return the valid one
	results, err := s.ListRecords(ListOptions{RecordType: models.TypeMeeting})
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 valid record, got %d", len(results))
	}
	if results[0].Title != "正常会议" {
		t.Errorf("Title = %q, want 正常会议", results[0].Title)
	}
}

func TestEmptyDirectory_Listing(t *testing.T) {
	s, _ := newTestStorage(t)

	// No records added — should return empty list, not error
	results, err := s.ListRecords(ListOptions{RecordType: models.TypeMeeting})
	if err != nil {
		t.Fatalf("ListRecords on empty dir: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}

	// All types, empty
	allResults, err := s.ListRecords(ListOptions{})
	if err != nil {
		t.Fatalf("ListRecords all types on empty dir: %v", err)
	}
	if len(allResults) != 0 {
		t.Errorf("expected 0 results, got %d", len(allResults))
	}
}

func TestMultipleRecords_SameDate(t *testing.T) {
	s, _ := newTestStorage(t)

	_, err := s.AddRecord(newTestLog("晨会记录", "2026-05-02", "09:00"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddRecord(newTestLog("午后记录", "2026-05-02", "14:00"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddRecord(newTestLog("晚间记录", "2026-05-02", "18:00"))
	if err != nil {
		t.Fatal(err)
	}

	results, err := s.ListRecords(ListOptions{
		RecordType: models.TypeLog,
		Date:       "2026-05-02",
	})
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 records, got %d", len(results))
	}

	// Should be sorted newest first
	for i, r := range results {
		t.Logf("  result[%d]: date=%s time=%s title=%s", i, r.Date, r.Time, r.Title)
	}
	if results[0].Title != "晚间记录" {
		t.Errorf("first = %q, want 晚间记录", results[0].Title)
	}
	if results[2].Title != "晨会记录" {
		t.Errorf("last = %q, want 晨会记录", results[2].Title)
	}
}

func TestListRecords_AllTypes(t *testing.T) {
	s, _ := newTestStorage(t)

	_, _ = s.AddRecord(newTestMeeting("会议", "2026-05-02", "10:00"))
	_, _ = s.AddRecord(newTestTask("任务", "2026-05-02"))
	_, _ = s.AddRecord(newTestReminder("提醒", "2026-05-02", "09:00"))
	_, _ = s.AddRecord(newTestLog("日志", "2026-05-02", "08:00"))

	results, err := s.ListRecords(ListOptions{})
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("expected 4 records, got %d", len(results))
	}

	// Verify all types are represented
	types := map[string]bool{}
	for _, r := range results {
		types[string(r.Type)] = true
	}
	for _, rt := range []string{"meeting", "task", "reminder", "log"} {
		if !types[rt] {
			t.Errorf("type %s not found in results", rt)
		}
	}
}

func TestListRecords_IncludeCompleted(t *testing.T) {
	s, _ := newTestStorage(t)

	// Add two tasks, complete one
	_, _ = s.AddRecord(newTestTask("活跃任务", "2026-05-02"))
	rec2, _ := s.AddRecord(newTestTask("完成任务", "2026-05-02"))
	shortID2 := models.GetCommonFields(rec2).ShortID

	err := s.CompleteRecord(shortID2)
	if err != nil {
		t.Fatal(err)
	}

	// List without completed
	active, err := s.ListRecords(ListOptions{
		RecordType:       models.TypeTask,
		IncludeCompleted: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Fatalf("expected 1 active task, got %d", len(active))
	}

	// List with completed
	all, err := s.ListRecords(ListOptions{
		RecordType:       models.TypeTask,
		IncludeCompleted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 tasks (active+completed), got %d", len(all))
	}
}

func TestCompleteRecord_AlreadyCompleted(t *testing.T) {
	s, _ := newTestStorage(t)

	rec := newTestTask("重复完成", "2026-05-02")
	result, _ := s.AddRecord(rec)
	shortID := models.GetCommonFields(result).ShortID

	err := s.CompleteRecord(shortID)
	if err != nil {
		t.Fatal(err)
	}

	err = s.CompleteRecord(shortID)
	if err == nil {
		t.Fatal("expected error when completing already completed record")
	}
}

func TestAddRecord_SetsSavedAt(t *testing.T) {
	s, _ := newTestStorage(t)

	before := time.Now()
	result, err := s.AddRecord(newTestLog("时间测试", "2026-05-02", "10:00"))
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now()

	cf := models.GetCommonFields(result)
	savedAt, err := time.Parse(time.RFC3339Nano, cf.SavedAt)
	if err != nil {
		t.Fatalf("parse SavedAt: %v", err)
	}

	if savedAt.Before(before) || savedAt.After(after) {
		t.Errorf("SavedAt = %v, expected between %v and %v", savedAt, before, after)
	}
}

func TestAddRecord_DefaultStatus(t *testing.T) {
	s, _ := newTestStorage(t)

	// Task with no status set
	rec := &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeTask,
			Title: "无状态任务",
			Date:  "2026-05-02",
		},
	}
	result, err := s.AddRecord(rec)
	if err != nil {
		t.Fatal(err)
	}

	cf := models.GetCommonFields(result)
	if cf.Status != models.StatusActive {
		t.Errorf("default Status = %q, want active", cf.Status)
	}
}

func TestJSONFileIsHumanReadable(t *testing.T) {
	s, dir := newTestStorage(t)

	rec := newTestMeeting("项目评审会", "2026-05-02", "15:00")
	_, err := s.AddRecord(rec)
	if err != nil {
		t.Fatal(err)
	}

	// Read the file directly and verify it's pretty-printed
	meetingDir := filepath.Join(dir, "meetings", "2026", "05", "02")
	files, _ := os.ReadDir(meetingDir)
	if len(files) == 0 {
		t.Fatal("no files created")
	}

	data, err := os.ReadFile(filepath.Join(meetingDir, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)

	// Should be indented (2-space)
	if !strings.Contains(content, "  \"type\"") {
		t.Error("JSON should be indented with 2-space")
	}

	// Should contain the title in Chinese
	if !strings.Contains(content, "项目评审会") {
		t.Error("JSON should contain the Chinese title")
	}

	// Should be valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("JSON should be valid: %v", err)
	}
}

func TestShortID_DeterministicFromTimestamp(t *testing.T) {
	// Verify that ShortIDFromTimestamp is deterministic
	t1 := time.Date(2026, 5, 2, 15, 30, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 2, 15, 30, 0, 0, time.UTC)

	id1 := models.ShortIDFromTimestamp(t1)
	id2 := models.ShortIDFromTimestamp(t2)

	if id1 != id2 {
		t.Errorf("same timestamp produced different IDs: %s vs %s", id1, id2)
	}
	if len(id1) != 8 {
		t.Errorf("ShortID length = %d, want 8", len(id1))
	}
}

func TestShortID_DifferentTimestamps(t *testing.T) {
	t1 := time.Date(2026, 5, 2, 15, 30, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 2, 15, 30, 1, 0, time.UTC)

	id1 := models.ShortIDFromTimestamp(t1)
	id2 := models.ShortIDFromTimestamp(t2)

	if id1 == id2 {
		t.Error("different timestamps produced same ShortID")
	}
}

func TestCompleteRecord_Meeting(t *testing.T) {
	s, _ := newTestStorage(t)

	rec := newTestMeeting("评审会", "2026-05-02", "15:00")
	result, _ := s.AddRecord(rec)
	shortID := models.GetCommonFields(result).ShortID

	err := s.CompleteRecord(shortID)
	if err != nil {
		t.Fatalf("CompleteRecord meeting: %v", err)
	}

	found, path, err := s.GetByID(shortID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	foundCF := models.GetCommonFields(found)
	if foundCF.Status != models.StatusCompleted {
		t.Errorf("Status = %q, want completed", foundCF.Status)
	}
	if !strings.Contains(path, "completed") {
		t.Errorf("path should contain 'completed': %s", path)
	}
}

func TestReadRecordFile(t *testing.T) {
	s, dir := newTestStorage(t)

	rec := newTestLog("外部读取测试", "2026-05-02", "10:00")
	result, _ := s.AddRecord(rec)
	cf := models.GetCommonFields(result)

	// Find the file path
	results, _ := s.ListRecords(ListOptions{RecordType: models.TypeLog})
	if len(results) != 1 {
		t.Fatal("expected 1 log record")
	}

	readRec, err := ReadRecordFile(results[0].FilePath)
	if err != nil {
		t.Fatalf("ReadRecordFile: %v", err)
	}

	readCF := models.GetCommonFields(readRec)
	if readCF.Title != "外部读取测试" {
		t.Errorf("Title = %q", readCF.Title)
	}
	if readCF.ShortID != cf.ShortID {
		t.Errorf("ShortID = %q, want %q", readCF.ShortID, cf.ShortID)
	}

	// Also test reading from the dir path
	_ = dir
}

func TestReadRecordFile_NotFound(t *testing.T) {
	_, err := ReadRecordFile("/nonexistent/path/file.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestReadRecordFile_Corrupt(t *testing.T) {
	dir := t.TempDir()
	corruptPath := filepath.Join(dir, "corrupt.json")
	os.WriteFile(corruptPath, []byte("not json at all"), 0644)

	_, err := ReadRecordFile(corruptPath)
	if err == nil {
		t.Fatal("expected error for corrupt file")
	}
}

func TestStorage_NewWithNilLogger(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, nil)
	if s == nil {
		t.Error("New should return non-nil Storage")
	}
	if s.logger == nil {
		t.Error("default logger should be set")
	}
}

func TestListRecords_AllTypesWithDateFilter(t *testing.T) {
	s, _ := newTestStorage(t)

	_, _ = s.AddRecord(newTestMeeting("会议", "2026-05-02", "10:00"))
	_, _ = s.AddRecord(newTestTask("任务", "2026-05-03"))
	_, _ = s.AddRecord(newTestLog("日志", "2026-05-02", "08:00"))

	results, err := s.ListRecords(ListOptions{Date: "2026-05-02"})
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 records on 2026-05-02, got %d", len(results))
	}

	for _, r := range results {
		if r.Date != "2026-05-02" {
			t.Errorf("Date = %q, want 2026-05-02", r.Date)
		}
	}
}

// TestCompatibility_NanobotFormat verifies we can read actual nanobot-format
// files using the sample data from the docs/exsample directory.
func TestCompatibility_NanobotFormat(t *testing.T) {
	sampleBase := filepath.Join("..", "..", "docs", "exsample", ".nanobot-work-helper--20260501", ".nanobot-work-helper", "work-records")
	if _, err := os.Stat(sampleBase); os.IsNotExist(err) {
		t.Skip("sample work-records directory not found")
	}

	s := New(sampleBase, nil)

	// List logs
	logs, err := s.ListRecords(ListOptions{RecordType: models.TypeLog})
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if len(logs) == 0 {
		t.Error("expected some log records in sample data")
	}

	// Verify we can get by ID from sample data
	if len(logs) > 0 {
		rec, path, err := s.GetByID(logs[0].ShortID)
		if err != nil {
			t.Fatalf("GetByID(%s): %v", logs[0].ShortID, err)
		}
		cf := models.GetCommonFields(rec)
		if cf.Title == "" {
			t.Error("expected non-empty title")
		}
		if path == "" {
			t.Error("expected non-empty path")
		}
	}

	// List tasks
	tasks, err := s.ListRecords(ListOptions{
		RecordType:       models.TypeTask,
		IncludeCompleted: true,
	})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) == 0 {
		t.Error("expected some task records in sample data")
	}

	// List meetings
	meetings, err := s.ListRecords(ListOptions{
		RecordType:       models.TypeMeeting,
		IncludeCompleted: true,
	})
	if err != nil {
		t.Fatalf("list meetings: %v", err)
	}
	if len(meetings) == 0 {
		t.Error("expected some meeting records in sample data")
	}

	t.Logf("sample data: %d logs, %d tasks, %d meetings", len(logs), len(tasks), len(meetings))
}

func BenchmarkAddRecord(b *testing.B) {
	dir := b.TempDir()
	s := New(dir, log.New(io.Discard, "", 0))

	for i := 0; i < b.N; i++ {
		rec := newTestLog(
			fmt.Sprintf("日志 %d", i),
			"2026-05-02",
			"10:00",
		)
		_, err := s.AddRecord(rec)
		if err != nil {
			b.Fatal(err)
		}
	}
}
