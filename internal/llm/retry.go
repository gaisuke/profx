package llm

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	// maxRetries is the number of attempts a Generate call makes before giving up.
	maxRetries = 3
	// timeout caps a single provider call so one hung request cannot pin a worker.
	timeout = 30 * time.Second
)

// generateFunc is one attempt at a provider call.
type generateFunc func(ctx context.Context) (string, error)

// generateWithRetry runs an attempt with exponential backoff, retrying only the
// errors worth retrying. The returned error reports how many attempts were
// actually made — an earlier version always said "failed after N retries", which
// was wrong (and misleading) when the first attempt failed with a 4xx and no
// retry happened at all.
func generateWithRetry(ctx context.Context, retries int, call generateFunc) (string, error) {
	var lastErr error
	attempts := 0
	for attempt := 0; attempt < retries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
		}
		attempts++
		out, err := call(ctx)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !isRetryableError(err) {
			break
		}
	}
	return "", fmt.Errorf("giving up after %d attempt(s): %w", attempts, lastErr)
}

// isRetryableError decides whether an error is transient. Retrying a malformed
// request, a rejected key or a bad model name just burns quota, so only genuine
// transient conditions qualify.
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
