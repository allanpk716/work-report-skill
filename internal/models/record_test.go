package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func init() {
	// Go test working directory varies by version. Ensure we're in the module root
	// so relative paths to docs/exsample/ resolve correctly.
	if _, err := os.Stat("docs/exsample"); os.IsNotExist(err) {
		// Probably running from internal/models/ — walk up to module root
		if _, err := os.Stat("../../docs/exsample"); err == nil {
			if err := os.Chdir("../.."); err != nil {
				panic("cannot chdir to module root: " + err.Error())
			}
		}
	}
}

// sampleDir is the path to the nanobot sample data directory.
// These are real sample files from docs/exsample/ tracked in git.
const sampleDir = "docs/exsample/.nanobot-work-helper--20260501/.nanobot-work-helper/work-records"

// TestParseMeetingRoundTrip verifies that a meeting JSON file round-trips
// through ParseRecord → MarshalRecord without data loss.
func TestParseMeetingRoundTrip(t *testing.T) {
	path := filepath.Join(sampleDir, "meetings/2026/04/30/20260430_103211.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample file: %v", err)
	}

	parsed, err := ParseRecord(original)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}

	meeting, ok := parsed.(*MeetingRecord)
	if !ok {
		t.Fatalf("expected *MeetingRecord, got %T", parsed)
	}

	if meeting.Type != TypeMeeting {
		t.Errorf("Type = %q, want %q", meeting.Type, TypeMeeting)
	}
	if meeting.Title != "BM-200 月度总结会" {
		t.Errorf("Title = %q, want %q", meeting.Title, "BM-200 月度总结会")
	}
	if meeting.RelatedPerson != "石艳民" {
		t.Errorf("RelatedPerson = %q, want %q", meeting.RelatedPerson, "石艳民")
	}
	if meeting.Status != "completed" {
		t.Errorf("Status = %q, want %q", meeting.Status, "completed")
	}
	if len(meeting.Tags) != 3 {
		t.Errorf("Tags length = %d, want 3", len(meeting.Tags))
	}

	// Round-trip: marshal and compare JSON field values
	roundTrip, err := MarshalRecord(meeting)
	if err != nil {
		t.Fatalf("MarshalRecord: %v", err)
	}

	var origMap, rtMap map[string]interface{}
	if err := json.Unmarshal(original, &origMap); err != nil {
		t.Fatalf("unmarshal original: %v", err)
	}
	if err := json.Unmarshal(roundTrip, &rtMap); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}

	// Verify key fields preserved
	for _, key := range []string{"type", "title", "description", "date", "related_person", "status", "saved_at"} {
		if origMap[key] != rtMap[key] {
			t.Errorf("round-trip mismatch for %q: original=%v, roundtrip=%v", key, origMap[key], rtMap[key])
		}
	}
}

// TestParseTaskRoundTrip verifies task record parsing with all optional fields.
func TestParseTaskRoundTrip(t *testing.T) {
	path := filepath.Join(sampleDir, "tasks/active/20260506_115236.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample file: %v", err)
	}

	parsed, err := ParseRecord(original)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}

	task, ok := parsed.(*TaskRecord)
	if !ok {
		t.Fatalf("expected *TaskRecord, got %T", parsed)
	}

	if task.Type != TypeTask {
		t.Errorf("Type = %q, want %q", task.Type, TypeTask)
	}
	if task.Title != "制作外部问题反馈提交表单" {
		t.Errorf("Title = %q", task.Title)
	}
	if task.Priority != "high" {
		t.Errorf("Priority = %q, want %q", task.Priority, "high")
	}
	if task.Status != "pending" {
		t.Errorf("Status = %q, want %q", task.Status, "pending")
	}

	// Round-trip
	roundTrip, err := MarshalRecord(task)
	if err != nil {
		t.Fatalf("MarshalRecord: %v", err)
	}

	var origMap, rtMap map[string]interface{}
	json.Unmarshal(original, &origMap)
	json.Unmarshal(roundTrip, &rtMap)

	for _, key := range []string{"type", "title", "description", "date", "priority", "status", "saved_at"} {
		if origMap[key] != rtMap[key] {
			t.Errorf("round-trip mismatch for %q: original=%v, roundtrip=%v", key, origMap[key], rtMap[key])
		}
	}
}

// TestParseTaskWithCompletedAt verifies the completed task format with extra fields.
func TestParseTaskWithCompletedAt(t *testing.T) {
	path := filepath.Join(sampleDir, "tasks/completed/20260311_121145.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}

	parsed, err := ParseRecord(data)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}

	task, ok := parsed.(*TaskRecord)
	if !ok {
		t.Fatalf("expected *TaskRecord, got %T", parsed)
	}

	if task.Status != "completed" {
		t.Errorf("Status = %q, want completed", task.Status)
	}
	if task.CompletedAt != "2026-03-13T23:59:59" {
		t.Errorf("CompletedAt = %q, want %q", task.CompletedAt, "2026-03-13T23:59:59")
	}
	if task.RelatedPersons[0] != "唐总" {
		t.Errorf("RelatedPersons[0] = %q, want %q", task.RelatedPersons[0], "唐总")
	}
}

