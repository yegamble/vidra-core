package usecase

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"time"
)

// retryConfig controls exponential backoff behaviour for ATProto HTTP calls.
type retryConfig struct {
	maxRetries int
	baseDelay  time.Duration
}

func defaultRetryConfig() retryConfig {
	return retryConfig{maxRetries: 3, baseDelay: 500 * time.Millisecond}
}

// retryable returns true for HTTP status codes that should trigger a retry.
func retryable(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// retryableError wraps an error and HTTP status code for retry decisions.
type retryableError struct {
	StatusCode int
	Err        error
}

func (e *retryableError) Error() string { return e.Err.Error() }
func (e *retryableError) Unwrap() error { return e.Err }

// doWithRetry executes fn with exponential backoff. fn should return a
// *retryableError when the HTTP status code is available so the retry
// logic can decide whether to retry. Plain errors are not retried.
func doWithRetry(ctx context.Context, rc retryConfig, operation string, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt <= rc.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%s: context cancelled: %w", operation, err)
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// Only retry on retryable HTTP errors.
		re, ok := lastErr.(*retryableError)
		if !ok || !retryable(re.StatusCode) {
			return lastErr
		}

		if attempt < rc.maxRetries {
			delay := rc.baseDelay * time.Duration(math.Pow(2, float64(attempt)))
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("%s: context cancelled during backoff: %w", operation, ctx.Err())
			case <-time.After(delay):
			}
		}
	}
	return fmt.Errorf("%s: max retries (%d) exhausted: %w", operation, rc.maxRetries, lastErr)
}
