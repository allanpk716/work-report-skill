package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/client"
	"wr/internal/config"
	"wr/internal/daemon"
	"wr/internal/models"
	"wr/internal/pushover"
	"wr/internal/scheduler"
	"wr/internal/storage"

	"github.com/spf13/cobra"
)

// newDaemonGroupCmd creates the "agent daemon" command group with start/stop/status/ensure-running subcommands.
// This command is passed to AgentCommands(extra...) so it's registered under the agent tree
// without requiring Find() guard logic.
func newDaemonGroupCmd() *cobra.Command {
	daemonGroupCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the wr daemon process",
	}

	daemonGroupCmd.AddCommand(newDaemonStartCmd())
	daemonGroupCmd.AddCommand(newDaemonStopCmd())
	daemonGroupCmd.AddCommand(newDaemonStatusCmd())
	daemonGroupCmd.AddCommand(newDaemonEnsureRunningCmd())

	return daemonGroupCmd
}

func newDaemonStartCmd() *cobra.Command {
	var detach bool
	var internalDaemonize bool

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the wr daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if internalDaemonize {
				return runDaemon(true)
			}
			if detach {
				return startDetached()
			}
			return runDaemon(false)
		},
	}

	cmd.Flags().BoolVar(&detach, "detach", false, "Start daemon in background and return immediately")
	cmd.Flags().BoolVar(&internalDaemonize, "internal-daemonize", false, "Internal flag: run as background daemon child process")
	_ = cmd.Flags().MarkHidden("internal-daemonize")

	return cmd
}

// runDaemon contains the core daemon startup logic extracted from the RunE handler.
// When suppressStartupMsg is true (internal-daemonize mode), the "starting" JSONL
// message is suppressed so the parent process only sees the final detached response.
func runDaemon(suppressStartupMsg bool) error {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start scheduler
	if err := sched.Start(); err != nil {
		log.Printf("[daemon] scheduler start warning: %v", err)
	}
	defer sched.Stop()

	// Catch up missed reminders after restart — run in a goroutine so the HTTP
	// server binds immediately.  Pushover retries with bad credentials can take
	// 35+ s; without async catchup the daemon would be unreachable during that
	// window, breaking status checks and UAT tests.
	go func() {
		catchUpRecords, err := store.ListFullRecords(storage.ListOptions{
			RecordType: models.TypeReminder,
		})
		if err != nil {
			log.Printf("[daemon] catchup: failed to list reminders: %v", err)
			return
		}
		// Also include tasks/meetings with remind_before
		remindable, err := store.ListFullRecords(storage.ListOptions{})
		if err != nil {
			log.Printf("[daemon] catchup: failed to list all records: %v", err)
			return
		}
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

		result, err := sched.CatchUp(catchUpRecords)
		if err != nil {
			log.Printf("[daemon] catchup error: %v", err)
		} else {
			log.Printf("[daemon] catchup: scanned=%d fired=%d errors=%d",
				result.Scanned, result.Fired, result.Errors)
		}
	}()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		app.JSONL().Warning("daemon shutting down")
		sched.Stop()
		cancel()
	}()

	if !suppressStartupMsg {
		app.JSONL().Success(map[string]interface{}{
			"status":   "starting",
			"port":     port,
			"pid":      os.Getpid(),
			"data_dir": dataDir,
		})
	}
	return srv.Start(ctx, func() {
		// Write state file AFTER the HTTP server is listening so that status
		// polls never see a state file pointing at an unbound port (which
		// triggers stale-state cleanup and deletes the file).
		if err := daemon.WriteState(dir, state); err != nil {
			log.Printf("[daemon] warning: cannot write state: %v", err)
		}
	})
}

// startDetached spawns the daemon as a background child process, polls until the
// port is bound (up to 10s), then returns a JSONL success response with pid/port.
func startDetached() error {
	// Load config to determine port
	cfg, err := config.LoadDefault()
	if err != nil {
		return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("config error: %v", err))
	}
	port := cfg.Daemon.Port

	dir, err := daemon.DefaultStateDir()
	if err != nil {
		return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("cannot determine state dir: %v", err))
	}

	// Check if daemon is already running
	existingState, stateErr := daemon.ReadState(dir)
	if stateErr == nil && daemon.IsPortInUse(existingState.Port) {
		return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("daemon already running on port %d (pid=%d). Run 'wr agent daemon stop' first.", existingState.Port, existingState.PID))
	}
	// Clean up stale state if port not in use
	if stateErr == nil {
		_ = daemon.RemoveState(dir)
	}

	// Spawn child process with --internal-daemonize
	child := exec.Command(os.Args[0], "agent", "daemon", "start", "--internal-daemonize")
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("cannot open devnull: %v", err))
	}
	defer devNull.Close()
	child.Stdout = devNull
	child.Stderr = devNull

	if err := child.Start(); err != nil {
		return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("failed to spawn daemon: %v", err))
	}

	// Poll until port is bound or timeout
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(10 * time.Second)

	for {
		select {
		case <-ticker.C:
			if daemon.IsPortInUse(port) {
				// Daemon is ready — read state to get pid
				state, readErr := daemon.ReadState(dir)
				if readErr != nil {
					// Port is bound but state file not yet written; retry on next tick
					continue
				}
				return app.JSONL().Success(map[string]interface{}{
					"status": "running",
					"pid":    state.PID,
					"port":   state.Port,
				})
			}
		case <-timeout:
			_ = child.Process.Kill()
			return writeExitErrorWithCode(agentsdk.ExitFatalError, "daemon_start_timeout",
				fmt.Sprintf("daemon failed to start within 10 seconds on port %d", port))
		}
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
			// CallDaemonGet writes a JSONL error to stdout and returns ExitError
			// when the daemon is unreachable — just propagate the error, no double write.
			return client.CallDaemonGet(os.Stdout, "/api/status")
		},
	}
}

