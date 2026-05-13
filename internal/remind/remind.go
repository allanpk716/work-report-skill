// Package remind provides due-detection and push logic for reminder records.
// It implements IsDue, ListDue, PushDue, and PushSingle functions that the
// "wr remind" CLI subcommand group builds on.
//
// Structured logs use the [remind] prefix for scan results and push outcomes.
package remind

import (
	"context"
	"fmt"
	"time"

	"wr/internal/logger"
	"wr/internal/models"
	"wr/internal/pushover"
	"wr/internal/storage"
)

const (
	// maxPushCount is the upper bound on reminders processed per PushDue call.
	maxPushCount = 10

	// staleThreshold is how long past-due a reminder must be to be "stale".
	staleThreshold = 24 * time.Hour
)

// DueReminder is a lightweight view of a reminder that is currently due.
type DueReminder struct {
	ShortID             string             `json:"short_id"`
	Type                models.RecordType  `json:"type"`
	Title               string             `json:"title"`
	Date                string             `json:"date"`
	Time                string             `json:"time"`
	IsStale             bool               `json:"is_stale"`
	NotificationPriority string             `json:"notification_priority,omitempty"`
}

// PushedItem represents a successfully pushed reminder.
type PushedItem struct {
	ShortID string `json:"short_id"`
	Title   string `json:"title"`
}

// FailedItem represents a reminder that failed to push.
type FailedItem struct {
	ShortID string `json:"short_id"`
	Title   string `json:"title"`
	Error   string `json:"error"`
}

// PushResult is the outcome of a batch push operation.
type PushResult struct {
	Pushed []PushedItem `json:"pushed"`
	Failed []FailedItem `json:"failed"`
}

