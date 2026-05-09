package daemon

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestServerShutdownOnce(t *testing.T) {
	srv, _ := newTestServer(t)

	// Use port 0 to avoid conflicts. Start the server and immediately cancel
	// the context to trigger the shutdown goroutine, then call shutdown()
	// multiple times to verify sync.Once prevents double execution.
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		_ = srv.Start(ctx, func() {
			// onReady: cancel context to trigger shutdown path
			cancel()
		})
	}()

	// Wait for server to start and shut down
	time.Sleep(500 * time.Millisecond)

	// Call shutdown() multiple times — sync.Once should prevent double execution.
	// If we get here without panic/deadlock, sync.Once is working.
	srv.shutdown()
	srv.shutdown()
	srv.shutdown()
}

func TestServerShutdownOnce_Idempotent(t *testing.T) {
	// Verify that calling shutdown on a server that was never started is safe.
	srv, _ := newTestServer(t)

	// Should not panic or hang even with no http.Server assigned.
	srv.shutdown()
	srv.shutdown()
	srv.shutdown()
}

func TestWaitForPortRelease(t *testing.T) {
	// Bind a random free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind test listener: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	port := addr.Port

	// Port is in use — WaitForPortRelease should return false within a short timeout
	released := WaitForPortRelease(port, 300*time.Millisecond)
	if released {
		t.Error("expected port to NOT be released while listener is bound")
	}

	// Close the listener — port should become free
	ln.Close()

	// Poll with a generous timeout — should return true quickly
	released = WaitForPortRelease(port, 2*time.Second)
	if !released {
		t.Error("expected port to be released after listener closed")
	}
}

func TestWaitForPortRelease_Timeout(t *testing.T) {
	// Bind a port that will stay open for the entire test
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind test listener: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	port := addr.Port

	// Use a 100ms timeout — port should still be bound, so this must return false
	start := time.Now()
	released := WaitForPortRelease(port, 100*time.Millisecond)
	elapsed := time.Since(start)

	if released {
		t.Error("expected timeout (port still bound) to return false")
	}
	if elapsed < 80*time.Millisecond {
		t.Errorf("expected ~100ms timeout, but elapsed %v (too fast)", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected ~100ms timeout, but elapsed %v (too slow)", elapsed)
	}
}
