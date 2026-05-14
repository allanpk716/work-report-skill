package remind

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wr/internal/models"
	"wr/internal/pushover"
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
	if isDue {
		t.Error("expected isDue=false for past date without time (no time = never due)")
	}
	if isStale {
		t.Error("expected isStale=false for past date without time (no time = never due)")
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
	// Exactly 24h ago → no time = never due
	rec := storage.ListedRecord{
		Date: "2026-06-14",
		Time: "",
	}

	isDue, isStale := IsDue(rec, now, 0, true)
	if isDue {
		t.Error("expected isDue=false for past date without time (no time = never due)")
	}
	if isStale {
		t.Error("expected isStale=false for past date without time (no time = never due)")
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

func TestListDue_NotificationPriority(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	// Create a reminder with high notification priority
	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:               models.TypeReminder,
			Title:              "High priority reminder",
			Date:               "2026-06-14",
			Time:               "09:00",
			NotificationPriority: "high",
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
	if due[0].NotificationPriority != "high" {
		t.Errorf("NotificationPriority = %q, want high", due[0].NotificationPriority)
	}
}

func TestListDue_NotificationPriority_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	// Create a reminder without notification priority (backward compat)
	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeReminder,
			Title: "Normal reminder",
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
	if due[0].NotificationPriority != "" {
		t.Errorf("NotificationPriority = %q, want empty (backward compat)", due[0].NotificationPriority)
	}
}

// --- ListDue personal record tests ---

func TestListDue_PersonalRecord(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.PersonalRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypePersonal,
			Title: "Pay electricity bill",
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
		t.Fatalf("expected 1 due personal record, got %d", len(due))
	}
	if due[0].Title != "Pay electricity bill" {
		t.Errorf("expected title 'Pay electricity bill', got %q", due[0].Title)
	}
	if due[0].Type != models.TypePersonal {
		t.Errorf("expected type %q, got %q", models.TypePersonal, due[0].Type)
	}
}

func TestListDue_PersonalAndReminder(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	personal := &models.PersonalRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypePersonal,
			Title: "Personal task",
			Date:  "2026-06-14",
			Time:  "09:00",
		},
	}
	_, err := store.AddRecord(personal)
	if err != nil {
		t.Fatalf("add personal: %v", err)
	}

	reminder := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeReminder,
			Title: "Team meeting",
			Date:  "2026-06-14",
			Time:  "10:00",
		},
	}
	_, err = store.AddRecord(reminder)
	if err != nil {
		t.Fatalf("add reminder: %v", err)
	}

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	due, err := ListDue(store, now, 0, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("expected 2 due records, got %d", len(due))
	}

	// Both types should be present
	types := map[models.RecordType]bool{}
	for _, d := range due {
		types[d.Type] = true
	}
	if !types[models.TypePersonal] {
		t.Error("expected personal record in results")
	}
	if !types[models.TypeReminder] {
		t.Error("expected reminder record in results")
	}
}

func TestListDue_FuturePersonalNotDue(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.PersonalRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypePersonal,
			Title: "Future personal",
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
		t.Errorf("expected 0 due records for future personal, got %d", len(due))
	}
}

func TestPushDue_PersonalRecord(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	pushover.SetPushoverURL(srv.URL)
	defer pushover.SetPushoverURL("https://api.pushover.net/1/messages.json")

	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.PersonalRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypePersonal,
			Title: "Grocery shopping",
			Date:  "2026-06-14",
			Time:  "09:00",
		},
	}
	_, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}

	ctx := context.Background()
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	cfg := pushover.Config{APIToken: "test-token", UserKey: "test-user"}

	result, err := PushDue(ctx, store, cfg, now, 0, true)
	if err != nil {
		t.Fatalf("PushDue: %v", err)
	}
	if len(result.Pushed) != 1 {
		t.Fatalf("expected 1 pushed, got %d", len(result.Pushed))
	}
	if result.Pushed[0].Title != "Grocery shopping" {
		t.Errorf("expected title 'Grocery shopping', got %q", result.Pushed[0].Title)
	}

	if !strings.Contains(capturedBody, "%F0%9F%8F%A0") && !strings.Contains(capturedBody, "\U0001f3e0") {
		t.Errorf("expected 🏠 emoji in push message body, got: %s", capturedBody)
	}

	// Verify the record was completed (no longer active)
	due, err := ListDue(store, now, 0, true)
	if err != nil {
		t.Fatalf("ListDue after push: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("expected 0 due after push+complete, got %d", len(due))
	}
}

