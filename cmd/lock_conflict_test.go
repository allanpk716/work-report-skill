package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/storage"
)

// TestWriteStorageErrorLockConflict verifies that writeStorageError emits a
// lock_conflict error code with retry_after_ms in the JSONL data field when
// the error is storage.ErrLockConflict.
func TestWriteStorageErrorLockConflict(t *testing.T) {
	if app == nil {
		InitApp()
	}

	// Capture JSONL output
	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	// Also capture stdout (writeStorageError writes raw JSONL for lock_conflict)
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w

	err := writeStorageError("test operation", storage.ErrLockConflict)

	w.Close()
	os.Stdout = oldStdout
	stdoutData, _ := io.ReadAll(r)

	// Combine outputs
	allOutput := buf.String() + string(stdoutData)

	// Verify ExitError
	var exitErr *agentsdk.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != agentsdk.ExitLockConflict {
		t.Errorf("expected exit code %d (ExitLockConflict), got %d", agentsdk.ExitLockConflict, exitErr.Code)
	}

	// Parse JSONL lines to find the lock_conflict envelope
	lines := strings.Split(strings.TrimSpace(allOutput), "\n")
	var foundLockConflict bool
	for _, line := range lines {
		if line == "" {
			continue
		}
		var env map[string]interface{}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue
		}
		code, _ := env["error_code"].(string)
		if code != "lock_conflict" {
			continue
		}
		foundLockConflict = true

		// Verify retry_after_ms is in data field
		data, ok := env["data"].(map[string]interface{})
		if !ok {
			t.Errorf("lock_conflict envelope missing data field")
			continue
		}
		retryMs, ok := data["retry_after_ms"].(float64)
		if !ok {
			t.Errorf("lock_conflict data missing retry_after_ms field")
			continue
		}
		if int(retryMs) != lockRetryAfterMs {
			t.Errorf("expected retry_after_ms=%d, got %d", lockRetryAfterMs, int(retryMs))
		}
	}

	if !foundLockConflict {
		t.Errorf("no lock_conflict envelope found in output: %s", allOutput)
	}
}

// TestWriteStorageErrorGeneric verifies that writeStorageError falls back to
// storage_error for non-lock errors.
func TestWriteStorageErrorGeneric(t *testing.T) {
	if app == nil {
		InitApp()
	}

	var buf strings.Builder
	origWriter := app.JSONL()
	app.SetWriter(agentsdk.NewWriter(&buf, "wr"))
	defer func() { app.SetWriter(origWriter) }()

	err := writeStorageError("test operation", errors.New("some other error"))

	var exitErr *agentsdk.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	// storage_error maps to ExitFatalError (1)
	if exitErr.Code != agentsdk.ExitFatalError {
		t.Errorf("expected exit code %d (ExitFatalError), got %d", agentsdk.ExitFatalError, exitErr.Code)
	}

	lines := parseJSONLMaps([]byte(buf.String()))
	found := false
	for _, line := range lines {
		if code, _ := line["error_code"].(string); code == "lock_conflict" {
			t.Error("should not emit lock_conflict for generic errors")
			found = true
		}
	}
	if !found {
		// Expected: no lock_conflict in output for generic error
	}
}

// TestWithLockRetrySuccess verifies that withLockRetry returns immediately on
// success without any retries.
func TestWithLockRetrySuccess(t *testing.T) {
	calls := 0
	err := withLockRetry(func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

// TestWithLockRetryNonLockError verifies that withLockRetry does not retry
// on non-lock errors.
func TestWithLockRetryNonLockError(t *testing.T) {
	calls := 0
	genericErr := errors.New("not a lock error")
	err := withLockRetry(func() error {
		calls++
		return genericErr
	})
	if !errors.Is(err, genericErr) {
		t.Errorf("expected generic error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call (no retry for non-lock errors), got %d", calls)
	}
}

// TestWithLockRetryExhaustsRetries verifies that withLockRetry retries the
// correct number of times on ErrLockConflict before returning the error.
func TestWithLockRetryExhaustsRetries(t *testing.T) {
	// Use short backoffs for test speed
	origBackoffs := lockRetryBackoffs
	lockRetryBackoffs = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond}
	defer func() { lockRetryBackoffs = origBackoffs }()

	calls := 0
	err := withLockRetry(func() error {
		calls++
		return storage.ErrLockConflict
	})
	if !errors.Is(err, storage.ErrLockConflict) {
		t.Errorf("expected ErrLockConflict, got %v", err)
	}
	// Initial attempt + 2 retries = 3 calls
	if calls != 3 {
		t.Errorf("expected 3 calls (1 initial + 2 retries), got %d", calls)
	}
}

// TestWithLockRetryRecoversOnSecondAttempt verifies that withLockRetry succeeds
// when the second attempt returns nil.
func TestWithLockRetryRecoversOnSecondAttempt(t *testing.T) {
	origBackoffs := lockRetryBackoffs
	lockRetryBackoffs = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond}
	defer func() { lockRetryBackoffs = origBackoffs }()

	calls := 0
	err := withLockRetry(func() error {
		calls++
		if calls < 2 {
			return storage.ErrLockConflict
		}
		return nil
	})
	if err != nil {
		t.Errorf("expected nil error after recovery, got %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

// TestWithLockRetryResultSuccess verifies the generic result-returning variant.
func TestWithLockRetryResultSuccess(t *testing.T) {
	result, err := withLockRetryResult(func() (string, error) {
		return "hello", nil
	})
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

// TestWithLockRetryResultRetries verifies the generic result-returning variant
// retries on ErrLockConflict.
func TestWithLockRetryResultRetries(t *testing.T) {
	origBackoffs := lockRetryBackoffs
	lockRetryBackoffs = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond}
	defer func() { lockRetryBackoffs = origBackoffs }()

	calls := 0
	result, err := withLockRetryResult(func() (int, error) {
		calls++
		if calls < 3 {
			return 0, storage.ErrLockConflict
		}
		return 42, nil
	})
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if result != 42 {
		t.Errorf("expected 42, got %d", result)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}
