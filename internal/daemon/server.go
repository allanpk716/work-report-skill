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
)

const DefaultPort = 17530

// Server is the HTTP daemon server.
type Server struct {
	port   int
	router *http.ServeMux
	http   *http.Server
}

// NewServer creates a new daemon server bound to the given port.
func NewServer(port int) *Server {
	s := &Server{
		port:   port,
		router: http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

// Port returns the port the server is configured to listen on.
func (s *Server) Port() int {
	return s.port
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
		Handler: s.loggingMiddleware(s.router),
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

func (s *Server) registerRoutes() {
	s.router.HandleFunc("/health", s.handleHealth)
	s.router.HandleFunc("/api/add", s.handleAdd)
	s.router.HandleFunc("/api/list", s.handleList)
	s.router.HandleFunc("/api/complete/", s.handleComplete)
	s.router.HandleFunc("/api/cancel/", s.handleCancel)
	s.router.HandleFunc("/api/report", s.handleReport)
	s.router.HandleFunc("/api/report/today", s.handleReportToday)
}
