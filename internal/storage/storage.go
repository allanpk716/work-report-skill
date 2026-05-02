// Package storage provides CRUD operations for work records stored in the
// work-records/ directory hierarchy. It handles reading, writing, listing,
// completing, and cancelling records across the four record types (meeting,
// task, reminder, log).
//
// Directory layout mirrors the existing nanobot format:
//
//	meetings/YYYY/MM/DD/<timestamp>.json          (active meetings)
//	meetings/completed/YYYY/MM/DD/<timestamp>.json (completed meetings)
//	tasks/active/<timestamp>.json                   (active tasks)
//	tasks/completed/YYYY/MM/DD/<timestamp>.json    (completed tasks)
//	reminders/active/<timestamp>.json               (active reminders)
//	reminders/completed/YYYY/MM/DD/<timestamp>.json (completed reminders)
//	logs/YYYY/MM/DD/<timestamp>.json               (logs, no active/completed)
//
// All operations log record ID, type, and action for observability.
package storage

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wr/internal/models"
)

// Storage provides CRUD access to work records on disk.
type Storage struct {
	baseDir string
	logger  *log.Logger
}

// New creates a Storage rooted at baseDir (the work-records/ directory).
func New(baseDir string, logger *log.Logger) *Storage {
	if logger == nil {
		logger = log.New(os.Stderr, "[storage] ", log.LstdFlags)
	}
	return &Storage{baseDir: baseDir, logger: logger}
}

// AddRecord writes a record to the correct subdirectory, using a
// timestamp-based filename. It populates ShortID and SavedAt on the record,
// then returns the record with those fields set.
//
// For meetings and logs, the file is placed under <type>/YYYY/MM/DD/.
// For tasks and reminders, the file is placed under <type>/active/.
func (s *Storage) AddRecord(rec interface{}) (interface{}, error) {
	cf := models.GetCommonFields(rec)
	if cf == nil {
		return nil, fmt.Errorf("storage: add: unknown record type")
	}

	now := time.Now()
	cf.SavedAt = now.Format(time.RFC3339Nano)
	if cf.Status == "" {
		cf.Status = models.StatusActive
	}

	shortID := models.ShortIDFromTimestamp(now)
	cf.ShortID = shortID

	dir := s.activeDirForRecord(cf.Type, cf.Date)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("storage: add: mkdir %s: %w", dir, err)
	}

	// Use nanosecond-precision timestamp for filename uniqueness.
	// If a collision occurs (extremely rare), append a counter suffix.
	filename := now.Format("20060102_150405") + ".json"
	path := filepath.Join(dir, filename)

	// Handle filename collision by adding nanosecond suffix
	if _, err := os.Stat(path); err == nil {
		filename = now.Format("20060102_150405") + fmt.Sprintf("_%d", now.Nanosecond()) + ".json"
		path = filepath.Join(dir, filename)
	}

	data, err := models.MarshalRecord(rec)
	if err != nil {
		return nil, fmt.Errorf("storage: add: marshal: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return nil, fmt.Errorf("storage: add: write %s: %w", path, err)
	}

	s.logger.Printf("add record: short_id=%s type=%s path=%s", shortID, cf.Type, path)
	return rec, nil
}

// ListOptions controls filtering for ListRecords.
type ListOptions struct {
	// RecordType filters by type. Empty means all types.
	RecordType models.RecordType
	// Date filters records by date string (YYYY-MM-DD). Empty means no filter.
	Date string
	// IncludeCompleted controls whether to also scan completed directories.
	// For tasks and reminders this includes completed/ subdirs.
	// For meetings this includes completed/ subdirs.
	// For logs this is ignored (logs have no completed state).
	IncludeCompleted bool
}

// ListedRecord is a lightweight view of a record returned by listing.
type ListedRecord struct {
	ShortID string
	Type    models.RecordType
	Title   string
	Date    string
	Time    string
	Status  string
	FilePath string
}

