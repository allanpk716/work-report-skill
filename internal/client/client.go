// Package client provides the thin HTTP client for CLI subcommands to communicate with the daemon.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
	"wr/internal/daemon"
)

const clientTimeout = 5 * time.Second

// registry holds the injected ErrorCodeRegistry for mapping daemon error_code
// strings to OS exit codes. Must be set via SetRegistry() before any CallDaemon
// call that may encounter a daemon error envelope.
var registry *agentsdk.ErrorCodeRegistry

// SetRegistry injects the ErrorCodeRegistry into the client package.
// Called from cmd/app.go InitApp() after registerErrorCodes().
func SetRegistry(r *agentsdk.ErrorCodeRegistry) {
	registry = r
}

// CallDaemon reads the daemon state file and sends an HTTP request to the daemon.
// It returns the response body or writes a JSONL error to the writer and returns an error.
func CallDaemon(w io.Writer, method, path string, body io.Reader) error {
	dir, err := daemon.DefaultStateDir()
	if err != nil {
		return writeDaemonError(w, "cannot determine state dir: %v", err)
	}

	state, err := daemon.ReadState(dir)
	if err != nil {
		return writeDaemonError(w, "daemon not running: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d%s", state.Port, path)
	httpClient := &http.Client{Timeout: clientTimeout}

	var req *http.Request
	if body != nil {
		req, err = http.NewRequest(method, url, body)
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		return writeDaemonError(w, "daemon not running: %v", err)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		// State file exists but daemon is unreachable — stale state, clean it up
		_ = daemon.RemoveState(dir)
		return writeDaemonError(w, "daemon not running (stale state cleaned): daemon unreachable at port %d", state.Port)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return writeDaemonError(w, "daemon response read failed: %v", err)
	}

	// Validate it's valid JSONL before forwarding
	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(respBody), &record); err != nil {
		return writeDaemonError(w, "malformed daemon response")
	}

	fmt.Fprintf(w, "%s", respBody)

	// If daemon returned an error envelope, return ExitError with mapped code.
	if record["type"] == "error" {
		errorCode, _ := record["error_code"].(string)
		msg, _ := record["message"].(string)
		exitCode := agentsdk.ExitFatalError
		if registry != nil {
			exitCode = registry.ToExitCode(errorCode)
		}
		return &agentsdk.ExitError{
			Code: exitCode,
			Err:  errors.New(msg),
		}
	}

	return nil
}

// CallDaemonGet is a convenience for GET requests.
func CallDaemonGet(w io.Writer, path string) error {
	return CallDaemon(w, http.MethodGet, path, nil)
}

// CallDaemonPost is a convenience for POST requests with a JSON body.
func CallDaemonPost(w io.Writer, path string, payload interface{}) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return writeDaemonError(w, "marshal request body: %v", err)
		}
		body = bytes.NewReader(b)
	}
	return CallDaemon(w, http.MethodPost, path, body)
}

func writeDaemonError(w io.Writer, format string, args ...interface{}) error {
	msg := fmt.Sprintf(format, args...)
	msg += " Run 'wr agent daemon start' to start the daemon, then retry your command."
	env := agentsdk.NewErrorEnvelope("wr", "daemon_not_running", msg)
	b, _ := json.Marshal(env)
	fmt.Fprintf(w, "%s\n", b)
	return &agentsdk.ExitError{
		Code: agentsdk.ExitNetworkError,
		Err:  errors.New(msg),
	}
}


