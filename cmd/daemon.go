package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"wr/internal/config"
	"wr/internal/daemon"
	"wr/internal/jsonl"
	"wr/internal/storage"

	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the wr daemon process",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the wr daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load config
		cfg, err := config.LoadDefault()
		if err != nil {
			return jsonl.Error(fmt.Sprintf("config error: %v", err))
		}

		port := cfg.Daemon.Port
		dataDir := cfg.DataDir

		// Ensure work-records directory exists
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return jsonl.Error(fmt.Sprintf("cannot create data dir %s: %v", dataDir, err))
		}

		// Create storage layer
		store := storage.New(dataDir, log.New(os.Stderr, "[storage] ", log.LstdFlags))

		srv := daemon.NewServer(port, store, cfg)

		dir, err := daemon.DefaultStateDir()
		if err != nil {
			return jsonl.Error(fmt.Sprintf("cannot create state dir: %v", err))
		}

		state := daemon.DaemonState{
			Port: port,
			PID:  os.Getpid(),
		}
		if err := daemon.WriteState(dir, state); err != nil {
			return jsonl.Error(fmt.Sprintf("cannot write state: %v", err))
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Cleanup state file on exit
		defer func() {
			_ = daemon.RemoveState(dir)
		}()

		// Handle signals
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			fmt.Fprintf(os.Stderr, "[daemon] shutting down...\n")
			cancel()
		}()

		fmt.Fprintf(os.Stderr, "[daemon] starting on port %d (pid=%d) data_dir=%s\n", port, os.Getpid(), dataDir)
		return srv.Start(ctx, nil)
	},
}

func init() {
	daemonCmd.AddCommand(daemonStartCmd)
	rootCmd.AddCommand(daemonCmd)
}