// ListRecords scans the directory for records matching the options.
// Returns records sorted by date and time (newest first).
func (s *Storage) ListRecords(opts ListOptions) ([]ListedRecord, error) {
	var results []ListedRecord

	scanTypes := []models.RecordType{opts.RecordType}
	if opts.RecordType == "" {
		for _, t := range models.ValidRecordTypes() {
			scanTypes = append(scanTypes, models.RecordType(t))
		}
	}

	for _, rt := range scanTypes {
		recs, err := s.scanType(rt, opts)
		if err != nil {
			return nil, err
		}
		results = append(results, recs...)
	}

	// Sort newest first by date+time descending
	sort.Slice(results, func(i, j int) bool {
		di := results[i].Date + results[i].Time
		dj := results[j].Date + results[j].Time
		return di > dj
	})

	return results, nil
}

// GetByID resolves a short ID to a record by scanning all record files.
// Returns the parsed record and its file path.
func (s *Storage) GetByID(shortID string) (interface{}, string, error) {
	if shortID == "" {
		return nil, "", fmt.Errorf("storage: get: empty short ID")
	}

	// Scan all type directories
	for _, rt := range models.ValidRecordTypes() {
		rec, path, err := s.findRecordByID(models.RecordType(rt), shortID)
		if err == nil && rec != nil {
			return rec, path, nil
		}
	}

	return nil, "", fmt.Errorf("storage: record not found: %s", shortID)
}

// CompleteRecord moves a record to the completed directory, updating its
// status and completed_at fields. For logs, this is a no-op (logs are not
// completable).
func (s *Storage) CompleteRecord(shortID string) error {
	rec, oldPath, err := s.GetByID(shortID)
	if err != nil {
		return err
	}

	cf := models.GetCommonFields(rec)
	if cf == nil {
		return fmt.Errorf("storage: complete: unknown record type for %s", shortID)
	}

	if cf.Type == models.TypeLog {
		return fmt.Errorf("storage: complete: logs cannot be completed")
	}

	if cf.Status == models.StatusCompleted {
		return fmt.Errorf("storage: complete: record %s already completed", shortID)
	}

	now := time.Now()
	cf.Status = models.StatusCompleted
	cf.UpdatedAt = now.Format(time.RFC3339Nano)

	// Set CompletedAt on types that support it
	switch v := rec.(type) {
	case *models.TaskRecord:
		v.CompletedAt = now.Format(time.RFC3339Nano)
	case *models.MeetingRecord:
		// MeetingRecord doesn't have CompletedAt, status is sufficient
	}

	// Compute new path in completed directory
	newDir := s.completedDirForRecord(cf.Type, now)
	if err := os.MkdirAll(newDir, 0755); err != nil {
		return fmt.Errorf("storage: complete: mkdir %s: %w", newDir, err)
	}

	filename := filepath.Base(oldPath)
	newPath := filepath.Join(newDir, filename)

	// Write updated content to new location
	data, err := models.MarshalRecord(rec)
	if err != nil {
		return fmt.Errorf("storage: complete: marshal: %w", err)
	}

	if err := os.WriteFile(newPath, data, 0644); err != nil {
		return fmt.Errorf("storage: complete: write %s: %w", newPath, err)
	}

	// Remove old file
	if err := os.Remove(oldPath); err != nil {
		s.logger.Printf("warning: complete: could not remove old file %s: %v", oldPath, err)
	}

	s.logger.Printf("complete record: short_id=%s type=%s from=%s to=%s", shortID, cf.Type, oldPath, newPath)
	return nil
}

