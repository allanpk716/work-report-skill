package report

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"wr/internal/models"
	"wr/internal/storage"
)

// addTestRecord is a helper that creates a record file in the test storage.
func addTestRecord(t *testing.T, store *storage.Storage, rec interface{}) {
	t.Helper()
	_, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add test record: %v", err)
	}
}

func TestGenerate_Empty(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir, log.New(io.Discard, "", 0))

	rpt, err := Generate(store, "2026-05-02", nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if rpt.Date != "2026-05-02" {
		t.Errorf("Date = %q, want 2026-05-02", rpt.Date)
	}
	if rpt.Summary.Total != 0 {
		t.Errorf("Total = %d, want 0", rpt.Summary.Total)
	}
	if len(rpt.Meetings) != 0 || len(rpt.Tasks) != 0 ||
		len(rpt.Reminders) != 0 || len(rpt.Logs) != 0 {
		t.Error("expected all entry slices to be empty")
	}
	// Markdown should have header but no sections
	if !strings.Contains(rpt.Markdown, "# 工作日报 2026-05-02") {
		t.Error("markdown missing header")
	}
	if strings.Contains(rpt.Markdown, "## 📅 会议") {
		t.Error("empty report should not contain meeting section")
	}
}

func TestGenerate_AllTypes(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir, log.New(io.Discard, "", 0))

	// Add one of each type
	addTestRecord(t, store, &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeMeeting,
			Title: "团队周会",
			Date:  "2026-05-02",
			Time:  "14:00",
		},
		Participants: []string{"Alice", "Bob"},
	})
	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeTask,
			Title: "完成设计文档",
			Date:  "2026-05-02",
		},
	})
	addTestRecord(t, store, &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeReminder,
			Title: "提交报告",
			Date:  "2026-05-02",
			Time:  "17:00",
		},
	})
	addTestRecord(t, store, &models.LogRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeLog,
			Title: "修复登录Bug",
			Date:  "2026-05-02",
		},
	})

	rpt, err := Generate(store, "2026-05-02", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if rpt.Summary.Meetings != 1 {
		t.Errorf("Meetings = %d, want 1", rpt.Summary.Meetings)
	}
	if rpt.Summary.Tasks != 1 {
		t.Errorf("Tasks = %d, want 1", rpt.Summary.Tasks)
	}
	if rpt.Summary.Reminders != 1 {
		t.Errorf("Reminders = %d, want 1", rpt.Summary.Reminders)
	}
	if rpt.Summary.Logs != 1 {
		t.Errorf("Logs = %d, want 1", rpt.Summary.Logs)
	}
	if rpt.Summary.Total != 4 {
		t.Errorf("Total = %d, want 4", rpt.Summary.Total)
	}

	// Verify grouping — each entry should be in the correct slice
	if rpt.Meetings[0].Title != "团队周会" {
		t.Errorf("meeting title = %q, want 团队周会", rpt.Meetings[0].Title)
	}
	if rpt.Tasks[0].Title != "完成设计文档" {
		t.Errorf("task title = %q, want 完成设计文档", rpt.Tasks[0].Title)
	}
	if rpt.Reminders[0].Title != "提交报告" {
		t.Errorf("reminder title = %q, want 提交报告", rpt.Reminders[0].Title)
	}
	if rpt.Logs[0].Title != "修复登录Bug" {
		t.Errorf("log title = %q, want 修复登录Bug", rpt.Logs[0].Title)
	}

	// Verify participants extracted for meeting
	if len(rpt.Meetings[0].Participants) != 2 {
		t.Errorf("meeting participants = %d, want 2", len(rpt.Meetings[0].Participants))
	}
}

func TestGenerate_DateFilter(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir, log.New(io.Discard, "", 0))

	// Add records for two different dates
	addTestRecord(t, store, &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeMeeting,
			Title: "meeting-day1",
			Date:  "2026-05-02",
		},
	})
	addTestRecord(t, store, &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeMeeting,
			Title: "meeting-day2",
			Date:  "2026-05-03",
		},
	})

	rpt, err := Generate(store, "2026-05-02", nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if rpt.Summary.Total != 1 {
		t.Errorf("Total = %d, want 1 (only day1 records)", rpt.Summary.Total)
	}
	if rpt.Meetings[0].Title != "meeting-day1" {
		t.Errorf("title = %q, want meeting-day1", rpt.Meetings[0].Title)
	}
}

func TestGenerate_Markdown(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir, log.New(io.Discard, "", 0))

	addTestRecord(t, store, &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:     models.TypeMeeting,
			Title:    "项目评审",
			Date:     "2026-05-02",
			Time:     "14:00",
			EndTime:  "15:30",
			Location: "会议室A",
		},
		Participants: []string{"张三", "李四"},
	})
	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:   models.TypeTask,
			Title:  "编写测试",
			Date:   "2026-05-02",
			Status: models.StatusCompleted,
		},
	})

	rpt, err := Generate(store, "2026-05-02", nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	md := rpt.Markdown

	// Header
	if !strings.Contains(md, "# 工作日报 2026-05-02") {
		t.Error("markdown missing date header")
	}

	// Summary line
	if !strings.Contains(md, "会议 1 | 任务 1") {
		t.Error("markdown missing summary line with counts")
	}

	// Meeting section with time, location, participants
	if !strings.Contains(md, "## 📅 会议 (1)") {
		t.Error("markdown missing meeting section header")
	}
	if !strings.Contains(md, "项目评审 [14:00-15:30 @会议室A (张三, 李四)]") {
		t.Errorf("markdown meeting entry unexpected:\n%s", md)
	}

	// Task section with status
	if !strings.Contains(md, "## ✅ 任务 (1)") {
		t.Error("markdown missing task section header")
	}
	if !strings.Contains(md, "编写测试 [已完成]") {
		t.Errorf("markdown task entry unexpected:\n%s", md)
	}
}

