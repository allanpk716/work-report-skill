package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/backup"
	"wr/internal/config"
	"wr/internal/digest"
	"wr/internal/logger"
	"wr/internal/scheduler"
	"wr/internal/storage"
)

// Server is the HTTP daemon server.
type Server struct {
	port            int
	router          *http.ServeMux
	http            *http.Server
	storage         *storage.Storage
	config          *config.Config
	scheduler       *scheduler.Scheduler
	digestStore     *digest.DigestStore
	digestScheduler *digest.DigestScheduler
	backupScheduler *backup.BackupScheduler
}

// NewServer creates a new daemon server bound to the given port with storage.
func NewServer(port int, store *storage.Storage, cfg *config.Config) *Server {
	s := &Server{
		port:    port,
		router:  http.NewServeMux(),
		storage: store,
		config:  cfg,
	}

	// Initialize digest store backed by DataDir/digests.json.
	if cfg != nil && cfg.DataDir != "" {
		s.digestStore = digest.NewStore(filepath.Join(cfg.DataDir, "digests.json"))
	} else {
		// Fallback: use default path when config is nil (tests).
		if defaultPath, err := digest.DefaultStorePath(); err == nil {
			s.digestStore = digest.NewStore(defaultPath)
		}
	}

	s.registerRoutes()
	return s
}

// Storage returns the server's storage instance.
func (s *Server) Storage() *storage.Storage {
	return s.storage
}

// SetScheduler sets the scheduler instance for the server. Nil-safe.
func (s *Server) SetScheduler(sched *scheduler.Scheduler) {
	s.scheduler = sched
}

// Scheduler returns the server's scheduler instance (may be nil).
func (s *Server) Scheduler() *scheduler.Scheduler {
	return s.scheduler
}

// Port returns the port the server is configured to listen on.
func (s *Server) Port() int {
	return s.port
}

// Config returns the server's configuration.
func (s *Server) Config() *config.Config {
	return s.config
}

// DigestStore returns the server's digest store instance (may be nil if init failed).
func (s *Server) DigestStore() *digest.DigestStore {
	return s.digestStore
}

// SetDigestScheduler sets the digest scheduler instance for the server. Nil-safe.
func (s *Server) SetDigestScheduler(ds *digest.DigestScheduler) {
	s.digestScheduler = ds
}

// DigestScheduler returns the server's digest scheduler instance (may be nil).
func (s *Server) DigestScheduler() *digest.DigestScheduler {
	return s.digestScheduler
}

// SyncDigestScheduler triggers a sync on the digest scheduler (reloads all
// enabled configs from the store and updates cron entries). No-op if the
// scheduler is nil.
func (s *Server) SyncDigestScheduler() {
	if s.digestScheduler != nil {
		s.digestScheduler.Sync()
	}
}

// SetBackupScheduler sets the backup scheduler instance for the server. Nil-safe.
func (s *Server) SetBackupScheduler(bs *backup.BackupScheduler) {
	s.backupScheduler = bs
}

// BackupScheduler returns the server's backup scheduler instance (may be nil).
func (s *Server) BackupScheduler() *backup.BackupScheduler {
	return s.backupScheduler
}

// SyncBackupScheduler triggers a sync on the backup scheduler (reloads the
// backup config from disk and registers/unregisters the cron entry). No-op if
// the scheduler is nil.
func (s *Server) SyncBackupScheduler() {
	if s.backupScheduler != nil {
		s.backupScheduler.Sync()
	}
}

// Router returns the underlying HTTP handler for testing.
func (s *Server) Router() *http.ServeMux {
	return s.router
}

// Start starts the HTTP server and blocks until shutdown signal or context cancel.
// The onReady callback is called once the server is listening.
func (s *Server) Start(ctx context.Context, onReady func()) error {
	s.http = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: s.panicRecoveryMiddleware(s.loggingMiddleware(s.router)),
	}

	// Graceful shutdown on context cancel.
	// Signal handling is owned by the caller (cmd/agent_daemon.go), which
	// cancels the context when SIGINT/SIGTERM is received.
	go func() {
		<-ctx.Done()
		s.shutdown()
	}()

	if onReady != nil {
		onReady()
	}

	logger.WithField("port", s.port).WithField("pid", os.Getpid()).Infof("daemon listening")
	if err := s.http.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("daemon: server error: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) shutdown() {
	logger.Infof("daemon shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.http != nil {
		_ = s.http.Shutdown(ctx)
	}
	logger.Infof("daemon shutdown complete")
}

// loggingMiddleware logs each HTTP request.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.WithField("method", r.Method).WithField("path", r.URL.Path).Debugf("request")
		next.ServeHTTP(w, r)
	})
}

// panicRecoveryMiddleware wraps the handler chain with defer/recover so that
// any panic in a handler (or downstream middleware) is caught and converted
// into a structured JSONL error envelope with error_code "FATAL_CRASH".
func (s *Server) panicRecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				logger.WithField("path", r.URL.Path).Errorf("handler panic: %v", err)
				w.Header().Set("Content-Type", "application/jsonl")
				w.WriteHeader(http.StatusOK)
				agentsdk.NewWriter(w, "wr").ErrorWithCode("FATAL_CRASH", fmt.Sprintf("panic: %v", err))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) registerRoutes() {
	s.router.HandleFunc("/api/stop", s.handleStop)
	s.router.HandleFunc("/health", s.handleHealth)
	s.router.HandleFunc("/api/status", s.handleStatus)
	s.router.HandleFunc("/api/export", s.handleExport)
	s.router.HandleFunc("/api/import", s.handleImport)
	s.router.HandleFunc("/api/add", s.handleAdd)
	s.router.HandleFunc("/api/list", s.handleList)
	s.router.HandleFunc("/api/update/", s.handleUpdate)
	s.router.HandleFunc("/api/complete/", s.handleComplete)
	s.router.HandleFunc("/api/cancel/", s.handleCancel)
	s.router.HandleFunc("/api/report", s.handleReport)
	s.router.HandleFunc("/api/report/today", s.handleReportToday)
	s.router.HandleFunc("/api/report/push/today", s.handleReportPushToday)
	s.router.HandleFunc("/api/report/push/date/", s.handleReportPushDate)
	s.router.HandleFunc("/api/report/week", s.handleReportWeek)
	s.router.HandleFunc("/api/report/range", s.handleReportRange)
	s.router.HandleFunc("/api/report/push/week", s.handleReportPushWeek)
	s.router.HandleFunc("/api/report/push/range", s.handleReportPushRange)

	// Digest CRUD endpoints
	s.router.HandleFunc("/api/digest/add", s.handleDigestAdd)
	s.router.HandleFunc("/api/digest/list", s.handleDigestList)
	s.router.HandleFunc("/api/digest/remove/", s.handleDigestRemove)
	s.router.HandleFunc("/api/digest/enable/", s.handleDigestEnable)
	s.router.HandleFunc("/api/digest/disable/", s.handleDigestDisable)

	// Prompt CRUD endpoints
	s.router.HandleFunc("/api/prompt/list", s.handlePromptList)
	s.router.HandleFunc("/api/prompt/show/", s.handlePromptShow)
	s.router.HandleFunc("/api/prompt/set/", s.handlePromptSet)
	s.router.HandleFunc("/api/prompt/reset/", s.handlePromptReset)

	// Preview endpoints
	s.router.HandleFunc("/api/digest/preview/", s.handleDigestPreview)
	s.router.HandleFunc("/api/prompt/preview/", s.handlePromptPreview)

	// Backup scheduler endpoint
	s.router.HandleFunc("/api/backup/sync", s.handleBackupSync)
}
