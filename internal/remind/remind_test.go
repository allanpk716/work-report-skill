package remind

import (
	"testing"
	"time"

	"wr/internal/models"
	"wr/internal/storage"
)

// --- IsDue unit tests ---

func TestIsDue_PastWithoutTime(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		ShortID: "abc123",
		Date:    "2026-06-13",
		Time:    "",
	}

	isDue, isStale := IsDue(rec, now, 0, true)
	if !isDue {
		t.Error("expected isDue=true for past date without time")
	}
	if !isStale {
		t.Error("expected isStale=true for >24h overdue without time")
	}
}

func TestIsDue_TodayWithoutTime(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		Date: "2026-06-15",
		Time: "",
	}

	isDue, _ := IsDue(rec, now, 0, true)
	if isDue {
		t.Error("expected isDue=false for today without time")
	}
}

func TestIsDue_FutureWithoutTime(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		Date: "2026-06-16",
		Time: "",
	}

	isDue, _ := IsDue(rec, now, 0, true)
	if isDue {
		t.Error("expected isDue=false for future date without time")
	}
}

func TestIsDue_ExactDueTime(t *testing.T) {
	now := time.Date(2026, 6, 15, 9, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		Date: "2026-06-15",
		Time: "09:00",
	}

	isDue, isStale := IsDue(rec, now, 0, true)
	if !isDue {
		t.Error("expected isDue=true for exact due time")
	}
	if isStale {
		t.Error("expected isStale=false for exactly-on-time")
	}
}

func TestIsDue_PastDueTime(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		Date: "2026-06-15",
		Time: "08:00",
	}

	isDue, isStale := IsDue(rec, now, 0, true)
	if !isDue {
		t.Error("expected isDue=true for past due time")
	}
	if isStale {
		t.Error("expected isStale=false for 2h overdue")
	}
}

func TestIsDue_StaleDueTime(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		Date: "2026-06-14",
		Time: "08:00",
	}

	isDue, isStale := IsDue(rec, now, 0, true)
	if !isDue {
		t.Error("expected isDue=true for stale reminder with includeStale")
	}
	if !isStale {
		t.Error("expected isStale=true for >24h overdue with time")
	}
}

func TestIsDue_StaleExcluded(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		Date: "2026-06-14",
		Time: "08:00",
	}

	isDue, isStale := IsDue(rec, now, 0, false)
	if isDue {
		t.Error("expected isDue=false when stale and includeStale=false")
	}
	if !isStale {
		t.Error("expected isStale=true even when excluded")
	}
}

func TestIsDue_FutureDueTime(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		Date: "2026-06-15",
		Time: "11:00",
	}

	isDue, _ := IsDue(rec, now, 0, true)
	if isDue {
		t.Error("expected isDue=false for future time")
	}
}

func TestIsDue_Window(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		Date: "2026-06-15",
		Time: "10:14",
	}

	// 15-minute window should include a reminder 14 minutes away
	isDue, _ := IsDue(rec, now, 15*time.Minute, true)
	if !isDue {
		t.Error("expected isDue=true within 15-minute window")
	}
}

func TestIsDue_Window_Exceeds(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		Date: "2026-06-15",
		Time: "10:20",
	}

	// 15-minute window should NOT include a reminder 20 minutes away
	isDue, _ := IsDue(rec, now, 15*time.Minute, true)
	if isDue {
		t.Error("expected isDue=false outside 15-minute window")
	}
}

func TestIsDue_Window_Zero(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		Date: "2026-06-15",
		Time: "10:14",
	}

	// Zero window should not include future reminders
	isDue, _ := IsDue(rec, now, 0, true)
	if isDue {
		t.Error("expected isDue=false with zero window for future time")
	}
}

func TestIsDue_InvalidDate(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		Date: "not-a-date",
		Time: "",
	}

	isDue, _ := IsDue(rec, now, 0, true)
	if isDue {
		t.Error("expected isDue=false for invalid date")
	}
}

func TestIsDue_InvalidTime(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		Date: "2026-06-14",
		Time: "not-a-time",
	}

	// Invalid time means we can't parse datetime; should return not due
	isDue, _ := IsDue(rec, now, 0, true)
	if isDue {
		t.Error("expected isDue=false for invalid time")
	}
}

func TestIsDue_StaleWithoutTime_Exactly24h(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	// Exactly 24h ago → not stale (stale > 24h, not >=)
	rec := storage.ListedRecord{
		Date: "2026-06-14",
		Time: "",
	}

	isDue, isStale := IsDue(rec, now, 0, true)
	if !isDue {
		t.Error("expected isDue=true for past date without time")
	}
	if isStale {
		t.Error("expected isStale=false for exactly 24h overdue (must be >24h)")
	}
}

func TestIsDue_StaleWithTime_Exactly24h(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	rec := storage.ListedRecord{
		Date: "2026-06-14",
		Time: "10:00",
	}

	isDue, isStale := IsDue(rec, now, 0, true)
	if !isDue {
		t.Error("expected isDue=true")
	}
	if isStale {
		t.Error("expected isStale=false for exactly 24h overdue (must be >24h)")
	}
}

// --- ListDue integration tests (temp storage) ---

func TestListDue_NoReminders(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	due, err := ListDue(store, now, 0, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("expected 0 due reminders, got %d", len(due))
	}
}

func TestListDue_ActiveReminderDue(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	// Create an active reminder that's past due
	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeReminder,
			Title: "Test reminder",
			Date:  "2026-06-14",
			Time:  "09:00",
		},
	}
	_, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	due, err := ListDue(store, now, 0, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due reminder, got %d", len(due))
	}
	if due[0].Title != "Test reminder" {
		t.Errorf("expected title 'Test reminder', got %q", due[0].Title)
	}
	if due[0].IsStale != true {
		t.Error("expected isStale=true")
	}
}

func TestListDue_FutureReminderNotDue(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeReminder,
			Title: "Future reminder",
			Date:  "2026-06-16",
			Time:  "10:00",
		},
	}
	_, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	due, err := ListDue(store, now, 0, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("expected 0 due reminders, got %d", len(due))
	}
}

func TestListDue_CompletedReminderExcluded(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeReminder,
			Title: "Old reminder",
			Date:  "2026-06-14",
			Time:  "09:00",
		},
	}
	added, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}

	// Complete the record
	cf := models.GetCommonFields(added)
	if err := store.CompleteRecord(cf.ShortID); err != nil {
		t.Fatalf("complete: %v", err)
	}

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	due, err := ListDue(store, now, 0, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("expected 0 due reminders (completed excluded), got %d", len(due))
	}
}

func TestListDue_WindowIncludesFutureReminder(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeReminder,
			Title: "Soon reminder",
			Date:  "2026-06-15",
			Time:  "10:14",
		},
	}
	_, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	due, err := ListDue(store, now, 15*time.Minute, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due reminder within window, got %d", len(due))
	}
	if due[0].IsStale {
		t.Error("expected isStale=false for window-captured reminder")
	}
}

func TestListDue_StaleExcluded(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeReminder,
			Title: "Stale reminder",
			Date:  "2026-06-14",
			Time:  "08:00",
		},
	}
	_, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	due, err := ListDue(store, now, 0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("expected 0 due reminders (stale excluded), got %d", len(due))
	}
}