func TestGenerate_Markdown_EmptySections(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir, log.New(io.Discard, "", 0))

	// Only add a log — other sections should be absent from markdown
	addTestRecord(t, store, &models.LogRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeLog,
			Title: "只写日志",
			Date:  "2026-05-02",
		},
	})

	rpt, err := Generate(store, "2026-05-02", nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	md := rpt.Markdown
	if strings.Contains(md, "## 📅 会议") {
		t.Error("empty meeting section should not appear in markdown")
	}
	if strings.Contains(md, "## ✅ 任务") {
		t.Error("empty task section should not appear in markdown")
	}
	if !strings.Contains(md, "## 📝 日志") {
		t.Error("log section should appear in markdown")
	}
}

func TestGenerate_NilStorage(t *testing.T) {
	_, err := Generate(nil, "2026-05-02", nil)
	if err == nil {
		t.Fatal("expected error for nil storage")
	}
	if !strings.Contains(err.Error(), "storage is nil") {
		t.Errorf("error = %v, want storage is nil", err)
	}
}

func TestGenerateToday(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir, log.New(io.Discard, "", 0))

	loc := time.UTC
	today := time.Now().In(loc).Format("2006-01-02")

	addTestRecord(t, store, &models.LogRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeLog,
			Title: "today's log",
			Date:  today,
		},
	})

	rpt, err := GenerateToday(store, loc, nil)
	if err != nil {
		t.Fatalf("GenerateToday: %v", err)
	}

	if rpt.Date != today {
		t.Errorf("Date = %q, want %q", rpt.Date, today)
	}
	if rpt.Summary.Total != 1 {
		t.Errorf("Total = %d, want 1", rpt.Summary.Total)
	}
}

func TestGenerate_SortingByTime(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir, log.New(io.Discard, "", 0))

	// Add meetings with different times (out of order).
	// Use slight delays between AddRecord calls since filenames use second-precision
	// timestamps and would collide if added in the same second.
	addTestRecord(t, store, &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeMeeting,
			Title: "later meeting",
			Date:  "2026-05-02",
			Time:  "16:00",
		},
	})
	time.Sleep(10 * time.Millisecond)
	addTestRecord(t, store, &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeMeeting,
			Title: "earlier meeting",
			Date:  "2026-05-02",
			Time:  "09:00",
		},
	})
	time.Sleep(10 * time.Millisecond)
	addTestRecord(t, store, &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeMeeting,
			Title: "no time meeting",
			Date:  "2026-05-02",
		},
	})

	rpt, err := Generate(store, "2026-05-02", nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(rpt.Meetings) != 3 {
		t.Fatalf("expected 3 meetings, got %d", len(rpt.Meetings))
	}

	// Should be sorted: 09:00, 16:00, (empty time last)
	if rpt.Meetings[0].Title != "earlier meeting" {
		t.Errorf("first meeting = %q, want earlier meeting", rpt.Meetings[0].Title)
	}
	if rpt.Meetings[1].Title != "later meeting" {
		t.Errorf("second meeting = %q, want later meeting", rpt.Meetings[1].Title)
	}
	if rpt.Meetings[2].Title != "no time meeting" {
		t.Errorf("third meeting = %q, want no time meeting", rpt.Meetings[2].Title)
	}
}

func TestGenerate_MultipleRecordsPerType(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir, log.New(io.Discard, "", 0))

	// Add 3 tasks
	for i := 0; i < 3; i++ {
		addTestRecord(t, store, &models.TaskRecord{
			CommonFields: models.CommonFields{
				Type:  models.TypeTask,
				Title: "task " + string(rune('A'+i)),
				Date:  "2026-05-02",
			},
		})
	}

	rpt, err := Generate(store, "2026-05-02", nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if rpt.Summary.Tasks != 3 {
		t.Errorf("Tasks = %d, want 3", rpt.Summary.Tasks)
	}
	if rpt.Summary.Total != 3 {
		t.Errorf("Total = %d, want 3", rpt.Summary.Total)
	}
}

func TestGenerate_LogWithProgress(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir, log.New(io.Discard, "", 0))

	// LogRecord has its own Priority field that shadows CommonFields.Priority.
	// Set the type-specific Priority directly.
	rec := &models.LogRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeLog,
			Title: "代码重构",
			Date:  "2026-05-02",
		},
		Progress: "80%",
		Priority: "high", // LogRecord's own Priority (shadows CommonFields)
	}
	addTestRecord(t, store, rec)

	rpt, err := Generate(store, "2026-05-02", nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	md := rpt.Markdown
	if !strings.Contains(md, "代码重构 [80% 优先级:high]") {
		t.Errorf("log entry unexpected in markdown:\n%s", md)
	}
}

func TestGenerate_CompletedTaskStatus(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir, log.New(io.Discard, "", 0))

	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:   models.TypeTask,
			Title:  "active task",
			Date:   "2026-05-02",
			Status: models.StatusActive,
		},
	})

	rpt, err := Generate(store, "2026-05-02", nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	md := rpt.Markdown
	if !strings.Contains(md, "active task [进行中]") {
		t.Errorf("active task entry unexpected in markdown:\n%s", md)
	}
}


