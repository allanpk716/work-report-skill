// Package storage provides CRUD operations for work records stored in the
// work-records/ directory hierarchy. It handles reading, writing, listing,
// completing, and cancelling records across the four record types (meeting,
// task, reminder, done_things).
//
// Directory layout mirrors the existing nanobot format:
//
//	meetings/YYYY/MM/DD/<timestamp>.json          (active meetings)
//	meetings/completed/YYYY/MM/DD/<timestamp>.json (completed meetings)
//	tasks/active/<timestamp>.json                   (active tasks)
//	tasks/completed/YYYY/MM/DD/<timestamp>.json    (completed tasks)
//	reminders/active/<timestamp>.json               (active reminders)
//	reminders/completed/YYYY/MM/DD/<timestamp>.json (completed reminders)
//	done_things/YYYY/MM/DD/<timestamp>.json         (done_things, no active/completed)
//
// All operations log record ID, type, and action for observability.
package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"wr/internal/logger"
	"wr/internal/models"
)

// Storage provides CRUD access to work records on disk.
// All write operations (AddRecord, CompleteRecord, CancelRecord, UpdateRecord)
// are serialized via a cross-process file lock to prevent concurrent file I/O
// corruption and ShortID collisions across multiple processes.
// The monotonic seq counter is persisted in <baseDir>/.seq so that separate
// processes sharing the same data directory generate unique filenames and IDs.
type Storage struct {
	baseDir string
	lock    *LockFile
}

// New creates a Storage rooted at baseDir (the work-records/ directory).
// It also runs any pending data migrations (e.g., logs/ → done_things/).
func New(baseDir string) *Storage {
	// Run startup migration; log warning on failure but don't block Storage creation.
	if err := MigrateIfNeeded(baseDir); err != nil {
		logger.WithField("error", err).Warnf("[storage] startup migration failed")
	}
	return &Storage{baseDir: baseDir, lock: NewLockFile(baseDir)}
}

// AddRecord writes a record to the correct subdirectory, using a
// timestamp-based filename. It populates ShortID and SavedAt on the record,
// then returns the record with those fields set.
//
// For meetings and logs, the file is placed under <type>/YYYY/MM/DD/.
// For tasks and reminders, the file is placed under <type>/active/.
func (s *Storage) AddRecord(rec interface{}) (interface{}, error) {
	if err := s.lock.Acquire(DefaultLockTimeout); err != nil {
		return nil, err
	}
	defer s.lock.Release()

	cf := models.GetCommonFields(rec)
	if cf == nil {
		return nil, fmt.Errorf("storage: add: unknown record type")
	}

	now := time.Now()
	cf.SavedAt = now.Format(time.RFC3339Nano)
	if cf.Status == "" {
		cf.Status = models.StatusActive
	}

	seq, err := s.nextSeq()
	if err != nil {
		return nil, err
	}
	shortID := models.ShortIDFromTimestampAndSeq(now, seq)
	cf.ShortID = shortID

	dir := s.activeDirForRecord(cf.Type, cf.Date)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("storage: add: mkdir %s: %w", dir, err)
	}

	// Use sequence counter for guaranteed-unique filenames.
	// The seq counter is monotonic under the file lock, so collisions are impossible.
	filename := now.Format("20060102_150405") + fmt.Sprintf("_%d", seq) + ".json"
	path := filepath.Join(dir, filename)

	data, err := models.MarshalRecord(rec)
	if err != nil {
		return nil, fmt.Errorf("storage: add: marshal: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return nil, fmt.Errorf("storage: add: write %s: %w", path, err)
	}

	logger.WithField("short_id", shortID).WithField("type", string(cf.Type)).WithField("path", path).Info("add record")
	return rec, nil
}

