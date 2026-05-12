package report

import (
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
	store := storage.New(dir)

	rpt, err := Generate(store, "2026-05-02")
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
		len(rpt.Reminders) != 0 || len(rpt.DoneThings) != 0 {
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
	store := storage.New(dir)

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
	addTestRecord(t, store, &models.DoneThingsRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeDoneThings,
			Title: "修复登录Bug",
			Date:  "2026-05-02",
		},
	})

	rpt, err := Generate(store, "2026-05-02")
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
	if rpt.Summary.DoneThings != 1 {
		t.Errorf("Logs = %d, want 1", rpt.Summary.DoneThings)
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
	if rpt.DoneThings[0].Title != "修复登录Bug" {
		t.Errorf("log title = %q, want 修复登录Bug", rpt.DoneThings[0].Title)
	}

	// Verify participants extracted for meeting
	if len(rpt.Meetings[0].Participants) != 2 {
		t.Errorf("meeting participants = %d, want 2", len(rpt.Meetings[0].Participants))
	}
}

func TestGenerate_DateFilter(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

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

	rpt, err := Generate(store, "2026-05-02")
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
	store := storage.New(dir)

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

	rpt, err := Generate(store, "2026-05-02")
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
	store := storage.New(dir)

	// Only add a log — other sections should be absent from markdown
	addTestRecord(t, store, &models.DoneThingsRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeDoneThings,
			Title: "只写已完成的事",
			Date:  "2026-05-02",
		},
	})

	rpt, err := Generate(store, "2026-05-02")
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
	if !strings.Contains(md, "## 📝 已完成的事") {
		t.Error("done_things section should appear in markdown")
	}
}

func TestGenerate_NilStorage(t *testing.T) {
	_, err := Generate(nil, "2026-05-02")
	if err == nil {
		t.Fatal("expected error for nil storage")
	}
	if !strings.Contains(err.Error(), "storage is nil") {
		t.Errorf("error = %v, want storage is nil", err)
	}
}

func TestGenerateToday(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	loc := time.UTC
	today := time.Now().In(loc).Format("2006-01-02")

	addTestRecord(t, store, &models.DoneThingsRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeDoneThings,
			Title: "today's log",
			Date:  today,
		},
	})

	rpt, err := GenerateToday(store, loc)
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
	store := storage.New(dir)

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

	rpt, err := Generate(store, "2026-05-02")
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
	store := storage.New(dir)

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

	rpt, err := Generate(store, "2026-05-02")
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
	store := storage.New(dir)

	// LogRecord has its own Priority field that shadows CommonFields.Priority.
	// Set the type-specific Priority directly.
	rec := &models.DoneThingsRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeDoneThings,
			Title: "代码重构",
			Date:  "2026-05-02",
		},
		Progress: "80%",
		Priority: "high", // LogRecord's own Priority (shadows CommonFields)
	}
	addTestRecord(t, store, rec)

	rpt, err := Generate(store, "2026-05-02")
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
	store := storage.New(dir)

	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:   models.TypeTask,
			Title:  "active task",
			Date:   "2026-05-02",
			Status: models.StatusActive,
		},
	})

	rpt, err := Generate(store, "2026-05-02")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	md := rpt.Markdown
	if !strings.Contains(md, "active task [进行中]") {
		t.Errorf("active task entry unexpected in markdown:\n%s", md)
	}
}

func TestGenerateRange_EmptyRange(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	rr, err := GenerateRange(store, "2026-05-02", "2026-05-02", time.UTC)
	if err != nil {
		t.Fatalf("GenerateRange: %v", err)
	}

	if rr.DateFrom != "2026-05-02" {
		t.Errorf("DateFrom = %q, want 2026-05-02", rr.DateFrom)
	}
	if rr.DateTo != "2026-05-02" {
		t.Errorf("DateTo = %q, want 2026-05-02", rr.DateTo)
	}
	if rr.DaysCount != 1 {
		t.Errorf("DaysCount = %d, want 1", rr.DaysCount)
	}
	if rr.Summary.Total != 0 {
		t.Errorf("Total = %d, want 0", rr.Summary.Total)
	}
	if len(rr.MergedMeetings) != 0 || len(rr.MergedTasks) != 0 {
		t.Error("expected empty merged slices for empty range")
	}
}

