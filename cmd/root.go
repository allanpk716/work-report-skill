package cmd

import (
	"fmt"
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
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
