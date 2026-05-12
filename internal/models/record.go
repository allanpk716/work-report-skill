// Package models defines data structures for work records (meetings, tasks,
// reminders, done_things) that map to the existing nanobot JSON record formats in the
// work-records/ directory hierarchy.
//
// Each record type includes a ShortID field — a 16-character hex string derived
// from the filename timestamp (e.g. "20260430_103211" → "2a0f2b1c4e7d9a01") — used for
// concise CLI references.
package models

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// RecordType enumerates the four supported work-record categories.
type RecordType string

const (
	TypeMeeting  RecordType = "meeting"
	TypeTask     RecordType = "task"
	TypeReminder RecordType = "reminder"
	TypeDoneThings RecordType = "done_things"
	TypePersonal  RecordType = "personal"
)

// ValidRecordTypes returns the set of valid record type strings.
func ValidRecordTypes() []string {
	return []string{string(TypeMeeting), string(TypeTask), string(TypeReminder), string(TypeDoneThings), string(TypePersonal)}
}

// IsActionableType returns true for record types that support lifecycle state
// transitions (complete/cancel). done_things are factual records and do not
// support these actions.
func IsActionableType(rt RecordType) bool {
	switch rt {
	case TypeMeeting, TypeTask, TypeReminder, TypePersonal:
		return true
	default:
		return false
	}
}

// IsValidType checks whether s is a valid record type.
func IsValidType(s string) bool {
	switch RecordType(s) {
	case TypeMeeting, TypeTask, TypeReminder, TypeDoneThings, TypePersonal:
		return true
	}
	return false
}

// DirName returns the filesystem directory name for this record type.
// For example, TypeDoneThings returns "done_things" (not "done_things" + "s").
func (rt RecordType) DirName() string {
	switch rt {
	case TypeDoneThings:
		return "done_things"
	default:
		return string(rt) + "s" // meetings, tasks, reminders
	}
}

// Status constants for tasks and reminders.
const (
	StatusPending   = "pending"
	StatusActive    = "active"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
)

// Record is the common envelope for all work-record types. The Type field
// determines which specialized struct holds the type-specific fields.
// We use a flat approach: each record type has its own struct that embeds
// common fields, and all are serialised directly from/to JSON.
type Record struct {
	ShortID string `json:"short_id,omitempty"`
}

// CommonFields holds fields shared across all record types.
type CommonFields struct {
	Type               RecordType `json:"type"`
	Title              string     `json:"title"`
	Description        string     `json:"description,omitempty"`
	Date               string     `json:"date"`                          // YYYY-MM-DD
	Time               string     `json:"time,omitempty"`                // HH:MM
	EndTime            string     `json:"end_time,omitempty"`
	Location           string     `json:"location,omitempty"`
	RelatedPerson      string     `json:"related_person,omitempty"`
	RemindBefore       string     `json:"remind_before,omitempty"`       // e.g. "15m", "30m"
	Priority           string     `json:"priority,omitempty"`            // normal, high, medium, 低, 高
	Status             string     `json:"status,omitempty"`
	Tags               []string   `json:"tags,omitempty"`
	SavedAt            string     `json:"saved_at"`                      // ISO-8601 timestamp
	UpdatedAt          string     `json:"updated_at,omitempty"`
	ShortID            string     `json:"short_id,omitempty"`
	IdempotencyKey     string     `json:"idempotency_key,omitempty"`
	NotificationPriority string   `json:"notification_priority,omitempty"` // normal, high; empty = normal
}

// IsValidNotificationPriority returns true if p is a valid notification priority value.
func IsValidNotificationPriority(p string) bool {
	switch p {
	case "", "normal", "high":
		return true
	}
	return false
}

// NotificationPriorityToPushover maps notification priority strings to
// Pushover API priority integers: "" and "normal" → 0, "high" → 1.
func NotificationPriorityToPushover(p string) int {
	switch p {
	case "high":
		return 1
	default:
		return 0
	}
}

// MeetingRecord maps to the meeting JSON format stored in
// work-records/meetings/YYYY/MM/DD/<timestamp>.json.
type MeetingRecord struct {
	CommonFields
	Participants []string `json:"participants,omitempty"`
	Agenda       string   `json:"agenda,omitempty"`
}