// ListOptions controls filtering for ListRecords.
type ListOptions struct {
	// RecordType filters by type. Empty means all types.
	RecordType models.RecordType
	// Date filters records by date string (YYYY-MM-DD). Empty means no filter.
	Date string
	// DateFrom filters records on or after this date (YYYY-MM-DD). Empty means no lower bound.
	DateFrom string
	// DateTo filters records on or before this date (YYYY-MM-DD). Empty means no upper bound.
	DateTo string
	// Status filters by status (active, completed, cancelled, all). Empty means no filter.
	// When set to "completed" or "all", IncludeCompleted is automatically enabled.
	Status string
	// Query performs case-insensitive keyword search against title and description.
	Query string
	// IncludeCompleted controls whether to also scan completed directories.
	// For tasks and reminders this includes completed/ subdirs.
	// For meetings this includes completed/ subdirs.
	// For logs this is ignored (logs have no completed state).
	IncludeCompleted bool
}

// ListedRecord is a lightweight view of a record returned by listing.
type ListedRecord struct {
	ShortID     string
	Type        models.RecordType
	Title       string
	Description string
	Date        string
	Time        string
	Status      string
	FilePath    string
}

// ListRecords scans the directory for records matching the options.
// Returns records sorted by date and time (newest first).
func (s *Storage) ListRecords(opts ListOptions) ([]ListedRecord, error) {
	// Auto-enable IncludeCompleted when Status filter requires it.
	if opts.Status == models.StatusCompleted || opts.Status == "all" {
		opts.IncludeCompleted = true
	}

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
	if err := s.lock.Acquire(DefaultLockTimeout); err != nil {
		return err
	}
	defer s.lock.Release()

	rec, oldPath, err := s.GetByID(shortID)
	if err != nil {
		return err
	}

	cf := models.GetCommonFields(rec)
	if cf == nil {
		return fmt.Errorf("storage: complete: unknown record type for %s", shortID)
	}

	if cf.Type == models.TypeDoneThings {
		return fmt.Errorf("storage: complete: done_things entries cannot be completed")
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
		logger.WithField("old_path", oldPath).Warnf("complete: could not remove old file: %v", err)
	}

	logger.WithField("short_id", shortID).WithField("type", string(cf.Type)).WithField("from", oldPath).WithField("to", newPath).Info("complete record")
	return nil
}

// CancelRecord sets a record's status to cancelled, updating the file in place.
func (s *Storage) CancelRecord(shortID string) error {
	if err := s.lock.Acquire(DefaultLockTimeout); err != nil {
		return err
	}
	defer s.lock.Release()

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

	logger.WithField("short_id", shortID).WithField("type", string(cf.Type)).WithField("path", path).Info("cancel record")
	return nil
}

// allowedUpdateFields lists the fields that may be passed to UpdateRecord.
// Immutable or system-managed fields (type, status, short_id, saved_at) are excluded.
var allowedUpdateFields = map[string]bool{
	"title": true, "description": true, "date": true, "time": true,
	"end_time": true, "location": true, "related_person": true,
	"priority": true, "tags": true, "remind_before": true,
	"recurring": true, "participants": true, "agenda": true,
	"notes": true, "progress": true,
}

// ErrRecordNotFound is returned by UpdateRecord when the short ID does not
// resolve to any record on disk.
var ErrRecordNotFound = fmt.Errorf("storage: record not found")

// ErrRecordCompleted is returned by UpdateRecord when the target record has
// already been completed.
var ErrRecordCompleted = fmt.Errorf("storage: record already completed")

// ErrRecordCancelled is returned by UpdateRecord when the target record has
// already been cancelled.
var ErrRecordCancelled = fmt.Errorf("storage: record already cancelled")

// ErrEmptyUpdate is returned by UpdateRecord when the fields map is empty.
var ErrEmptyUpdate = fmt.Errorf("storage: empty update fields")

// ErrFieldNotAllowed is returned by UpdateRecord when the fields map contains
// a key that is not in the allowed set (e.g. "type", "status", "short_id").
var ErrFieldNotAllowed = fmt.Errorf("storage: field not allowed for update")

