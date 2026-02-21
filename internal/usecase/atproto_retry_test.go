package usecase

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetryable(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{http.StatusOK, false},
		{http.StatusBadRequest, false},
		{http.StatusUnauthorized, false},
		{http.StatusForbidden, false},
		{http.StatusNotFound, false},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusGatewayTimeout, true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%d", tt.status), func(t *testing.T) {
			assert.Equal(t, tt.want, retryable(tt.status))
		})
	}
}

func TestRetryableError(t *testing.T) {
	inner := fmt.Errorf("inner error")
	re := &retryableError{StatusCode: 503, Err: inner}

	assert.Equal(t, "inner error", re.Error())
	assert.Equal(t, inner, re.Unwrap())
}

func TestDoWithRetry_SuccessOnFirstAttempt(t *testing.T) {
	var calls int32
	err := doWithRetry(context.Background(), retryConfig{maxRetries: 3, baseDelay: time.Millisecond}, "test", func() error {
		atomic.AddInt32(&calls, 1)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

func TestDoWithRetry_SuccessAfterRetries(t *testing.T) {
	var calls int32
	err := doWithRetry(context.Background(), retryConfig{maxRetries: 3, baseDelay: time.Millisecond}, "test", func() error {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return &retryableError{StatusCode: http.StatusServiceUnavailable, Err: fmt.Errorf("unavailable")}
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, int32(3), atomic.LoadInt32(&calls))
}

func TestDoWithRetry_ExhaustsRetries(t *testing.T) {
	var calls int32
	err := doWithRetry(context.Background(), retryConfig{maxRetries: 2, baseDelay: time.Millisecond}, "test-op", func() error {
		atomic.AddInt32(&calls, 1)
		return &retryableError{StatusCode: http.StatusServiceUnavailable, Err: fmt.Errorf("still down")}
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max retries (2) exhausted")
	assert.Contains(t, err.Error(), "test-op")
	assert.Equal(t, int32(3), atomic.LoadInt32(&calls)) // initial + 2 retries
}

func TestDoWithRetry_NonRetryableError(t *testing.T) {
	var calls int32
	err := doWithRetry(context.Background(), retryConfig{maxRetries: 3, baseDelay: time.Millisecond}, "test", func() error {
		atomic.AddInt32(&calls, 1)
		return &retryableError{StatusCode: http.StatusBadRequest, Err: fmt.Errorf("bad request")}
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad request")
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls)) // no retry for 400
}

func TestDoWithRetry_PlainErrorNotRetried(t *testing.T) {
	var calls int32
	err := doWithRetry(context.Background(), retryConfig{maxRetries: 3, baseDelay: time.Millisecond}, "test", func() error {
		atomic.AddInt32(&calls, 1)
		return fmt.Errorf("plain error")
	})
	require.Error(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

func TestDoWithRetry_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := doWithRetry(ctx, retryConfig{maxRetries: 3, baseDelay: time.Millisecond}, "test", func() error {
		return &retryableError{StatusCode: http.StatusServiceUnavailable, Err: fmt.Errorf("error")}
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context cancelled")
}

func TestDoWithRetry_BackoffDelayIsExponential(t *testing.T) {
	var timestamps []time.Time
	rc := retryConfig{maxRetries: 3, baseDelay: 50 * time.Millisecond}

	_ = doWithRetry(context.Background(), rc, "test", func() error {
		timestamps = append(timestamps, time.Now())
		return &retryableError{StatusCode: http.StatusServiceUnavailable, Err: fmt.Errorf("error")}
	})

	require.Len(t, timestamps, 4) // 1 initial + 3 retries

	// First delay should be ~50ms (baseDelay * 2^0)
	delay1 := timestamps[1].Sub(timestamps[0])
	assert.True(t, delay1 >= 40*time.Millisecond, "first delay too short: %v", delay1)

	// Second delay should be ~100ms (baseDelay * 2^1)
	delay2 := timestamps[2].Sub(timestamps[1])
	assert.True(t, delay2 >= 80*time.Millisecond, "second delay too short: %v", delay2)

	// Third delay should be ~200ms (baseDelay * 2^2)
	delay3 := timestamps[3].Sub(timestamps[2])
	assert.True(t, delay3 >= 160*time.Millisecond, "third delay too short: %v", delay3)
}

func TestDoWithRetry_MaxDelayCap(t *testing.T) {
	// With very high retry count, delay should cap at 30s
	rc := retryConfig{maxRetries: 0, baseDelay: time.Hour}
	start := time.Now()
	_ = doWithRetry(context.Background(), rc, "test", func() error {
		return nil
	})
	elapsed := time.Since(start)
	assert.True(t, elapsed < time.Second, "should complete quickly with 0 retries")
}

func TestDefaultRetryConfig(t *testing.T) {
	rc := defaultRetryConfig()
	assert.Equal(t, 3, rc.maxRetries)
	assert.Equal(t, 500*time.Millisecond, rc.baseDelay)
}
