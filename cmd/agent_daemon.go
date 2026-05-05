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

	agentsdk "github.com/allanpk716/agent-cli-sdk"

	"wr/internal/client"
	"wr/internal/config"
	"wr/internal/daemon"
	"wr/internal/models"
	"wr/internal/pushover"
	"wr/internal/scheduler"
	"wr/internal/storage"

	"github.com/spf13/cobra"
)

// registerAgentDaemonCommands registers the daemon command group under the
// "agent" command tree. Uses Find guard to prevent double-registration in
// tests that call InitApp() repeatedly.
func registerAgentDaemonCommands() {
	agentCmd, _, err := rootCmd.Find([]string{"agent"})
	if err != nil || agentCmd == nil {
		// Agent command tree not yet registered — skip silently.
		// This should not happen in normal flow since registerAgentDaemonCommands
		// is called after rootCmd.AddCommand(app.AgentCommands()).
		return
	}

	// Guard: if "agent daemon" already exists, skip.
	if existing, _, _ := agentCmd.Find([]string{"daemon"}); existing != nil && existing != agentCmd {
		return
	}

	daemonGroupCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the wr daemon process",
	}

	daemonGroupCmd.AddCommand(newDaemonStartCmd())
	daemonGroupCmd.AddCommand(newDaemonStopCmd())
	daemonGroupCmd.AddCommand(newDaemonStatusCmd())

	agentCmd.AddCommand(daemonGroupCmd)
}

func newDaemonStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the wr daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load config
			cfg, err := config.LoadDefault()
			if err != nil {
				return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("config error: %v", err))
			}

			port := cfg.Daemon.Port
			dataDir := cfg.DataDir

			// Ensure work-records directory exists
			if err := os.MkdirAll(dataDir, 0755); err != nil {
				return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("cannot create data dir %s: %v", dataDir, err))
			}

			// Set up sandbox directories and daemon log file
			if err := app.Sandbox().Ensure(); err != nil {
				return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("cannot create sandbox dirs: %v", err))
			}

			logPath := filepath.Join(app.Sandbox().BaseDir(), "daemon.log")
			logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("cannot open daemon log: %v", err))
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
				return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("cannot determine scheduler state path: %v", err))
			}
			sched := scheduler.NewScheduler(cfg, &pushoverBridge{client: pushoverClient}, statePath, log.New(logFile, "[scheduler] ", log.LstdFlags))
			srv.SetScheduler(sched)

			dir, err := daemon.DefaultStateDir()
			if err != nil {
				return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("cannot create state dir: %v", err))
			}

			// Check for existing daemon state
			existingState, stateErr := daemon.ReadState(dir)
			if stateErr == nil {
				// State file exists — check if daemon is actually running on that port
				if daemon.IsPortInUse(existingState.Port) {
					return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("daemon already running on port %d (pid=%d). Run 'wr agent daemon stop' first.", existingState.Port, existingState.PID))
				}
				// Stale state file — port not in use, clean up and proceed
				_ = daemon.RemoveState(dir)
			}

			state := daemon.DaemonState{
				Port: port,
				PID:  os.Getpid(),
			}
			if err := daemon.WriteState(dir, state); err != nil {
				return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("cannot write state: %v", err))
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
				app.JSONL().Warning("daemon shutting down")
				sched.Stop()
				cancel()
			}()

			app.JSONL().Success(map[string]interface{}{
				"status":   "starting",
				"port":     port,
				"pid":      os.Getpid(),
				"data_dir": dataDir,
			})
			return srv.Start(ctx, nil)
		},
	}
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the wr daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := daemon.DefaultStateDir()
			if err != nil {
				return writeExitErrorWithCode(agentsdk.ExitFatalError, "daemon_not_running", fmt.Sprintf("cannot determine state dir: %v", err))
			}

			state, err := daemon.ReadState(dir)
			if err != nil {
				return writeExitErrorWithCode(agentsdk.ExitFatalError, "daemon_not_running", "daemon is not running (no state file found)")
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

			return app.JSONL().Success(fmt.Sprintf("daemon stopped (was pid=%d, port=%d)", state.PID, state.Port))
		},
	}
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Try calling the daemon's /api/status endpoint
			err := client.CallDaemonGet(os.Stdout, "/api/status")
			if err == nil {
				return nil
			}

			// Daemon is not running — output JSONL with status=not_running
			app.JSONL().Success(map[string]interface{}{
				"status":     "not_running",
				"suggestion": "Run 'wr agent daemon start' to start the daemon.",
			})
			return nil
		},
	}
}
