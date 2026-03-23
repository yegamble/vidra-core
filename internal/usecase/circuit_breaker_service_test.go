package usecase

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCircuitBreakerService_StateTransitions(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)

	config := CircuitBreakerConfig{
		FailureThreshold:   3,
		SuccessThreshold:   2,
		Timeout:            100 * time.Millisecond,
		HalfOpenMaxCalls:   2,
		ErrorRateThreshold: 0.5,
		WindowSize:         time.Second,
	}

	service := NewCircuitBreakerService(mockHardening, config)
	endpoint := "test-endpoint"

	var callCount int32
	successFunc := func() error {
		atomic.AddInt32(&callCount, 1)
		return nil
	}

	failureFunc := func() error {
		atomic.AddInt32(&callCount, 1)
		return errors.New("intentional failure")
	}

	// Test 1: Circuit starts in closed state
	state, err := service.GetState(ctx, endpoint)
	assert.NoError(t, err)
	assert.Equal(t, CircuitClosed, state)

	// Test 2: Successful calls work in closed state
	err = service.Call(ctx, endpoint, successFunc)
	assert.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	state, err = service.GetState(ctx, endpoint)
	assert.NoError(t, err)
	assert.Equal(t, CircuitClosed, state)

	// Test 3: Accumulate failures to open circuit
	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "circuit_breaker_opened"
	})).Return(nil).Once()

	atomic.StoreInt32(&callCount, 0)
	for i := 0; i < 3; i++ {
		_ = service.Call(ctx, endpoint, failureFunc)
	}
	assert.Equal(t, int32(3), atomic.LoadInt32(&callCount))

	// Circuit should be open now
	state, err = service.GetState(ctx, endpoint)
	assert.NoError(t, err)
	assert.Equal(t, CircuitOpen, state)

	// Test 4: Calls blocked when circuit is open
	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "circuit_breaker_blocked"
	})).Return(nil).Once()

	atomic.StoreInt32(&callCount, 0)
	err = service.Call(ctx, endpoint, successFunc)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circuit breaker is open")
	assert.Equal(t, int32(0), atomic.LoadInt32(&callCount)) // Function not called

	// Test 5: Wait for timeout to transition to half-open
	time.Sleep(150 * time.Millisecond)

	// First call in half-open should be allowed
	atomic.StoreInt32(&callCount, 0)
	err = service.Call(ctx, endpoint, successFunc)
	assert.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// Test 6: Success in half-open moves toward closing
	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "circuit_breaker_closed"
	})).Return(nil).Once()

	err = service.Call(ctx, endpoint, successFunc)
	assert.NoError(t, err)

	// Small delay to let state transition complete
	time.Sleep(10 * time.Millisecond)

	// Circuit should be closed after success threshold
	state, err = service.GetState(ctx, endpoint)
	assert.NoError(t, err)
	assert.Equal(t, CircuitClosed, state)

	mockHardening.AssertExpectations(t)
}

func TestCircuitBreakerService_ErrorRate(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)

	config := CircuitBreakerConfig{
		FailureThreshold:   10, // High threshold
		SuccessThreshold:   2,
		Timeout:            100 * time.Millisecond,
		HalfOpenMaxCalls:   3,
		ErrorRateThreshold: 0.3, // 30% error rate
		WindowSize:         time.Second,
	}

	service := NewCircuitBreakerService(mockHardening, config)
	endpoint := "error-rate-endpoint"

	successFunc := func() error { return nil }
	failureFunc := func() error { return errors.New("failure") }

	// Build up enough calls to calculate error rate
	// 7 successes, 3 failures = 30% error rate
	for i := 0; i < 7; i++ {
		_ = service.Call(ctx, endpoint, successFunc)
	}
	for i := 0; i < 3; i++ {
		_ = service.Call(ctx, endpoint, failureFunc)
	}

	// Next failure should trigger opening due to error rate
	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "circuit_breaker_opened"
	})).Return(nil).Once()

	_ = service.Call(ctx, endpoint, failureFunc)

	// Circuit should be open
	state, err := service.GetState(ctx, endpoint)
	assert.NoError(t, err)
	assert.Equal(t, CircuitOpen, state)

	mockHardening.AssertExpectations(t)
}

func TestCircuitBreakerService_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)
	mockHardening.On("RecordMetric", ctx, mock.Anything).Return(nil).Maybe()

	config := CircuitBreakerConfig{
		FailureThreshold:   5,
		SuccessThreshold:   2,
		Timeout:            50 * time.Millisecond,
		HalfOpenMaxCalls:   3,
		ErrorRateThreshold: 0.5,
		WindowSize:         time.Second,
	}

	service := NewCircuitBreakerService(mockHardening, config)

	var successCount int32
	var failureCount int32
	var blockedCount int32

	successFunc := func() error {
		atomic.AddInt32(&successCount, 1)
		return nil
	}

	failureFunc := func() error {
		atomic.AddInt32(&failureCount, 1)
		return errors.New("failure")
	}

	// Run concurrent calls
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		endpoint := "concurrent-endpoint"

		go func(idx int) {
			defer wg.Done()

			var fn func() error
			if idx%3 == 0 {
				fn = failureFunc
			} else {
				fn = successFunc
			}

			err := service.Call(ctx, endpoint, fn)
			if err != nil && err.Error() == "circuit breaker is open for endpoint: "+endpoint {
				atomic.AddInt32(&blockedCount, 1)
			}
		}(i)

		time.Sleep(10 * time.Millisecond) // Slight delay between goroutines
	}

	wg.Wait()

	totalCalls := atomic.LoadInt32(&successCount) + atomic.LoadInt32(&failureCount) + atomic.LoadInt32(&blockedCount)
	assert.Equal(t, int32(10), totalCalls, "all calls should be accounted for")
}

