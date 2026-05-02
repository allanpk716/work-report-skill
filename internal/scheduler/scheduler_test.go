package scheduler

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"wr/internal/config"
	"wr/internal/models"
)

// mockPushover records calls to Send for verification.
type mockPushover struct {
	mu       sync.Mutex
	calls    []call
	fail     bool // if true, always return error
	failCount int  // fail first N calls, then succeed
}

type call struct {
	message  string
	title    string
	priority int
}

func (m *mockPushover) Send(_ context.Context, _ PushoverConfig, message, title string, priority int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, call{message: message, title: title, priority: priority})
	if m.fail {
		return fmt.Errorf("pushover: mock failure")
	}
	if m.failCount > 0 {
		m.failCount--
		return fmt.Errorf("pushover: mock failure (count)")
	}
	return nil
}

func (m *mockPushover) getCalls() []call {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]call, len(m.calls))
	copy(out, m.calls)
	return out
}

// testScheduler creates a scheduler with a mock pushover and temp state path.
func testScheduler(t *testing.T) (*Scheduler, *mockPushover, string) {
	t.Helper()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "scheduler-state.json")
	mock := &mockPushover{}
	cfg := &config.Config{
		Timezone: "Asia/Shanghai",
		Pushover: config.PushoverConfig{APIToken: "test-token", UserKey: "test-user"},
	}
	s := NewScheduler(cfg, mock, statePath, log.New(os.Stderr, "[test-scheduler] ", log.LstdFlags))
	return s, mock, statePath
}

// --- ParseRemindBefore ---

func TestParseRemindBefore(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"15m", 15 * time.Minute},
		{"30m", 30 * time.Minute},
		{"1h", 1 * time.Hour},
		{"2h", 2 * time.Hour},
		{"", 0},
		{"invalid", 0},
		{"0m", 0},       // 0 is not > 0
		{"5s", 5 * time.Second}, // fallback to time.ParseDuration
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseRemindBefore(tt.input)
			if got != tt.want {
				t.Errorf("ParseRemindBefore(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- computeTriggerTime ---

func TestComputeTriggerTime(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")

	tests := []struct {
		name        string
		date        string
		timeStr     string
		remindBefore string
		wantHour    int
		wantMin     int
	}{
		{
			name:        "no remind_before",
			date:        "2026-05-10",
			timeStr:     "14:30",
			remindBefore: "",
			wantHour:    14,
			wantMin:     30,
		},
		{
			name:        "with 15m remind_before",
			date:        "2026-05-10",
			timeStr:     "14:30",
			remindBefore: "15m",
			wantHour:    14,
			wantMin:     15,
		},
		{
			name:        "with 1h remind_before",
			date:        "2026-05-10",
			timeStr:     "14:30",
			remindBefore: "1h",
			wantHour:    13,
			wantMin:     30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := computeTriggerTime(tt.date, tt.timeStr, tt.remindBefore, loc)
			if err != nil {
				t.Fatalf("computeTriggerTime error: %v", err)
			}
			gotInLoc := got.In(loc)
			if gotInLoc.Hour() != tt.wantHour || gotInLoc.Minute() != tt.wantMin {
				t.Errorf("got %02d:%02d, want %02d:%02d", gotInLoc.Hour(), gotInLoc.Minute(), tt.wantHour, tt.wantMin)
			}
		})
	}
}

func TestComputeTriggerTime_InvalidInput(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	_, err := computeTriggerTime("not-a-date", "14:30", "", loc)
	if err == nil {
		t.Error("expected error for invalid date")
	}
}

// --- Register ---

func TestRegister_Reminder(t *testing.T) {
	s, mock, statePath := testScheduler(t)
	s.Start()
	defer s.Stop()

	// Register a reminder 2 minutes from now
	loc := s.cfg.Location()
	future := time.Now().In(loc).Add(2 * time.Minute)
	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:    models.TypeReminder,
			Title:   "Test reminder",
			Date:    future.Format("2006-01-02"),
			Time:    future.Format("15:04"),
			ShortID: "testreg01",
			Status:  models.StatusActive,
		},
	}

	err := s.Register(rec)
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	// Verify state entry was created
	entry := s.State().GetEntry("testreg01")
	if entry == nil {
		t.Fatal("state entry not created")
	}
	if entry.RecordShortID != "testreg01" {
		t.Errorf("RecordShortID = %q", entry.RecordShortID)
	}
	if entry.RecordType != "reminder" {
		t.Errorf("RecordType = %q", entry.RecordType)
	}
	if entry.Fired {
		t.Error("should not be fired yet")
	}

	// Verify state file exists on disk
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Error("state file not written to disk")
	}

	_ = mock // not triggered yet
}

