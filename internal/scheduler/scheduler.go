// Package scheduler manages cron-based scheduling and pushover notifications.
//
// The Scheduler wraps robfig/cron/v3 and provides Register/Unregister methods
// that compute trigger times from record fields (date, time, remind_before,
// recurring). On trigger, it calls PushoverSender.Send and updates the state
// file.
//
// Observability: Every register/trigger/error/skip action logs with prefix
// [scheduler] and includes short_id and type for greppability.
package scheduler

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"wr/internal/config"
	"wr/internal/logger"
	"wr/internal/models"
)

// PushoverSender is the interface for sending pushover notifications.
// The real pushover.Client implements Send(); tests inject a mock.
type PushoverSender interface {
	Send(ctx context.Context, cfg PushoverConfig, message, title string, priority int) error
}

// PushoverConfig mirrors the pushover.Config struct to avoid an import cycle
// (pushover imports only stdlib). Callers should convert with AsPushoverConfig.
type PushoverConfig struct {
	APIToken string
	UserKey  string
}

// AsPushoverConfig converts a config.PushoverConfig to the scheduler-local
// PushoverConfig type used by the PushoverSender interface.
func AsPushoverConfig(c config.PushoverConfig) PushoverConfig {
	return PushoverConfig{APIToken: c.APIToken, UserKey: c.UserKey}
}

// Scheduler wraps a cron instance and manages scheduled pushover notifications.
type Scheduler struct {
	cron     *cron.Cron
	state    *SchedulerState
	stateMgr *stateManager // internal wrapper for state path management
	pushover PushoverSender
	cfg      *config.Config
	mu       sync.Mutex
}

// stateManager wraps a state path for the scheduler to use.
type stateManager struct {
	path string
}

// SchedulerOption allows functional-style configuration of NewScheduler.
type SchedulerOption func(*Scheduler)

// NewScheduler creates a new scheduler. The scheduler is not started; call
// Start() to begin processing cron entries.
func NewScheduler(cfg *config.Config, pushoverClient PushoverSender, statePath string) *Scheduler {
	return &Scheduler{
		cron:     cron.New(cron.WithSeconds(), cron.WithLocation(cfg.Location())),
		state:    NewState(),
		stateMgr: &stateManager{path: statePath},
		pushover: pushoverClient,
		cfg:      cfg,
	}
}

// Start begins the cron scheduler. Returns an error if the cron cannot start.
func (s *Scheduler) Start() error {
	// Load existing state if available
	loaded, err := LoadState(s.stateMgr.path)
	if err != nil {
		logger.Warnf("state load: %v (starting fresh)", err)
	} else {
		s.state = loaded
	}
	s.cron.Start()
	logger.Info("scheduler started")
	return nil
}

// Stop gracefully stops the cron scheduler.
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
	logger.Info("scheduler stopped")
}