// CancelRecord sets a record's status to cancelled, updating the file in place.
func (s *Storage) CancelRecord(shortID string) error {
	rec, path, err := s.GetByID(shortID)
	if err != nil {
		return err
	}

	cf := models.GetCommonFields(rec)
	if cf == nil {
		return fmt.Errorf("storage: cancel: unknown record type for %s", shortID)
	}

	if cf.Status == models.StatusCancelled {
		return fmt.Errorf("storage: cancel: record %s already cancelled", shortID)
	}
	if cf.Status == models.StatusCompleted {
		return fmt.Errorf("storage: cancel: record %s is completed, cannot cancel", shortID)
	}

	now := time.Now()
	cf.Status = models.StatusCancelled
	cf.UpdatedAt = now.Format(time.RFC3339Nano)

	data, err := models.MarshalRecord(rec)
	if err != nil {
		return fmt.Errorf("storage: cancel: marshal: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("storage: cancel: write %s: %w", path, err)
	}

	s.logger.Printf("cancel record: short_id=%s type=%s path=%s", shortID, cf.Type, path)
	return nil
}

// --- internal helpers ---

// activeDirForRecord returns the directory where a new active record should be
// stored, based on its type and date.
func (s *Storage) activeDirForRecord(rt models.RecordType, date string) string {
	typeDir := filepath.Join(s.baseDir, string(rt)+"s") // "tasks", "meetings", etc.

	switch rt {
	case models.TypeLog:
		return filepath.Join(typeDir, datePath(date))
	case models.TypeMeeting:
		return filepath.Join(typeDir, datePath(date))
	case models.TypeTask:
		return filepath.Join(typeDir, "active")
	case models.TypeReminder:
		return filepath.Join(typeDir, "active")
	default:
		return filepath.Join(typeDir, datePath(date))
	}
}

// completedDirForRecord returns the completed directory for a record type at a
// given time (used for date-based completed paths).
func (s *Storage) completedDirForRecord(rt models.RecordType, t time.Time) string {
	typeDir := filepath.Join(s.baseDir, string(rt)+"s")
	return filepath.Join(typeDir, "completed", t.Format("2006"), t.Format("01"), t.Format("02"))
}

// scanType scans all directories for a given record type, applying filters.
func (s *Storage) scanType(rt models.RecordType, opts ListOptions) ([]ListedRecord, error) {
	var results []ListedRecord
	typeDir := filepath.Join(s.baseDir, string(rt)+"s")

	switch rt {
	case models.TypeLog:
		return s.scanDateTree(typeDir, rt, opts)
	case models.TypeMeeting:
		// Meetings use date tree (no active/ subdir)
		recs, err := s.scanDateTree(typeDir, rt, opts)
		if err != nil {
			return nil, err
		}
		results = append(results, recs...)
		if opts.IncludeCompleted {
			completedRecs, err := s.scanDateTree(filepath.Join(typeDir, "completed"), rt, opts)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			results = append(results, completedRecs...)
		}
		return results, nil
	case models.TypeTask, models.TypeReminder:
		// Scan active/ dir
		activeRecs, err := s.scanFlatDir(filepath.Join(typeDir, "active"), rt, opts)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		results = append(results, activeRecs...)

		if opts.IncludeCompleted {
			completedRecs, err := s.scanDateTree(filepath.Join(typeDir, "completed"), rt, opts)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			results = append(results, completedRecs...)
		}
		return results, nil
	default:
		return nil, nil
	}
}

// scanDateTree recursively walks a date-structured directory (YYYY/MM/DD/),
// reading JSON files and applying filters.
func (s *Storage) scanDateTree(root string, rt models.RecordType, opts ListOptions) ([]ListedRecord, error) {
	var results []ListedRecord

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		fullPath := filepath.Join(root, entry.Name())

		if entry.IsDir() {
			recs, err := s.scanDateTree(fullPath, rt, opts)
			if err != nil {
				return nil, err
			}
			results = append(results, recs...)
			continue
		}

		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		rec, err := s.readFile(fullPath)
		if err != nil {
			s.logger.Printf("warning: skipping corrupt file %s: %v", fullPath, err)
			continue
		}

		if lr := s.recordToListed(rec, fullPath); lr != nil {
			if s.matchesFilter(lr, opts) {
				results = append(results, *lr)
			}
		}
	}

	return results, nil
}

// scanFlatDir reads all JSON files from a flat directory (like active/).
func (s *Storage) scanFlatDir(dir string, rt models.RecordType, opts ListOptions) ([]ListedRecord, error) {
	var results []ListedRecord

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())

		if entry.IsDir() {
			continue
		}

		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		rec, err := s.readFile(fullPath)
		if err != nil {
			s.logger.Printf("warning: skipping corrupt file %s: %v", fullPath, err)
			continue
		}

		if lr := s.recordToListed(rec, fullPath); lr != nil {
			if s.matchesFilter(lr, opts) {
				results = append(results, *lr)
			}
		}
	}

	return results, nil
}

