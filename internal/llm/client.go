// Package llm provides an OpenAI-compatible chat completions client and
// natural-language text classifier for work-record entries.
//
// The client is intentionally thin: it sends a chat-completions request and
// returns the first choice's content string.  All classification logic lives
// in classify.go.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// chatRequest is the request body sent to the OpenAI chat completions endpoint.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

// chatMessage is a single message in the chat completions request.
// Content may be a plain string (text-only) or an array of content parts
// (multimodal: text + image_url).
type chatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

// chatResponse is the response body from the OpenAI chat completions endpoint.
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// Client is an OpenAI-compatible chat completions client.
type Client struct {
	apiBase     string
	apiKey      string
	model       string
	httpClient  *http.Client
}

// NewClient creates a new Client targeting the given OpenAI-compatible API.
// The HTTP client uses the given timeout for all requests.
func NewClient(apiBase, apiKey, model string, timeout time.Duration) *Client {
	return &Client{
		apiBase: apiBase,
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// CallChat sends a chat-completions request and returns the content of the
// first choice.  The request is POSTed to {apiBase}/chat/completions.
func (c *Client) CallChat(ctx context.Context, messages []chatMessage) (string, error) {
	reqBody := chatRequest{
		Model:       c.model,
		Messages:    messages,
		Temperature: 0.1,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("llm: marshal request: %w", err)
	}

	url := c.apiBase + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", &APIError{
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
		}
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("llm: parse response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("llm: api error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("llm: no choices in response")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// APIError represents a non-200 HTTP response from the API.
type APIError struct {
	StatusCode int
	Body       string
}

// Error implements the error interface.
func (e *APIError) Error() string {
	return fmt.Sprintf("llm: api returned status %d: %s", e.StatusCode, e.Body)
}
