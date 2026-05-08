package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"wr/internal/digest"
	"wr/internal/logger"
	"wr/internal/models"
	"wr/internal/pushover"
	"wr/internal/scheduler"
	"wr/internal/storage"

	"github.com/sirupsen/logrus"
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

	logDir := filepath.Join(app.Sandbox().BaseDir(), "logs")
	if err := logger.Init(logDir); err != nil {
		return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("cannot init logger: %v", err))
	}
	defer logger.Shutdown()
	defer logger.Infof("daemon exiting")

	logger.WithFields(logrus.Fields{"port": port, "pid": os.Getpid(), "data_dir": dataDir}).Infof("daemon started")

	// Create storage layer
	store := storage.New(dataDir)

	srv := daemon.NewServer(port, store, cfg)

	// Create and configure scheduler for pushover notifications
	pushoverClient := pushover.NewClient()
	statePath, err := scheduler.DefaultStatePath()
	if err != nil {
		return writeExitError(agentsdk.ExitFatalError, fmt.Sprintf("cannot determine scheduler state path: %v", err))
	}
	sched := scheduler.NewScheduler(cfg, &pushoverBridge{client: pushoverClient}, statePath)
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
		logger.Warnf("scheduler start warning: %v", err)
	}
	defer sched.Stop()

	// Initialize and start digest scheduler (T03).
	// The digest scheduler has its own cron instance, separate from the
	// record-based scheduler above (D008). On startup it syncs all enabled
	// digest configs from the store. Failures are non-fatal — the daemon
	// still runs without digest scheduling.
	if srv.DigestStore() != nil {
		digestSched := digest.NewDigestScheduler(srv.DigestStore(), func(cfg digest.DigestConfig) {
			// The callback builds a SummarizeInput and runs the pipeline.
			// LLM/Pushover config comes from the daemon's config.
			logger.WithField("digest_id", cfg.ID).
				WithField("scope", string(cfg.Scope)).
				WithField("direction", string(cfg.Direction)).
				Info("[digest-daemon] cron triggered, invoking summarize pipeline")

			// Construct adapters from daemon dependencies.
			loc := srv.Config().Location()
			storageAdapter := digest.NewStorageAdapter(srv.Storage(), loc)
			if storageAdapter == nil {
				logger.WithField("digest_id", cfg.ID).Error("[digest-daemon] storage adapter is nil, skipping digest")
				return
			}

			llmCfg := srv.Config().LLM.Text
			llmTimeout := time.Duration(llmCfg.Timeout) * time.Second
			if llmTimeout == 0 {
				llmTimeout = 60 * time.Second
			}
			llmAdapter := digest.NewLLMAdapter(llmCfg.APIBase, llmCfg.APIKey, llmCfg.Model, llmTimeout)

			pushCfg := pushover.Config{
				APIToken: srv.Config().Pushover.APIToken,
				UserKey:  srv.Config().Pushover.UserKey,
			}

			input := digest.SummarizeInput{
				DigestID:     cfg.ID,
				Scope:        cfg.Scope,
				Direction:    cfg.Direction,
				CustomRange:  "", // cron-triggered digests use configured scope
				Loc:          loc,
				Storage:      storageAdapter,
				LLM:          llmAdapter, // nil if config incomplete → raw markdown fallback
				PushCfg:      pushCfg,
				PushPriority: 0,
			}

			if err := digest.Summarize(context.Background(), input); err != nil {
				logger.WithField("digest_id", cfg.ID).
					WithField("error", err).
					Error("[digest-daemon] summarize pipeline failed")
			} else {
				logger.WithField("digest_id", cfg.ID).Info("[digest-daemon] summarize pipeline complete")
			}
		})
		srv.SetDigestScheduler(digestSched)
		digestSched.Start()
		digestSched.Sync()
		logger.WithField("entry_count", digestSched.RegisteredEntries()).Info("[digest-daemon] digest scheduler initialized")
		defer func() {
			digestSched.Stop()
			logger.Info("[digest-daemon] digest scheduler stopped")
		}()
	} else {
		logger.Warn("[digest-daemon] digest store not available, digest scheduler disabled")
	}

	// Catch up missed reminders after restart — run in a goroutine so the HTTP
	// server binds immediately.  Pushover retries with bad credentials can take
	// 35+ s; without async catchup the daemon would be unreachable during that
	// window, breaking status checks and UAT tests.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.WithField("goroutine", "catchup").Errorf("panic recovered: %v", r)
			}
		}()
		catchUpRecords, err := store.ListFullRecords(storage.ListOptions{
			RecordType: models.TypeReminder,
		})
		if err != nil {
			logger.Warnf("catchup: failed to list reminders: %v", err)
			return
		}
		// Also include tasks/meetings with remind_before
		remindable, err := store.ListFullRecords(storage.ListOptions{})
		if err != nil {
			logger.Warnf("catchup: failed to list all records: %v", err)
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
			logger.Errorf("catchup error: %v", err)
		} else {
			logger.Infof("catchup: scanned=%d fired=%d errors=%d",
				result.Scanned, result.Fired, result.Errors)
		}
	}()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.WithField("signal", sig).Warnf("daemon received signal")
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
			logger.Warnf("warning: cannot write state: %v", err)
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

			// Phase 1: Try graceful shutdown via HTTP POST /api/stop.
			url := fmt.Sprintf("http://127.0.0.1:%d/api/stop", state.Port)
			httpClient := &http.Client{Timeout: 3 * time.Second}
			resp, err := httpClient.Post(url, "", nil)
			if err == nil {
				resp.Body.Close()
				logger.Infof("stop: http stop sent to port %d", state.Port)
			}

			// Phase 2: If port still in use after HTTP stop, send SIGTERM.
			if daemon.IsPortInUse(state.Port) {
				proc, err := os.FindProcess(state.PID)
				if err == nil {
					_ = proc.Signal(syscall.SIGTERM)
					logger.Infof("stop: sigterm sent to pid %d", state.PID)
				}

				// Phase 3: Poll for port release with 5s timeout.
				if daemon.WaitForPortRelease(state.Port, 5*time.Second) {
					logger.Infof("stop: port %d released after sigterm", state.Port)
				} else {
					// Phase 4: Port still occupied after 5s — SIGKILL as last resort.
					logger.Warnf("stop: port %d not released after 5s, sending sigkill to pid %d", state.Port, state.PID)
					_ = proc.Kill()
					time.Sleep(100 * time.Millisecond)
				}
			} else {
				logger.Infof("stop: port %d released after http stop", state.Port)
			}

			// Always clean up state file.
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


