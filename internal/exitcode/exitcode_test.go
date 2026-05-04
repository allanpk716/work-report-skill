package exitcode

import (
	"errors"
	"strings"
	"testing"
)

func TestFromErrorCode_InvalidParams(t *testing.T) {
	codes := []string{"invalid_type", "invalid_body", "invalid_field", "method_not_allowed"}
	for _, code := range codes {
		got := FromErrorCode(code)
		if got != ExitInvalidParams {
			t.Errorf("FromErrorCode(%q) = %d, want %d", code, got, ExitInvalidParams)
		}
	}
}

func TestFromErrorCode_DaemonUnreachable(t *testing.T) {
	got := FromErrorCode("daemon_not_running")
	if got != ExitDaemonUnreachable {
		t.Errorf("FromErrorCode(daemon_not_running) = %d, want %d", got, ExitDaemonUnreachable)
	}
}

func TestFromErrorCode_NetworkError(t *testing.T) {
	codes := []string{"llm_error", "llm_not_configured"}
	for _, code := range codes {
		got := FromErrorCode(code)
		if got != ExitNetworkError {
			t.Errorf("FromErrorCode(%q) = %d, want %d", code, got, ExitNetworkError)
		}
	}
}

func TestFromErrorCode_LockConflict(t *testing.T) {
	got := FromErrorCode("lock_conflict")
	if got != ExitLockConflict {
		t.Errorf("FromErrorCode(lock_conflict) = %d, want %d", got, ExitLockConflict)
	}
}

func TestFromErrorCode_FatalDefault(t *testing.T) {
	codes := []string{
		"storage_error", "record_not_found", "already_completed",
		"already_cancelled", "push_error", "pushover_not_configured",
		"marshal_error", "unknown", "something_unexpected", "",
	}
	for _, code := range codes {
		got := FromErrorCode(code)
		if got != ExitFatalError {
			t.Errorf("FromErrorCode(%q) = %d, want %d", code, got, ExitFatalError)
		}
	}
}

func TestExitError_Error(t *testing.T) {
	err := &ExitError{Code: ExitDaemonUnreachable, Err: errors.New("connection refused")}
	msg := err.Error()
	if !strings.Contains(msg, "exit 3") {
		t.Errorf("Error() should contain 'exit 3', got %q", msg)
	}
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("Error() should contain wrapped message, got %q", msg)
	}
}

func TestExitError_ErrorNil(t *testing.T) {
	err := &ExitError{Code: ExitSuccess, Err: nil}
	msg := err.Error()
	if msg != "exit 0" {
		t.Errorf("Error() with nil Err = %q, want 'exit 0'", msg)
	}
}

func TestExitError_Unwrap(t *testing.T) {
	inner := errors.New("inner error")
	err := &ExitError{Code: 1, Err: inner}
	if unwrapped := err.Unwrap(); unwrapped != inner {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, inner)
	}
}

func TestExitError_UnwrapNil(t *testing.T) {
	err := &ExitError{Code: 0, Err: nil}
	if unwrapped := err.Unwrap(); unwrapped != nil {
		t.Errorf("Unwrap() = %v, want nil", unwrapped)
	}
}

func TestExitError_ImplementsError(t *testing.T) {
	// Compile-time check: ExitError must implement error interface.
	var _ error = &ExitError{}
	var _ error = (*ExitError)(nil)
}