func TestRegister_WithRemindBefore(t *testing.T) {
	s, _, _ := testScheduler(t)
	s.Start()
	defer s.Stop()

	loc := s.cfg.Location()
	future := time.Now().In(loc).Add(1 * time.Hour)

	rec := &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:         models.TypeMeeting,
			Title:        "Team standup",
			Date:         future.Format("2006-01-02"),
			Time:         future.Format("15:04"),
			ShortID:      "meet001",
			Status:       models.StatusActive,
			RemindBefore: "15m",
		},
		Participants: []string{"Alice"},
	}

	err := s.Register(rec)
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	entry := s.State().GetEntry("meet001")
	if entry == nil {
		t.Fatal("state entry not created for meeting with remind_before")
	}

	// The trigger_at should be 15 minutes before the meeting time
	triggerAt, _ := time.Parse(time.RFC3339, entry.TriggerAt)
	meetingTime, _ := time.ParseInLocation("2006-01-02 15:04", future.Format("2006-01-02")+" "+future.Format("15:04"), loc)
	expectedTrigger := meetingTime.Add(-15 * time.Minute)

	// Allow 1 second tolerance
	diff := triggerAt.Sub(expectedTrigger)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("trigger_at = %v, expected ~%v (diff=%v)", triggerAt, expectedTrigger, diff)
	}
}

func TestRegister_SkipPast(t *testing.T) {
	s, _, _ := testScheduler(t)
	s.Start()
	defer s.Stop()

	// Create a record in the past
	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:    models.TypeReminder,
			Title:   "Past reminder",
			Date:    "2020-01-01",
			Time:    "10:00",
			ShortID: "past001",
			Status:  models.StatusActive,
		},
	}

	err := s.Register(rec)
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	// Should not be registered
	entry := s.State().GetEntry("past001")
	if entry != nil {
		t.Error("past entry should not be registered")
	}
}

func TestRegister_SkipNoDateTime(t *testing.T) {
	s, _, _ := testScheduler(t)
	s.Start()
	defer s.Stop()

	// No date
	rec1 := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:    models.TypeReminder,
			Title:   "No date",
			Time:    "10:00",
			ShortID: "nodate01",
			Status:  models.StatusActive,
		},
	}
	if err := s.Register(rec1); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if s.State().GetEntry("nodate01") != nil {
		t.Error("entry without date should not be registered")
	}

	// No time
	rec2 := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:    models.TypeReminder,
			Title:   "No time",
			Date:    "2026-05-10",
			ShortID: "notime01",
			Status:  models.StatusActive,
		},
	}
	if err := s.Register(rec2); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if s.State().GetEntry("notime01") != nil {
		t.Error("entry without time should not be registered")
	}
}

func TestRegister_SkipCompleted(t *testing.T) {
	s, _, _ := testScheduler(t)
	s.Start()
	defer s.Stop()

	loc := s.cfg.Location()
	future := time.Now().In(loc).Add(1 * time.Hour)

	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:    models.TypeReminder,
			Title:   "Completed reminder",
			Date:    future.Format("2006-01-02"),
			Time:    future.Format("15:04"),
			ShortID: "comp001",
			Status:  models.StatusCompleted,
		},
	}

	if err := s.Register(rec); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if s.State().GetEntry("comp001") != nil {
		t.Error("completed entry should not be registered")
	}
}

func TestRegister_NonReminderWithoutRemindBefore(t *testing.T) {
	s, _, _ := testScheduler(t)
	s.Start()
	defer s.Stop()

	loc := s.cfg.Location()
	future := time.Now().In(loc).Add(1 * time.Hour)

	rec := &models.MeetingRecord{
		CommonFields: models.CommonFields{
			Type:    models.TypeMeeting,
			Title:   "Meeting without reminder",
			Date:    future.Format("2006-01-02"),
			Time:    future.Format("15:04"),
			ShortID: "noremind01",
			Status:  models.StatusActive,
			// RemindBefore is empty, type is not reminder
		},
	}

	if err := s.Register(rec); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if s.State().GetEntry("noremind01") != nil {
		t.Error("non-reminder without remind_before should not be registered")
	}
}

// --- Unregister ---