// State returns the current scheduler state (for inspection).
func (s *Scheduler) State() *SchedulerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Register parses a record and, if it qualifies, registers a cron entry to
// trigger a Pushover notification at the computed time.
//
// Qualification rules:
//   - Must have date and time fields
//   - Status must be active or empty
//   - Must be type "reminder" OR have a non-empty remind_before
//
// Trigger time = parse(date+time) - parseDuration(remind_before).
// If the trigger time is in the past, the entry is skipped.
// For recurring records (daily/weekly/monthly), a standard cron expression is used.
// For one-time records, the entry is removed after firing.
func (s *Scheduler) Register(rec interface{}) error {
	cf := models.GetCommonFields(rec)
	if cf == nil {
		return fmt.Errorf("scheduler: cannot extract common fields from record")
	}

	// --- Skip conditions ---
	if cf.Date == "" || cf.Time == "" {
		logger.WithField("short_id", cf.ShortID).WithField("type", string(cf.Type)).Debug("skip: no date/time")
		return nil
	}
	if cf.Status == models.StatusCompleted || cf.Status == models.StatusCancelled {
		logger.WithField("short_id", cf.ShortID).WithField("type", string(cf.Type)).WithField("status", cf.Status).Debug("skip: completed/cancelled")
		return nil
	}

	// Must be reminder type OR have remind_before
	if cf.Type != models.TypeReminder && cf.RemindBefore == "" {
		logger.WithField("short_id", cf.ShortID).WithField("type", string(cf.Type)).Debug("skip: non-reminder without remind_before")
		return nil
	}

	// Extract recurring from ReminderRecord if present
	recurring := extractRecurring(rec)

	// Compute trigger time
	loc := s.cfg.Location()
	triggerAt, err := computeTriggerTime(cf.Date, cf.Time, cf.RemindBefore, loc)
	if err != nil {
		return fmt.Errorf("scheduler: compute trigger time for %s: %w", cf.ShortID, err)
	}

	// Skip if trigger time is in the past
	now := time.Now().In(loc)
	if triggerAt.Before(now) && recurring == "" {
		logger.WithField("short_id", cf.ShortID).WithField("type", string(cf.Type)).WithField("trigger_at", triggerAt.Format(time.RFC3339)).Debug("skip: past entry")
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Build cron spec and register
	var entryID cron.EntryID
	if recurring != "" {
		spec, err := recurringCronSpec(triggerAt, recurring, loc)
		if err != nil {
			return fmt.Errorf("scheduler: build cron spec for %s: %w", cf.ShortID, err)
		}
		entryID, err = s.cron.AddFunc(spec, s.makeTriggerCallback(cf.ShortID, string(cf.Type), cf.Title, cf.Date, cf.Time, recurring))
		if err != nil {
			return fmt.Errorf("scheduler: register recurring cron for %s: %w", cf.ShortID, err)
		}
	} else {
		// One-time: use a custom schedule that fires at an exact time
		sched := cron.Schedule(oneTimeSchedule{triggerAt: triggerAt})
		entryID = s.cron.Schedule(sched, cron.FuncJob(s.makeTriggerCallback(cf.ShortID, string(cf.Type), cf.Title, cf.Date, cf.Time, "")))
	}

	// Update state
	entry := &ScheduleEntry{
		RecordShortID: cf.ShortID,
		RecordType:    string(cf.Type),
		Title:         cf.Title,
		TriggerAt:     triggerAt.UTC().Format(time.RFC3339),
		CronEntryID:   int(entryID),
		Recurring:     recurring,
	}
	s.state.AddEntry(entry)
	if err := SaveState(s.stateMgr.path, s.state); err != nil {
		logger.Warnf("error saving state after register: %v", err)
	}

	logger.WithField("short_id", cf.ShortID).WithField("type", string(cf.Type)).WithField("trigger_at", triggerAt.Format(time.RFC3339)).WithField("recurring", recurring).WithField("cron_entry", entryID).Info("register")

	return nil
}

// Unregister removes a scheduled entry by its short ID.
func (s *Scheduler) Unregister(shortID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.state.GetEntry(shortID)
	if entry == nil {
		return fmt.Errorf("scheduler: entry not found: %s", shortID)
	}

	s.cron.Remove(cron.EntryID(entry.CronEntryID))
	s.state.RemoveEntry(shortID)
	if err := SaveState(s.stateMgr.path, s.state); err != nil {
		logger.Warnf("error saving state after unregister: %v", err)
	}

	logger.WithField("short_id", shortID).Info("unregister")
	return nil
}

// makeTriggerCallback returns a function that fires the pushover notification
// and updates state. For non-recurring entries, it auto-unregisters after firing.
func (s *Scheduler) makeTriggerCallback(shortID, recordType, title, date, timeStr, recurring string) func() {
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		logger.WithField("short_id", shortID).WithField("type", recordType).Info("trigger")

		// Build message
		body := fmt.Sprintf("%s %s - %s", date, timeStr, title)
		pushTitle := fmt.Sprintf("[%s] %s", recordType, title)

		cfg := AsPushoverConfig(s.cfg.Pushover)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err := s.pushover.Send(ctx, cfg, body, pushTitle, 0)
		if err != nil {
			s.state.MarkError(shortID, err.Error())
			logger.WithField("short_id", shortID).WithField("type", recordType).Errorf("trigger error: %v", err)
		} else {
			s.state.MarkFired(shortID, time.Now())
		}

		if err := SaveState(s.stateMgr.path, s.state); err != nil {
			logger.Warnf("error saving state after trigger: %v", err)
		}

		// Auto-unregister one-time entries after firing
		if recurring == "" {
			entry := s.state.GetEntry(shortID)
			if entry != nil {
				s.cron.Remove(cron.EntryID(entry.CronEntryID))
				// Don't remove from state — we want to keep fired/error status
			}
		}
	}
}

