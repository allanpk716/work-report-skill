package scheduler

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wr/internal/config"
	"wr/internal/models"
)

// mockPushover captures sent messages for assertion.
type catchupMockPushover struct {
	messages []catchupMessage
}

type catchupMessage struct {
	message string
	title   string
}

func (m *catchupMockPushover) Send(_ context.Context, _ PushoverConfig, message, title string, priority int) error {
	m.messages = append(m.messages, catchupMessage{message: message, title: title})
	return nil
}

// failingPushover always returns an error.
type failingPushover struct{}

func (f *failingPushover) Send(_ context.Context, _ PushoverConfig, _, _ string, _ int) error {
	return os.ErrDeadlineExceeded
}

func TestCatchUp_OverdueReminder(t *testing.T) {
	// Setup: a reminder with date/time in the past, not yet fired
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "scheduler-state.json")

	cfg := &config.Config{Timezone: "Asia/Shanghai"}
	mock := &catchupMockPushover{}
	sched := NewScheduler(cfg, mock, statePath, log.New(os.Stderr, "[test] ", log.LstdFlags))

	// Create an overdue reminder: yesterday at 10:00
	yesterday := time.Now().In(cfg.Location()).AddDate(0, 0, -1).Format("2006-01-02")
	records := []interface{}{
		&models.ReminderRecord{
			CommonFields: models.CommonFields{
				Type:    models.TypeReminder,
				Title:   "Test overdue reminder",
				Date:    yesterday,
				Time:    "10:00",
				Status:  models.StatusActive,
				ShortID: "abc12345",
			},
		},
	}

	result, err := sched.CatchUp(records)
	if err != nil {
		t.Fatalf("CatchUp returned error: %v", err)
	}

	if result.Scanned != 1 {
		t.Errorf("Scanned = %d, want 1", result.Scanned)
	}
	if result.Fired != 1 {
		t.Errorf("Fired = %d, want 1", result.Fired)
	}
	if result.Errors != 0 {
		t.Errorf("Errors = %d, want 0", result.Errors)
	}

	// Verify pushover was called with delayed prefix
	if len(mock.messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(mock.messages))
	}
	msg := mock.messages[0]
	if msg.title == "" {
		t.Fatal("Title is empty")
	}
	delayedPrefix := "【延迟提醒】"
	if !contains(msg.title, delayedPrefix) {
		t.Errorf("Title %q does not contain %q", msg.title, delayedPrefix)
	}

	// Verify state was persisted with fired=true
	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	entry := state.GetEntry("abc12345")
	if entry == nil {
		t.Fatal("Entry not found in state")
	}
	if !entry.Fired {
		t.Error("Entry should be marked as fired")
	}
}

func TestCatchUp_AlreadyFired(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "scheduler-state.json")

	cfg := &config.Config{Timezone: "Asia/Shanghai"}

	mock := &catchupMockPushover{}
	sched := NewScheduler(cfg, mock, statePath, log.New(os.Stderr, "[test] ", log.LstdFlags))

	// Pre-populate state with fired entry
	yesterday := time.Now().In(cfg.Location()).AddDate(0, 0, -1).Format("2006-01-02")
	sched.state.AddEntry(&ScheduleEntry{
		RecordShortID: "abc12345",
		RecordType:    "reminder",
		Title:         "Already fired",
		TriggerAt:     time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339),
		Fired:         true,
		FiredAt:       time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339),
	})

	records := []interface{}{
		&models.ReminderRecord{
			CommonFields: models.CommonFields{
				Type:    models.TypeReminder,
				Title:   "Already fired",
				Date:    yesterday,
				Time:    "10:00",
				Status:  models.StatusActive,
				ShortID: "abc12345",
			},
		},
	}

	result, err := sched.CatchUp(records)
	if err != nil {
		t.Fatalf("CatchUp error: %v", err)
	}

	if result.Fired != 0 {
		t.Errorf("Fired = %d, want 0 (already fired)", result.Fired)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", result.Skipped)
	}
	if len(mock.messages) != 0 {
		t.Errorf("Expected 0 messages, got %d", len(mock.messages))
	}
}

func TestCatchUp_FutureReminder(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "scheduler-state.json")

	cfg := &config.Config{Timezone: "Asia/Shanghai"}

	mock := &catchupMockPushover{}
	sched := NewScheduler(cfg, mock, statePath, log.New(os.Stderr, "[test] ", log.LstdFlags))

	// Future reminder: tomorrow
	tomorrow := time.Now().In(cfg.Location()).AddDate(0, 0, 1).Format("2006-01-02")
	records := []interface{}{
		&models.ReminderRecord{
			CommonFields: models.CommonFields{
				Type:    models.TypeReminder,
				Title:   "Future reminder",
				Date:    tomorrow,
				Time:    "10:00",
				Status:  models.StatusActive,
				ShortID: "futur123",
			},
		},
	}

	result, err := sched.CatchUp(records)
	if err != nil {
		t.Fatalf("CatchUp error: %v", err)
	}

	if result.Fired != 0 {
		t.Errorf("Fired = %d, want 0 (future)", result.Fired)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", result.Skipped)
	}
}