func TestGenerateRange_SingleDay(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeTask,
			Title: "single day task",
			Date:  "2026-05-02",
		},
	})

	rr, err := GenerateRange(store, "2026-05-02", "2026-05-02", time.UTC)
	if err != nil {
		t.Fatalf("GenerateRange: %v", err)
	}

	if rr.DaysCount != 1 {
		t.Errorf("DaysCount = %d, want 1", rr.DaysCount)
	}
	if rr.Summary.Tasks != 1 {
		t.Errorf("Tasks = %d, want 1", rr.Summary.Tasks)
	}
	if len(rr.MergedTasks) != 1 {
		t.Fatalf("MergedTasks len = %d, want 1", len(rr.MergedTasks))
	}
	if rr.MergedTasks[0].Title != "single day task" {
		t.Errorf("MergedTasks[0].Title = %q, want single day task", rr.MergedTasks[0].Title)
	}
}

func TestGenerateRange_MultiDay(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	// Day 1
	addTestRecord(t, store, &models.MeetingRecord{
		CommonFields: models.CommonFields{Type: models.TypeMeeting, Title: "meeting-d1", Date: "2026-05-01"},
	})
	// Day 2
	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{Type: models.TypeTask, Title: "task-d2", Date: "2026-05-02"},
	})
	// Day 3
	addTestRecord(t, store, &models.DoneThingsRecord{
		CommonFields: models.CommonFields{Type: models.TypeDoneThings, Title: "log-d3", Date: "2026-05-03"},
	})

	rr, err := GenerateRange(store, "2026-05-01", "2026-05-03", time.UTC)
	if err != nil {
		t.Fatalf("GenerateRange: %v", err)
	}

	if rr.DaysCount != 3 {
		t.Errorf("DaysCount = %d, want 3", rr.DaysCount)
	}
	if rr.Summary.Total != 3 {
		t.Errorf("Total = %d, want 3", rr.Summary.Total)
	}
	if rr.Summary.Meetings != 1 || rr.Summary.Tasks != 1 || rr.Summary.DoneThings != 1 {
		t.Errorf("summary counts = meetings=%d tasks=%d done_things=%d, want 1/1/1",
			rr.Summary.Meetings, rr.Summary.Tasks, rr.Summary.DoneThings)
	}
	if len(rr.MergedMeetings) != 1 || len(rr.MergedTasks) != 1 || len(rr.MergedDoneThings) != 1 {
		t.Error("expected 1 merged entry per type")
	}
}

func TestGenerateRange_DateOrder(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	_, err := GenerateRange(store, "2026-05-05", "2026-05-01", time.UTC)
	if err == nil {
		t.Fatal("expected error when from > to")
	}
	if !strings.Contains(err.Error(), "after") {
		t.Errorf("error = %v, want 'after' in message", err)
	}
}

func TestGenerateRange_MergedSummary(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	// Two tasks on day 1
	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{Type: models.TypeTask, Title: "t1", Date: "2026-05-01"},
	})
	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{Type: models.TypeTask, Title: "t2", Date: "2026-05-01"},
	})
	// One task on day 2
	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{Type: models.TypeTask, Title: "t3", Date: "2026-05-02"},
	})

	rr, err := GenerateRange(store, "2026-05-01", "2026-05-02", time.UTC)
	if err != nil {
		t.Fatalf("GenerateRange: %v", err)
	}

	if rr.Summary.Tasks != 3 {
		t.Errorf("Tasks = %d, want 3", rr.Summary.Tasks)
	}
	if rr.Summary.Total != 3 {
		t.Errorf("Total = %d, want 3", rr.Summary.Total)
	}
	if len(rr.MergedTasks) != 3 {
		t.Errorf("MergedTasks len = %d, want 3", len(rr.MergedTasks))
	}
}

