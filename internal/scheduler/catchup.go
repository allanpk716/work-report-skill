package scheduler

import (
	"context"
	"fmt"
	"time"

	"wr/internal/models"
)

// CatchUpResult holds statistics about the catch-up process.
type CatchUpResult struct {
	Scanned         int // total records scanned
	Skipped         int // skipped (not due, already fired, etc.)
	Fired           int // successfully sent pushover
	Errors          int // pushover send failed
	RecurringSkipped int // recurring entries skipped (re-registered normally)
}

// CatchUp scans reminder records and fires Pushover notifications for any
// that are overdue (trigger_at in the past) and not yet marked as fired in
// the scheduler state. Overdue notifications include a 【延迟提醒】 prefix
// in the title.
//
// This is called once at daemon startup to recover missed reminders after
// a daemon restart or crash.
func (s *Scheduler) CatchUp(records []interface{}) (*CatchUpResult, error) {
	result := &CatchUpResult{}
	loc := s.cfg.Location()
	now := time.Now().In(loc)

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, rec := range records {
		cf := models.GetCommonFields(rec)
		if cf == nil {
			result.Skipped++
			continue
		}
		result.Scanned++

		// Skip non-active records
		if cf.Status == models.StatusCompleted || cf.Status == models.StatusCancelled {
			result.Skipped++
			continue
		}

		// Need date and time
		if cf.Date == "" || cf.Time == "" {
			result.Skipped++
			continue
		}

		// Must be reminder type OR have remind_before
		if cf.Type != models.TypeReminder && cf.RemindBefore == "" {
			result.Skipped++
			continue
		}

		// Check if already fired in state
		existing := s.state.GetEntry(cf.ShortID)
		if existing != nil && existing.Fired {
			result.Skipped++
			continue
		}

		// Compute trigger time
		triggerAt, err := computeTriggerTime(cf.Date, cf.Time, cf.RemindBefore, loc)
		if err != nil {
			s.logger.Printf("[scheduler] catchup: compute trigger for %s: %v", cf.ShortID, err)
			result.Skipped++
			continue
		}

		// Only fire if trigger time is in the past
		if !triggerAt.Before(now) {
			result.Skipped++
			continue
		}

		// Skip recurring entries — they will be re-registered normally
		recurring := extractRecurring(rec)
		if recurring != "" {
			result.RecurringSkipped++
			continue
		}

		// Fire the overdue notification
		body := fmt.Sprintf("%s %s - %s", cf.Date, cf.Time, cf.Title)
		pushTitle := fmt.Sprintf("【延迟提醒】%s - %s", cf.Type, cf.Title)

		cfg := AsPushoverConfig(s.cfg.Pushover)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		err = s.pushover.Send(ctx, cfg, body, pushTitle, 0)
		cancel()

		if err != nil {
			s.logger.Printf("[scheduler] catchup: send failed short_id=%s err=%v", cf.ShortID, err)
			if existing == nil {
				s.state.AddEntry(&ScheduleEntry{
					RecordShortID: cf.ShortID,
					RecordType:    string(cf.Type),
					Title:         cf.Title,
					TriggerAt:     triggerAt.UTC().Format(time.RFC3339),
				})
			}
			s.state.MarkError(cf.ShortID, err.Error())
			result.Errors++
		} else {
			s.logger.Printf("[scheduler] catchup: fired short_id=%s type=%s title=%s", cf.ShortID, cf.Type, cf.Title)
			if existing == nil {
				s.state.AddEntry(&ScheduleEntry{
					RecordShortID: cf.ShortID,
					RecordType:    string(cf.Type),
					Title:         cf.Title,
					TriggerAt:     triggerAt.UTC().Format(time.RFC3339),
				})
			}
			s.state.MarkFired(cf.ShortID, time.Now())
			result.Fired++
		}
	}

	// Persist state after all catch-up processing
	if result.Fired > 0 || result.Errors > 0 {
		if err := SaveState(s.stateMgr.path, s.state); err != nil {
			s.logger.Printf("[scheduler] catchup: error saving state: %v", err)
		}
	}

	s.logger.Printf("[scheduler] catchup complete: scanned=%d skipped=%d fired=%d errors=%d recurring_skipped=%d",
		result.Scanned, result.Skipped, result.Fired, result.Errors, result.RecurringSkipped)

	return result, nil
}
