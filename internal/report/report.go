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

// RangeReport holds the merged result of report queries across a date range.
type RangeReport struct {
	DateFrom       string        `json:"date_from"`
	DateTo         string        `json:"date_to"`
	DaysCount      int           `json:"days_count"`
	Days           []DailyReport `json:"days"`
	MergedMeetings []RecordEntry `json:"merged_meetings"`
	MergedTasks    []RecordEntry `json:"merged_tasks"`
	MergedReminders []RecordEntry `json:"merged_reminders"`
	MergedLogs     []RecordEntry `json:"merged_logs"`
	Summary        Summary       `json:"summary"`
	Markdown       string        `json:"markdown"`
}

// GenerateRange builds a RangeReport by generating a DailyReport for each date
// in the inclusive range [from, to] and merging the results.
func GenerateRange(store *storage.Storage, from, to string, loc *time.Location, logger *log.Logger) (*RangeReport, error) {
	if store == nil {
		return nil, fmt.Errorf("report: storage is nil")
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	if loc == nil {
		loc = time.UTC
	}

	fromTime, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil, fmt.Errorf("report: invalid from date %q: %w", from, err)
	}
	toTime, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil, fmt.Errorf("report: invalid to date %q: %w", to, err)
	}
	if fromTime.After(toTime) {
		return nil, fmt.Errorf("report: from date %s is after to date %s", from, to)
	}

	rr := &RangeReport{
		DateFrom: from,
		DateTo:   to,
	}

	// Iterate each date in [from, to].
	for d := fromTime; !d.After(toTime); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		dr, err := Generate(store, dateStr, logger)
		if err != nil {
			return nil, fmt.Errorf("report: generate %s: %w", dateStr, err)
		}
		rr.Days = append(rr.Days, *dr)
		rr.MergedMeetings = append(rr.MergedMeetings, dr.Meetings...)
		rr.MergedTasks = append(rr.MergedTasks, dr.Tasks...)
		rr.MergedReminders = append(rr.MergedReminders, dr.Reminders...)
		rr.MergedLogs = append(rr.MergedLogs, dr.Logs...)
		rr.Summary.Meetings += dr.Summary.Meetings
		rr.Summary.Tasks += dr.Summary.Tasks
		rr.Summary.Reminders += dr.Summary.Reminders
		rr.Summary.Logs += dr.Summary.Logs
	}

	rr.DaysCount = len(rr.Days)
	rr.Summary.Total = rr.Summary.Meetings + rr.Summary.Tasks +
		rr.Summary.Reminders + rr.Summary.Logs

	rr.Markdown = renderRangeMarkdown(rr)

	logger.Printf("[report] range: from=%s to=%s days=%d meetings=%d tasks=%d reminders=%d logs=%d total=%d",
		from, to, rr.DaysCount, rr.Summary.Meetings, rr.Summary.Tasks,
		rr.Summary.Reminders, rr.Summary.Logs, rr.Summary.Total)

	return rr, nil
}

// GenerateWeek builds a RangeReport for the current week (Monday–Sunday)
// using the provided timezone.
func GenerateWeek(store *storage.Storage, loc *time.Location, logger *log.Logger) (*RangeReport, error) {
	if loc == nil {
		loc = time.UTC
	}
	today := time.Now().In(loc)

	// weekday: Sunday=0, Monday=1, ..., Saturday=6
	// Walk back to Monday.
	weekday := int(today.Weekday())
	if weekday == 0 {
		weekday = 7 // treat Sunday as 7 for offset math
	}
	monday := today.AddDate(0, 0, -(weekday - 1))
	sunday := monday.AddDate(0, 0, 6)

	from := monday.Format("2006-01-02")
	to := sunday.Format("2006-01-02")

	return GenerateRange(store, from, to, loc, logger)
}

// renderRangeMarkdown produces a combined Markdown string for a RangeReport.
// Each type section groups entries by date with a date prefix.
func renderRangeMarkdown(r *RangeReport) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# 工作周报 %s ~ %s\n\n", r.DateFrom, r.DateTo)

	fmt.Fprintf(&b, "📊 **汇总** (%d天): 会议 %d | 任务 %d | 提醒 %d | 日志 %d | 共计 %d 条\n\n",
		r.DaysCount, r.Summary.Meetings, r.Summary.Tasks,
		r.Summary.Reminders, r.Summary.Logs, r.Summary.Total)

	renderRangeSection(&b, "📅 会议", r.Days, func(dr DailyReport) []RecordEntry {
		return dr.Meetings
	}, func(e RecordEntry) string {
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
			return fmt.Sprintf("- [%s] %s [%s]", e.Date, e.Title, detail)
		}
		return fmt.Sprintf("- [%s] %s", e.Date, e.Title)
	})

	renderRangeSection(&b, "✅ 任务", r.Days, func(dr DailyReport) []RecordEntry {
		return dr.Tasks
	}, func(e RecordEntry) string {
		status := "进行中"
		if e.Status == "completed" {
			status = "已完成"
		} else if e.Status == "cancelled" {
			status = "已取消"
		}
		return fmt.Sprintf("- [%s] %s [%s]", e.Date, e.Title, status)
	})

	renderRangeSection(&b, "🔔 提醒", r.Days, func(dr DailyReport) []RecordEntry {
		return dr.Reminders
	}, func(e RecordEntry) string {
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
			return fmt.Sprintf("- [%s] %s [%s]", e.Date, e.Title, detail)
		}
		return fmt.Sprintf("- [%s] %s", e.Date, e.Title)
	})

	renderRangeSection(&b, "📝 日志", r.Days, func(dr DailyReport) []RecordEntry {
		return dr.Logs
	}, func(e RecordEntry) string {
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
			return fmt.Sprintf("- [%s] %s [%s]", e.Date, e.Title, detail)
		}
		return fmt.Sprintf("- [%s] %s", e.Date, e.Title)
	})

	return b.String()
}

// renderRangeSection appends a per-type section to the builder, iterating over
// days and rendering each entry with a date prefix. Omits the section entirely
// if no day has entries of this type.
func renderRangeSection(b *strings.Builder, header string, days []DailyReport, extract func(DailyReport) []RecordEntry, fmtEntry func(RecordEntry) string) {
	total := 0
	for _, dr := range days {
		total += len(extract(dr))
	}
	if total == 0 {
		return
	}
	fmt.Fprintf(b, "## %s (%d)\n\n", header, total)
	for _, dr := range days {
		for _, e := range extract(dr) {
			fmt.Fprintln(b, fmtEntry(e))
		}
	}
	fmt.Fprintln(b)
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