func TestUnregister(t *testing.T) {
	s, _, _ := testScheduler(t)
	s.Start()
	defer s.Stop()

	loc := s.cfg.Location()
	future := time.Now().In(loc).Add(1 * time.Hour)

	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:    models.TypeReminder,
			Title:   "To unregister",
			Date:    future.Format("2006-01-02"),
			Time:    future.Format("15:04"),
			ShortID: "unreg01",
			Status:  models.StatusActive,
		},
	}

	if err := s.Register(rec); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if s.State().GetEntry("unreg01") == nil {
		t.Fatal("entry should exist after register")
	}

	if err := s.Unregister("unreg01"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if s.State().GetEntry("unreg01") != nil {
		t.Error("entry should be removed after unregister")
	}
}

func TestUnregister_NotFound(t *testing.T) {
	s, _, _ := testScheduler(t)
	err := s.Unregister("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent entry")
	}
}

// --- Trigger ---

func TestTrigger_Success(t *testing.T) {
	s, mock, statePath := testScheduler(t)
	s.Start()
	defer s.Stop()

	loc := s.cfg.Location()
	// We need the trigger to happen within a few seconds, but computeTriggerTime
	// only parses HH:MM (seconds=00). So we target the start of the next minute
	// which is at most 60 seconds away.
	now := time.Now().In(loc)
	nextMinute := now.Truncate(time.Minute).Add(time.Minute)
	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:    models.TypeReminder,
			Title:   "Trigger test",
			Date:    nextMinute.Format("2006-01-02"),
			Time:    nextMinute.Format("15:04"),
			ShortID: "trig001",
			Status:  models.StatusActive,
		},
	}

	if err := s.Register(rec); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Wait up to 90 seconds (worst case: 59s until next minute + 30s buffer)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		entry := s.State().GetEntry("trig001")
		if entry != nil && entry.Fired {
			break
		}
	}

	entry := s.State().GetEntry("trig001")
	if entry == nil {
		t.Fatal("entry not found")
	}
	if !entry.Fired {
		t.Error("entry should be fired after trigger time")
	}

	calls := mock.getCalls()
	if len(calls) == 0 {
		t.Fatal("pushover Send was not called")
	}
	if calls[0].title != "[reminder] Trigger test" {
		t.Errorf("title = %q, want %q", calls[0].title, "[reminder] Trigger test")
	}

	// Verify state persisted
	loaded, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	loadedEntry := loaded.GetEntry("trig001")
	if loadedEntry == nil || !loadedEntry.Fired {
		t.Error("state file should show entry as fired")
	}
}

func TestTrigger_PushoverFail(t *testing.T) {
	s, mock, _ := testScheduler(t)
	mock.fail = true // all calls fail
	s.Start()
	defer s.Stop()

	loc := s.cfg.Location()
	now := time.Now().In(loc)
	nextMinute := now.Truncate(time.Minute).Add(time.Minute)
	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:    models.TypeReminder,
			Title:   "Fail test",
			Date:    nextMinute.Format("2006-01-02"),
			Time:    nextMinute.Format("15:04"),
			ShortID: "fail001",
			Status:  models.StatusActive,
		},
	}

	if err := s.Register(rec); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Wait for trigger
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		entry := s.State().GetEntry("fail001")
		if entry != nil && entry.LastError != "" {
			break
		}
	}

	entry := s.State().GetEntry("fail001")
	if entry == nil {
		t.Fatal("entry not found")
	}
	if entry.Fired {
		t.Error("entry should NOT be marked fired when pushover fails")
	}
	if entry.LastError == "" {
		t.Error("entry should have last_error set on pushover failure")
	}
}

func TestTrigger_Recurring(t *testing.T) {
	s, mock, _ := testScheduler(t)
	s.Start()
	defer s.Stop()

	loc := s.cfg.Location()
	now := time.Now().In(loc)
	nextMinute := now.Truncate(time.Minute).Add(time.Minute)
	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:    models.TypeReminder,
			Title:   "Daily standup",
			Date:    nextMinute.Format("2006-01-02"),
			Time:    nextMinute.Format("15:04"),
			ShortID: "recur001",
			Status:  models.StatusActive,
		},
		Recurring: "daily",
	}

	if err := s.Register(rec); err != nil {
		t.Fatalf("Register: %v", err)
	}

	entry := s.State().GetEntry("recur001")
	if entry == nil {
		t.Fatal("entry not found")
	}
	if entry.Recurring != "daily" {
		t.Errorf("Recurring = %q, want %q", entry.Recurring, "daily")
	}

	// Wait for first trigger
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		entry := s.State().GetEntry("recur001")
		if entry != nil && entry.Fired {
			break
		}
	}

	entry = s.State().GetEntry("recur001")
	if entry == nil || !entry.Fired {
		t.Error("recurring entry should be fired after trigger time")
	}

	// The entry should still exist in state (not auto-unregistered for recurring)
	entry2 := s.State().GetEntry("recur001")
	if entry2 == nil {
		t.Error("recurring entry should remain in state after firing")
	}

	calls := mock.getCalls()
	if len(calls) == 0 {
		t.Fatal("pushover Send not called for recurring entry")
	}
}