// UpdateRecord applies field-level updates to the record identified by shortID.
// It validates that the record exists, is not completed or cancelled, and that
// only allowed fields are being modified. It writes the updated record back to
// the same file path and returns the updated record.
//
// Allowed fields: title, description, date, time, end_time, location,
// related_person, priority, tags, remind_before, recurring, participants,
// agenda, notes, progress.
//
// Disallowed fields: type (immutable), status (use Complete/Cancel),
// short_id, saved_at.
func (s *Storage) UpdateRecord(shortID string, fields map[string]interface{}) (interface{}, error) {
	if err := s.lock.Acquire(DefaultLockTimeout); err != nil {
		return nil, err
	}
	defer s.lock.Release()

	if len(fields) == 0 {
		return nil, ErrEmptyUpdate
	}

	// Validate field names before doing any I/O
	for key := range fields {
		if !allowedUpdateFields[key] {
			return nil, fmt.Errorf("%w: %q", ErrFieldNotAllowed, key)
		}
	}

	// Resolve record
	rec, path, err := s.GetByID(shortID)
	if err != nil {
		return nil, ErrRecordNotFound
	}

	cf := models.GetCommonFields(rec)
	if cf == nil {
		return nil, fmt.Errorf("storage: update: unknown record type for %s", shortID)
	}

	// Validate status
	if cf.Status == models.StatusCompleted {
		return nil, ErrRecordCompleted
	}
	if cf.Status == models.StatusCancelled {
		return nil, ErrRecordCancelled
	}

	// Track which fields changed for logging
	var changedFields []string

	// Apply common fields
	changedFields = append(changedFields, s.applyCommonFields(cf, fields)...)

	// Apply type-specific fields
	changedFields = append(changedFields, s.applyTypeFields(rec, fields)...)

	// Set updated timestamp
	now := time.Now()
	cf.UpdatedAt = now.Format(time.RFC3339Nano)

	// Marshal and write back
	data, err := models.MarshalRecord(rec)
	if err != nil {
		return nil, fmt.Errorf("storage: update: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return nil, fmt.Errorf("storage: update: write %s: %w", path, err)
	}

	logger.WithField("short_id", shortID).WithField("type", string(cf.Type)).WithField("fields", changedFields).WithField("path", path).Info("update record")

	return rec, nil
}

// applyCommonFields sets allowed common fields on cf and returns the names of
// fields that were actually changed.
func (s *Storage) applyCommonFields(cf *models.CommonFields, fields map[string]interface{}) []string {
	var changed []string

	if v, ok := fields["title"]; ok {
		if sv, ok := v.(string); ok {
			cf.Title = sv
			changed = append(changed, "title")
		}
	}
	if v, ok := fields["description"]; ok {
		if sv, ok := v.(string); ok {
			cf.Description = sv
			changed = append(changed, "description")
		}
	}
	if v, ok := fields["date"]; ok {
		if sv, ok := v.(string); ok {
			cf.Date = sv
			changed = append(changed, "date")
		}
	}
	if v, ok := fields["time"]; ok {
		if sv, ok := v.(string); ok {
			cf.Time = sv
			changed = append(changed, "time")
		}
	}
	if v, ok := fields["end_time"]; ok {
		if sv, ok := v.(string); ok {
			cf.EndTime = sv
			changed = append(changed, "end_time")
		}
	}
	if v, ok := fields["location"]; ok {
		if sv, ok := v.(string); ok {
			cf.Location = sv
			changed = append(changed, "location")
		}
	}
	if v, ok := fields["related_person"]; ok {
		if sv, ok := v.(string); ok {
			cf.RelatedPerson = sv
			changed = append(changed, "related_person")
		}
	}
	if v, ok := fields["remind_before"]; ok {
		if sv, ok := v.(string); ok {
			cf.RemindBefore = sv
			changed = append(changed, "remind_before")
		}
	}
	if v, ok := fields["tags"]; ok {
		if slice := toStringSlice(v); slice != nil {
			cf.Tags = slice
			changed = append(changed, "tags")
		}
	}

	// priority is handled in applyTypeFields for DoneThingsRecord (shadowed field)
	// and here for all other types
	if v, ok := fields["priority"]; ok {
		if sv, ok := v.(string); ok {
			cf.Priority = sv
			changed = append(changed, "priority")
		}
	}

	return changed
}

// applyTypeFields sets type-specific fields based on the concrete record type.
// Returns the names of fields that were changed.
func (s *Storage) applyTypeFields(rec interface{}, fields map[string]interface{}) []string {
	var changed []string

	switch v := rec.(type) {
	case *models.MeetingRecord:
		if p, ok := fields["participants"]; ok {
			if slice := toStringSlice(p); slice != nil {
				v.Participants = slice
				changed = append(changed, "participants")
			}
		}
		if a, ok := fields["agenda"]; ok {
			if sv, ok := a.(string); ok {
				v.Agenda = sv
				changed = append(changed, "agenda")
			}
		}
	case *models.ReminderRecord:
		if n, ok := fields["notes"]; ok {
			if sv, ok := n.(string); ok {
				v.Notes = sv
				changed = append(changed, "notes")
			}
		}
		if r, ok := fields["recurring"]; ok {
			if sv, ok := r.(string); ok {
				v.Recurring = sv
				changed = append(changed, "recurring")
			}
		}
	case *models.DoneThingsRecord:
		// DoneThingsRecord has its own Priority field that shadows CommonFields.Priority.
		// Setting priority on DoneThingsRecord must go through the outer struct.
		if p, ok := fields["priority"]; ok {
			if sv, ok := p.(string); ok {
				v.Priority = sv
				changed = append(changed, "priority")
			}
		}
		if pr, ok := fields["progress"]; ok {
			if sv, ok := pr.(string); ok {
				v.Progress = sv
				changed = append(changed, "progress")
			}
		}
	case *models.TaskRecord:
		// TaskRecord has no type-specific updatable fields in the allowed list.
		// All its unique fields (completed_at, raw_input, etc.) are system-managed.
	}

	return changed
}

// toStringSlice converts an interface{} to []string. Accepts both []string
// directly and []interface{} containing strings.
func toStringSlice(v interface{}) []string {
	if slice, ok := v.([]string); ok {
		return slice
	}
	if slice, ok := v.([]interface{}); ok {
		result := make([]string, 0, len(slice))
		for _, item := range slice {
			if s, ok := item.(string); ok {
				result = append(result, s)
			} else {
				return nil // not all strings, reject
			}
		}
		return result
	}
	return nil
}

// --- internal helpers ---

// seqFileName is the name of the persisted monotonic counter file.
const seqFileName = ".seq"

// nextSeq atomically reads, increments, and persists the monotonic sequence
// counter. Must be called while holding the file lock so that concurrent
// processes and goroutines get unique values.
func (s *Storage) nextSeq() (uint64, error) {
	seqPath := filepath.Join(s.baseDir, seqFileName)

	var seq uint64
	data, err := os.ReadFile(seqPath)
	if err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("storage: read seq: %w", err)
	}
	if len(data) > 0 {
		seq, err = strconv.ParseUint(string(strings.TrimSpace(string(data))), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("storage: parse seq: %w", err)
		}
	}
	seq++

	if err := os.WriteFile(seqPath, []byte(strconv.FormatUint(seq, 10)), 0644); err != nil {
		return 0, fmt.Errorf("storage: write seq: %w", err)
	}

	return seq, nil
}

