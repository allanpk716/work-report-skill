package daemon

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"

	"wr/internal/config"
	"wr/internal/scheduler"
	"wr/internal/storage"
)

const DefaultPort = 17530

// Server is the HTTP daemon server.
type Server struct {
	port      int
	router    *http.ServeMux
	http      *http.Server
	storage   *storage.Storage
	config    *config.Config
	scheduler *scheduler.Scheduler
}

// NewServer creates a new daemon server bound to the given port with storage.
func NewServer(port int, store *storage.Storage, cfg *config.Config) *Server {
	s := &Server{
		port:    port,
		router:  http.NewServeMux(),
		storage: store,
		config:  cfg,
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

	// Graceful shutdown on context cancel or signals
	go func() {
		<-ctx.Done()
		s.shutdown()
	}()

	// Signal handler for SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		s.shutdown()
	}()

	if onReady != nil {
		onReady()
	}

	log.Printf("[daemon] listening on :%d (pid=%d)", s.port, os.Getpid())
	if err := s.http.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("daemon: server error: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.http.Shutdown(ctx)
}

// loggingMiddleware logs each HTTP request.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[daemon] %s %s", r.Method, r.URL.Path)
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
}
