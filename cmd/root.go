package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"wr/internal/exitcode"
	"wr/internal/jsonl"
)

var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "wr",
	Short: "wr — work report CLI for AI agents",
	Long:  `wr is a CLI tool for AI agents to manage work reports via a local HTTP daemon. All output is JSONL format.`,
}

func init() {
	rootCmd.Version = version
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
}

// Execute runs the root command and returns an OS exit code.
// Callers should pass the return value to os.Exit().
// Errors are written as JSONL to stdout by the command handlers.
// No stderr output — the JSONL-only contract forbids it.
// Panics are caught via recover() and emitted as FATAL_CRASH JSONL envelopes.
func Execute() (code int) {
	defer func() {
		if r := recover(); r != nil {
			jsonl.DefaultWriter.ErrorWithCode("FATAL_CRASH", fmt.Sprintf("panic: %v", r))
			code = exitcode.ExitFatalError
		}
	}()
	if err := rootCmd.Execute(); err != nil {
		var exitErr *exitcode.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.Code
		}
		return exitcode.ExitFatalError
	}
	return exitcode.ExitSuccess
}

// writeExitError writes a JSONL error envelope to the jsonl default writer
// and returns an ExitError with the given code. Command handlers use this
// to both produce user-visible output and propagate the exit code.
func writeExitError(code int, msg string) error {
	jsonl.Error(msg)
	return &exitcode.ExitError{Code: code, Err: errors.New(msg)}
}