func TestCatchUp_PushoverFailure(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "scheduler-state.json")

	cfg := &config.Config{Timezone: "Asia/Shanghai"}

	mock := &failingPushover{}
	sched := NewScheduler(cfg, mock, statePath, log.New(os.Stderr, "[test] ", log.LstdFlags))

	yesterday := time.Now().In(cfg.Location()).AddDate(0, 0, -1).Format("2006-01-02")
	records := []interface{}{
		&models.ReminderRecord{
			CommonFields: models.CommonFields{
				Type:    models.TypeReminder,
				Title:   "Failing reminder",
				Date:    yesterday,
				Time:    "10:00",
				Status:  models.StatusActive,
				ShortID: "fail1234",
			},
		},
	}

	result, err := sched.CatchUp(records)
	if err != nil {
		t.Fatalf("CatchUp error: %v", err)
	}

	if result.Fired != 0 {
		t.Errorf("Fired = %d, want 0 (pushover failed)", result.Fired)
	}
	if result.Errors != 1 {
		t.Errorf("Errors = %d, want 1", result.Errors)
	}

	// State should have error recorded
	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	entry := state.GetEntry("fail1234")
	if entry == nil {
		t.Fatal("Entry not found in state")
	}
	if entry.LastError == "" {
		t.Error("Expected last_error to be set")
	}
	if entry.Fired {
		t.Error("Entry should NOT be marked as fired (send failed)")
	}
}

func TestCatchUp_SkipsCompleted(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "scheduler-state.json")

	cfg := &config.Config{Timezone: "Asia/Shanghai"}

	mock := &catchupMockPushover{}
	sched := NewScheduler(cfg, mock, statePath, log.New(os.Stderr, "[test] ", log.LstdFlags))

	yesterday := time.Now().In(cfg.Location()).AddDate(0, 0, -1).Format("2006-01-02")
	records := []interface{}{
		&models.ReminderRecord{
			CommonFields: models.CommonFields{
				Type:    models.TypeReminder,
				Title:   "Completed reminder",
				Date:    yesterday,
				Time:    "10:00",
				Status:  models.StatusCompleted,
				ShortID: "comp12345",
			},
		},
		&models.ReminderRecord{
			CommonFields: models.CommonFields{
				Type:    models.TypeReminder,
				Title:   "Cancelled reminder",
				Date:    yesterday,
				Time:    "10:00",
				Status:  models.StatusCancelled,
				ShortID: "canc12345",
			},
		},
	}

	result, err := sched.CatchUp(records)
	if err != nil {
		t.Fatalf("CatchUp error: %v", err)
	}

	if result.Fired != 0 {
		t.Errorf("Fired = %d, want 0", result.Fired)
	}
	if result.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2", result.Scanned)
	}
	if result.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", result.Skipped)
	}
}

func TestCatchUp_SkipsNonReminderWithoutRemindBefore(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "scheduler-state.json")

	cfg := &config.Config{Timezone: "Asia/Shanghai"}

	mock := &catchupMockPushover{}
	sched := NewScheduler(cfg, mock, statePath, log.New(os.Stderr, "[test] ", log.LstdFlags))

	yesterday := time.Now().In(cfg.Location()).AddDate(0, 0, -1).Format("2006-01-02")
	records := []interface{}{
		&models.TaskRecord{
			CommonFields: models.CommonFields{
				Type:    models.TypeTask,
				Title:   "Old task",
				Date:    yesterday,
				Time:    "10:00",
				Status:  models.StatusActive,
				ShortID: "task12345",
			},
		},
	}

	result, err := sched.CatchUp(records)
	if err != nil {
		t.Fatalf("CatchUp error: %v", err)
	}

	if result.Scanned != 1 {
		t.Errorf("Scanned = %d, want 1", result.Scanned)
	}
	if result.Fired != 0 {
		t.Errorf("Fired = %d, want 0 (task without remind_before)", result.Fired)
	}
}

func TestCatchUp_TaskWithRemindBefore(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "scheduler-state.json")

	cfg := &config.Config{Timezone: "Asia/Shanghai"}

	mock := &catchupMockPushover{}
	sched := NewScheduler(cfg, mock, statePath, log.New(os.Stderr, "[test] ", log.LstdFlags))

	yesterday := time.Now().In(cfg.Location()).AddDate(0, 0, -1).Format("2006-01-02")
	records := []interface{}{
		&models.TaskRecord{
			CommonFields: models.CommonFields{
				Type:         models.TypeTask,
				Title:        "Task with reminder",
				Date:         yesterday,
				Time:         "10:00",
				RemindBefore: "30m",
				Status:       models.StatusActive,
				ShortID:      "taskrem1",
			},
		},
	}

	result, err := sched.CatchUp(records)
	if err != nil {
		t.Fatalf("CatchUp error: %v", err)
	}

	if result.Fired != 1 {
		t.Errorf("Fired = %d, want 1 (task with remind_before)", result.Fired)
	}
}

func TestCatchUp_EmptyRecords(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "scheduler-state.json")

	cfg := &config.Config{Timezone: "Asia/Shanghai"}
	mock := &catchupMockPushover{}
	sched := NewScheduler(cfg, mock, statePath, log.New(os.Stderr, "[test] ", log.LstdFlags))

	result, err := sched.CatchUp(nil)
	if err != nil {
		t.Fatalf("CatchUp error: %v", err)
	}
	if result.Scanned != 0 {
		t.Errorf("Scanned = %d, want 0", result.Scanned)
	}
}

// contains checks if substr is in s.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