// TaskRecord maps to the task JSON format stored in
// work-records/tasks/active/<timestamp>.json or
// work-records/tasks/completed/YYYY/MM/DD/<timestamp>.json.
type TaskRecord struct {
	CommonFields
	CompletedAt    string   `json:"completed_at,omitempty"`
	RawInput       string   `json:"raw_input,omitempty"`
	ProcessedAt    string   `json:"processed_at,omitempty"`
	RelatedPersons []string `json:"related_persons,omitempty"`
	Reminder       string   `json:"reminder,omitempty"` // e.g. "30m"
}

// ReminderRecord maps to the reminder JSON format stored in
// work-records/reminders/active/<timestamp>.json or
// work-records/reminders/YYYY/MM/DD/<timestamp>.json.
type ReminderRecord struct {
	CommonFields
	Notes     string `json:"notes,omitempty"`
	Recurring string `json:"recurring,omitempty"`
}

// DoneThingsRecord maps to the done_things JSON format stored in
// work-records/done_things/YYYY/MM/DD/<timestamp>.json.
type DoneThingsRecord struct {
	CommonFields
	Priority string `json:"priority,omitempty"`
	Progress string `json:"progress,omitempty"`
}

// PersonalRecord maps to the personal JSON format stored in
// work-records/personals/active/<timestamp>.json or
// work-records/personals/completed/YYYY/MM/DD/<timestamp>.json.
type PersonalRecord struct {
	CommonFields
	Notes       string `json:"notes,omitempty"`
	Recurring   string `json:"recurring,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

// ParseRecord unmarshals JSON bytes into the appropriate typed record struct
// based on the "type" field. Returns the concrete type as an interface.
func ParseRecord(data []byte) (interface{}, error) {
	// First pass: extract the type field.
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("models: parse record type: %w", err)
	}

	switch RecordType(probe.Type) {
	case TypeMeeting:
		var r MeetingRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("models: parse meeting: %w", err)
		}
		return &r, nil
	case TypeTask:
		var r TaskRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("models: parse task: %w", err)
		}
		return &r, nil
	case TypeReminder:
		var r ReminderRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("models: parse reminder: %w", err)
		}
		return &r, nil
	case TypeDoneThings:
		var r DoneThingsRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("models: parse done_things: %w", err)
		}
		return &r, nil
	case TypePersonal:
		var r PersonalRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("models: parse personal: %w", err)
		}
		return &r, nil
	default:
		return nil, fmt.Errorf("models: unknown record type: %q", probe.Type)
	}
}

// ShortIDFromFilename generates a 16-character hex short ID from a record
// filename (e.g. "20260430_103211.json" or "20260317_1023_urine_health.json").
// The ID is derived by SHA-256 hashing the filename stem and taking the first
// 16 hex characters (8 bytes).
func ShortIDFromFilename(filename string) string {
	// Strip extension if present
	stem := filename
	if idx := len(stem) - 5; idx > 0 && stem[idx:] == ".json" {
		stem = stem[:idx]
	}
	h := sha256.Sum256([]byte(stem))
	return fmt.Sprintf("%x", h[:8])
}

// ShortIDFromTimestamp generates a 16-character hex short ID from a timestamp.
// This is the canonical way to create short IDs for new records. The hash input
// includes nanosecond precision to avoid collisions between records created in
// the same second.
func ShortIDFromTimestamp(t time.Time) string {
	h := sha256.Sum256([]byte(t.Format("20060102_150405.999999999")))
	return fmt.Sprintf("%x", h[:8])
}

// ShortIDFromTimestampAndSeq generates a 16-character hex short ID from a
// timestamp plus a monotonic sequence counter. The counter ensures unique IDs
// even when the OS clock resolution is too coarse to distinguish sequential
// calls (common on Windows where time.Now() has ~100ns–1ms granularity).
func ShortIDFromTimestampAndSeq(t time.Time, seq uint64) string {
	input := fmt.Sprintf("%s-%d", t.Format("20060102_150405.999999999"), seq)
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h[:8])
}

// GetCommonFields extracts the common fields from any record type.
func GetCommonFields(r interface{}) *CommonFields {
	switch v := r.(type) {
	case *MeetingRecord:
		return &v.CommonFields
	case *TaskRecord:
		return &v.CommonFields
	case *ReminderRecord:
		return &v.CommonFields
	case *DoneThingsRecord:
		return &v.CommonFields
	case *PersonalRecord:
		return &v.CommonFields
	default:
		return nil
	}
}

// MarshalRecord serialises a record to pretty-printed JSON, matching the
// existing nanobot file format with 2-space indentation.
func MarshalRecord(r interface{}) ([]byte, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("models: marshal record: %w", err)
	}
	return data, nil
}
