package backup

import (
	"archive/zip"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wr/internal/logger"
)

// BackupMeta describes a completed backup for listing and inspection.
type BackupMeta struct {
	Filename string    `json:"filename"`
	Size     int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// backupItems lists the relative paths (under dataDir) to include in a
// backup archive.
func backupItems() []string {
	return []string{
		"config.json",
		"work-records/",
		"digests.json",
		"scheduler-state.json",
		"logs/",
	}
}

// CreateBackup creates a timestamped zip archive of ~/.work-report/ data in
// outputDir. The archive includes config.json, work-records/, digests.json,
// scheduler-state.json, and logs/. Filename format:
//
//	wr-backup-YYYYMMDD-HHMMSS.zip
//
// If a file with that name already exists, an incrementing suffix is appended
// (wr-backup-YYYYMMDD-HHMMSS-1.zip, …).
//
// Returns the full path of the created archive and its size in bytes.
func CreateBackup(dataDir, outputDir string) (string, int64, error) {
	// Validate data directory exists.
	info, err := os.Stat(dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, fmt.Errorf("backup: data_dir_not_found: %s", dataDir)
		}
		return "", 0, fmt.Errorf("backup: stat data dir %s: %w", dataDir, err)
	}
	if !info.IsDir() {
		return "", 0, fmt.Errorf("backup: data_dir_not_found: %s is not a directory", dataDir)
	}

	// Ensure output directory exists.
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", 0, fmt.Errorf("backup: create output dir %s: %w", outputDir, err)
	}

	// Generate unique filename.
	filename := backupFilename(time.Now(), outputDir)
	zipPath := filepath.Join(outputDir, filename)

	// Create the zip file.
	f, err := os.Create(zipPath)
	if err != nil {
		return "", 0, fmt.Errorf("backup: create zip %s: %w", zipPath, err)
	}

	w := zip.NewWriter(f)
	added := 0

	for _, item := range backupItems() {
		src := filepath.Join(dataDir, item)
		n, err := addToFilesystem(w, src, item)
		if err != nil {
			_ = w.Close()
			_ = f.Close()
			_ = os.Remove(zipPath)
			return "", 0, fmt.Errorf("backup: backup_failed adding %s: %w", item, err)
		}
		added += n
	}

	if err := w.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(zipPath)
		return "", 0, fmt.Errorf("backup: close zip writer: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", 0, fmt.Errorf("backup: close zip file: %w", err)
	}

	// Get file size.
	stat, err := os.Stat(zipPath)
	if err != nil {
		return "", 0, fmt.Errorf("backup: stat result: %w", err)
	}

	logger.WithField("filename", filename).WithField("size_bytes", stat.Size()).WithField("files_added", added).
		Info("backup created")

	return zipPath, stat.Size(), nil
}

// addToFilesystem recursively adds files from src into the zip writer under
// the given prefix. Returns the number of files added.
func addToFilesystem(w *zip.Writer, src, prefix string) (int, error) {
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			// Optional items (e.g. scheduler-state.json) may not exist yet.
			return 0, nil
		}
		return 0, err
	}

	if !info.IsDir() {
		// Single file.
		return addSingleFile(w, src, prefix)
	}

	// Directory — walk it.
	var count int
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		zipName := filepath.Join(prefix, rel)

		n, err := addSingleFile(w, path, zipName)
		if err != nil {
			return err
		}
		count += n
		return nil
	})
	return count, err
}

// addSingleFile writes a single file into the zip archive.
func addSingleFile(w *zip.Writer, absPath, zipName string) (int, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return 0, err
	}

	// Use forward slashes in zip entries for compatibility.
	zipName = filepath.ToSlash(zipName)

	info, err := os.Stat(absPath)
	if err != nil {
		return 0, err
	}
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return 0, err
	}
	hdr.Name = zipName
	hdr.Method = zip.Deflate

	writer, err := w.CreateHeader(hdr)
	if err != nil {
		return 0, err
	}
	if _, err := writer.Write(data); err != nil {
		return 0, err
	}
	return 1, nil
}

// backupFilename generates a timestamped filename, appending an incrementing
// suffix if a file with that name already exists in outputDir.
func backupFilename(t time.Time, outputDir string) string {
	base := fmt.Sprintf("wr-backup-%s.zip", t.Format("20060102-150405"))
	candidate := base

	for i := 1; ; i++ {
		_, err := os.Stat(filepath.Join(outputDir, candidate))
		if os.IsNotExist(err) {
			return candidate
		}
		candidate = fmt.Sprintf("wr-backup-%s-%d.zip", t.Format("20060102-150405"), i)
	}
}

// ListBackups returns metadata for all wr-backup-*.zip files in outputDir,
// sorted newest first.
func ListBackups(outputDir string) ([]BackupMeta, error) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup: list dir %s: %w", outputDir, err)
	}

	var metas []BackupMeta
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "wr-backup-") || !strings.HasSuffix(name, ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		metas = append(metas, BackupMeta{
			Filename:  name,
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
		})
	}

	// Sort newest first.
	for i := 0; i < len(metas)-1; i++ {
		for j := i + 1; j < len(metas); j++ {
			if metas[j].CreatedAt.After(metas[i].CreatedAt) {
				metas[i], metas[j] = metas[j], metas[i]
			}
		}
	}

	return metas, nil
}

// DataDir returns the parent directory that contains work-report data files.
// This is typically ~/.work-report/.
func DataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("backup: cannot determine home dir: %w", err)
	}
	return filepath.Join(home, ".work-report"), nil
}