// TestParseReminderRoundTrip verifies reminder record parsing.
func TestParseReminderRoundTrip(t *testing.T) {
	path := filepath.Join(sampleDir, "reminders/active/20261201_143546.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample file: %v", err)
	}

	parsed, err := ParseRecord(original)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}

	reminder, ok := parsed.(*ReminderRecord)
	if !ok {
		t.Fatalf("expected *ReminderRecord, got %T", parsed)
	}

	if reminder.Type != TypeReminder {
		t.Errorf("Type = %q, want %q", reminder.Type, TypeReminder)
	}
	if reminder.Title != "问自动化文档编写规范是否完成" {
		t.Errorf("Title = %q", reminder.Title)
	}
	if reminder.Time != "09:00" {
		t.Errorf("Time = %q, want %q", reminder.Time, "09:00")
	}
	if reminder.Notes != "去确认自动化文档编写规范是否已完成" {
		t.Errorf("Notes = %q", reminder.Notes)
	}

	// Round-trip
	roundTrip, err := MarshalRecord(reminder)
	if err != nil {
		t.Fatalf("MarshalRecord: %v", err)
	}

	var origMap, rtMap map[string]interface{}
	json.Unmarshal(original, &origMap)
	json.Unmarshal(roundTrip, &rtMap)

	for _, key := range []string{"type", "title", "date", "time", "notes", "saved_at"} {
		if origMap[key] != rtMap[key] {
			t.Errorf("round-trip mismatch for %q: original=%v, roundtrip=%v", key, origMap[key], rtMap[key])
		}
	}
}

// TestParseReminderWithRemindBefore verifies the newer reminder format with extra fields.
func TestParseReminderWithRemindBefore(t *testing.T) {
	path := filepath.Join(sampleDir, "reminders/2026/04/21/20260421_090000.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}

	parsed, err := ParseRecord(data)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}

	reminder, ok := parsed.(*ReminderRecord)
	if !ok {
		t.Fatalf("expected *ReminderRecord, got %T", parsed)
	}

	if reminder.CommonFields.RelatedPerson != "王总" {
		t.Errorf("RelatedPerson = %q, want %q", reminder.CommonFields.RelatedPerson, "王总")
	}
	if reminder.CommonFields.RemindBefore != "15m" {
		t.Errorf("RemindBefore = %q, want %q", reminder.CommonFields.RemindBefore, "15m")
	}
	if reminder.CommonFields.Priority != "medium" {
		t.Errorf("Priority = %q, want %q", reminder.CommonFields.Priority, "medium")
	}
}

// TestParseLogRoundTrip verifies log record parsing.
func TestParseLogRoundTrip(t *testing.T) {
	path := filepath.Join(sampleDir, "logs/2026/04/30/20260430_073456.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample file: %v", err)
	}

	parsed, err := ParseRecord(original)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}

	log, ok := parsed.(*LogRecord)
	if !ok {
		t.Fatalf("expected *LogRecord, got %T", parsed)
	}

	if log.Type != TypeLog {
		t.Errorf("Type = %q, want %q", log.Type, TypeLog)
	}
	if log.Title != "集团算法现状和改进方向PPT评审" {
		t.Errorf("Title = %q", log.Title)
	}
	if len(log.Tags) != 3 {
		t.Errorf("Tags length = %d, want 3", len(log.Tags))
	}

	// Round-trip
	roundTrip, err := MarshalRecord(log)
	if err != nil {
		t.Fatalf("MarshalRecord: %v", err)
	}

	var origMap, rtMap map[string]interface{}
	json.Unmarshal(original, &origMap)
	json.Unmarshal(roundTrip, &rtMap)

	for _, key := range []string{"type", "title", "description", "date", "saved_at"} {
		if origMap[key] != rtMap[key] {
			t.Errorf("round-trip mismatch for %q: original=%v, roundtrip=%v", key, origMap[key], rtMap[key])
		}
	}
}

// TestParseLogWithAllFields verifies a log record that has time, priority, progress.
func TestParseLogWithAllFields(t *testing.T) {
	path := filepath.Join(sampleDir, "logs/2026/03/11/20260311_092924.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}

	parsed, err := ParseRecord(data)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}

	log, ok := parsed.(*LogRecord)
	if !ok {
		t.Fatalf("expected *LogRecord, got %T", parsed)
	}

	if log.Time != "09:29" {
		t.Errorf("Time = %q, want %q", log.Time, "09:29")
	}
	if log.Priority != "normal" {
		t.Errorf("Priority = %q, want %q", log.Priority, "normal")
	}
	if log.Progress != "completed" {
		t.Errorf("Progress = %q, want %q", log.Progress, "completed")
	}
}

