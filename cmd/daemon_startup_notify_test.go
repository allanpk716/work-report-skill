package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"wr/internal/pushover"
)

// TestStartupNotify_SendsPushover verifies that sendStartupNotification
// POSTs to the Pushover API with a message containing hostname, port, and PID.
func TestStartupNotify_SendsPushover(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotBody = r.Form.Encode()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origURL := pushover.PushoverURL()
	pushover.SetPushoverURL(srv.URL)
	defer pushover.SetPushoverURL(origURL)

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	sendStartupNotificationCtx(context.Background(), "testhost", 9999, 12345, pushover.Config{
		APIToken: "test-api-token",
		UserKey:  "test-user-key",
	}, log)

	parsed, _ := url.ParseQuery(gotBody)

	// Verify Pushover credentials
	if got := parsed.Get("token"); got != "test-api-token" {
		t.Errorf("token = %q, want %q", got, "test-api-token")
	}
	if got := parsed.Get("user"); got != "test-user-key" {
		t.Errorf("user = %q, want %q", got, "test-user-key")
	}

	// Verify message contains hostname, port, PID
	msg := parsed.Get("message")
	if !strings.Contains(msg, "testhost") {
		t.Errorf("message missing hostname: %q", msg)
	}
	if !strings.Contains(msg, "9999") {
		t.Errorf("message missing port: %q", msg)
	}
	if !strings.Contains(msg, "12345") {
		t.Errorf("message missing pid: %q", msg)
	}

	// Verify title
	if got := parsed.Get("title"); got != "wr daemon 已上线" {
		t.Errorf("title = %q, want %q", got, "wr daemon 已上线")
	}

	// Verify priority
	if got := parsed.Get("priority"); got != "0" {
		t.Errorf("priority = %q, want %q", got, "0")
	}
}

// TestStartupNotify_EmptyHostnameUsesUnknown verifies that an empty hostname
// is replaced with "unknown" in the notification message.
func TestStartupNotify_EmptyHostnameUsesUnknown(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotBody = r.Form.Encode()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origURL := pushover.PushoverURL()
	pushover.SetPushoverURL(srv.URL)
	defer pushover.SetPushoverURL(origURL)

	log := logrus.New()

	sendStartupNotificationCtx(context.Background(), "", 8080, 1, pushover.Config{
		APIToken: "tok",
		UserKey:  "key",
	}, log)

	parsed, _ := url.ParseQuery(gotBody)
	msg := parsed.Get("message")
	if !strings.Contains(msg, "unknown") {
		t.Errorf("message should contain 'unknown' for empty hostname, got: %q", msg)
	}
}

// TestStartupNotify_SkipsWhenNotConfigured verifies that with empty Pushover
// credentials the function returns without error (ErrNotConfigured is handled
// silently at debug level).
func TestStartupNotify_SkipsWhenNotConfigured(t *testing.T) {
	// No httptest server needed — Send returns ErrNotConfigured immediately.

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	// Empty config — should not panic or error
	sendStartupNotification("myhost", 3000, 42, pushover.Config{
		APIToken: "",
		UserKey:  "",
	}, log)

	// If we get here without panic, the test passes.
	// The function handles ErrNotConfigured internally with a debug log.
}

// TestStartupNotify_FailureNonBlocking verifies that when the Pushover API
// returns an error (HTTP 500), the function does not panic or block.
// Uses a short context timeout to bound test runtime.
func TestStartupNotify_FailureNonBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	origURL := pushover.PushoverURL()
	pushover.SetPushoverURL(srv.URL)
	defer pushover.SetPushoverURL(origURL)

	log := logrus.New()

	// Use a short timeout so the test finishes quickly even though
	// pushover.Send retries with increasing delays.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sendStartupNotificationCtx(ctx, "failhost", 4000, 99, pushover.Config{
		APIToken: "tok",
		UserKey:  "key",
	}, log)

	// If we get here, the function returned despite Pushover failure.
}