// readFile reads and parses a single JSON record file.
func (s *Storage) readFile(path string) (interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	rec, err := models.ParseRecord(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// Populate ShortID from filename if not already set
	cf := models.GetCommonFields(rec)
	if cf != nil && cf.ShortID == "" {
		cf.ShortID = models.ShortIDFromFilename(filepath.Base(path))
	}

	return rec, nil
}

// recordToListed converts a parsed record to a ListedRecord.
func (s *Storage) recordToListed(rec interface{}, filePath string) *ListedRecord {
	cf := models.GetCommonFields(rec)
	if cf == nil {
		return nil
	}
	return &ListedRecord{
		ShortID:  cf.ShortID,
		Type:     cf.Type,
		Title:    cf.Title,
		Date:     cf.Date,
		Time:     cf.Time,
		Status:   cf.Status,
		FilePath: filePath,
	}
}

// matchesFilter checks if a listed record matches the filter options.
func (s *Storage) matchesFilter(lr *ListedRecord, opts ListOptions) bool {
	if opts.Date != "" && lr.Date != opts.Date {
		return false
	}
	return true
}

// findRecordByID scans all files of a given type looking for one whose ShortID
// matches.
func (s *Storage) findRecordByID(rt models.RecordType, shortID string) (interface{}, string, error) {
	typeDir := filepath.Join(s.baseDir, string(rt)+"s")

	switch rt {
	case models.TypeLog:
		return s.findInDateTree(typeDir, shortID)
	case models.TypeMeeting:
		rec, path, err := s.findInDateTree(typeDir, shortID)
		if err == nil && rec != nil {
			return rec, path, nil
		}
		return s.findInDateTree(filepath.Join(typeDir, "completed"), shortID)
	case models.TypeTask, models.TypeReminder:
		rec, path, err := s.findInFlatDir(filepath.Join(typeDir, "active"), shortID)
		if err == nil && rec != nil {
			return rec, path, nil
		}
		return s.findInDateTree(filepath.Join(typeDir, "completed"), shortID)
	default:
		return nil, "", nil
	}
}

// findInDateTree walks a date tree looking for a record by short ID.
func (s *Storage) findInDateTree(root string, shortID string) (interface{}, string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, "", err
	}

	for _, entry := range entries {
		fullPath := filepath.Join(root, entry.Name())

		if entry.IsDir() {
			rec, path, err := s.findInDateTree(fullPath, shortID)
			if err == nil && rec != nil {
				return rec, path, nil
			}
			continue
		}

		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// Quick ID check from filename before full parse
		computedID := models.ShortIDFromFilename(entry.Name())
		if computedID == shortID {
			rec, err := s.readFile(fullPath)
			if err != nil {
				continue // skip corrupt
			}
			return rec, fullPath, nil
		}

		// Fallback: parse and check ShortID field
		rec, err := s.readFile(fullPath)
		if err != nil {
			continue
		}
		cf := models.GetCommonFields(rec)
		if cf != nil && cf.ShortID == shortID {
			return rec, fullPath, nil
		}
	}

	return nil, "", fmt.Errorf("not found")
}

// findInFlatDir scans a flat directory for a record by short ID.
func (s *Storage) findInFlatDir(dir string, shortID string) (interface{}, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())

		computedID := models.ShortIDFromFilename(entry.Name())
		if computedID == shortID {
			rec, err := s.readFile(fullPath)
			if err != nil {
				continue
			}
			return rec, fullPath, nil
		}

		rec, err := s.readFile(fullPath)
		if err != nil {
			continue
		}
		cf := models.GetCommonFields(rec)
		if cf != nil && cf.ShortID == shortID {
			return rec, fullPath, nil
		}
	}

	return nil, "", fmt.Errorf("not found")
}

// datePath converts "2026-03-11" to "2026/03/11".
func datePath(date string) string {
	parts := strings.SplitN(date, "-", 3)
	if len(parts) != 3 {
		return date
	}
	return filepath.Join(parts[0], parts[1], parts[2])
}

// ReadRecordFile is a convenience function that reads a record file from an
// absolute path and returns the parsed record.
func ReadRecordFile(path string) (interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("storage: read file %s: %w", path, err)
	}

	rec, err := models.ParseRecord(data)
	if err != nil {
		return nil, fmt.Errorf("storage: parse file %s: %w", path, err)
	}

	cf := models.GetCommonFields(rec)
	if cf != nil && cf.ShortID == "" {
		cf.ShortID = models.ShortIDFromFilename(filepath.Base(path))
	}

	return rec, nil
}

// MarshalToJSON is a convenience function that marshals a record to indented
// JSON without exposing internal models package details.
func MarshalToJSON(rec interface{}) ([]byte, error) {
	return json.MarshalIndent(rec, "", "  ")
}
