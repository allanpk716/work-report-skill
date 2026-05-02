package pushover

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSend_Success(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("content-type = %s, want application/x-www-form-urlencoded", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotBody = r.Form.Encode()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Override pushoverURL for testing
	origURL := pushoverURL
	pushoverURL = srv.URL
	defer func() { pushoverURL = origURL }()

	cfg := Config{APIToken: "test-token-abc", UserKey: "test-user-xyz"}
	err := Send(context.Background(), cfg, "hello world", "Test Title", 1)
	if err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}

	parsed, _ := url.ParseQuery(gotBody)
	if got := parsed.Get("token"); got != "test-token-abc" {
		t.Errorf("token = %q, want %q", got, "test-token-abc")
	}
	if got := parsed.Get("user"); got != "test-user-xyz" {
		t.Errorf("user = %q, want %q", got, "test-user-xyz")
	}
	if got := parsed.Get("message"); got != "hello world" {
		t.Errorf("message = %q, want %q", got, "hello world")
	}
	if got := parsed.Get("title"); got != "Test Title" {
		t.Errorf("title = %q, want %q", got, "Test Title")
	}
	if got := parsed.Get("priority"); got != "1" {
		t.Errorf("priority = %q, want %q", got, "1")
	}
}

func TestSend_NotConfigured(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"empty token", Config{APIToken: "", UserKey: "some-key"}},
		{"empty user key", Config{APIToken: "some-token", UserKey: ""}},
		{"both empty", Config{APIToken: "", UserKey: ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Send(context.Background(), tt.cfg, "msg", "title", 0)
			if !errors.Is(err, ErrNotConfigured) {
				t.Errorf("Send() = %v, want ErrNotConfigured", err)
			}
		})
	}
}

func TestSend_RetryOnFailure(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&calls, 1)
		if call < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origURL := pushoverURL
	pushoverURL = srv.URL
	defer func() { pushoverURL = origURL }()

	cfg := Config{APIToken: "tok", UserKey: "usr"}
	err := Send(context.Background(), cfg, "msg", "title", 0)
	if err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestSend_AllRetriesFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	origURL := pushoverURL
	pushoverURL = srv.URL
	defer func() { pushoverURL = origURL }()

	cfg := Config{APIToken: "tok", UserKey: "usr"}
	err := Send(context.Background(), cfg, "msg", "title", 0)
	if err == nil {
		t.Fatal("Send() = nil, want error")
	}
	if !strings.Contains(err.Error(), "after 3 retries") {
		t.Errorf("error = %q, want to contain 'after 3 retries'", err.Error())
	}
}

func TestSend_Timeout(t *testing.T) {
	// Use a pre-closed server to trigger immediate connection refused.
	// This tests the retry/error-wrapping path without waiting for real timeouts.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// slow handler — will never be reached since we close the server
		time.Sleep(30 * time.Second)
	}))
	srv.Close() // close immediately to trigger connection errors

	origURL := pushoverURL
	pushoverURL = srv.URL
	defer func() { pushoverURL = origURL }()

	cfg := Config{APIToken: "tok", UserKey: "usr"}
	err := Send(context.Background(), cfg, "msg", "title", 0)
	if err == nil {
		t.Fatal("Send() = nil, want error")
	}
	if !strings.Contains(err.Error(), "after 3 retries") {
		t.Errorf("error = %q, want to contain 'after 3 retries'", err.Error())
	}
}

func TestSend_EmptyMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if msg := r.FormValue("message"); msg != "" {
			t.Errorf("message = %q, want empty", msg)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origURL := pushoverURL
	pushoverURL = srv.URL
	defer func() { pushoverURL = origURL }()

	cfg := Config{APIToken: "tok", UserKey: "usr"}
	err := Send(context.Background(), cfg, "", "title", 0)
	if err != nil {
		t.Fatalf("Send(empty message) = %v, want nil", err)
	}
}

func TestSend_ContextCancellation(t *testing.T) {
	// Server returns 500 so retries happen; cancel context during retry delay.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	origURL := pushoverURL
	pushoverURL = srv.URL
	defer func() { pushoverURL = origURL }()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	cfg := Config{APIToken: "tok", UserKey: "usr"}
	err := Send(ctx, cfg, "msg", "title", 0)
	if err == nil {
		t.Fatal("Send() = nil, want error")
	}
}