// newDaemonEnsureRunningCmd creates the "agent daemon ensure-running" command.
// Unlike "start --detach" (which errors when already running), this is idempotent:
// it returns success whether the daemon was already running or just started.
// The response includes a "source" field ("already_running" or "started") so the
// agent knows what happened.
func newDaemonEnsureRunningCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ensure-running",
		Short: "Ensure the daemon is running (start if needed) and return status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEnsureRunning()
		},
	}
}

// runEnsureRunning implements the ensure-running logic:
// 1. Check if daemon is already running (state file + port check).
// 2. If running: call /api/status, wrap with source=already_running, return success.
// 3. If not running: clean stale state, start daemon detached, wait for ready,
//    call /api/status, wrap with source=started, return success.
// 4. On start timeout: return daemon_start_timeout error.
func runEnsureRunning() error {
	cfg, err := config.LoadDefault()
	if err != nil {
		return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("config error: %v", err))
	}
	port := cfg.Daemon.Port

	dir, err := daemon.DefaultStateDir()
	if err != nil {
		return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("cannot determine state dir: %v", err))
	}

	// Check if daemon is already running.
	existingState, stateErr := daemon.ReadState(dir)
	if stateErr == nil && daemon.IsPortInUse(existingState.Port) {
		// Daemon is running — proxy to /api/status.
		return proxyStatusWithSource("already_running", existingState)
	}

	// Not running — clean up stale state and start.
	if stateErr == nil {
		_ = daemon.RemoveState(dir)
	}

	// Spawn child process with --internal-daemonize.
	child := exec.Command(os.Args[0], "agent", "daemon", "start", "--internal-daemonize")
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("cannot open devnull: %v", err))
	}
	defer devNull.Close()
	child.Stdout = devNull
	child.Stderr = devNull

	if err := child.Start(); err != nil {
		return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("failed to spawn daemon: %v", err))
	}

	// Poll until port is bound or timeout.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(10 * time.Second)

	for {
		select {
		case <-ticker.C:
			if daemon.IsPortInUse(port) {
				state, readErr := daemon.ReadState(dir)
				if readErr != nil {
					// Port bound but state file not yet written; retry next tick.
					continue
				}
				return proxyStatusWithSource("started", state)
			}
		case <-timeout:
			_ = child.Process.Kill()
			return writeExitErrorWithCode(agentsdk.ExitFatalError, "daemon_start_timeout",
				fmt.Sprintf("daemon failed to start within 10 seconds on port %d", port))
		}
	}
}

// proxyStatusWithSource calls the daemon's /api/status endpoint and wraps the
// response with a "source" field indicating whether the daemon was already
// running or just started. Output goes through app.JSONL() for consistent capture.
func proxyStatusWithSource(source string, state daemon.DaemonState) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/status", state.Port)
	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Get(url)
	if err != nil {
		return writeExitErrorWithCode(agentsdk.ExitNetworkError, "daemon_not_running",
			fmt.Sprintf("daemon status check failed: %v", err))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("failed to read daemon response: %v", err))
	}

	// Parse the JSONL envelope from the daemon.
	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(respBody), &record); err != nil {
		return writeExitError(agentsdk.ExitFatalError, "malformed daemon response")
	}

	// If the daemon returned an error, forward it.
	if record["type"] == "error" {
		errorCode, _ := record["error_code"].(string)
		msg, _ := record["message"].(string)
		return writeExitErrorWithCode(app.Registry().ToExitCode(errorCode), errorCode, msg)
	}

	// Extract data from daemon response and inject source/pid/port.
	data, ok := record["data"].(map[string]interface{})
	if !ok {
		data = make(map[string]interface{})
	}
	data["source"] = source
	data["pid"] = state.PID
	data["port"] = state.Port

	return app.JSONL().Success(data)
}