// --- Recurring cron expressions ---

func TestRecurringCronSpec(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	tm := time.Date(2026, 5, 10, 14, 30, 15, 0, loc) // 14:30:15 on a Sunday

	tests := []struct {
		recurring string
		want      string
	}{
		{"daily", "15 30 14 * * *"},
		{"weekly", "15 30 14 * * 0"}, // Sunday = 0
		{"monthly", "15 30 14 10 * *"},
	}

	for _, tt := range tests {
		t.Run(tt.recurring, func(t *testing.T) {
			got, err := recurringCronSpec(tm, tt.recurring, loc)
			if err != nil {
				t.Fatalf("recurringCronSpec error: %v", err)
			}
			if got != tt.want {
				t.Errorf("recurringCronSpec(%q) = %q, want %q", tt.recurring, got, tt.want)
			}
		})
	}
}

func TestRecurringCronSpec_Unsupported(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	tm := time.Date(2026, 5, 10, 14, 30, 0, 0, loc)
	_, err := recurringCronSpec(tm, "yearly", loc)
	if err == nil {
		t.Error("expected error for unsupported recurring value")
	}
}

// --- oneTimeSchedule ---

func TestOneTimeSchedule_Next(t *testing.T) {
	triggerAt := time.Date(2026, 5, 10, 14, 30, 0, 0, time.UTC)
	sched := oneTimeSchedule{triggerAt: triggerAt}

	// Before trigger: returns triggerAt
	got := sched.Next(triggerAt.Add(-1 * time.Hour))
	if !got.Equal(triggerAt) {
		t.Errorf("Next(before) = %v, want %v", got, triggerAt)
	}

	// At trigger: returns zero (no more)
	got = sched.Next(triggerAt)
	if !got.IsZero() {
		t.Errorf("Next(at) = %v, want zero", got)
	}

	// After trigger: returns zero
	got = sched.Next(triggerAt.Add(1 * time.Hour))
	if !got.IsZero() {
		t.Errorf("Next(after) = %v, want zero", got)
	}
}

// --- Nil record ---

func TestRegister_NilRecord(t *testing.T) {
	s, _, _ := testScheduler(t)
	err := s.Register(nil)
	if err == nil {
		t.Error("expected error for nil record")
	}
}

// --- Empty status should be accepted ---

func TestRegister_EmptyStatus(t *testing.T) {
	s, _, _ := testScheduler(t)
	s.Start()
	defer s.Stop()

	loc := s.cfg.Location()
	future := time.Now().In(loc).Add(1 * time.Hour)
	rec := &models.ReminderRecord{
		CommonFields: models.CommonFields{
			Type:    models.TypeReminder,
			Title:   "Empty status",
			Date:    future.Format("2006-01-02"),
			Time:    future.Format("15:04"),
			ShortID: "empty01",
			Status:  "", // empty status should be accepted
		},
	}

	if err := s.Register(rec); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if s.State().GetEntry("empty01") == nil {
		t.Error("entry with empty status should be registered")
	}
}

// --- extractRecurring ---

func TestExtractRecurring(t *testing.T) {
	// ReminderRecord with recurring
	rr := &models.ReminderRecord{
		CommonFields: models.CommonFields{Type: models.TypeReminder},
		Recurring:    "daily",
	}
	if got := extractRecurring(rr); got != "daily" {
		t.Errorf("extractRecurring(ReminderRecord) = %q, want %q", got, "daily")
	}

	// MeetingRecord has no recurring field
	mr := &models.MeetingRecord{
		CommonFields: models.CommonFields{Type: models.TypeMeeting},
	}
	if got := extractRecurring(mr); got != "" {
		t.Errorf("extractRecurring(MeetingRecord) = %q, want empty", got)
	}
}
