package cmd

import (
	"os"

	"github.com/spf13/cobra"
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

// Execute runs the root command.
// Errors are written as JSONL to stdout by the command handlers.
// No stderr output — the JSONL-only contract forbids it.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