// --- PushDue/PushSingle priority mapping tests (mock HTTP server) ---

func TestPushDue_HighPriority(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	pushover.SetPushoverURL(srv.URL)
	defer pushover.SetPushoverURL("https://api.pushover.net/1/messages.json")

	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:               models.TypeReminder,
			Title:              "Urgent meeting",
			Date:               "2026-06-14",
			Time:               "09:00",
			NotificationPriority: "high",
		},
	}
	_, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}

	ctx := context.Background()
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	cfg := pushover.Config{APIToken: "test-token", UserKey: "test-user"}

	result, err := PushDue(ctx, store, cfg, now, 0, true)
	if err != nil {
		t.Fatalf("PushDue: %v", err)
	}
	if len(result.Pushed) != 1 {
		t.Fatalf("expected 1 pushed, got %d", len(result.Pushed))
	}

	if !strings.Contains(capturedBody, "priority=1") {
		t.Errorf("expected priority=1 in POST body, got: %s", capturedBody)
	}
}

func TestPushDue_NormalPriority(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	pushover.SetPushoverURL(srv.URL)
	defer pushover.SetPushoverURL("https://api.pushover.net/1/messages.json")

	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	// No notification_priority set — should default to priority=0
	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeReminder,
			Title: "Normal task",
			Date:  "2026-06-14",
			Time:  "09:00",
		},
	}
	_, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}

	ctx := context.Background()
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	cfg := pushover.Config{APIToken: "test-token", UserKey: "test-user"}

	result, err := PushDue(ctx, store, cfg, now, 0, true)
	if err != nil {
		t.Fatalf("PushDue: %v", err)
	}
	if len(result.Pushed) != 1 {
		t.Fatalf("expected 1 pushed, got %d", len(result.Pushed))
	}

	if !strings.Contains(capturedBody, "priority=0") {
		t.Errorf("expected priority=0 in POST body, got: %s", capturedBody)
	}
}

func TestPushSingle_HighPriority(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	pushover.SetPushoverURL(srv.URL)
	defer pushover.SetPushoverURL("https://api.pushover.net/1/messages.json")

	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:               models.TypeReminder,
			Title:              "Single high reminder",
			Date:               "2026-06-14",
			Time:               "09:00",
			NotificationPriority: "high",
		},
	}
	added, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}
	shortID := models.GetCommonFields(added).ShortID

	ctx := context.Background()
	cfg := pushover.Config{APIToken: "test-token", UserKey: "test-user"}

	pushed, err := PushSingle(ctx, store, cfg, shortID)
	if err != nil {
		t.Fatalf("PushSingle: %v", err)
	}
	if pushed.ShortID != shortID {
		t.Errorf("expected short_id %q, got %q", shortID, pushed.ShortID)
	}

	if !strings.Contains(capturedBody, "priority=1") {
		t.Errorf("expected priority=1 in POST body, got: %s", capturedBody)
	}
}

// --- ListDue meeting record tests ---

func TestListDue_MeetingRecord(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeMeeting,
			Title: "Team standup",
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
		t.Fatalf("expected 1 due meeting record, got %d", len(due))
	}
	if due[0].Title != "Team standup" {
		t.Errorf("expected title 'Team standup', got %q", due[0].Title)
	}
	if due[0].Type != models.TypeMeeting {
		t.Errorf("expected type %q, got %q", models.TypeMeeting, due[0].Type)
	}
}

func TestListDue_TaskRecord(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeTask,
			Title: "Submit report",
			Date:  "2026-06-14",
			Time:  "17:00",
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
		t.Fatalf("expected 1 due task record, got %d", len(due))
	}
	if due[0].Title != "Submit report" {
		t.Errorf("expected title 'Submit report', got %q", due[0].Title)
	}
	if due[0].Type != models.TypeTask {
		t.Errorf("expected type %q, got %q", models.TypeTask, due[0].Type)
	}
}

