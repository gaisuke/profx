package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The OpenCode Go endpoint speaks the Anthropic Messages API.
const (
	openCodeGoDefaultBaseURL   = "https://opencode.ai/zen/go/v1"
	openCodeGoDefaultModel     = "deepseek-v4.1-flash"
	openCodeGoDefaultMaxTokens = 4096
	// openCodeGoMaxTokenCeiling bounds the automatic retry below.
	openCodeGoMaxTokenCeiling = 16384
	// openCodeGoUserAgent is not cosmetic: the provider sits behind Cloudflare,
	// which rejects requests with a non-browser User-Agent (error 1010).
	openCodeGoUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/128.0 Safari/537.36"
)

// OpenCodeGoClient calls the OpenCode Go gateway. Reasoning models there spend
// the completion budget on hidden reasoning before emitting any text: at 2500
// max_tokens the answer never starts and the reply comes back empty. The client
// therefore detects the empty+max_tokens case and retries once with a larger
// budget instead of failing the job with a confusing parse error.
type OpenCodeGoClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
	maxTokens  int
	sessionID  string
}

func NewOpenCodeGoClient(apiKey, baseURL, model string, maxTokens int) *OpenCodeGoClient {
	if baseURL == "" {
		baseURL = openCodeGoDefaultBaseURL
	}
	if model == "" {
		model = openCodeGoDefaultModel
	}
	if maxTokens <= 0 {
		maxTokens = openCodeGoDefaultMaxTokens
	}
	return &OpenCodeGoClient{
		httpClient: &http.Client{Timeout: timeout + 5*time.Second},
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
		maxTokens:  maxTokens,
		sessionID:  uuid.NewString(),
	}
}

// Model reports the configured model, for logs and eval reports.
func (c *OpenCodeGoClient) Model() string { return c.model }

// Generate sends a prompt and returns the model's text reply.
func (c *OpenCodeGoClient) Generate(ctx context.Context, prompt string) (string, error) {
	return c.GenerateWithRetry(ctx, prompt, maxRetries)
}

// GenerateWithRetry retries transient failures with exponential backoff.
func (c *OpenCodeGoClient) GenerateWithRetry(ctx context.Context, prompt string, retries int) (string, error) {
	return generateWithRetry(ctx, retries, func(ctx context.Context) (string, error) {
		return c.generate(ctx, prompt)
	})
}

type anthropicMessageResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *OpenCodeGoClient) generate(ctx context.Context, prompt string) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	maxTokens := c.maxTokens
	for {
		text, stopReason, err := c.call(timeoutCtx, prompt, maxTokens)
		if err != nil {
			return "", err
		}
		if text != "" {
			return text, nil
		}
		// Empty text with stop_reason=max_tokens means the budget went to
		// hidden reasoning; give the model room and try once more.
		if stopReason == "max_tokens" && maxTokens < openCodeGoMaxTokenCeiling {
			maxTokens *= 2
			continue
		}
		return "", fmt.Errorf("empty response from OpenCode Go (stop_reason=%q)", stopReason)
	}
}

func (c *OpenCodeGoClient) call(ctx context.Context, prompt string, maxTokens int) (string, string, error) {
	body, err := json.Marshal(map[string]any{
		"model":       c.model,
		"max_tokens":  maxTokens,
		"temperature": 0.2, // low temperature keeps scoring consistent across runs
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", openCodeGoUserAgent)
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	// The gateway requires a session id on every request.
	req.Header.Set("x-opencode-session", c.sessionID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to call OpenCode Go: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("OpenCode Go returned %d: %s", resp.StatusCode, string(raw))
	}

	var parsed anthropicMessageResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", "", fmt.Errorf("failed to decode response: %w", err)
	}
	if parsed.Error != nil {
		return "", "", fmt.Errorf("OpenCode Go error %s: %s", parsed.Error.Type, parsed.Error.Message)
	}

	var text strings.Builder
	for _, block := range parsed.Content {
		// Reasoning models also return thinking blocks; only text is the answer.
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return strings.TrimSpace(text.String()), parsed.StopReason, nil
}
