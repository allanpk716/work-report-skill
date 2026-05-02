package llm

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- EncodeImageFile tests ---

func TestEncodeImageFile(t *testing.T) {
	// Create a minimal valid PNG (1x1 pixel red PNG).
	pngData := createMinimalPNG(t, filepath.Join(t.TempDir(), "test.png"))

	dataURL, err := EncodeImageFile(pngData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPrefix := "data:image/png;base64,"
	if !startsWith(dataURL, expectedPrefix) {
		t.Fatalf("expected data URL prefix %q, got %q", expectedPrefix, dataURL[:len(expectedPrefix)+20])
	}

	// Verify the base64 portion decodes cleanly.
	b64 := dataURL[len(expectedPrefix):]
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode error: %v", err)
	}
	if len(decoded) == 0 {
		t.Fatal("decoded data should not be empty")
	}
}

func TestEncodeImageFile_MIMEType(t *testing.T) {
	tests := []struct {
		ext  string
		mime string
	}{
		{".png", "image/png"},
		{".jpg", "image/jpeg"},
		{".jpeg", "image/jpeg"},
		{".gif", "image/gif"},
		{".webp", "image/webp"},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "test"+tt.ext)
			if err := os.WriteFile(path, []byte("fake-image-data"), 0644); err != nil {
				t.Fatal(err)
			}
			dataURL, err := EncodeImageFile(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			expectedPrefix := "data:" + tt.mime + ";base64,"
			if !startsWith(dataURL, expectedPrefix) {
				t.Fatalf("expected prefix %q, got %q", expectedPrefix, dataURL[:len(expectedPrefix)+10])
			}
		})
	}
}

func TestEncodeImageFile_FileNotFound(t *testing.T) {
	_, err := EncodeImageFile("/nonexistent/path/image.png")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestEncodeImageFile_UnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := EncodeImageFile(path)
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func TestEncodeImageFile_EmptyPath(t *testing.T) {
	_, err := EncodeImageFile("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestEncodeImageFile_FileTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.png")

	// Create a file that claims to be > 20MB via a sparse file trick.
	// Actually write a real file of maxImageSize + 1 bytes.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Write 20MB + 1 byte
	buf := make([]byte, 4096)
	for i := range buf {
		buf[i] = byte(i % 256)
	}
	remaining := maxImageSize + 1
	for remaining > 0 {
		chunk := len(buf)
		if chunk > remaining {
			chunk = remaining
		}
		if _, err := f.Write(buf[:chunk]); err != nil {
			// If the filesystem doesn't support files this large (e.g. tmpfs),
			// skip the test gracefully.
			f.Close()
			os.Remove(path)
			t.Skipf("cannot create large test file: %v", err)
		}
		remaining -= chunk
	}
	f.Close()

	_, err = EncodeImageFile(path)
	if err == nil {
		t.Fatal("expected error for file too large")
	}
}

func TestEncodeImageFile_ExactlyMaxSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exact.png")

	// Create a file exactly maxImageSize bytes.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	for i := range buf {
		buf[i] = byte(i % 256)
	}
	remaining := maxImageSize
	for remaining > 0 {
		chunk := len(buf)
		if chunk > remaining {
			chunk = remaining
		}
		if _, err := f.Write(buf[:chunk]); err != nil {
			f.Close()
			os.Remove(path)
			t.Skipf("cannot create test file: %v", err)
		}
		remaining -= chunk
	}
	f.Close()

	dataURL, err := EncodeImageFile(path)
	if err != nil {
		t.Fatalf("expected file of exactly max size to pass, got error: %v", err)
	}
	if !startsWith(dataURL, "data:image/png;base64,") {
		t.Fatal("expected valid data URL")
	}
}

func TestEncodeImageFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.png")
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	// Empty file should succeed — it's valid (0 bytes < 20MB), just produces empty base64.
	dataURL, err := EncodeImageFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !startsWith(dataURL, "data:image/png;base64,") {
		t.Fatal("expected valid data URL prefix")
	}
}

// --- ClassifyImage tests ---

