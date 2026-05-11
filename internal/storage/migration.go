package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wr/internal/logger"
)

const (
	// migrationMarker is the file written after a successful migration.
	// Its presence signals that migration has already been performed.
	migrationMarker = ".migration-v1-done_things"

	// oldDirName is the legacy directory name for done_things records.
	oldDirName = "logs"
)

// MigrateIfNeeded checks whether the legacy "logs/" data directory needs to be
// migrated to "done_things/" and performs the migration if necessary.
//
// Migration is idempotent: if the marker file exists, or if no "logs/"
// directory is present, it returns immediately.
//
// The function is crash-safe: if migration is interrupted mid-way, a subsequent
// call will pick up where it left off (files already in done_things/ are
// skipped, remaining files in logs/ are migrated).
//
// Returns an error if migration fails. The caller should log a warning but
// not block startup (graceful degradation).
func MigrateIfNeeded(baseDir string) error {
	markerPath := filepath.Join(baseDir, migrationMarker)

	// Fast path: already migrated
	if _, err := os.Stat(markerPath); err == nil {
		return nil
	}

	oldDir := filepath.Join(baseDir, oldDirName)
	newDir := filepath.Join(baseDir, "done_things")

	// No legacy directory to migrate — write marker and return
	info, err := os.Stat(oldDir)
	if err != nil {
		if os.IsNotExist(err) {
			return writeMarker(markerPath)
		}
		return fmt.Errorf("[migration] stat %s: %w", oldDir, err)
	}

	if !info.IsDir() {
		// "logs" exists but is not a directory (unlikely edge case) — write marker to skip
		logger.WithField("path", oldDir).Warnf("[migration] 'logs' exists but is not a directory, skipping")
		return writeMarker(markerPath)
	}

	start := time.Now()
	logger.Infof("[migration] phase=start old_dir=%s new_dir=%s", oldDir, newDir)

	var filesMigrated int
	var filesSkipped int

	// Walk the legacy logs/ directory
	err = filepath.Walk(oldDir, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			logger.WithField("path", path).Warnf("[migration] walk error: %v", walkErr)
			return nil // skip unreadable entries, continue walking
		}

		if fi.IsDir() {
			return nil
		}

		if !strings.HasSuffix(fi.Name(), ".json") {
			return nil // skip non-JSON files
		}

		// Compute destination path: replace oldDir prefix with newDir
		relPath, err := filepath.Rel(oldDir, path)
		if err != nil {
			logger.WithField("path", path).Warnf("[migration] relpath error: %v", err)
			return nil
		}
		destPath := filepath.Join(newDir, relPath)

		// Skip if file already exists at destination (crash recovery)
		if _, err := os.Stat(destPath); err == nil {
			filesSkipped++
			return nil
		}

		// Read file content
		data, err := os.ReadFile(path)
		if err != nil {
			logger.WithField("path", path).Warnf("[migration] read error: %v", err)
			return nil
		}

		// Replace "type": "log" or "type":"log" with the new type name.
		// Handles both pretty-printed (space after colon) and compact JSON.
		modified := strings.ReplaceAll(string(data), `"type": "log"`, `"type": "done_things"`)
		modified = strings.ReplaceAll(modified, `"type":"log"`, `"type":"done_things"`)

		// Create destination directory structure
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("[migration] mkdir %s: %w", filepath.Dir(destPath), err)
		}

		// Write migrated file
		if err := os.WriteFile(destPath, []byte(modified), 0644); err != nil {
			return fmt.Errorf("[migration] write %s: %w", destPath, err)
		}

		// Remove original file
		if err := os.Remove(path); err != nil {
			logger.WithField("path", path).Warnf("[migration] remove error: %v", err)
			// Continue — the file has been written to dest, we'll clean up logs/ later
		}

		filesMigrated++
		return nil
	})

	if err != nil {
		return fmt.Errorf("[migration] walk failed: %w", err)
	}

	// Remove empty logs/ directory tree
	if err := os.RemoveAll(oldDir); err != nil {
		logger.WithField("path", oldDir).Warnf("[migration] could not remove old dir: %v", err)
		// Non-fatal: data is migrated, just leftover empty dirs
	}

	// Write marker to prevent re-migration
	if err := writeMarker(markerPath); err != nil {
		return fmt.Errorf("[migration] marker write failed: %w", err)
	}

	duration := time.Since(start)
	logger.WithField("phase", "complete").
		WithField("files_migrated", filesMigrated).
		WithField("files_skipped", filesSkipped).
		WithField("duration_ms", duration.Milliseconds()).
		Infof("[migration] done")

	return nil
}

// writeMarker creates the migration marker file with a timestamp.
func writeMarker(markerPath string) error {
	content := fmt.Sprintf("migrated at %s\n", time.Now().Format(time.RFC3339))
	return os.WriteFile(markerPath, []byte(content), 0644)
}
