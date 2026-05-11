package models

import (
	"encoding/json"
	"testing"
)

// Inline sample data fixtures — real sample files from
// docs/exsample/.nanobot-work-helper--20260501/.nanobot-work-helper/work-records/
// inlined here so tests are self-contained and work in any worktree.

const sampleMeetingJSON = `{
  "type": "meeting",
  "title": "BM-200 月度总结会",
  "description": "BM-200 注册检项目月度总结会议",
  "date": "2026-04-30",
  "time": "",
  "end_time": "",
  "location": "",
  "related_person": "石艳民",
  "status": "completed",
  "tags": [
    "BM-200",
    "注册检",
    "月度总结"
  ],
  "saved_at": "2026-04-30T10:32:11.032293",
  "updated_at": "2026-04-30T10:41:00.000000"
}`

const sampleTaskActiveJSON = `{
  "type": "task",
  "title": "制作外部问题反馈提交表单",
  "description": "做出以后外部问题反馈的提交表单，避免反馈问题来来回回问几次",
  "date": "2026-05-06",
  "time": "",
  "location": "",
  "related_person": "",
  "priority": "high",
  "status": "pending",
  "tags": [
    "流程优化",
    "外部反馈"
  ],
  "saved_at": "2026-04-29T11:52:36.116597"
}`

const sampleTaskCompletedJSON = `{
  "type": "task",
  "title": "帮唐总安装Nanobot",
  "description": "今天下午17:00去帮唐总安装Nanobot。",
  "date": "2026-03-11",
  "time": "17:00",
  "end_time": "",
  "location": "未指定",
  "related_persons": [
    "唐总"
  ],
  "tags": [
    "协助",
    "安装"
  ],
  "priority": "高",
  "reminder": "30m",
  "raw_input": "今天下午17点要去帮唐总安装Nanobot",
  "processed_at": "2026-03-11T12:11:00",
  "saved_at": "2026-03-11T12:11:45.241674",
  "status": "completed",
  "completed_at": "2026-03-13T23:59:59"
}`

const sampleReminderActiveJSON = `{
  "type": "reminder",
  "title": "问自动化文档编写规范是否完成",
  "date": "2026-12-01",
  "time": "09:00",
  "notes": "去确认自动化文档编写规范是否已完成",
  "saved_at": "2026-04-15T14:35:46.487827"
}`

const sampleReminderWithRemindBeforeJSON = `{
  "type": "reminder",
  "title": "调研华为交流内容并反馈王总",
  "description": "给王总调研跟华为交流的内容，涉及大健康、IVD、动物等方向",
  "date": "2026-04-21",
  "time": "09:00",
  "end_time": "",
  "location": "",
  "related_person": "王总",
  "remind_before": "15m",
  "priority": "medium",
  "status": "pending",
  "tags": [
    "调研",
    "华为",
    "大健康",
    "IVD",
    "动物"
  ],
  "saved_at": "2026-04-16T17:10:00"
}`

const sampleLogJSON = `{
  "type": "done_things",
  "date": "2026-04-30",
  "title": "集团算法现状和改进方向PPT评审",
  "description": "对集团算法现状和改进方向PPT进行了评审，提出了改进意见。",
  "tags": [
    "算法",
    "PPT评审",
    "集团"
  ],
  "saved_at": "2026-05-01T07:34:56.347112"
}`

const sampleLogWithFieldsJSON = `{
  "type": "done_things",
  "title": "完成代码审查",
  "description": "对项目相关代码进行了审查，已完成本次审查任务。",
  "date": "2026-03-11",
  "time": "09:29",
  "tags": [
    "代码审查",
    "开发"
  ],
  "priority": "normal",
  "progress": "completed",
  "saved_at": "2026-03-11T09:29:24.848804"
}`

