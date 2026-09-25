package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestIsRetryableError(t *testing.T) {
	retryable := []string{
		"context deadline exceeded",
		"connection reset by peer",
		"rate limit exceeded",
		"OpenCode Go returned 429: slow down",
		"OpenCode Go returned 503: unavailable",
	}
	for _, msg := range retryable {
		if !isRetryableError(errors.New(msg)) {
			t.Errorf("%q should be retryable", msg)
		}
	}
	permanent := []string{
		"OpenCode Go returned 400: bad request",
		"OpenCode Go returned 401: invalid api key",
		"failed to decode response: unexpected end of JSON input",
	}
	for _, msg := range permanent {
		if isRetryableError(errors.New(msg)) {
			t.Errorf("%q must not be retried", msg)
		}
	}
	if isRetryableError(nil) {
		t.Error("nil error must not be retryable")
	}
}

func TestGenerateWithRetryStopsOnPermanentError(t *testing.T) {
	attempts := 0
	_, err := generateWithRetry(context.Background(), 3, func(context.Context) (string, error) {
		attempts++
		return "", errors.New("OpenCode Go returned 400: bad request")
	})
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (a 400 must not be retried)", attempts)
	}
	if err == nil || !strings.Contains(err.Error(), "1 attempt(s)") {
		t.Fatalf("error should report the real attempt count, got: %v", err)
	}
}

func TestGenerateWithRetryRecoversFromTransientError(t *testing.T) {
	attempts := 0
	out, err := generateWithRetry(context.Background(), 3, func(context.Context) (string, error) {
		attempts++
		if attempts == 1 {
			return "", errors.New("OpenCode Go returned 503: unavailable")
		}
		return "recovered", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "recovered" || attempts != 2 {
		t.Fatalf("out = %q, attempts = %d; want recovered after 2 attempts", out, attempts)
	}
}

func TestGenerateWithRetryHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := generateWithRetry(ctx, 3, func(context.Context) (string, error) {
		return "", errors.New("OpenCode Go returned 503: unavailable")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
