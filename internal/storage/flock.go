package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"wr/internal/logger"
)

// DefaultLockTimeout is how long Acquire waits before returning ErrLockConflict.
const DefaultLockTimeout = 5 * time.Second

// lockPollInterval is the sleep duration between non-blocking lock attempts.
const lockPollInterval = 50 * time.Millisecond

// ErrLockConflict is returned when the file lock cannot be acquired within the timeout.
var ErrLockConflict = fmt.Errorf("storage: lock conflict: timed out acquiring file lock")

// LockFile provides cross-process file locking for write serialization.
// It uses platform-specific implementations selected via Go build tags.
// The file handle is stored using atomic operations so that concurrent
// goroutines within the same process can safely contend on the same LockFile.
type LockFile struct {
	path string
	file atomic.Pointer[os.File]
}

// NewLockFile creates a LockFile whose lock will be placed in baseDir.
func NewLockFile(baseDir string) *LockFile {
	return &LockFile{path: filepath.Join(baseDir, ".lock")}
}

// LockPath returns the path to the lock file for a given base directory.
func LockPath(baseDir string) string {
	return filepath.Join(baseDir, ".lock")
}

// Path returns the lock file path.
func (lf *LockFile) Path() string {
	return lf.path
}

// lockAcquired logs a successful lock acquisition with timing.
func (lf *LockFile) lockAcquired(start time.Time) {
	logger.WithField("lock_file", lf.path).
		WithField("duration_ms", time.Since(start).Milliseconds()).
		Info("file lock acquired")
}

// lockTimedOut logs a lock timeout event.
func (lf *LockFile) lockTimedOut() {
	logger.WithField("lock_file", lf.path).Warn("file lock acquisition timed out")
}

// lockReleased logs a successful lock release with timing.
func (lf *LockFile) lockReleased(start time.Time) {
	logger.WithField("lock_file", lf.path).
		WithField("duration_ms", time.Since(start).Milliseconds()).
		Info("file lock released")
}