// IsDue checks whether a listed reminder record is currently due.
// Returns (isDue, isStale).
//
// Rules:
//   - Reminders without time: date < today → due, date == today → not due.
//   - Reminders with time:    parsed datetime <= now → due.
//   - Stale = due AND overdue > 24 hours.
//   - Window = due within window duration from now (future reminders within
//     the window are considered due too).
func IsDue(rec storage.ListedRecord, now time.Time, window time.Duration, includeStale bool) (isDue bool, isStale bool) {
	date, err := time.Parse("2006-01-02", rec.Date)
	if err != nil {
		return false, false
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	if rec.Time == "" {
		// No time: date < today is due; date == today is not due (no time to trigger).
		if date.Before(today) {
			overdue := today.Sub(date)
			stale := overdue > staleThreshold
			if stale && !includeStale {
				return false, true
			}
			return true, stale
		}
		// date == today or future → not due (no time component)
		return false, false
	}

	// Parse time and combine with date
	dueTime, err := time.Parse("15:04", rec.Time)
	if err != nil {
		return false, false
	}
	dueDateTime := time.Date(date.Year(), date.Month(), date.Day(),
		dueTime.Hour(), dueTime.Minute(), 0, 0, now.Location())

	// Check window: if dueDateTime is within (now, now+window], treat as due.
	if window > 0 && dueDateTime.After(now) && dueDateTime.Before(now.Add(window)) {
		return true, false
	}

	if dueDateTime.After(now) {
		return false, false
	}

	// dueDateTime <= now → due
	overdue := now.Sub(dueDateTime)
	stale := overdue > staleThreshold
	if stale && !includeStale {
		return false, true
	}

	return true, stale
}

// ListDue returns all currently due reminders, meetings, tasks, and personal records.
// It queries storage for active records of these types, filters by IsDue,
// merges results, and deduplicates by ShortID.
// Each DueReminder includes the NotificationPriority from the full record.
func ListDue(store *storage.Storage, now time.Time, window time.Duration, includeStale bool) ([]DueReminder, error) {
	var allRecs []storage.ListedRecord

	for _, rt := range []models.RecordType{models.TypeReminder, models.TypePersonal, models.TypeMeeting, models.TypeTask} {
		recs, err := store.ListRecords(storage.ListOptions{
			RecordType: rt,
			Status:     models.StatusActive,
		})
		if err != nil {
			return nil, fmt.Errorf("remind: list due: %w", err)
		}
		allRecs = append(allRecs, recs...)
	}

	// Deduplicate by ShortID (shouldn't happen in practice, but be safe).
	seen := make(map[string]bool)
	var recs []storage.ListedRecord
	for _, r := range allRecs {
		if seen[r.ShortID] {
			continue
		}
		seen[r.ShortID] = true
		recs = append(recs, r)
	}

	var due []DueReminder
	for _, rec := range recs {
		isDue, isStale := IsDue(rec, now, window, includeStale)
		if isDue {
			// Read full record to extract NotificationPriority
			np := ""
			if full, _, err := store.GetByID(rec.ShortID); err == nil {
				if cf := models.GetCommonFields(full); cf != nil {
					np = cf.NotificationPriority
				}
			}

			due = append(due, DueReminder{
				ShortID:             rec.ShortID,
				Type:                rec.Type,
				Title:               rec.Title,
				Date:                rec.Date,
				Time:                rec.Time,
				IsStale:             isStale,
				NotificationPriority: np,
			})
		}
	}

	logger.WithField("count", len(due)).Info("[remind] due reminders found")
	return due, nil
}

// PushDue atomically finds due reminders, pushes each via Pushover, and
// completes only the successfully pushed ones (up to maxPushCount).
//
// 3-phase pattern:
//  1. Scan under lock (ListRecords handles locking internally).
//  2. Process+send without lock (pushover.Send — network I/O).
//  3. Update under lock (CompleteRecord — each call acquires/releases).
func PushDue(ctx context.Context, store *storage.Storage, cfg pushover.Config, now time.Time, window time.Duration, includeStale bool) (*PushResult, error) {
	due, err := ListDue(store, now, window, includeStale)
	if err != nil {
		return nil, err
	}

	if len(due) == 0 {
		logger.Info("[remind] no due reminders to push")
		return &PushResult{}, nil
	}

	result := &PushResult{}
	limit := len(due)
	if limit > maxPushCount {
		limit = maxPushCount
	}

	for i := 0; i < limit; i++ {
		r := due[i]
		prefix := "⏰ 提醒"
		if r.Type == models.TypeMeeting {
			prefix = "📅 会议"
		} else if r.Type == models.TypeTask {
			prefix = "📋 任务"
		} else if r.Type == models.TypePersonal {
			prefix = "🏠 个人事务"
		}
		message := fmt.Sprintf("%s: %s (%s)", prefix, r.Title, r.Date)
		if r.Time != "" {
			message = fmt.Sprintf("%s: %s (%s %s)", prefix, r.Title, r.Date, r.Time)
		}

		priority := models.NotificationPriorityToPushover(r.NotificationPriority)
		logger.WithField("short_id", r.ShortID).WithField("type", r.Type).WithField("priority", priority).Info("[remind] resolved push priority")
		err := pushover.Send(ctx, cfg, message, "wr 提醒", priority)
		if err != nil {
			logger.WithField("short_id", r.ShortID).WithField("error", err.Error()).Warn("[remind] push failed")
			result.Failed = append(result.Failed, FailedItem{
				ShortID: r.ShortID,
				Title:   r.Title,
				Error:   err.Error(),
			})
			continue
		}

		// Complete on success
		if completeErr := store.CompleteRecord(r.ShortID); completeErr != nil {
			logger.WithField("short_id", r.ShortID).WithField("error", completeErr.Error()).Warn("[remind] complete after push failed")
			result.Failed = append(result.Failed, FailedItem{
				ShortID: r.ShortID,
				Title:   r.Title,
				Error:   fmt.Sprintf("push succeeded but complete failed: %v", completeErr),
			})
			continue
		}

		logger.WithField("short_id", r.ShortID).Info("[remind] pushed and completed")
		result.Pushed = append(result.Pushed, PushedItem{
			ShortID: r.ShortID,
			Title:   r.Title,
		})
	}

	logger.WithField("pushed", len(result.Pushed)).WithField("failed", len(result.Failed)).Info("[remind] push due complete")
	return result, nil
}

// PushSingle pushes a single reminder by short_id. It resolves the record,
// sends via Pushover, and completes on success.
func PushSingle(ctx context.Context, store *storage.Storage, cfg pushover.Config, shortID string) (*PushedItem, error) {
	rec, _, err := store.GetByID(shortID)
	if err != nil {
		return nil, fmt.Errorf("remind: push single: %w", err)
	}

	cf := models.GetCommonFields(rec)
	if cf == nil {
		return nil, fmt.Errorf("remind: push single: unknown record type for %s", shortID)
	}

	message := fmt.Sprintf("⏰ 提醒: %s (%s)", cf.Title, cf.Date)
	if cf.Time != "" {
		message = fmt.Sprintf("⏰ 提醒: %s (%s %s)", cf.Title, cf.Date, cf.Time)
	}

	priority := models.NotificationPriorityToPushover(cf.NotificationPriority)
	logger.WithField("short_id", shortID).WithField("priority", priority).Info("[remind] resolved push priority")
	if err := pushover.Send(ctx, cfg, message, "wr 提醒", priority); err != nil {
		logger.WithField("short_id", shortID).WithField("error", err.Error()).Warn("[remind] push single failed")
		return nil, err
	}

	if err := store.CompleteRecord(shortID); err != nil {
		logger.WithField("short_id", shortID).WithField("error", err.Error()).Warn("[remind] complete after push single failed")
		return nil, fmt.Errorf("push succeeded but complete failed: %w", err)
	}

	logger.WithField("short_id", shortID).Info("[remind] pushed single and completed")
	return &PushedItem{
		ShortID: shortID,
		Title:   cf.Title,
	}, nil
}
