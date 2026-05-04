// Package sandbox manages the work-report sandbox directory structure and
// daemon log file. It provides helpers for creating the canonical
// subdirectories (locks/, crash_dumps/, cache/) under a base directory and
// opening the daemon log file for append-only writing.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
)

// Canonical subdirectory names under the sandbox base directory.
const (
	DirLocks     = "locks"
	DirCrashDumps = "crash_dumps"
	DirCache     = "cache"
)

// sandboxDirs is the list of subdirectories EnsureSandboxDirs creates.
var sandboxDirs = []string{DirLocks, DirCrashDumps, DirCache}

// EnsureSandboxDirs creates the locks/, crash_dumps/, and cache/
// subdirectories under baseDir. Each directory is created with mode 0755.
// The operation is idempotent — no error is returned if the directories
// already exist.
func EnsureSandboxDirs(baseDir string) error {
	for _, sub := range sandboxDirs {
		path := filepath.Join(baseDir, sub)
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("sandbox: cannot create directory %s: %w", path, err)
		}
	}
	return nil
}

// OpenDaemonLog opens or creates the daemon.log file in baseDir for
// append-only writing. The file is created with mode 0644 if it does not
// exist. The parent directory is created if needed. The caller is
// responsible for closing the returned file handle (typically on daemon
// shutdown).
func OpenDaemonLog(baseDir string) (*os.File, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("sandbox: cannot create base dir %s: %w", baseDir, err)
	}

	path := DaemonLogPath(baseDir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("sandbox: cannot open daemon log %s: %w", path, err)
	}
	return f, nil
}

// DaemonLogPath returns the full path to the daemon.log file under baseDir.
func DaemonLogPath(baseDir string) string {
	return filepath.Join(baseDir, "daemon.log")
}
