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
	"wr/internal/models"
	"wr/internal/pushover"
	"wr/internal/scheduler"
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

		// Create and configure scheduler for pushover notifications
		pushoverClient := pushover.NewClient()
		statePath, err := scheduler.DefaultStatePath()
		if err != nil {
			return jsonl.Error(fmt.Sprintf("cannot determine scheduler state path: %v", err))
		}
		sched := scheduler.NewScheduler(cfg, &pushoverBridge{client: pushoverClient}, statePath, log.New(os.Stderr, "[scheduler] ", log.LstdFlags))
		srv.SetScheduler(sched)

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

		// Start scheduler
		if err := sched.Start(); err != nil {
			log.Printf("[daemon] scheduler start warning: %v", err)
		}
		defer sched.Stop()

		// Catch up missed reminders after restart
		catchUpRecords, err := store.ListFullRecords(storage.ListOptions{
			RecordType: models.TypeReminder,
		})
		if err != nil {
			log.Printf("[daemon] catchup: failed to list reminders: %v", err)
		} else {
			// Also include tasks/meetings with remind_before
			remindable, err := store.ListFullRecords(storage.ListOptions{})
			if err != nil {
				log.Printf("[daemon] catchup: failed to list all records: %v", err)
			} else {
				// Filter to only those with remind_before or type=reminder
				var filtered []interface{}
				for _, rec := range remindable {
					cf := models.GetCommonFields(rec)
					if cf != nil && (cf.Type == models.TypeReminder || cf.RemindBefore != "") {
						if cf.Status != models.StatusCompleted && cf.Status != models.StatusCancelled {
							filtered = append(filtered, rec)
						}
					}
				}
				catchUpRecords = filtered
			}

			result, err := sched.CatchUp(catchUpRecords)
			if err != nil {
				log.Printf("[daemon] catchup error: %v", err)
			} else {
				log.Printf("[daemon] catchup: scanned=%d fired=%d errors=%d",
					result.Scanned, result.Fired, result.Errors)
			}
		}

		// Handle signals
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			fmt.Fprintf(os.Stderr, "[daemon] shutting down...\n")
			sched.Stop()
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

// pushoverBridge adapts a *pushover.Client to the scheduler.PushoverSender
// interface by converting scheduler.PushoverConfig to pushover.Config.
type pushoverBridge struct {
	client *pushover.Client
}

func (b *pushoverBridge) Send(ctx context.Context, cfg scheduler.PushoverConfig, message, title string, priority int) error {
	return b.client.Send(ctx, pushover.Config{APIToken: cfg.APIToken, UserKey: cfg.UserKey}, message, title, priority)
}