// TestParseUnknownType returns an error for unrecognised types.
func TestParseUnknownType(t *testing.T) {
	data := []byte(`{"type": "unknown", "title": "test"}`)
	_, err := ParseRecord(data)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

// TestParseInvalidJSON returns an error for malformed JSON.
func TestParseInvalidJSON(t *testing.T) {
	_, err := ParseRecord([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestShortIDFromFilename verifies deterministic 8-char hex output.
func TestShortIDFromFilename(t *testing.T) {
	id := ShortIDFromFilename("20260430_103211.json")
	if len(id) != 8 {
		t.Errorf("ShortID length = %d, want 8", len(id))
	}

	// Same input should produce same output
	id2 := ShortIDFromFilename("20260430_103211.json")
	if id != id2 {
		t.Errorf("ShortID not deterministic: %q != %q", id, id2)
	}

	// Different input should produce different output
	id3 := ShortIDFromFilename("20260506_115236.json")
	if id == id3 {
		t.Errorf("different filenames produced same ShortID: %q", id)
	}

	// Non-standard filename (with suffix)
	id4 := ShortIDFromFilename("20260317_1023_urine_health.json")
	if len(id4) != 8 {
		t.Errorf("ShortID length for non-standard filename = %d, want 8", len(id4))
	}
}

// TestIsValidType checks type validation.
func TestIsValidType(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"meeting", true},
		{"task", true},
		{"reminder", true},
		{"log", true},
		{"", false},
		{"unknown", false},
		{"Meeting", false},
	}
	for _, tt := range tests {
		got := IsValidType(tt.input)
		if got != tt.want {
			t.Errorf("IsValidType(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// TestGetCommonFields verifies field extraction from each record type.
func TestGetCommonFields(t *testing.T) {
	meeting := &MeetingRecord{
		CommonFields: CommonFields{Type: TypeMeeting, Title: "test meeting"},
	}
	cf := GetCommonFields(meeting)
	if cf == nil || cf.Title != "test meeting" {
		t.Errorf("GetCommonFields(meeting) failed")
	}

	task := &TaskRecord{
		CommonFields: CommonFields{Type: TypeTask, Title: "test task"},
	}
	cf = GetCommonFields(task)
	if cf == nil || cf.Title != "test task" {
		t.Errorf("GetCommonFields(task) failed")
	}

	reminder := &ReminderRecord{
		CommonFields: CommonFields{Type: TypeReminder, Title: "test reminder"},
	}
	cf = GetCommonFields(reminder)
	if cf == nil || cf.Title != "test reminder" {
		t.Errorf("GetCommonFields(reminder) failed")
	}

	log := &LogRecord{
		CommonFields: CommonFields{Type: TypeLog, Title: "test log"},
	}
	cf = GetCommonFields(log)
	if cf == nil || cf.Title != "test log" {
		t.Errorf("GetCommonFields(log) failed")
	}

	// Unknown type
	cf = GetCommonFields("not a record")
	if cf != nil {
		t.Errorf("GetCommonFields(string) = %v, want nil", cf)
	}
}

// TestMeetingWithRemindBefore verifies a meeting with remind_before and participants.
func TestMeetingWithRemindBefore(t *testing.T) {
	path := filepath.Join(sampleDir, "meetings/2026/03/24/20260324_144157.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}

	parsed, err := ParseRecord(data)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}

	meeting, ok := parsed.(*MeetingRecord)
	if !ok {
		t.Fatalf("expected *MeetingRecord, got %T", parsed)
	}

	if meeting.CommonFields.RemindBefore != "15m" {
		t.Errorf("RemindBefore = %q, want %q", meeting.CommonFields.RemindBefore, "15m")
	}
	if meeting.CommonFields.Priority != "high" {
		t.Errorf("Priority = %q, want %q", meeting.CommonFields.Priority, "high")
	}
}

// TestMeetingWithParticipants verifies a meeting with participants list.
func TestMeetingWithParticipants(t *testing.T) {
	path := filepath.Join(sampleDir, "meetings/2026/03/19/20260319_1000_dept7_meeting.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}

	parsed, err := ParseRecord(data)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}

	meeting, ok := parsed.(*MeetingRecord)
	if !ok {
		t.Fatalf("expected *MeetingRecord, got %T", parsed)
	}

	if len(meeting.Participants) != 1 || meeting.Participants[0] != "七部相关人员" {
		t.Errorf("Participants = %v, want [七部相关人员]", meeting.Participants)
	}
	if meeting.Location != "七部" {
		t.Errorf("Location = %q, want %q", meeting.Location, "七部")
	}
}