func TestClassifyImage_MeetingScreenshot(t *testing.T) {
	imgPath := createMinimalPNG(t, filepath.Join(t.TempDir(), "meeting.png"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request has multimodal content.
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", 400)
			return
		}

		// Verify user message has array content (multimodal).
		if len(req.Messages) < 2 {
			t.Errorf("expected at least 2 messages, got %d", len(req.Messages))
		}
		userMsg := req.Messages[1]
		if userMsg.Role != "user" {
			t.Errorf("expected user role, got %q", userMsg.Role)
		}

		// Content should be an array of parts.
		parts, ok := userMsg.Content.([]interface{})
		if !ok {
			t.Errorf("expected content to be array, got %T", userMsg.Content)
		} else if len(parts) != 2 {
			t.Errorf("expected 2 content parts, got %d", len(parts))
		}

		// Return meeting classification.
		payload := map[string]interface{}{
			"type":  "meeting",
			"title": "产品评审会",
			"date":  "2026-05-03",
			"time":  "14:00",
		}
		b, _ := json.Marshal(payload)
		writeMockResponse(w, string(b))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "gpt-4o")
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	result, err := ClassifyImage(client, imgPath, "", today, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != "meeting" {
		t.Fatalf("expected type meeting, got %q", result.Type)
	}
	if result.Title != "产品评审会" {
		t.Fatalf("expected title 产品评审会, got %q", result.Title)
	}
}

func TestClassifyImage_Task(t *testing.T) {
	imgPath := createMinimalPNG(t, filepath.Join(t.TempDir(), "task.png"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]interface{}{
			"type":     "task",
			"title":    "完成项目报告",
			"priority": "high",
		}
		b, _ := json.Marshal(payload)
		writeMockResponse(w, string(b))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "gpt-4o")
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	result, err := ClassifyImage(client, imgPath, "", today, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != "task" {
		t.Fatalf("expected type task, got %q", result.Type)
	}
	if result.Priority != "high" {
		t.Fatalf("expected priority high, got %q", result.Priority)
	}
}

func TestClassifyImage_WithTextContext(t *testing.T) {
	imgPath := createMinimalPNG(t, filepath.Join(t.TempDir(), "with_text.png"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request includes text context in the instruction.
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", 400)
			return
		}

		userMsg := req.Messages[1]
		parts, ok := userMsg.Content.([]interface{})
		if !ok {
			t.Errorf("expected content to be array, got %T", userMsg.Content)
			http.Error(w, "bad request", 400)
			return
		}

		// First part should be text with supplementary info.
		if len(parts) > 0 {
			partMap, ok := parts[0].(map[string]interface{})
			if ok && partMap["type"] == "text" {
				text, _ := partMap["text"].(string)
				if !contains(text, "补充信息: 这是补充文字") {
					t.Errorf("expected text context in instruction, got: %s", text)
				}
			}
		}

		payload := map[string]interface{}{
			"type":  "meeting",
			"title": "团队周会",
		}
		b, _ := json.Marshal(payload)
		writeMockResponse(w, string(b))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "gpt-4o")
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	result, err := ClassifyImage(client, imgPath, "这是补充文字", today, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != "meeting" {
		t.Fatalf("expected type meeting, got %q", result.Type)
	}
}

func TestClassifyImage_FileNotFound(t *testing.T) {
	client := NewClient("http://localhost", "key", "model")
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := ClassifyImage(client, "/nonexistent/image.png", "", today, loc)
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "validate_input" {
		t.Fatalf("expected op validate_input, got %q", ce.Op)
	}
}

func TestClassifyImage_UnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	txtPath := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(txtPath, []byte("not an image"), 0644); err != nil {
		t.Fatal(err)
	}

	client := NewClient("http://localhost", "key", "model")
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := ClassifyImage(client, txtPath, "", today, loc)
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "validate_input" {
		t.Fatalf("expected op validate_input, got %q", ce.Op)
	}
}

func TestClassifyImage_LLMError(t *testing.T) {
	imgPath := createMinimalPNG(t, filepath.Join(t.TempDir(), "test.png"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "gpt-4o")
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := ClassifyImage(client, imgPath, "", today, loc)
	if err == nil {
		t.Fatal("expected error for LLM 500")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "call_api" {
		t.Fatalf("expected op call_api, got %q", ce.Op)
	}
}

func TestClassifyImage_LLM401(t *testing.T) {
	imgPath := createMinimalPNG(t, filepath.Join(t.TempDir(), "test.png"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "invalid api key"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "bad-key", "gpt-4o")
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := ClassifyImage(client, imgPath, "", today, loc)
	if err == nil {
		t.Fatal("expected error for LLM 401")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "call_api" {
		t.Fatalf("expected op call_api, got %q", ce.Op)
	}
}

func TestClassifyImage_ConnectionRefused(t *testing.T) {
	imgPath := createMinimalPNG(t, filepath.Join(t.TempDir(), "test.png"))

	// Use a closed server to get connection refused.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	client := NewClient(server.URL, "test-key", "gpt-4o")
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := ClassifyImage(client, imgPath, "", today, loc)
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "call_api" {
		t.Fatalf("expected op call_api, got %q", ce.Op)
	}
}

func TestClassifyImage_EmptyChoices(t *testing.T) {
	imgPath := createMinimalPNG(t, filepath.Join(t.TempDir(), "test.png"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "gpt-4o")
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := ClassifyImage(client, imgPath, "", today, loc)
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "call_api" {
		t.Fatalf("expected op call_api, got %q", ce.Op)
	}
}

func TestClassifyImage_InvalidJSON(t *testing.T) {
	imgPath := createMinimalPNG(t, filepath.Join(t.TempDir(), "test.png"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMockResponse(w, "not valid json at all")
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "gpt-4o")
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := ClassifyImage(client, imgPath, "", today, loc)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
	if ce.Op != "parse_json" {
		t.Fatalf("expected op parse_json, got %q", ce.Op)
	}
}

func TestClassifyImage_VisionNotConfigured(t *testing.T) {
	imgPath := createMinimalPNG(t, filepath.Join(t.TempDir(), "test.png"))

	// Empty client — no server, but we test that ClassifyImage handles it gracefully.
	client := NewClient("", "key", "model")
	today := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)

	_, err := ClassifyImage(client, imgPath, "", today, loc)
	// With empty URL, the HTTP request will fail — should get call_api error.
	if err == nil {
		t.Fatal("expected error with empty client")
	}
	var ce *ClassifyError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClassifyError, got %T: %v", err, err)
	}
}

// --- Test helpers ---

// createMinimalPNG creates a minimal valid 1x1 PNG file and returns its path.
func createMinimalPNG(t *testing.T, path string) string {
	t.Helper()
	// Minimal valid PNG: 8-byte signature + IHDR + IDAT + IEND
	// This is a well-known minimal PNG byte sequence for a 1x1 red pixel.
	minPNG := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, // IHDR length
		0x49, 0x48, 0x44, 0x52, // IHDR
		0x00, 0x00, 0x00, 0x01, // width: 1
		0x00, 0x00, 0x00, 0x01, // height: 1
		0x08, 0x02, // bit depth: 8, color type: 2 (RGB)
		0x00, 0x00, 0x00, // compression, filter, interlace
		0x90, 0x77, 0x53, 0xDE, // CRC
		0x00, 0x00, 0x00, 0x0C, // IDAT length
		0x49, 0x44, 0x41, 0x54, // IDAT
		0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, 0x00,
		0x00, 0x02, 0x00, 0x01, // compressed data for 1x1 red pixel
		0xE2, 0x21, 0xBC, 0x33, // CRC
		0x00, 0x00, 0x00, 0x00, // IEND length
		0x49, 0x45, 0x4E, 0x44, // IEND
		0xAE, 0x42, 0x60, 0x82, // CRC
	}
	if err := os.WriteFile(path, minPNG, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeMockResponse writes a mock chat response with the given content.
func writeMockResponse(w http.ResponseWriter, content string) {
	resp := chatResponse{
		Choices: []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{{
			Message: struct {
				Content string `json:"content"`
			}{Content: content}},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// startsWith checks if s starts with prefix.
func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