func TestGenerateWeek_Bounds(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	// Use a fixed "now" by choosing a known date's timezone-relative week.
	// We can't mock time.Now directly, so we verify structural properties:
	// - DaysCount should be 7
	// - DateFrom should be a Monday
	// - DateTo should be a Sunday
	rr, err := GenerateWeek(store, time.UTC)
	if err != nil {
		t.Fatalf("GenerateWeek: %v", err)
	}

	if rr.DaysCount != 7 {
		t.Errorf("DaysCount = %d, want 7", rr.DaysCount)
	}

	fromTime, _ := time.Parse("2006-01-02", rr.DateFrom)
	if fromTime.Weekday() != time.Monday {
		t.Errorf("DateFrom weekday = %v, want Monday", fromTime.Weekday())
	}
	toTime, _ := time.Parse("2006-01-02", rr.DateTo)
	if toTime.Weekday() != time.Sunday {
		t.Errorf("DateTo weekday = %v, want Sunday", toTime.Weekday())
	}

	// from and to should be exactly 6 days apart (Mon to Sun inclusive = 7 days)
	diff := toTime.Sub(fromTime).Hours() / 24
	if diff != 6 {
		t.Errorf("date span = %.0f days, want 6", diff)
	}
}

func TestGenerateRange_Markdown(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	addTestRecord(t, store, &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:     models.TypeMeeting,
			Title:    "项目评审",
			Date:     "2026-04-28",
			Time:     "10:00",
			Location: "会议室A",
		},
	})
	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:   models.TypeTask,
			Title:  "编写测试",
			Date:   "2026-04-29",
			Status: models.StatusCompleted,
		},
	})

	rr, err := GenerateRange(store, "2026-04-28", "2026-04-29", time.UTC)
	if err != nil {
		t.Fatalf("GenerateRange: %v", err)
	}

	md := rr.Markdown

	// Header
	if !strings.Contains(md, "# 工作周报 2026-04-28 ~ 2026-04-29") {
		t.Error("markdown missing range header")
	}

	// Summary line with day count
	if !strings.Contains(md, "2天") {
		t.Error("markdown missing day count in summary")
	}

	// Meeting section with date prefix
	if !strings.Contains(md, "## 📅 会议 (1)") {
		t.Error("markdown missing meeting section header")
	}
	if !strings.Contains(md, "[2026-04-28] 项目评审") {
		t.Error("markdown missing date-prefixed meeting entry")
	}

	// Task section with date prefix and status
	if !strings.Contains(md, "## ✅ 任务 (1)") {
		t.Error("markdown missing task section header")
	}
	if !strings.Contains(md, "[2026-04-29] 编写测试 [已完成]") {
		t.Error("markdown missing date-prefixed task entry")
	}
}

func TestGenerateRange_Markdown_EmptyDays(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	rr, err := GenerateRange(store, "2026-05-01", "2026-05-03", time.UTC)
	if err != nil {
		t.Fatalf("GenerateRange: %v", err)
	}

	md := rr.Markdown

	if !strings.Contains(md, "# 工作周报 2026-05-01 ~ 2026-05-03") {
		t.Error("markdown missing range header")
	}
	if !strings.Contains(md, "3天") {
		t.Error("markdown missing day count")
	}
	if strings.Contains(md, "## 📅 会议") {
		t.Error("empty meeting section should not appear")
	}
	if strings.Contains(md, "## ✅ 任务") {
		t.Error("empty task section should not appear")
	}
	if strings.Contains(md, "## 🔔 提醒") {
		t.Error("empty reminder section should not appear")
	}
	if strings.Contains(md, "## 📝 已完成的事") {
		t.Error("empty done_things section should not appear")
	}
}

func TestGenerateRange_InvalidDate(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	_, err := GenerateRange(store, "not-a-date", "2026-05-02", time.UTC)
	if err == nil {
		t.Fatal("expected error for invalid from date")
	}
	if !strings.Contains(err.Error(), "invalid from date") {
		t.Errorf("error = %v, want invalid from date", err)
	}

	_, err = GenerateRange(store, "2026-05-02", "bad", time.UTC)
	if err == nil {
		t.Fatal("expected error for invalid to date")
	}
	if !strings.Contains(err.Error(), "invalid to date") {
		t.Errorf("error = %v, want invalid to date", err)
	}
}

func TestGenerateRange_NilStorage(t *testing.T) {
	_, err := GenerateRange(nil, "2026-05-01", "2026-05-02", time.UTC)
	if err == nil {
		t.Fatal("expected error for nil storage")
	}
	if !strings.Contains(err.Error(), "storage is nil") {
		t.Errorf("error = %v, want storage is nil", err)
	}
}

