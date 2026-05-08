package cmd

import (
	"encoding/json"
	"net/http"
	"testing"

	agentsdk "github.com/allanpk716/ai-agent-cli-rules/sdks/go"
)

// TestPromptListSuccess verifies that "wr prompt list" calls the daemon and
// returns a successful JSONL envelope.
func TestPromptListSuccess(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request hits the correct endpoint
		if r.URL.Path != "/api/prompt/list" {
			t.Errorf("expected path /api/prompt/list, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}

		env := agentsdk.NewResultEnvelope("wr", map[string]interface{}{
			"prompts": []map[string]interface{}{
				{"name": "agenda", "is_default": true, "text": "default agenda prompt"},
				{"name": "report", "is_default": true, "text": "default report prompt"},
			},
		})
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		w.Write(append(b, '\n'))
	}))
	defer srv.Close()

	code, out := executeCmd("prompt", "list")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "result" {
		t.Errorf("expected type=result, got %v", lines[0]["type"])
	}

	validateAllEnvelopes(t, out)
}

// TestPromptShowSuccess verifies that "wr prompt show <name>" calls the daemon.
func TestPromptShowSuccess(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/prompt/show/agenda" {
			t.Errorf("expected path /api/prompt/show/agenda, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}

		env := agentsdk.NewResultEnvelope("wr", map[string]interface{}{
			"name":      "agenda",
			"text":      "default agenda prompt",
			"is_default": true,
		})
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		w.Write(append(b, '\n'))
	}))
	defer srv.Close()

	code, out := executeCmd("prompt", "show", "agenda")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "result" {
		t.Errorf("expected type=result, got %v", lines[0]["type"])
	}

	validateAllEnvelopes(t, out)
}

// TestPromptSetWithText verifies that "wr prompt set <name> --text ..." sends
// a POST with the text payload.
func TestPromptSetWithText(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/prompt/set/agenda" {
			t.Errorf("expected path /api/prompt/set/agenda, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		if body["text"] != "custom agenda text" {
			t.Errorf("expected text='custom agenda text', got %q", body["text"])
		}
		if body["file"] != "" {
			t.Errorf("expected no file field, got %q", body["file"])
		}

		env := agentsdk.NewResultEnvelope("wr", map[string]interface{}{
			"action":  "set",
			"name":    "agenda",
			"message": "prompt updated",
		})
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		w.Write(append(b, '\n'))
	}))
	defer srv.Close()

	code, out := executeCmd("prompt", "set", "agenda", "--text", "custom agenda text")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	validateAllEnvelopes(t, out)
}

// TestPromptSetWithFile verifies that "wr prompt set <name> --file ..." sends
// a POST with the file path payload.
func TestPromptSetWithFile(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		if body["file"] != "/tmp/prompt.txt" {
			t.Errorf("expected file='/tmp/prompt.txt', got %q", body["file"])
		}
		if body["text"] != "" {
			t.Errorf("expected no text field, got %q", body["text"])
		}

		env := agentsdk.NewResultEnvelope("wr", map[string]interface{}{
			"action":  "set",
			"name":    "report",
			"message": "prompt updated",
		})
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		w.Write(append(b, '\n'))
	}))
	defer srv.Close()

	code, out := executeCmd("prompt", "set", "report", "--file", "/tmp/prompt.txt")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	validateAllEnvelopes(t, out)
}

// TestPromptSetMissingFlags verifies that "wr prompt set <name>" without
// --text or --file returns exit code 2 (invalid params).
func TestPromptSetMissingFlags(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	// No need for a fake daemon — the CLI should fail before making the call
	code, out := executeCmd("prompt", "set", "agenda")
	if code != agentsdk.ExitInvalidParams {
		t.Errorf("expected exit code %d (ExitInvalidParams), got %d; output: %s", agentsdk.ExitInvalidParams, code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL error output")
	}
	if lines[0]["type"] != "error" {
		t.Errorf("expected type=error, got %v", lines[0]["type"])
	}
}

// TestPromptResetSuccess verifies that "wr prompt reset <name>" sends a POST.
func TestPromptResetSuccess(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/prompt/reset/agenda" {
			t.Errorf("expected path /api/prompt/reset/agenda, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		env := agentsdk.NewResultEnvelope("wr", map[string]interface{}{
			"action":  "reset",
			"name":    "agenda",
			"message": "prompt reset to default",
		})
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		w.Write(append(b, '\n'))
	}))
	defer srv.Close()

	code, out := executeCmd("prompt", "reset", "agenda")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	validateAllEnvelopes(t, out)
}

// TestPromptNotFound verifies that prompt_not_found daemon error maps to
// exit code 2 (invalid params, as registered in errors.go).
func TestPromptNotFound(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := agentsdk.NewErrorEnvelope("wr", "prompt_not_found", "prompt \"unknown\" not found")
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		w.Write(append(b, '\n'))
	}))
	defer srv.Close()

	code, out := executeCmd("prompt", "show", "unknown")
	if code != agentsdk.ExitInvalidParams {
		t.Errorf("expected exit code %d (ExitInvalidParams), got %d; output: %s", agentsdk.ExitInvalidParams, code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL error output")
	}
	found := false
	for _, line := range lines {
		if line["error_code"] == "prompt_not_found" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error_code=prompt_not_found in output, got %v", lines)
	}

	validateAllEnvelopes(t, out)
}

