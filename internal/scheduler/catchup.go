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

	// Phase 1: Scan records under lock to determine which need firing.
	type pendingFire struct {
		rec       interface{}
		existing  *ScheduleEntry
		triggerAt time.Time
		body      string
		pushTitle string
	}
	var pending []pendingFire

	s.mu.Lock()
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

		body := fmt.Sprintf("%s %s - %s", cf.Date, cf.Time, cf.Title)
		pushTitle := fmt.Sprintf("【延迟提醒】%s - %s", cf.Type, cf.Title)
		pending = append(pending, pendingFire{rec: rec, existing: existing, triggerAt: triggerAt, body: body, pushTitle: pushTitle})
	}
	s.mu.Unlock()

	// Phase 2: Send pushover notifications WITHOUT holding the mutex.
	// This prevents blocking State() / Register() calls during the
	// potentially slow pushover retries (up to 35 s with bad creds).
	type fireOutcome struct {
		idx  int
		err  error
		cf   *models.CommonFields
	}
	var outcomes []fireOutcome
	for i, p := range pending {
		cf := models.GetCommonFields(p.rec)
		cfg := AsPushoverConfig(s.cfg.Pushover)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := s.pushover.Send(ctx, cfg, p.body, p.pushTitle, 0)
		cancel()
		outcomes = append(outcomes, fireOutcome{idx: i, err: err, cf: cf})
	}

	// Phase 3: Update state under lock with results.
	s.mu.Lock()
	for _, o := range outcomes {
		p := pending[o.idx]
		if o.err != nil {
			s.logger.Printf("[scheduler] catchup: send failed short_id=%s err=%v", o.cf.ShortID, o.err)
			if p.existing == nil {
				s.state.AddEntry(&ScheduleEntry{
					RecordShortID: o.cf.ShortID,
					RecordType:    string(o.cf.Type),
					Title:         o.cf.Title,
					TriggerAt:     p.triggerAt.UTC().Format(time.RFC3339),
				})
			}
			s.state.MarkError(o.cf.ShortID, o.err.Error())
			result.Errors++
		} else {
			s.logger.Printf("[scheduler] catchup: fired short_id=%s type=%s title=%s", o.cf.ShortID, o.cf.Type, o.cf.Title)
			if p.existing == nil {
				s.state.AddEntry(&ScheduleEntry{
					RecordShortID: o.cf.ShortID,
					RecordType:    string(o.cf.Type),
					Title:         o.cf.Title,
					TriggerAt:     p.triggerAt.UTC().Format(time.RFC3339),
				})
			}
			s.state.MarkFired(o.cf.ShortID, time.Now())
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
	s.mu.Unlock()

	return result, nil
}