func TestGenerate_PersonalsSection(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	addTestRecord(t, store, &models.PersonalRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypePersonal,
			Title: "看牙医",
			Date:  "2026-05-02",
			Time:  "10:00",
		},
	})
	addTestRecord(t, store, &models.PersonalRecord{
		CommonFields: models.CommonFields{
			Type:   models.TypePersonal,
			Title:  "取快递",
			Date:   "2026-05-02",
			Status: models.StatusCompleted,
		},
	})
	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeTask,
			Title: "工作事项",
			Date:  "2026-05-02",
		},
	})

	rpt, err := Generate(store, "2026-05-02")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Personals count in Summary but NOT in Total
	if rpt.Summary.Personals != 2 {
		t.Errorf("Personals = %d, want 2", rpt.Summary.Personals)
	}
	if rpt.Summary.Total != 1 {
		t.Errorf("Total = %d, want 1 (only work types)", rpt.Summary.Total)
	}

	// Personals slice populated
	if len(rpt.Personals) != 2 {
		t.Fatalf("Personals len = %d, want 2", len(rpt.Personals))
	}

	// Markdown contains personals section
	md := rpt.Markdown
	if !strings.Contains(md, "## 🏠 个人事务 (2)") {
		t.Error("markdown missing personals section header")
	}
	if !strings.Contains(md, "看牙医 [10:00]") {
		t.Error("markdown missing personal entry with time")
	}
	if !strings.Contains(md, "取快递 [已处理]") {
		t.Error("markdown missing completed personal entry")
	}

	// Summary line includes personals count
	if !strings.Contains(md, "个人事务 2") {
		t.Error("summary line missing personals count")
	}
}

func TestGenerate_NoPersonals_OmitsSection(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeTask,
			Title: "工作事项",
			Date:  "2026-05-02",
		},
	})

	rpt, err := Generate(store, "2026-05-02")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if rpt.Summary.Personals != 0 {
		t.Errorf("Personals = %d, want 0", rpt.Summary.Personals)
	}
	if rpt.Summary.Total != 1 {
		t.Errorf("Total = %d, want 1", rpt.Summary.Total)
	}

	md := rpt.Markdown
	if strings.Contains(md, "🏠 个人事务") {
		t.Error("no personals section should appear when empty")
	}
	if strings.Contains(md, "个人事务") {
		t.Error("summary line should not mention personals when zero")
	}
}

func TestGenerate_PersonalsNotInTotal(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{Type: models.TypeTask, Title: "t", Date: "2026-05-02"},
	})
	addTestRecord(t, store, &models.PersonalRecord{
		CommonFields: models.CommonFields{Type: models.TypePersonal, Title: "p", Date: "2026-05-02"},
	})

	rpt, err := Generate(store, "2026-05-02")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Total = work types only (tasks=1, personals NOT counted in total)
	if rpt.Summary.Total != 1 {
		t.Errorf("Total = %d, want 1 (personals excluded from total)", rpt.Summary.Total)
	}
	if rpt.Summary.Personals != 1 {
		t.Errorf("Personals = %d, want 1", rpt.Summary.Personals)
	}
}

func TestGenerateRange_PersonalsMerged(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	addTestRecord(t, store, &models.PersonalRecord{
		CommonFields: models.CommonFields{Type: models.TypePersonal, Title: "p1", Date: "2026-05-01"},
	})
	addTestRecord(t, store, &models.PersonalRecord{
		CommonFields: models.CommonFields{Type: models.TypePersonal, Title: "p2", Date: "2026-05-02"},
	})
	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{Type: models.TypeTask, Title: "t1", Date: "2026-05-01"},
	})

	rr, err := GenerateRange(store, "2026-05-01", "2026-05-02", time.UTC)
	if err != nil {
		t.Fatalf("GenerateRange: %v", err)
	}

	if rr.Summary.Personals != 2 {
		t.Errorf("Personals = %d, want 2", rr.Summary.Personals)
	}
	if rr.Summary.Total != 1 {
		t.Errorf("Total = %d, want 1 (personals excluded)", rr.Summary.Total)
	}
	if len(rr.MergedPersonals) != 2 {
		t.Errorf("MergedPersonals len = %d, want 2", len(rr.MergedPersonals))
	}

	md := rr.Markdown
	if !strings.Contains(md, "## 🏠 个人事务 (2)") {
		t.Error("range markdown missing personals section")
	}
	if !strings.Contains(md, "[2026-05-01] p1") {
		t.Error("range markdown missing date-prefixed personal entry")
	}
	if !strings.Contains(md, "[2026-05-02] p2") {
		t.Error("range markdown missing date-prefixed personal entry")
	}
	if !strings.Contains(md, "个人事务 2") {
		t.Error("range summary line missing personals count")
	}
}

