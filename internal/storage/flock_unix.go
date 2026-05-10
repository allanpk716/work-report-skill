//go:build !windows

package storage

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// Acquire obtains an exclusive file lock, blocking up to timeout.
// It polls with LOCK_NB until the lock is available or the deadline is reached.
//
// On Unix, flock locks are per-open-file-description. If the same process
// opens the file twice, the second flock attempt will block (or return
// EWOULDBLOCK with LOCK_NB) when the first fd holds the lock, which
// provides correct same-process serialization.
func (lf *LockFile) Acquire(timeout time.Duration) error {
	start := time.Now()

	f, err := os.OpenFile(lf.path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("storage: lock: open %s: %w", lf.path, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			lf.file.Store(f)
			lf.lockAcquired(start)
			return nil
		}
		if err != syscall.EWOULDBLOCK {
			f.Close()
			return fmt.Errorf("storage: lock: flock: %w", err)
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
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	closeErr := f.Close()
	lf.file.Store(nil)
	lf.lockReleased(start)
	if err != nil {
		return fmt.Errorf("storage: lock: unlock: %w", err)
	}
	return closeErr
}