func TestCircuitBreakerService_Reset(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)

	config := CircuitBreakerConfig{
		FailureThreshold:   2,
		SuccessThreshold:   2,
		Timeout:            100 * time.Millisecond,
		HalfOpenMaxCalls:   1,
		ErrorRateThreshold: 0.5,
		WindowSize:         time.Second,
	}

	service := NewCircuitBreakerService(mockHardening, config)
	endpoint := "reset-endpoint"

	failureFunc := func() error { return errors.New("failure") }

	// Open the circuit
	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "circuit_breaker_opened"
	})).Return(nil).Once()

	for i := 0; i < 2; i++ {
		_ = service.Call(ctx, endpoint, failureFunc)
	}

	state, _ := service.GetState(ctx, endpoint)
	assert.Equal(t, CircuitOpen, state)

	// Reset the circuit
	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "circuit_breaker_reset"
	})).Return(nil).Once()

	err := service.Reset(ctx, endpoint)
	assert.NoError(t, err)

	// Circuit should be closed after reset
	state, _ = service.GetState(ctx, endpoint)
	assert.Equal(t, CircuitClosed, state)

	// Stats should be cleared
	stats, err := service.GetStats(ctx, endpoint)
	assert.NoError(t, err)
	assert.Equal(t, 0, stats.FailureCount)
	assert.Equal(t, 0, stats.SuccessCount)
	assert.Equal(t, 0, stats.ConsecutiveFailures)
	assert.Equal(t, 0.0, stats.ErrorRate)

	mockHardening.AssertExpectations(t)
}

func TestCircuitBreakerService_GetStats(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)

	config := CircuitBreakerConfig{
		FailureThreshold:   5,
		SuccessThreshold:   2,
		Timeout:            100 * time.Millisecond,
		HalfOpenMaxCalls:   3,
		ErrorRateThreshold: 0.5,
		WindowSize:         time.Second,
	}

	service := NewCircuitBreakerService(mockHardening, config)
	endpoint := "stats-endpoint"

	successFunc := func() error { return nil }
	failureFunc := func() error { return errors.New("failure") }

	// Make some calls
	for i := 0; i < 3; i++ {
		_ = service.Call(ctx, endpoint, successFunc)
	}
	for i := 0; i < 2; i++ {
		_ = service.Call(ctx, endpoint, failureFunc)
	}

	// Get stats
	stats, err := service.GetStats(ctx, endpoint)
	require.NoError(t, err)

	assert.Equal(t, CircuitClosed, stats.State)
	assert.Equal(t, 2, stats.FailureCount)
	assert.Equal(t, 3, stats.SuccessCount)
	assert.Equal(t, 2, stats.ConsecutiveFailures)
	assert.Equal(t, 0, stats.ConsecutiveSuccess)
	assert.Equal(t, 0.4, stats.ErrorRate) // 2 failures out of 5 total
	assert.NotNil(t, stats.LastFailure)
	assert.NotNil(t, stats.LastSuccess)
}

func TestCircuitBreakerService_HalfOpenMaxCalls(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)

	config := CircuitBreakerConfig{
		FailureThreshold:   2,
		SuccessThreshold:   3, // Higher than HalfOpenMaxCalls
		Timeout:            50 * time.Millisecond,
		HalfOpenMaxCalls:   2, // Only allow 2 calls in half-open
		ErrorRateThreshold: 0.5,
		WindowSize:         time.Second,
	}

	service := NewCircuitBreakerService(mockHardening, config)
	endpoint := "half-open-endpoint"

	failureFunc := func() error { return errors.New("failure") }
	successFunc := func() error { return nil }

	// Open the circuit
	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "circuit_breaker_opened"
	})).Return(nil).Once()

	for i := 0; i < 2; i++ {
		_ = service.Call(ctx, endpoint, failureFunc)
	}

	// Wait for timeout to transition to half-open
	time.Sleep(60 * time.Millisecond)

	// Expect blocked metric for the third call
	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "circuit_breaker_blocked"
	})).Return(nil).Once()

	// Make max allowed calls in half-open state
	callCount := 0
	for i := 0; i < 3; i++ {
		err := service.Call(ctx, endpoint, successFunc)
		if err == nil {
			callCount++
		}
	}

	// Only HalfOpenMaxCalls should succeed
	assert.Equal(t, 2, callCount, "only HalfOpenMaxCalls should be allowed")

	mockHardening.AssertExpectations(t)
}

func TestCircuitBreakerService_MultipleEndpoints(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)
	mockHardening.On("RecordMetric", ctx, mock.Anything).Return(nil).Maybe()

	config := CircuitBreakerConfig{
		FailureThreshold:   2,
		SuccessThreshold:   2,
		Timeout:            100 * time.Millisecond,
		HalfOpenMaxCalls:   3,
		ErrorRateThreshold: 0.5,
		WindowSize:         time.Second,
	}

	service := NewCircuitBreakerService(mockHardening, config)

	endpoint1 := "endpoint-1"
	endpoint2 := "endpoint-2"

	failureFunc := func() error { return errors.New("failure") }
	successFunc := func() error { return nil }

	// Open circuit for endpoint1
	for i := 0; i < 2; i++ {
		_ = service.Call(ctx, endpoint1, failureFunc)
	}

	// endpoint2 should still work
	err := service.Call(ctx, endpoint2, successFunc)
	assert.NoError(t, err)

	// Verify states
	state1, _ := service.GetState(ctx, endpoint1)
	state2, _ := service.GetState(ctx, endpoint2)

	assert.Equal(t, CircuitOpen, state1, "endpoint1 should be open")
	assert.Equal(t, CircuitClosed, state2, "endpoint2 should be closed")
}