func TestGenerateRange_NoPersonals_OmitsSection(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{Type: models.TypeTask, Title: "t", Date: "2026-05-01"},
	})

	rr, err := GenerateRange(store, "2026-05-01", "2026-05-02", time.UTC)
	if err != nil {
		t.Fatalf("GenerateRange: %v", err)
	}

	if rr.Summary.Personals != 0 {
		t.Errorf("Personals = %d, want 0", rr.Summary.Personals)
	}

	md := rr.Markdown
	if strings.Contains(md, "🏠 个人事务") {
		t.Error("no personals section should appear when empty")
	}
}

func TestGenerate_BacklogsSection(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	// Add an active backlog
	addTestRecord(t, store, &models.BacklogRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeBacklog,
			Title: "整理文档",
			Date:  "",
		},
	})
	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{Type: models.TypeTask, Title: "工作事项", Date: "2026-05-02"},
	})

	rpt, err := Generate(store, "2026-05-02")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Backlogs count in Summary but NOT in Total
	if rpt.Summary.Backlogs != 1 {
		t.Errorf("Backlogs = %d, want 1", rpt.Summary.Backlogs)
	}
	if rpt.Summary.Total != 1 {
		t.Errorf("Total = %d, want 1 (backlogs excluded from total)", rpt.Summary.Total)
	}

	// Backlogs slice populated
	if len(rpt.Backlogs) != 1 {
		t.Fatalf("Backlogs len = %d, want 1", len(rpt.Backlogs))
	}
	if rpt.Backlogs[0].Title != "整理文档" {
		t.Errorf("Backlog title = %q, want 整理文档", rpt.Backlogs[0].Title)
	}

	// Markdown contains backlogs section
	md := rpt.Markdown
	if !strings.Contains(md, "## 📋 待办积压 (1)") {
		t.Error("markdown missing backlogs section header")
	}
	if !strings.Contains(md, "- 整理文档") {
		t.Error("markdown missing backlog entry")
	}
	if !strings.Contains(md, "待办积压 1") {
		t.Error("summary line missing backlogs count")
	}
}

func TestGenerate_BacklogCompletedToday(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	// Add a backlog, then complete it
	br := &models.BacklogRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeBacklog,
			Title: "处理退款",
			Date:  "",
		},
	}
	added, _ := store.AddRecord(br)
	addedBR := added.(*models.BacklogRecord)
	shortID := addedBR.ShortID

	// Complete it
	store.CompleteRecord(shortID)

	// Now generate a report for today — the completed backlog should appear
	today := time.Now().UTC().Format("2006-01-02")
	rpt, err := Generate(store, today)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if rpt.Summary.Backlogs != 1 {
		t.Errorf("Backlogs = %d, want 1 (completed today should show)", rpt.Summary.Backlogs)
	}

	md := rpt.Markdown
	if !strings.Contains(md, "处理退款 [已处理]") {
		t.Errorf("markdown should show completed backlog with 已处理:\n%s", md)
	}
}

func TestGenerate_BacklogCompletedOtherDay_OmitsFromReport(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	// Add a backlog, then complete it — it will have today's CompletedAt
	br := &models.BacklogRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeBacklog,
			Title: "过期的积压",
			Date:  "",
		},
	}
	added, _ := store.AddRecord(br)
	addedBR := added.(*models.BacklogRecord)
	shortID := addedBR.ShortID

	// Complete it — CompletedAt will be today
	store.CompleteRecord(shortID)

	// Generate report for a different date — should NOT include this backlog
	rpt, err := Generate(store, "2025-01-01")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if rpt.Summary.Backlogs != 0 {
		t.Errorf("Backlogs = %d, want 0 (completed on other day)", rpt.Summary.Backlogs)
	}

	md := rpt.Markdown
	if strings.Contains(md, "📋 待办积压") {
		t.Error("should not show backlogs section when no backlogs match")
	}
}

