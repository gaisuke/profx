package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

type GeminiClient struct {
	client *genai.Client
	model  string
}

func NewGeminiClient(ctx context.Context, apiKey, model string) (*GeminiClient, error) {
	if model == "" {
		model = "gemini-1.5-flash" // Default model
	}

	// Create client with API key
	opts := &genai.ClientConfig{
		APIKey: apiKey,
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

// Generate sends a prompt to Gemini and returns the response
func (c *GeminiClient) Generate(ctx context.Context, prompt string) (string, error) {
	return c.GenerateWithRetry(ctx, prompt, maxRetries)
}

// GenerateWithRetry delegates to the shared retry helper, so Gemini and
// OpenCode Go back off and classify errors identically.
func (c *GeminiClient) GenerateWithRetry(ctx context.Context, prompt string, retries int) (string, error) {
	return generateWithRetry(ctx, retries, func(ctx context.Context) (string, error) {
		return c.generate(ctx, prompt)
	})
}

func (c *GeminiClient) generate(ctx context.Context, prompt string) (string, error) {
	// Create context with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Configure generation parameters
	generateOpts := &genai.GenerateContentConfig{
		Temperature:     ptrFloat32(0.2), // Low temperature for consistency
		MaxOutputTokens: int32(8192),
	}

	// Generate content
	result, err := c.client.Models.GenerateContent(
		timeoutCtx,
		c.model,
		genai.Text(prompt),
		generateOpts,
	)
	if err != nil {
		return "", fmt.Errorf("failed to generate content: %w", err)
	}

	// Extract text from result
	text := result.Text()
	if text == "" {
		return "", fmt.Errorf("empty response from Gemini")
	}

	return text, nil
}

// Helper function to create pointer to float32
func ptrFloat32(f float32) *float32 {
	return &f
}

// ParseJSONResponse extracts and parses JSON from LLM response
func ParseJSONResponse(response string, target interface{}) error {
	cleaned := cleanJSONResponse(response)

	if !json.Valid([]byte(cleaned)) {
		return fmt.Errorf("invalid JSON after cleaning (likely truncated model output): %s", cleaned)
	}

	if err := json.Unmarshal([]byte(cleaned), target); err != nil {
		return fmt.Errorf("failed to parse JSON: %w (cleaned response: %s)", err, cleaned)
	}

	return nil
}

func cleanJSONResponse(response string) string {
	response = strings.TrimSpace(response)
	return extractJSONObject(response)
}

func extractJSONObject(input string) string {
	start := strings.Index(input, "{")
	if start == -1 {
		return input
	}

	count := 0
	for i := start; i < len(input); i++ {
		switch input[i] {
		case '{':
			count++
		case '}':
			count--
			if count == 0 {
				return input[start : i+1]
			}
		}
	}

	return input
}
