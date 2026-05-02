// Package report generates structured daily work reports from storage records.
// Each report groups entries by type (meetings, tasks, reminders, logs),
// computes summary counts per type, and renders a Markdown string suitable
// for terminal display or Pushover notification.
package report

import (
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"time"

	"wr/internal/models"
	"wr/internal/storage"
)

// DailyReport holds the grouped result of a report query for a single date.
type DailyReport struct {
	Date      string        `json:"date"`
	Meetings []RecordEntry `json:"meetings"`
	Tasks    []RecordEntry `json:"tasks"`
	Reminders []RecordEntry `json:"reminders"`
	Logs     []RecordEntry `json:"logs"`
	Summary  Summary       `json:"summary"`
	Markdown string        `json:"markdown"`
}

// RecordEntry is a lightweight view of a single record within a report,
// carrying the fields needed for both JSON serialisation and Markdown rendering.
type RecordEntry struct {
	ShortID      string   `json:"short_id"`
	Type         string   `json:"type"`
	Title        string   `json:"title"`
	Date         string   `json:"date"`
	Time         string   `json:"time,omitempty"`
	EndTime      string   `json:"end_time,omitempty"`
	Location     string   `json:"location,omitempty"`
	Status       string   `json:"status,omitempty"`
	Priority     string   `json:"priority,omitempty"`
	Progress     string   `json:"progress,omitempty"`
	Participants []string `json:"participants,omitempty"`
}

// Summary holds aggregate counts per record type.
type Summary struct {
	Total     int `json:"total"`
	Meetings  int `json:"meetings"`
	Tasks     int `json:"tasks"`
	Reminders int `json:"reminders"`
	Logs      int `json:"logs"`
}