func TestGenerate_NoBacklogs_OmitsSection(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{Type: models.TypeTask, Title: "工作事项", Date: "2026-05-02"},
	})

	rpt, err := Generate(store, "2026-05-02")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if rpt.Summary.Backlogs != 0 {
		t.Errorf("Backlogs = %d, want 0", rpt.Summary.Backlogs)
	}

	md := rpt.Markdown
	if strings.Contains(md, "📋 待办积压") {
		t.Error("no backlogs section should appear when empty")
	}
	if strings.Contains(md, "待办积压") {
		t.Error("summary line should not mention backlogs when zero")
	}
}

func TestGenerate_BacklogsNotInTotal(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{Type: models.TypeTask, Title: "t", Date: "2026-05-02"},
	})
	addTestRecord(t, store, &models.BacklogRecord{
		CommonFields: models.CommonFields{Type: models.TypeBacklog, Title: "b", Date: ""},
	})
	addTestRecord(t, store, &models.PersonalRecord{
		CommonFields: models.CommonFields{Type: models.TypePersonal, Title: "p", Date: "2026-05-02"},
	})

	rpt, err := Generate(store, "2026-05-02")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Total = work types only (tasks=1, personals and backlogs NOT counted)
	if rpt.Summary.Total != 1 {
		t.Errorf("Total = %d, want 1 (backlogs and personals excluded from total)", rpt.Summary.Total)
	}
	if rpt.Summary.Backlogs != 1 {
		t.Errorf("Backlogs = %d, want 1", rpt.Summary.Backlogs)
	}
	if rpt.Summary.Personals != 1 {
		t.Errorf("Personals = %d, want 1", rpt.Summary.Personals)
	}
}

func TestGenerateRange_BacklogsMerged(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	// Active backlog appears on every day
	addTestRecord(t, store, &models.BacklogRecord{
		CommonFields: models.CommonFields{Type: models.TypeBacklog, Title: "always-active", Date: ""},
	})
	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{Type: models.TypeTask, Title: "t1", Date: "2026-05-01"},
	})

	rr, err := GenerateRange(store, "2026-05-01", "2026-05-02", time.UTC)
	if err != nil {
		t.Fatalf("GenerateRange: %v", err)
	}

	// Active backlogs appear on each day's report, so merged count = days * active backlogs
	if rr.Summary.Backlogs != 2 {
		t.Errorf("Backlogs = %d, want 2 (active backlog appears on each day)", rr.Summary.Backlogs)
	}
	if rr.Summary.Total != 1 {
		t.Errorf("Total = %d, want 1 (backlogs excluded)", rr.Summary.Total)
	}
	if len(rr.MergedBacklogs) != 2 {
		t.Errorf("MergedBacklogs len = %d, want 2", len(rr.MergedBacklogs))
	}

	md := rr.Markdown
	if !strings.Contains(md, "## 📋 待办积压 (2)") {
		t.Error("range markdown missing backlogs section")
	}
	if !strings.Contains(md, "待办积压 2") {
		t.Error("range summary line missing backlogs count")
	}
}

func TestGenerateRange_NoBacklogs_OmitsSection(t *testing.T) {
	dir := t.TempDir()
	store := storage.New(dir)

	addTestRecord(t, store, &models.TaskRecord{
		CommonFields: models.CommonFields{Type: models.TypeTask, Title: "t", Date: "2026-05-01"},
	})

	rr, err := GenerateRange(store, "2026-05-01", "2026-05-02", time.UTC)
	if err != nil {
		t.Fatalf("GenerateRange: %v", err)
	}

	if rr.Summary.Backlogs != 0 {
		t.Errorf("Backlogs = %d, want 0", rr.Summary.Backlogs)
	}

	md := rr.Markdown
	if strings.Contains(md, "📋 待办积压") {
		t.Error("no backlogs section should appear when empty")
	}
}


