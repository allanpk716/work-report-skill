package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"wr/internal/daemon"
	"wr/internal/exitcode"
	"wr/internal/jsonl"

	"github.com/spf13/cobra"
)

var importFilePath string

// importTimeout is longer than the default client timeout because bulk imports
// may take significantly more time than single-record operations.
const importTimeout = 30 * time.Second

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Bulk import work report entries from a JSON file",
	RunE: func(cmd *cobra.Command, args []string) error {
		if importFilePath == "" {
			return writeExitErrorWithCode(exitcode.ExitInvalidParams, "invalid_params", "--file is required")
		}

		data, err := os.ReadFile(importFilePath)
		if err != nil {
			return writeExitErrorWithCode(exitcode.ExitInvalidParams, "invalid_body",
				fmt.Sprintf("cannot read file %q: %v", importFilePath, err))
		}

		payload, err := normalizeImportPayload(data)
		if err != nil {
			return writeExitErrorWithCode(exitcode.ExitInvalidParams, "invalid_body",
				fmt.Sprintf("invalid JSON in %q: %v", importFilePath, err))
		}

		return callDaemonPostWithTimeout(os.Stdout, "/api/import", payload, importTimeout)
	},
}

// normalizeImportPayload validates JSON input and normalizes it into the
// expected {"records": [...]} format. Accepts either a top-level array or
// an object with a "records" key.
func normalizeImportPayload(data []byte) (interface{}, error) {
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	switch v := raw.(type) {
	case []interface{}:
		return map[string]interface{}{"records": v}, nil
	case map[string]interface{}:
		if _, ok := v["records"]; !ok {
			return nil, fmt.Errorf("object must contain a \"records\" key")
		}
		return v, nil
	default:
		return nil, fmt.Errorf("expected JSON array or object, got %T", raw)
	}
}

// callDaemonPostWithTimeout sends a POST request to the daemon with a custom
// timeout. Used by import to allow longer processing for bulk operations.
func callDaemonPostWithTimeout(w io.Writer, path string, payload interface{}, timeout time.Duration) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return writeDaemonError(w, "marshal request body: %v", err)
	}

	dir, err := daemon.DefaultStateDir()
	if err != nil {
		return writeDaemonError(w, "cannot determine state dir: %v", err)
	}

	state, err := daemon.ReadState(dir)
	if err != nil {
		return writeDaemonError(w, "daemon not running: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d%s", state.Port, path)
	httpClient := &http.Client{Timeout: timeout}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return writeDaemonError(w, "daemon not running: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		_ = daemon.RemoveState(dir)
		return writeDaemonError(w, "daemon not running (stale state cleaned): daemon unreachable at port %d", state.Port)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return writeDaemonError(w, "daemon response read failed: %v", err)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(respBody), &record); err != nil {
		return writeDaemonError(w, "malformed daemon response")
	}

	fmt.Fprintf(w, "%s", respBody)

	if record["type"] == "error" {
		errorCode, _ := record["error_code"].(string)
		msg, _ := record["message"].(string)
		return &exitcode.ExitError{
			Code: exitcode.FromErrorCode(errorCode),
			Err:  fmt.Errorf("%s", msg),
		}
	}

	return nil
}

// writeExitErrorWithCode writes a JSONL error envelope with a specific error code
// and returns an ExitError. This is used by import for CLI-side validation errors
// that should carry the same error_code the daemon would use.
func writeExitErrorWithCode(exitCode int, errorCode string, msg string) error {
	jsonl.DefaultWriter.ErrorWithCode(errorCode, msg)
	return &exitcode.ExitError{Code: exitCode, Err: fmt.Errorf("%s", msg)}
}

// writeDaemonError reuses the client package error pattern locally for the
// extended-timeout import path. This avoids exporting an internal helper.
func writeDaemonError(w io.Writer, format string, args ...interface{}) error {
	msg := fmt.Sprintf(format, args...)
	msg += " Run 'wr daemon start' to start the daemon, then retry your command."
	env := jsonl.ErrorEnvelope("daemon_not_running", msg)
	b, _ := json.Marshal(env)
	fmt.Fprintf(w, "%s\n", b)
	return &exitcode.ExitError{
		Code: exitcode.ExitDaemonUnreachable,
		Err:  fmt.Errorf("%s", msg),
	}
}

func init() {
	importCmd.Flags().StringVar(&importFilePath, "file", "", "Path to JSON file containing records to import")

	rootCmd.AddCommand(importCmd)
}
