//go:build windows

package storage

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// Acquire obtains an exclusive file lock, blocking up to timeout.
// It polls with LOCKFILE_FAIL_IMMEDIATELY until the lock is available or
// the deadline is reached.
//
// On Windows, LockFileEx locks are associated with file handles, so
// separate handles to the same file from the same or different processes
// will correctly contend.
func (lf *LockFile) Acquire(timeout time.Duration) error {
	start := time.Now()

	f, err := os.OpenFile(lf.path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("storage: lock: open %s: %w", lf.path, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		var overlapped windows.Overlapped
		err := windows.LockFileEx(
			windows.Handle(f.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0, 1, 0, &overlapped,
		)
		if err == nil {
			lf.file.Store(f)
			lf.lockAcquired(start)
			return nil
		}
		if err != windows.ERROR_LOCK_VIOLATION {
			f.Close()
			return fmt.Errorf("storage: lock: LockFileEx: %w", err)
		}
		if time.Now().After(deadline) {
			f.Close()
			lf.lockTimedOut()
			return ErrLockConflict
		}
		time.Sleep(lockPollInterval)
	}
}

// Release releases the file lock and closes the file handle.
// If the lock was never acquired, this is a no-op.
func (lf *LockFile) Release() error {
	start := time.Now()
	f := lf.file.Load()
	if f == nil {
		return nil
	}
	var overlapped windows.Overlapped
	err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
	closeErr := f.Close()
	lf.file.Store(nil)
	lf.lockReleased(start)
	if err != nil {
		return fmt.Errorf("storage: lock: unlock: %w", err)
	}
	return closeErr
}
