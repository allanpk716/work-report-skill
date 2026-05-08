package daemon

import (
	"net"
	"testing"
	"time"
)

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
