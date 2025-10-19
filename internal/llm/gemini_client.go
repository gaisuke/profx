package llm

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"google.golang.org/genai"
)

const (
	maxRetries = 3
	timeout    = 30 * time.Second
)

type GeminiClient struct {
	client *genai.Client
	model  string
}

func NewGeminiClient(ctx context.Context, apikey, model string) (*GeminiClient, error) {
	if model == "" {
		model = "gemini-2.5-flash"
	}

	opts := &genai.ClientConfig{
		APIKey: apikey,
	}

	client, err := genai.NewClient(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	return &GeminiClient{
		client: client,
		model:  model,
	}, nil
}

func (gc *GeminiClient) Generate(ctx context.Context, prompt string) (string, error) {
	return gc.GenerateWithRetry(ctx, prompt, maxRetries)
}

func (gc *GeminiClient) GenerateWithRetry(ctx context.Context, prompt string, retries int) (string, error) {
	var lastErr error

	for attempt := 0; attempt < retries; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			backoff := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
		}

		response, err := gc.generate(ctx, prompt)
		if err == nil {
			return response, nil
		}

		lastErr = err

		if !isRetryableError(err) {
			break
		}
	}

	return "", fmt.Errorf("failed after %d retries: %w", retries, lastErr)
}

func (gc *GeminiClient) generate(ctx context.Context, prompt string) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	generateOpts := &genai.GenerateContentConfig{
		Temperature:     ptrFloat32(0.2),
		MaxOutputTokens: 2048,
	}

	result, err := gc.client.Models.GenerateContent(
		timeoutCtx,
		gc.model,
		genai.Text(prompt),
		generateOpts,
	)

	if err != nil {
		return "", fmt.Errorf("failed to generate content: %w", err)
	}

	text := result.Text()
	if text == "" {
		return "", fmt.Errorf("empty response from Gemini")
	}

	return text, nil
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	retryable := []string{
		"timeout",
		"connection",
		"rate limit",
		"429",
		"500",
		"502",
		"503",
		"504",
		"deadline exceeded",
		"context deadline",
	}

	for _, keyword := range retryable {
		if strings.Contains(errStr, keyword) {
			return true
		}
	}

	return false
}

func ptrFloat32(f float32) *float32 {
	return &f
}
