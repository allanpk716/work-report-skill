// Package scheduler manages cron-based scheduling and pushover notifications.
package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ScheduleEntry tracks the scheduling state for a single record.
type ScheduleEntry struct {
	RecordShortID string `json:"record_short_id"`
	RecordType    string `json:"record_type"`
	Title         string `json:"title"`
	TriggerAt     string `json:"trigger_at"`        // ISO-8601
	CronEntryID   int    `json:"cron_entry_id"`
	Fired         bool   `json:"fired"`
	FiredAt       string `json:"fired_at,omitempty"` // ISO-8601
	LastError     string `json:"last_error,omitempty"`
	Recurring     string `json:"recurring,omitempty"`
}

// SchedulerState holds all scheduled entry states.
type SchedulerState struct {
	Entries     map[string]*ScheduleEntry `json:"entries"` // key = short_id
	LastUpdated string                    `json:"last_updated"`
}

// NewState creates an empty SchedulerState.
func NewState() *SchedulerState {
	return &SchedulerState{
		Entries:     make(map[string]*ScheduleEntry),
		LastUpdated: time.Now().UTC().Format(time.RFC3339),
	}
}

// DefaultStatePath returns ~/.work-report/scheduler-state.json.
func DefaultStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("scheduler: cannot determine home dir: %w", err)
	}
	dir := filepath.Join(home, ".work-report")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("scheduler: cannot create state dir: %w", err)
	}
	return filepath.Join(dir, "scheduler-state.json"), nil
}

func backupPath(mainPath string) string {
	return mainPath + ".bak"
}

// LoadState reads the scheduler state from path, recovering from .bak if the
// main file is corrupt or missing.
func LoadState(path string) (*SchedulerState, error) {
	bakPath := backupPath(path)

	// Try main file first
	state, err := loadStateFile(path)
	if err == nil {
		return state, nil
	}

	// Main file failed, try backup
	state, errBak := loadStateFile(bakPath)
	if errBak == nil {
		return state, nil
	}

	return nil, fmt.Errorf("scheduler: state file and backup both unreadable: main=%v, backup=%v", err, errBak)
}

// SaveState writes the scheduler state to path, creating a .bak backup first.
func SaveState(path string, state *SchedulerState) error {
	state.LastUpdated = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("scheduler: marshal state: %w", err)
	}

	bakPath := backupPath(path)

	// Write backup first
	if err := os.WriteFile(bakPath, data, 0644); err != nil {
		return fmt.Errorf("scheduler: write backup: %w", err)
	}

	// Write main file
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("scheduler: write state: %w", err)
	}

	return nil
}

// AddEntry adds or replaces a schedule entry keyed by short_id.
func (s *SchedulerState) AddEntry(entry *ScheduleEntry) {
	s.Entries[entry.RecordShortID] = entry
}

// RemoveEntry removes the entry for the given short_id.
func (s *SchedulerState) RemoveEntry(shortID string) {
	delete(s.Entries, shortID)
}

// GetEntry returns the entry for shortID, or nil if not found.
func (s *SchedulerState) GetEntry(shortID string) *ScheduleEntry {
	return s.Entries[shortID]
}

// MarkFired sets the entry as fired with the given timestamp.
func (s *SchedulerState) MarkFired(shortID string, firedAt time.Time) {
	entry, ok := s.Entries[shortID]
	if !ok {
		return
	}
	entry.Fired = true
	entry.FiredAt = firedAt.UTC().Format(time.RFC3339)
}

// MarkError records an error message on the entry.
func (s *SchedulerState) MarkError(shortID string, errMsg string) {
	entry, ok := s.Entries[shortID]
	if !ok {
		return
	}
	entry.LastError = errMsg
}

func loadStateFile(path string) (*SchedulerState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("scheduler: empty state file: %s", path)
	}
	var state SchedulerState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("scheduler: corrupt state file %s: %w", path, err)
	}
	if state.Entries == nil {
		state.Entries = make(map[string]*ScheduleEntry)
	}
	return &state, nil
}
