// Package exitcode defines semantic exit codes for the wr CLI.
// AI agents can inspect the OS exit code to quickly determine the result
// category without parsing JSONL output:
//
//	0 — success
//	1 — fatal/internal error
//	2 — invalid parameters (bad type, body, field)
//	3 — daemon unreachable (not running, stale state, connection refused)
//	4 — network/LLM error (LLM call failed or not configured)
//	5 — lock conflict (concurrent access)
package exitcode

import "fmt"

// Semantic exit codes.
const (
	ExitSuccess          = 0
	ExitFatalError       = 1
	ExitInvalidParams    = 2
	ExitDaemonUnreachable = 3
	ExitNetworkError     = 4
	ExitLockConflict     = 5
)

// ExitError wraps an error with a semantic exit code.
// Commands return this so cmd.Execute() can propagate the code to os.Exit().
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("exit %d: %v", e.Code, e.Err)
	}
	return fmt.Sprintf("exit %d", e.Code)
}

func (e *ExitError) Unwrap() error {
	return e.Err
}

// FromErrorCode maps a daemon error_code string to an OS exit code.
func FromErrorCode(code string) int {
	switch code {
	case "invalid_type", "invalid_body", "invalid_field", "method_not_allowed":
		return ExitInvalidParams
	case "daemon_not_running":
		return ExitDaemonUnreachable
	case "llm_error", "llm_not_configured":
		return ExitNetworkError
	case "lock_conflict":
		return ExitLockConflict
	case "FATAL_CRASH":
		// Panic recovery — always maps to fatal error exit code
		return ExitFatalError
	default:
		// storage_error, record_not_found, already_completed,
		// already_cancelled, push_error, pushover_not_configured,
		// marshal_error, unknown, and anything unexpected
		return ExitFatalError
	}
}