func TestListDue_AllFourTypes(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	for _, r := range []interface{}{
		&models.ReminderRecord{
			CommonFields: models.CommonFields{
				Type:  models.TypeReminder,
				Title: "Reminder item",
				Date:  "2026-06-14",
				Time:  "09:00",
			},
		},
		&models.MeetingRecord{
			CommonFields: models.CommonFields{
				Type:  models.TypeMeeting,
				Title: "Meeting item",
				Date:  "2026-06-14",
				Time:  "10:00",
			},
		},
		&models.TaskRecord{
			CommonFields: models.CommonFields{
				Type:  models.TypeTask,
				Title: "Task item",
				Date:  "2026-06-14",
				Time:  "11:00",
			},
		},
		&models.PersonalRecord{
			CommonFields: models.CommonFields{
				Type:  models.TypePersonal,
				Title: "Personal item",
				Date:  "2026-06-14",
				Time:  "12:00",
			},
		},
	} {
		if _, err := store.AddRecord(r); err != nil {
			t.Fatalf("add record: %v", err)
		}
	}

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	due, err := ListDue(store, now, 0, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(due) != 4 {
		t.Fatalf("expected 4 due records (one per type), got %d", len(due))
	}

	types := map[models.RecordType]bool{}
	for _, d := range due {
		types[d.Type] = true
	}
	for _, expected := range []models.RecordType{models.TypeReminder, models.TypeMeeting, models.TypeTask, models.TypePersonal} {
		if !types[expected] {
			t.Errorf("expected %q in results", expected)
		}
	}
}

func TestListDue_FutureMeetingNotDue(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeMeeting,
			Title: "Future meeting",
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
		t.Errorf("expected 0 due records for future meeting, got %d", len(due))
	}
}

func TestListDue_FutureTaskNotDue(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeTask,
			Title: "Future task",
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
		t.Errorf("expected 0 due records for future task, got %d", len(due))
	}
}

func TestPushDue_MeetingRecord(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	pushover.SetPushoverURL(srv.URL)
	defer pushover.SetPushoverURL("https://api.pushover.net/1/messages.json")

	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeMeeting,
			Title: "Sprint planning",
			Date:  "2026-06-14",
			Time:  "09:00",
		},
	}
	_, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}

	ctx := context.Background()
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	cfg := pushover.Config{APIToken: "test-token", UserKey: "test-user"}

	result, err := PushDue(ctx, store, cfg, now, 0, true)
	if err != nil {
		t.Fatalf("PushDue: %v", err)
	}
	if len(result.Pushed) != 1 {
		t.Fatalf("expected 1 pushed, got %d", len(result.Pushed))
	}
	if result.Pushed[0].Title != "Sprint planning" {
		t.Errorf("expected title 'Sprint planning', got %q", result.Pushed[0].Title)
	}

	// Check that the meeting prefix emoji (📅) appears in the push message
	if !strings.Contains(capturedBody, "%F0%9F%93%85") && !strings.Contains(capturedBody, "\U0001f4c5") {
		t.Errorf("expected 📅 emoji (meeting prefix) in push message body, got: %s", capturedBody)
	}

	// Verify the record was completed
	due, err := ListDue(store, now, 0, true)
	if err != nil {
		t.Fatalf("ListDue after push: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("expected 0 due after push+complete, got %d", len(due))
	}
}

func TestPushDue_TaskRecord(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	pushover.SetPushoverURL(srv.URL)
	defer pushover.SetPushoverURL("https://api.pushover.net/1/messages.json")

	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.TaskRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeTask,
			Title: "Code review",
			Date:  "2026-06-14",
			Time:  "15:00",
		},
	}
	_, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}

	ctx := context.Background()
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	cfg := pushover.Config{APIToken: "test-token", UserKey: "test-user"}

	result, err := PushDue(ctx, store, cfg, now, 0, true)
	if err != nil {
		t.Fatalf("PushDue: %v", err)
	}
	if len(result.Pushed) != 1 {
		t.Fatalf("expected 1 pushed, got %d", len(result.Pushed))
	}
	if result.Pushed[0].Title != "Code review" {
		t.Errorf("expected title 'Code review', got %q", result.Pushed[0].Title)
	}

	// Check that the task prefix emoji (📋) appears in the push message
	if !strings.Contains(capturedBody, "%F0%9F%93%8B") && !strings.Contains(capturedBody, "\U0001f4cb") {
		t.Errorf("expected 📋 emoji (task prefix) in push message body, got: %s", capturedBody)
	}

	// Verify the record was completed
	due, err := ListDue(store, now, 0, true)
	if err != nil {
		t.Fatalf("ListDue after push: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("expected 0 due after push+complete, got %d", len(due))
	}
}

