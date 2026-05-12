package cmd

import (
	"errors"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"github.com/spf13/cobra"
)

var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "wr",
	Short: "wr — work report CLI for AI agents",
	Long:  `wr is a pure CLI tool for AI agents to manage work reports. Single binary, local filesystem storage, JSONL output. No daemon, no HTTP layer.`,
}

func init() {
	rootCmd.Version = version
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
}

// Execute runs the root command via SDK app.Execute() and returns an OS exit code.
func Execute() int {
	return app.Execute(rootCmd)
}

// writeExitError writes a JSONL error envelope via the SDK Writer
// and returns an ExitError with the given code.
func writeExitError(code int, msg string) error {
	app.JSONL().Error(msg)
	return &agentsdk.ExitError{Code: code, Err: errors.New(msg)}
}
