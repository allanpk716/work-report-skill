package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"wr/internal/logger"
)

// backupFilenameRe matches wr-backup-YYYYMMDD-HHMMSS.zip and the collision
// variant wr-backup-YYYYMMDD-HHMMSS-N.zip.
var backupFilenameRe = regexp.MustCompile(
	`^wr-backup-(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})(?:-\d+)?\.zip$`,
)

// ParseBackupTime extracts the creation timestamp from a backup filename.
// Returns a zero Time if the filename does not match the expected pattern
// wr-backup-YYYYMMDD-HHMMSS[.zip|-N.zip].
func ParseBackupTime(filename string) time.Time {
	m := backupFilenameRe.FindStringSubmatch(filename)
	if m == nil {
		return time.Time{}
	}
	return time.Date(
		atoi(m[1]), time.Month(atoi(m[2])), atoi(m[3]),
		atoi(m[4]), atoi(m[5]), atoi(m[6]),
		0, time.Local,
	)
}

// atoi converts a decimal string to int without error handling — the regex
// guarantees the input is digits only.
func atoi(s string) int {
	var n int
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// RotationResult describes the outcome of a GFS rotation pass.
type RotationResult struct {
	Kept    []BackupMeta `json:"kept"`
	Removed []BackupMeta `json:"removed"`
}

// GFSRotate applies Grandfather-Father-Son rotation to the backup list.
//
// Backups must be sorted newest-first (as returned by ListBackups).  For each
// time granularity the algorithm walks the list and marks the newest backup
// per distinct time bucket until the retention count is reached:
//
//   - Daily:   newest backup per calendar day, up to policy.Daily buckets
//   - Weekly:  newest backup per ISO week,    up to policy.Weekly buckets
//   - Monthly: newest backup per calendar month, up to policy.Monthly buckets
//
// A backup protected by ANY rule is retained; unprotected backups are deleted
// from outputDir.
//
// Returns the kept and removed backup lists.  Deletion errors are logged but
// do not cause the function to fail — the caller can inspect Removed to
// verify cleanup.
func GFSRotate(backups []BackupMeta, policy RetentionPolicy, outputDir string) (*RotationResult, error) {
	protected := make(map[string]bool, len(backups))

	dailyN, weeklyN, monthlyN := 0, 0, 0
	seenDays := make(map[string]bool)
	seenWeeks := make(map[string]bool)
	seenMonths := make(map[string]bool)

	for _, b := range backups {
		t := b.CreatedAt

		// Daily rule — one newest backup per calendar day.
		dayKey := t.Format("2006-01-02")
		if dailyN < policy.Daily && !seenDays[dayKey] {
			seenDays[dayKey] = true
			dailyN++
			protected[b.Filename] = true
		}

		// Weekly rule — one newest backup per ISO week.
		_, weekNum := t.ISOWeek()
		weekKey := fmt.Sprintf("%d-W%02d", t.Year(), weekNum)
		if weeklyN < policy.Weekly && !seenWeeks[weekKey] {
			seenWeeks[weekKey] = true
			weeklyN++
			protected[b.Filename] = true
		}

		// Monthly rule — one newest backup per calendar month.
		monthKey := t.Format("2006-01")
		if monthlyN < policy.Monthly && !seenMonths[monthKey] {
			seenMonths[monthKey] = true
			monthlyN++
			protected[b.Filename] = true
		}
	}

	// Classify backups into kept / removed.
	var kept, removed []BackupMeta
	for _, b := range backups {
		if protected[b.Filename] {
			kept = append(kept, b)
		} else {
			removed = append(removed, b)
		}
	}

	// Delete removed backups from disk.
	removedCount := 0
	for _, b := range removed {
		p := filepath.Join(outputDir, b.Filename)
		if err := os.Remove(p); err != nil {
			if !os.IsNotExist(err) {
				logger.WithField("filename", b.Filename).WithField("error", err).
					Error("backup rotation: failed to remove backup")
			}
			continue
		}
		removedCount++
		logger.WithField("filename", b.Filename).WithField("size_bytes", b.Size).
			Info("backup rotation: removed old backup")
	}

	logger.WithField("kept", len(kept)).WithField("removed", removedCount).
		Info("backup rotation completed")

	return &RotationResult{Kept: kept, Removed: removed}, nil
}