// ParseRemindBefore parses a remind_before duration string.
// Supported formats: "15m", "30m", "1h", "2h".
// Empty string returns 0. Invalid format returns 0 with a log warning.
func ParseRemindBefore(s string) time.Duration {
	if s == "" {
		return 0
	}
	s = strings.TrimSpace(strings.ToLower(s))

	// Try hours: "1h", "2h"
	if strings.HasSuffix(s, "h") {
		numStr := strings.TrimSuffix(s, "h")
		n, err := strconv.Atoi(numStr)
		if err == nil && n > 0 {
			return time.Duration(n) * time.Hour
		}
	}

	// Try minutes: "15m", "30m"
	if strings.HasSuffix(s, "m") {
		numStr := strings.TrimSuffix(s, "m")
		n, err := strconv.Atoi(numStr)
		if err == nil && n > 0 {
			return time.Duration(n) * time.Minute
		}
	}

	// Fallback: try standard Go duration
	d, err := time.ParseDuration(s)
	if err == nil {
		return d
	}

	return 0
}

// computeTriggerTime parses date+time into a time.Time in the given location,
// then subtracts the remind_before duration.
func computeTriggerTime(date, timeStr, remindBefore string, loc *time.Location) (time.Time, error) {
	// Parse date+time. Date is "YYYY-MM-DD", time is "HH:MM".
	dateTimeStr := fmt.Sprintf("%s %s", date, timeStr)
	t, err := time.ParseInLocation("2006-01-02 15:04", dateTimeStr, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse date/time %q: %w", dateTimeStr, err)
	}

	// Subtract remind_before
	if remindBefore != "" {
		d := ParseRemindBefore(remindBefore)
		t = t.Add(-d)
	}

	return t, nil
}

// recurringCronSpec builds a cron expression for recurring events.
// daily:   "SS MM HH DD * *"
// weekly:  "SS MM HH * DOW *"
// monthly: "SS MM HH * * DOM" (robfig uses standard 5-field or 6-field)
//
// Actually robfig/cron/v3 with WithSeconds expects 6-field: "SS MM HH DD MM DOW"
func recurringCronSpec(triggerAt time.Time, recurring string, loc *time.Location) (string, error) {
	second := triggerAt.In(loc).Second()
	minute := triggerAt.In(loc).Minute()
	hour := triggerAt.In(loc).Hour()
	day := triggerAt.In(loc).Day()
	weekday := int(triggerAt.In(loc).Weekday()) // 0=Sunday

	switch strings.ToLower(recurring) {
	case "daily":
		// Every day at the same time
		return fmt.Sprintf("%d %d %d * * *", second, minute, hour), nil
	case "weekly":
		// Every week on the same weekday
		return fmt.Sprintf("%d %d %d * * %d", second, minute, hour, weekday), nil
	case "monthly":
		// Every month on the same day
		return fmt.Sprintf("%d %d %d %d * *", second, minute, hour, day), nil
	default:
		return "", fmt.Errorf("unsupported recurring value: %q", recurring)
	}
}

// oneTimeSchedule implements cron.Schedule for a single fire-at time.
type oneTimeSchedule struct {
	triggerAt time.Time
}

// Next returns the next activation time. If t is before triggerAt, returns
// triggerAt. Otherwise returns a zero time (no future activations).
func (s oneTimeSchedule) Next(t time.Time) time.Time {
	if t.Before(s.triggerAt) {
		return s.triggerAt
	}
	return time.Time{} // zero time = no more activations
}

// extractRecurring extracts the recurring field from a record, if present.
func extractRecurring(rec interface{}) string {
	switch v := rec.(type) {
	case *models.ReminderRecord:
		return v.Recurring
	default:
		return ""
	}
}