// Generate builds a DailyReport by querying storage for all record types on
// the given date (including completed records).
func Generate(store *storage.Storage, date string, logger *log.Logger) (*DailyReport, error) {
	if store == nil {
		return nil, fmt.Errorf("report: storage is nil")
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	meetings, err := listType(store, models.TypeMeeting, date)
	if err != nil {
		return nil, fmt.Errorf("report: list meetings: %w", err)
	}
	tasks, err := listType(store, models.TypeTask, date)
	if err != nil {
		return nil, fmt.Errorf("report: list tasks: %w", err)
	}
	reminders, err := listType(store, models.TypeReminder, date)
	if err != nil {
		return nil, fmt.Errorf("report: list reminders: %w", err)
	}
	logs, err := listType(store, models.TypeLog, date)
	if err != nil {
		return nil, fmt.Errorf("report: list logs: %w", err)
	}

	rpt := &DailyReport{
		Date:      date,
		Meetings:  meetings,
		Tasks:     tasks,
		Reminders: reminders,
		Logs:      logs,
	}

	rpt.Summary = Summary{
		Meetings:  len(meetings),
		Tasks:     len(tasks),
		Reminders: len(reminders),
		Logs:      len(logs),
	}
	rpt.Summary.Total = rpt.Summary.Meetings + rpt.Summary.Tasks +
		rpt.Summary.Reminders + rpt.Summary.Logs

	rpt.Markdown = renderMarkdown(rpt)

	logger.Printf("[report] date=%s meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		date, rpt.Summary.Meetings, rpt.Summary.Tasks,
		rpt.Summary.Reminders, rpt.Summary.Logs, rpt.Summary.Total)

	return rpt, nil
}

// GenerateToday generates a report for today's date using the provided timezone.
func GenerateToday(store *storage.Storage, loc *time.Location, logger *log.Logger) (*DailyReport, error) {
	today := time.Now().In(loc).Format("2006-01-02")
	return Generate(store, today, logger)
}

// listType queries storage for a single record type on the given date and
// converts the results to RecordEntry values.
func listType(store *storage.Storage, rt models.RecordType, date string) ([]RecordEntry, error) {
	opts := storage.ListOptions{
		RecordType:       rt,
		Date:             date,
		IncludeCompleted: true,
	}

	records, err := store.ListRecords(opts)
	if err != nil {
		return nil, err
	}

	entries := make([]RecordEntry, 0, len(records))
	for _, lr := range records {
		// Read full record to extract type-specific fields
		rec, _, err := store.GetByID(lr.ShortID)
		if err != nil {
			// Fallback to listed fields if full read fails
			entries = append(entries, RecordEntry{
				ShortID: lr.ShortID,
				Type:    string(lr.Type),
				Title:   lr.Title,
				Date:    lr.Date,
				Time:    lr.Time,
				Status:  lr.Status,
			})
			continue
		}

		entry := RecordEntry{
			ShortID: lr.ShortID,
			Type:    string(lr.Type),
			Title:   lr.Title,
			Date:    lr.Date,
			Time:    lr.Time,
			Status:  lr.Status,
		}

		cf := models.GetCommonFields(rec)
		if cf != nil {
			entry.EndTime = cf.EndTime
			entry.Location = cf.Location
		}

		// Extract type-specific fields
		switch v := rec.(type) {
		case *models.MeetingRecord:
			entry.Participants = v.Participants
		case *models.LogRecord:
			entry.Progress = v.Progress
			// LogRecord.Priority shadows CommonFields.Priority during JSON unmarshal
			if v.Priority != "" {
				entry.Priority = v.Priority
			}
		}

		// CommonFields.Priority may still be set for non-log types
		if entry.Priority == "" && cf != nil && cf.Priority != "" {
			entry.Priority = cf.Priority
		}

		entries = append(entries, entry)
	}

	// Sort by time ascending within each group (empty times last)
	sort.Slice(entries, func(i, j int) bool {
		ti, tj := entries[i].Time, entries[j].Time
		if ti == "" && tj == "" {
			return false
		}
		if ti == "" {
			return false
		}
		if tj == "" {
			return true
		}
		return ti < tj
	})

	return entries, nil
}

// renderMarkdown produces a Markdown string from a DailyReport.
func renderMarkdown(r *DailyReport) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# 工作日报 %s\n\n", r.Date)

	fmt.Fprintf(&b, "📊 **汇总**: 会议 %d | 任务 %d | 提醒 %d | 日志 %d | 共计 %d 条\n\n",
		r.Summary.Meetings, r.Summary.Tasks,
		r.Summary.Reminders, r.Summary.Logs, r.Summary.Total)

	renderSection(&b, "📅 会议", r.Meetings, func(e RecordEntry) string {
		detail := ""
		if e.Time != "" {
			detail += e.Time
			if e.EndTime != "" {
				detail += "-" + e.EndTime
			}
		}
		if e.Location != "" {
			if detail != "" {
				detail += " "
			}
			detail += "@" + e.Location
		}
		if len(e.Participants) > 0 {
			if detail != "" {
				detail += " "
			}
			detail += "(" + strings.Join(e.Participants, ", ") + ")"
		}
		if detail != "" {
			return fmt.Sprintf("- %s [%s]", e.Title, detail)
		}
		return "- " + e.Title
	})

	renderSection(&b, "✅ 任务", r.Tasks, func(e RecordEntry) string {
		status := "进行中"
		if e.Status == "completed" {
			status = "已完成"
		} else if e.Status == "cancelled" {
			status = "已取消"
		}
		return fmt.Sprintf("- %s [%s]", e.Title, status)
	})

	renderSection(&b, "🔔 提醒", r.Reminders, func(e RecordEntry) string {
		detail := ""
		if e.Time != "" {
			detail = e.Time
		}
		if e.Status == "completed" {
			if detail != "" {
				detail += " "
			}
			detail += "已处理"
		}
		if detail != "" {
			return fmt.Sprintf("- %s [%s]", e.Title, detail)
		}
		return "- " + e.Title
	})

	renderSection(&b, "📝 日志", r.Logs, func(e RecordEntry) string {
		detail := ""
		if e.Progress != "" {
			detail = e.Progress
		}
		if e.Priority != "" {
			if detail != "" {
				detail += " "
			}
			detail += "优先级:" + e.Priority
		}
		if detail != "" {
			return fmt.Sprintf("- %s [%s]", e.Title, detail)
		}
		return "- " + e.Title
	})

	return b.String()
}

// renderSection appends a titled section to the builder if there are entries.
func renderSection(b *strings.Builder, header string, entries []RecordEntry, fmtEntry func(RecordEntry) string) {
	if len(entries) == 0 {
		return
	}
	fmt.Fprintf(b, "## %s (%d)\n\n", header, len(entries))
	for _, e := range entries {
		fmt.Fprintln(b, fmtEntry(e))
	}
	fmt.Fprintln(b)
}