const sampleMeetingWithRemindBeforeJSON = `{
  "type": "meeting",
  "title": "集团软件组纪委会",
  "description": "明天上午 11 点开集团软件组纪委会",
  "date": "2026-03-24",
  "time": "11:00",
  "end_time": "",
  "location": "未指定",
  "remind_before": "15m",
  "priority": "high",
  "participants": [],
  "agenda": "",
  "saved_at": "2026-03-23T14:41:57.953806"
}`

const sampleMeetingWithParticipantsJSON = `{
  "type": "meeting",
  "title": "跟七部开尿有形临床相关的数据收集会议",
  "description": "与七部沟通尿有形临床相关的数据收集事宜。",
  "date": "2026-03-19",
  "time": "10:00",
  "end_time": "",
  "location": "七部",
  "remind_before": "15m",
  "priority": "normal",
  "participants": ["七部相关人员"],
  "saved_at": "2026-03-18T17:37:00"
}`

// TestParseMeetingRoundTrip verifies that a meeting JSON file round-trips
// through ParseRecord → MarshalRecord without data loss.
func TestParseMeetingRoundTrip(t *testing.T) {
	original := []byte(sampleMeetingJSON)

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
	original := []byte(sampleTaskActiveJSON)

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
	data := []byte(sampleTaskCompletedJSON)

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
	original := []byte(sampleReminderActiveJSON)

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
	data := []byte(sampleReminderWithRemindBeforeJSON)

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
	original := []byte(sampleLogJSON)

	parsed, err := ParseRecord(original)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}

	doneThings, ok := parsed.(*DoneThingsRecord)
	if !ok {
		t.Fatalf("expected *DoneThingsRecord, got %T", parsed)
	}

	if doneThings.Type != TypeDoneThings {
		t.Errorf("Type = %q, want %q", doneThings.Type, TypeDoneThings)
	}
	if doneThings.Title != "集团算法现状和改进方向PPT评审" {
		t.Errorf("Title = %q", doneThings.Title)
	}
	if len(doneThings.Tags) != 3 {
		t.Errorf("Tags length = %d, want 3", len(doneThings.Tags))
	}

	// Round-trip
	roundTrip, err := MarshalRecord(doneThings)
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
	data := []byte(sampleLogWithFieldsJSON)

	parsed, err := ParseRecord(data)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}

	doneThings, ok := parsed.(*DoneThingsRecord)
	if !ok {
		t.Fatalf("expected *DoneThingsRecord, got %T", parsed)
	}

	if doneThings.Time != "09:29" {
		t.Errorf("Time = %q, want %q", doneThings.Time, "09:29")
	}
	if doneThings.Priority != "normal" {
		t.Errorf("Priority = %q, want %q", doneThings.Priority, "normal")
	}
	if doneThings.Progress != "completed" {
		t.Errorf("Progress = %q, want %q", doneThings.Progress, "completed")
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

// TestShortIDFromFilename verifies deterministic 16-char hex output.
func TestShortIDFromFilename(t *testing.T) {
	id := ShortIDFromFilename("20260430_103211.json")
	if len(id) != 16 {
		t.Errorf("ShortID length = %d, want 16", len(id))
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
	if len(id4) != 16 {
		t.Errorf("ShortID length for non-standard filename = %d, want 16", len(id4))
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
		{"done_things", true},
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

	doneThings := &DoneThingsRecord{
		CommonFields: CommonFields{Type: TypeDoneThings, Title: "test done_things"},
	}
	cf = GetCommonFields(doneThings)
	if cf == nil || cf.Title != "test done_things" {
		t.Errorf("GetCommonFields(done_things) failed")
	}

	// Unknown type
	cf = GetCommonFields("not a record")
	if cf != nil {
		t.Errorf("GetCommonFields(string) = %v, want nil", cf)
	}
}

// TestMeetingWithRemindBefore verifies a meeting with remind_before and participants.
func TestMeetingWithRemindBefore(t *testing.T) {
	data := []byte(sampleMeetingWithRemindBeforeJSON)

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
	data := []byte(sampleMeetingWithParticipantsJSON)

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
