package cmd

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"wr/internal/config"
	"wr/internal/daemon"
	"wr/internal/exitcode"
	"wr/internal/jsonl"
	"wr/internal/models"
	"wr/internal/pushover"
	"wr/internal/sandbox"
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
			return writeExitError(exitcode.ExitFatalError, fmt.Sprintf("config error: %v", err))
		}

		port := cfg.Daemon.Port
		dataDir := cfg.DataDir

		// Ensure work-records directory exists
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return writeExitError(exitcode.ExitFatalError, fmt.Sprintf("cannot create data dir %s: %v", dataDir, err))
		}

		// Set up sandbox directories and daemon log file
		home, err := os.UserHomeDir()
		if err != nil {
			return writeExitError(exitcode.ExitFatalError, fmt.Sprintf("cannot determine home dir: %v", err))
		}
		baseDir := filepath.Join(home, ".work-report")

		if err := sandbox.EnsureSandboxDirs(baseDir); err != nil {
			return writeExitError(exitcode.ExitFatalError, fmt.Sprintf("cannot create sandbox dirs: %v", err))
		}

		logFile, err := sandbox.OpenDaemonLog(baseDir)
		if err != nil {
			return writeExitError(exitcode.ExitFatalError, fmt.Sprintf("cannot open daemon log: %v", err))
		}
		defer logFile.Close()

		// Redirect all default log.Printf output to the daemon log file
		log.SetOutput(logFile)

		// Create storage layer
		store := storage.New(dataDir, log.New(logFile, "[storage] ", log.LstdFlags))

		srv := daemon.NewServer(port, store, cfg)

		// Create and configure scheduler for pushover notifications
		pushoverClient := pushover.NewClient()
		statePath, err := scheduler.DefaultStatePath()
		if err != nil {
			return writeExitError(exitcode.ExitFatalError, fmt.Sprintf("cannot determine scheduler state path: %v", err))
		}
		sched := scheduler.NewScheduler(cfg, &pushoverBridge{client: pushoverClient}, statePath, log.New(logFile, "[scheduler] ", log.LstdFlags))
		srv.SetScheduler(sched)

		dir, err := daemon.DefaultStateDir()
		if err != nil {
			return writeExitError(exitcode.ExitFatalError, fmt.Sprintf("cannot create state dir: %v", err))
		}

		// Check for existing daemon state
		existingState, stateErr := daemon.ReadState(dir)
		if stateErr == nil {
			// State file exists — check if daemon is actually running on that port
			if daemon.IsPortInUse(existingState.Port) {
				return writeExitError(exitcode.ExitFatalError, fmt.Sprintf("daemon already running on port %d (pid=%d). Run 'wr daemon stop' first.", existingState.Port, existingState.PID))
			}
			// Stale state file — port not in use, clean up and proceed
			_ = daemon.RemoveState(dir)
		}

		state := daemon.DaemonState{
			Port: port,
			PID:  os.Getpid(),
		}
		if err := daemon.WriteState(dir, state); err != nil {
			return writeExitError(exitcode.ExitFatalError, fmt.Sprintf("cannot write state: %v", err))
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

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
			jsonl.Warning("daemon shutting down")
			sched.Stop()
			cancel()
		}()

		jsonl.Success(map[string]interface{}{
			"status":   "starting",
			"port":     port,
			"pid":      os.Getpid(),
			"data_dir": dataDir,
		})
		return srv.Start(ctx, nil)
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the wr daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := daemon.DefaultStateDir()
		if err != nil {
			return writeExitError(exitcode.ExitFatalError, fmt.Sprintf("cannot determine state dir: %v", err))
		}

		state, err := daemon.ReadState(dir)
		if err != nil {
			return writeExitError(exitcode.ExitFatalError, "daemon is not running (no state file found)")
		}

		// Try graceful shutdown via HTTP first
		url := fmt.Sprintf("http://127.0.0.1:%d/api/stop", state.Port)
		httpClient := &http.Client{Timeout: 3 * time.Second}
		resp, err := httpClient.Post(url, "", nil)
		if err == nil {
			resp.Body.Close()
			// Wait a moment for graceful shutdown
			time.Sleep(500 * time.Millisecond)
		}

		// If still running, force kill
		if daemon.IsPortInUse(state.Port) {
			proc, err := os.FindProcess(state.PID)
			if err == nil {
				_ = proc.Signal(syscall.SIGTERM)
				time.Sleep(500 * time.Millisecond)
			}
			if daemon.IsPortInUse(state.Port) {
				_ = proc.Kill()
				time.Sleep(300 * time.Millisecond)
			}
		}

		// Always clean up state file
		_ = daemon.RemoveState(dir)

		return jsonl.Success(fmt.Sprintf("daemon stopped (was pid=%d, port=%d)", state.PID, state.Port))
	},
}

func init() {
	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
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