// TestPromptCommandRegistration verifies that the prompt command and its
// subcommands are properly registered with the root command.
func TestPromptCommandRegistration(t *testing.T) {
	if app == nil {
		InitApp()
	}

	// Verify parent command
	prompt, _, err := rootCmd.Find([]string{"prompt"})
	if err != nil {
		t.Fatalf("prompt command not registered: %v", err)
	}
	if prompt.Use != "prompt" {
		t.Errorf("expected Use='prompt', got %q", prompt.Use)
	}

	// Verify subcommands exist
	for _, sub := range []string{"list", "show", "set", "reset", "preview"} {
		cmd, _, err := rootCmd.Find([]string{"prompt", sub})
		if err != nil {
			t.Errorf("prompt %s subcommand not registered: %v", sub, err)
		}
		if cmd == nil {
			continue
		}
		if cmd.Use != sub {
			// cobra Use includes args template (e.g. "show <name>"), so check prefix
			if len(cmd.Use) < len(sub) || cmd.Use[:len(sub)] != sub {
				t.Errorf("expected prompt %s Use to start with %q, got %q", sub, sub, cmd.Use)
			}
		}
	}
}

// --- Preview command tests ---

// TestDigestPreviewSuccess verifies that "wr digest preview <id>" hits the
// correct daemon endpoint and returns JSONL output.
func TestDigestPreviewSuccess(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/digest/preview/abc123" {
			t.Errorf("expected path /api/digest/preview/abc123, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}

		env := agentsdk.NewResultEnvelope("wr", map[string]interface{}{
			"digest_id": "abc123",
			"summary":   "Today you worked on implementing preview commands...",
			"llm_status": "success",
		})
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		w.Write(append(b, '\n'))
	}))
	defer srv.Close()

	code, out := executeCmd("digest", "preview", "abc123")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "result" {
		t.Errorf("expected type=result, got %v", lines[0]["type"])
	}

	validateAllEnvelopes(t, out)
}

// TestDigestPreviewMissingArg verifies that "wr digest preview" without an ID
// argument fails with a usage error (cobra validation).
func TestDigestPreviewMissingArg(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	code, _ := executeCmd("digest", "preview")
	// Cobra reports ExactArgs(1) violation as exit code 1
	if code == agentsdk.ExitSuccess {
		t.Error("expected non-zero exit code for missing argument")
	}
}

// TestPromptPreviewSuccess verifies that "wr prompt preview <name>" hits the
// correct daemon endpoint with default scope=today.
func TestPromptPreviewSuccess(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/prompt/preview/agenda" {
			t.Errorf("expected path /api/prompt/preview/agenda, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		// Default scope should be "today"
		scope := r.URL.Query().Get("scope")
		if scope != "today" {
			t.Errorf("expected scope=today, got %q", scope)
		}

		env := agentsdk.NewResultEnvelope("wr", map[string]interface{}{
			"prompt_name": "agenda",
			"scope":       "today",
			"preview":     "## Agenda Preview\n- Task 1\n- Task 2",
			"llm_status":  "success",
		})
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		w.Write(append(b, '\n'))
	}))
	defer srv.Close()

	code, out := executeCmd("prompt", "preview", "agenda")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	lines := parseJSONLMaps(out)
	if len(lines) == 0 {
		t.Fatal("expected JSONL output")
	}
	if lines[0]["type"] != "result" {
		t.Errorf("expected type=result, got %v", lines[0]["type"])
	}

	validateAllEnvelopes(t, out)
}

// TestPromptPreviewWithScope verifies that "wr prompt preview <name> --scope week"
// passes the scope as a query parameter.
func TestPromptPreviewWithScope(t *testing.T) {
	tmpHome, cleanup := setupTempHome(t)
	defer cleanup()

	srv := setupFakeDaemon(t, tmpHome, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope := r.URL.Query().Get("scope")
		if scope != "week" {
			t.Errorf("expected scope=week, got %q", scope)
		}

		env := agentsdk.NewResultEnvelope("wr", map[string]interface{}{
			"prompt_name": "report",
			"scope":       "week",
			"preview":     "Weekly summary preview...",
			"llm_status":  "success",
		})
		b, _ := json.Marshal(env)
		w.Header().Set("Content-Type", "application/jsonl")
		w.Write(append(b, '\n'))
	}))
	defer srv.Close()

	code, out := executeCmd("prompt", "preview", "report", "--scope", "week")
	if code != agentsdk.ExitSuccess {
		t.Errorf("expected exit code 0, got %d; output: %s", code, string(out))
	}

	validateAllEnvelopes(t, out)
}

// TestPromptPreviewMissingArg verifies that "wr prompt preview" without a name
// argument fails with a usage error.
func TestPromptPreviewMissingArg(t *testing.T) {
	_, cleanup := setupTempHome(t)
	defer cleanup()

	code, _ := executeCmd("prompt", "preview")
	if code == agentsdk.ExitSuccess {
		t.Error("expected non-zero exit code for missing argument")
	}
}

// TestDigestPreviewRegistration verifies that the digest preview command is
// properly registered.
func TestDigestPreviewRegistration(t *testing.T) {
	if app == nil {
		InitApp()
	}

	cmd, _, err := rootCmd.Find([]string{"digest", "preview"})
	if err != nil {
		t.Fatalf("digest preview subcommand not registered: %v", err)
	}
	if cmd == nil {
		t.Fatal("digest preview command is nil")
	}
	if len(cmd.Use) < 7 || cmd.Use[:7] != "preview" {
		t.Errorf("expected digest preview Use to start with 'preview', got %q", cmd.Use)
	}
}