// activeDirForRecord returns the directory where a new active record should be
// stored, based on its type and date.
func (s *Storage) activeDirForRecord(rt models.RecordType, date string) string {
	typeDir := filepath.Join(s.baseDir, rt.DirName())

	switch rt {
	case models.TypeDoneThings:
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
	typeDir := filepath.Join(s.baseDir, rt.DirName())
	return filepath.Join(typeDir, "completed", t.Format("2006"), t.Format("01"), t.Format("02"))
}

// scanType scans all directories for a given record type, applying filters.
func (s *Storage) scanType(rt models.RecordType, opts ListOptions) ([]ListedRecord, error) {
	var results []ListedRecord
	typeDir := filepath.Join(s.baseDir, rt.DirName())

	switch rt {
	case models.TypeDoneThings:
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
			logger.WithField("file", fullPath).Warnf("skipping corrupt file: %v", err)
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
			logger.WithField("file", fullPath).Warnf("skipping corrupt file: %v", err)
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
		ShortID:     cf.ShortID,
		Type:        cf.Type,
		Title:       cf.Title,
		Description: cf.Description,
		Date:        cf.Date,
		Time:        cf.Time,
		Status:      cf.Status,
		FilePath:    filePath,
	}
}

// matchesFilter checks if a listed record matches the filter options.
func (s *Storage) matchesFilter(lr *ListedRecord, opts ListOptions) bool {
	// Exact date match (original behavior)
	if opts.Date != "" && lr.Date != opts.Date {
		return false
	}
	// Date range: from (inclusive)
	if opts.DateFrom != "" && lr.Date < opts.DateFrom {
		return false
	}
	// Date range: to (inclusive)
	if opts.DateTo != "" && lr.Date > opts.DateTo {
		return false
	}
	// Status filter: skip when "all" or empty
	if opts.Status != "" && opts.Status != "all" && lr.Status != opts.Status {
		return false
	}
	// Keyword search: case-insensitive match on title and description
	if opts.Query != "" {
		q := strings.ToLower(opts.Query)
		if !strings.Contains(strings.ToLower(lr.Title), q) &&
			!strings.Contains(strings.ToLower(lr.Description), q) {
			return false
		}
	}
	return true
}

// findRecordByID scans all files of a given type looking for one whose ShortID
// matches.
func (s *Storage) findRecordByID(rt models.RecordType, shortID string) (interface{}, string, error) {
	typeDir := filepath.Join(s.baseDir, rt.DirName())

	switch rt {
	case models.TypeDoneThings:
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
// Supports prefix matching: if an exact match is not found, it will try
// matching records whose ShortID starts with the query string (enabling
// resolution of legacy 8-char IDs against newer 16-char IDs).
func (s *Storage) findInDateTree(root string, shortID string) (interface{}, string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, "", err
	}

	// Collect prefix-matched candidates in case exact match fails.
	var prefixCandidates []struct {
		rec      interface{}
		fullPath string
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

		// Check prefix match for backward compatibility (8-char → 16-char)
		if cf != nil && strings.HasPrefix(cf.ShortID, shortID) {
			prefixCandidates = append(prefixCandidates, struct {
				rec      interface{}
				fullPath string
			}{rec, fullPath})
		}
		// Also check filename-derived ID prefix
		if strings.HasPrefix(computedID, shortID) {
			prefixCandidates = append(prefixCandidates, struct {
				rec      interface{}
				fullPath string
			}{rec, fullPath})
		}
	}

	// If we got exactly one prefix match, use it
	if len(prefixCandidates) == 1 {
		return prefixCandidates[0].rec, prefixCandidates[0].fullPath, nil
	}
	if len(prefixCandidates) > 1 {
		return nil, "", fmt.Errorf("short ID %q matches multiple records (ambiguous prefix)", shortID)
	}

	return nil, "", fmt.Errorf("not found")
}

// findInFlatDir scans a flat directory for a record by short ID.
// Supports prefix matching: if an exact match is not found, it will try
// matching records whose ShortID starts with the query string.
func (s *Storage) findInFlatDir(dir string, shortID string) (interface{}, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}

	// Collect prefix-matched candidates in case exact match fails.
	var prefixCandidates []struct {
		rec      interface{}
		fullPath string
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

		// Check prefix match for backward compatibility
		if cf != nil && strings.HasPrefix(cf.ShortID, shortID) {
			prefixCandidates = append(prefixCandidates, struct {
				rec      interface{}
				fullPath string
			}{rec, fullPath})
		}
		if strings.HasPrefix(computedID, shortID) {
			prefixCandidates = append(prefixCandidates, struct {
				rec      interface{}
				fullPath string
			}{rec, fullPath})
		}
	}

	// If we got exactly one prefix match, use it
	if len(prefixCandidates) == 1 {
		return prefixCandidates[0].rec, prefixCandidates[0].fullPath, nil
	}
	if len(prefixCandidates) > 1 {
		return nil, "", fmt.Errorf("short ID %q matches multiple records (ambiguous prefix)", shortID)
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

// GetByIdempotencyKey scans all active records for one with a matching
// idempotency_key in the raw JSON. Returns the parsed record and its file path,
// or nil/"" if no match is found. The lookup degrades gracefully on errors.
func (s *Storage) GetByIdempotencyKey(key string) (interface{}, string, error) {
	if key == "" {
		return nil, "", nil
	}

	// Scan all types, all statuses (active + completed)
	for _, rt := range models.ValidRecordTypes() {
		recs, err := s.ListRecords(ListOptions{
			RecordType:       models.RecordType(rt),
			IncludeCompleted: true,
		})
		if err != nil {
			continue // degrade gracefully
		}
		for _, lr := range recs {
			// Read the raw JSON file to check idempotency_key
			raw, err := os.ReadFile(lr.FilePath)
			if err != nil {
				continue
			}
			var probe struct {
				IdempotencyKey string `json:"idempotency_key"`
			}
			if err := json.Unmarshal(raw, &probe); err != nil {
				continue
			}
			if probe.IdempotencyKey == key {
				// Parse the full record
				rec, err := s.readFile(lr.FilePath)
				if err != nil {
					continue
				}
				return rec, lr.FilePath, nil
			}
		}
	}
	return nil, "", nil
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

// ListFullRecords returns full parsed records (not just ListedRecord summaries)
// matching the given options. Useful when callers need type-specific fields
// (e.g., remind_before, recurring) that ListedRecord doesn't carry.
func (s *Storage) ListFullRecords(opts ListOptions) ([]interface{}, error) {
	// Use ListRecords to find matching files, then read full records
	listed, err := s.ListRecords(opts)
	if err != nil {
		return nil, err
	}

	var results []interface{}
	for _, lr := range listed {
		rec, err := s.readFile(lr.FilePath)
		if err != nil {
			logger.WithField("file", lr.FilePath).Warnf("ListFullRecords skip: %v", err)
			continue
		}
		results = append(results, rec)
	}
	return results, nil
}

// FindByContent looks up records by exact title and date, optionally filtered
// by record type. It uses ListRecords with Query + Date filters to narrow the
// candidate set (ListRecords uses case-insensitive substring matching on title
// and description), then keeps only results whose title matches exactly
// (case-sensitive).
//
// Returns the matching records. If no record matches, returns ErrRecordNotFound.
// If multiple records share the same title and date (e.g., different times),
// all matches are returned so the caller can decide how to handle ambiguity.
func (s *Storage) FindByContent(title, date string, recordType ...string) ([]ListedRecord, error) {
	opts := ListOptions{
		Query: title,
		Date:  date,
	}
	if len(recordType) > 0 && recordType[0] != "" {
		opts.RecordType = models.RecordType(recordType[0])
	}

	candidates, err := s.ListRecords(opts)
	if err != nil {
		return nil, fmt.Errorf("storage: find-by-content: %w", err)
	}

	// Narrow to exact title match (case-sensitive).
	var matches []ListedRecord
	for _, lr := range candidates {
		if lr.Title == title {
			matches = append(matches, lr)
		}
	}

	if len(matches) == 0 {
		return nil, ErrRecordNotFound
	}

	return matches, nil
}

// MarshalToJSON is a convenience function that marshals a record to indented
// JSON without exposing internal models package details.
func MarshalToJSON(rec interface{}) ([]byte, error) {
	return json.MarshalIndent(rec, "", "  ")
}
