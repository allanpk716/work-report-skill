// Package pushover implements a client for the Pushover Messages API
// with automatic retry on failure.
//
// Sensitive fields (API token, user key) are never included in log output.
package pushover

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNotConfigured is returned when Pushover credentials are missing.
var ErrNotConfigured = errors.New("pushover: not configured (api_token or user_key is empty)")

// Config holds the Pushover credentials needed to send notifications.
// These values are sensitive and must never be echoed in log output.
type Config struct {
	APIToken string
	UserKey  string
}

const (
	httpTimeout    = 10 * time.Second
	maxRetries     = 3
	retryDelayBase = 5 * time.Second
)

// pushoverURL is the Pushover Messages API endpoint. Package-level variable
// to allow override in tests.
var pushoverURL = "https://api.pushover.net/1/messages.json"

// Send posts a notification to Pushover with the given message, title and priority.
// It retries up to 3 times with increasing delays (5s, 15s, 30s) on failure.
// Returns ErrNotConfigured if the config has empty APIToken or UserKey.
func Send(ctx context.Context, cfg Config, message, title string, priority int) error {
	if cfg.APIToken == "" || cfg.UserKey == "" {
		return ErrNotConfigured
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := retryDelayBase * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				return fmt.Errorf("pushover: context cancelled during retry delay: %w", ctx.Err())
			case <-time.After(delay):
			}
		}

		lastErr = doSend(ctx, cfg, message, title, priority)
		if lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("pushover: send failed after %d retries: %w", maxRetries, lastErr)
}

// doSend performs a single HTTP POST to the Pushover API.
func doSend(ctx context.Context, cfg Config, message, title string, priority int) error {
	form := url.Values{}
	form.Set("token", cfg.APIToken)
	form.Set("user", cfg.UserKey)
	form.Set("message", message)
	form.Set("title", title)
	form.Set("priority", fmt.Sprintf("%d", priority))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pushoverURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	// Drain body to allow connection reuse
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pushover api returned status %d", resp.StatusCode)
	}
	return nil
}

// Client is a thin wrapper around the package-level Send function.
// It satisfies the scheduler.PushoverSender interface via its Send method.
type Client struct{}

// NewClient returns a new Pushover client.
func NewClient() *Client { return &Client{} }

// Send delegates to the package-level Send function.
func (c *Client) Send(ctx context.Context, cfg Config, message, title string, priority int) error {
	return Send(ctx, cfg, message, title, priority)
}

// NoopLogger returns a logger that discards all output, useful in tests.
func NoopLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}