// --- No-time records: never due ---

func TestIsDue_NoTime_AlwaysNotDue(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	tests := []struct {
		name string
		date string
	}{
		{"past_1_day", "2026-06-14"},
		{"past_25h", "2026-06-14"},
		{"past_2_days", "2026-06-13"},
		{"today", "2026-06-15"},
		{"future", "2026-06-16"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := storage.ListedRecord{ShortID: "x", Date: tt.date, Time: ""}
			isDue, isStale := IsDue(rec, now, 0, true)
			if isDue {
				t.Errorf("NoTime_AlwaysNotDue/%s: expected isDue=false", tt.name)
			}
			if isStale {
				t.Errorf("NoTime_AlwaysNotDue/%s: expected isStale=false", tt.name)
			}
		})
	}
}

func TestListDue_NoTimeRecordExcluded(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeReminder,
			Title: "No time reminder",
			Date:  "2026-06-13", // 2 days ago
			Time:  "",
		},
	}
	_, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	due, err := ListDue(store, now, 0, true)
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("expected 0 due for no-time record, got %d", len(due))
	}
}

func TestPushDue_NoTimeRecordNotPushed(t *testing.T) {
	httpCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalled = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	pushover.SetPushoverURL(srv.URL)
	defer pushover.SetPushoverURL("https://api.pushover.net/1/messages.json")

	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeReminder,
			Title: "Past no-time",
			Date:  "2026-06-13",
			Time:  "",
		},
	}
	_, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}

	ctx := context.Background()
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	cfg := pushover.Config{APIToken: "test-token", UserKey: "test-user"}

	result, err := PushDue(ctx, store, cfg, now, 0, true)
	if err != nil {
		t.Fatalf("PushDue: %v", err)
	}
	if len(result.Pushed) != 0 {
		t.Errorf("expected 0 pushed for no-time record, got %d", len(result.Pushed))
	}
	if httpCalled {
		t.Error("expected no Pushover HTTP call for no-time record")
	}
}

func TestPushDue_PrePushStatusCheck_SkipsCompleted(t *testing.T) {
	httpCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalled = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	pushover.SetPushoverURL(srv.URL)
	defer pushover.SetPushoverURL("https://api.pushover.net/1/messages.json")

	tmpDir := t.TempDir()
	store := storage.New(tmpDir)

	// Create a due record WITH time so it passes ListDue scan
	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:  models.TypeReminder,
			Title: "Will be completed externally",
			Date:  "2026-06-14",
			Time:  "09:00",
		},
	}
	added, err := store.AddRecord(rec)
	if err != nil {
		t.Fatalf("add record: %v", err)
	}
	shortID := models.GetCommonFields(added).ShortID

	// Complete the record externally (simulating scan-to-push race)
	if err := store.CompleteRecord(shortID); err != nil {
		t.Fatalf("complete: %v", err)
	}

	ctx := context.Background()
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.Local)
	cfg := pushover.Config{APIToken: "test-token", UserKey: "test-user"}

	// Since the record is completed before ListDue runs, ListDue returns 0 items
	// and PushDue returns empty result without HTTP call. The pre-push safety net
	// provides defense-in-depth for the case where completion happens between
	// ListDue scan and actual push.
	result, err := PushDue(ctx, store, cfg, now, 0, true)
	if err != nil {
		t.Fatalf("PushDue: %v", err)
	}
	if len(result.Pushed) != 0 {
		t.Errorf("expected 0 pushed for externally-completed record, got %d", len(result.Pushed))
	}
	if httpCalled {
		t.Error("expected no Pushover HTTP call for externally-completed record")
	}
}
