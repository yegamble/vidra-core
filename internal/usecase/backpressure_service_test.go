package usecase

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"athena/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBackpressureService_ThrottlingByQueueDepth(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)

	config := BackpressureConfig{
		QueueThreshold:       100,
		ErrorRateThreshold:   0.1,
		ThrottleFactor:       0.5,
		RecoveryFactor:       1.2,
		MeasurementWindow:    time.Minute,
		CooldownPeriod:       100 * time.Millisecond,
		MaxQueueDepth:        1000,
		EmergencyStopEnabled: true,
	}

	service := NewBackpressureService(mockHardening, config)
	instance := "test-instance"

	// Test 1: Initially not throttled
	shouldThrottle, factor, err := service.ShouldThrottle(ctx, instance)
	assert.NoError(t, err)
	assert.False(t, shouldThrottle)
	assert.Equal(t, 1.0, factor)

	// Test 2: High queue depth triggers throttling
	metrics := BackpressureMetrics{
		QueueDepth:     200, // Above threshold of 100
		ProcessingRate: 10.0,
		ErrorRate:      0.05, // Below error threshold
		SuccessCount:   100,
		FailureCount:   5,
	}

	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "backpressure_throttled"
	})).Return(nil).Once()

	err = service.RecordMetrics(ctx, instance, metrics)
	assert.NoError(t, err)

	// Test 3: Should be throttled now
	shouldThrottle, factor, err = service.ShouldThrottle(ctx, instance)
	assert.NoError(t, err)
	assert.True(t, shouldThrottle)
	assert.Less(t, factor, 1.0, "throttle factor should be less than 1.0")
	assert.Greater(t, factor, 0.0, "throttle factor should be greater than 0.0")

	// Test 4: Wait for recovery
	time.Sleep(150 * time.Millisecond)

	// Test 5: After recovery with good metrics
	goodMetrics := BackpressureMetrics{
		QueueDepth:     50, // Below threshold
		ProcessingRate: 20.0,
		ErrorRate:      0.02,
		SuccessCount:   200,
		FailureCount:   4,
	}

	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "backpressure_recovered"
	})).Return(nil).Maybe()

	err = service.RecordMetrics(ctx, instance, goodMetrics)
	assert.NoError(t, err)

	// Should not be throttled after recovery
	shouldThrottle, factor, err = service.ShouldThrottle(ctx, instance)
	assert.NoError(t, err)
	assert.False(t, shouldThrottle)
	assert.Equal(t, 1.0, factor)

	mockHardening.AssertExpectations(t)
}

func TestBackpressureService_EmergencyStop(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)

	config := BackpressureConfig{
		QueueThreshold:       100,
		ErrorRateThreshold:   0.1,
		ThrottleFactor:       0.5,
		RecoveryFactor:       1.2,
		MeasurementWindow:    time.Minute,
		CooldownPeriod:       100 * time.Millisecond,
		MaxQueueDepth:        500,
		EmergencyStopEnabled: true,
	}

	service := NewBackpressureService(mockHardening, config)
	instance := "critical-instance"

	// Test: Critical queue depth triggers emergency stop
	criticalMetrics := BackpressureMetrics{
		QueueDepth:     600, // Above max depth of 500
		ProcessingRate: 5.0,
		ErrorRate:      0.05,
	}

	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "backpressure_throttled"
	})).Return(nil).Once()

	err := service.RecordMetrics(ctx, instance, criticalMetrics)
	assert.NoError(t, err)

	// Should trigger emergency stop
	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "backpressure_emergency_stop"
	})).Return(nil).Once()

	shouldThrottle, factor, err := service.ShouldThrottle(ctx, instance)
	assert.NoError(t, err)
	assert.True(t, shouldThrottle)
	assert.Equal(t, 0.0, factor, "emergency stop should return 0.0 factor")

	mockHardening.AssertExpectations(t)
}

func TestBackpressureService_ErrorRateThrottling(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)

	config := BackpressureConfig{
		QueueThreshold:       100,
		ErrorRateThreshold:   0.2, // 20% error rate threshold
		ThrottleFactor:       0.5,
		RecoveryFactor:       1.2,
		MeasurementWindow:    time.Minute,
		CooldownPeriod:       100 * time.Millisecond,
		MaxQueueDepth:        1000,
		EmergencyStopEnabled: false,
	}

	service := NewBackpressureService(mockHardening, config)
	instance := "error-prone-instance"

	// Test: High error rate triggers throttling
	metrics := BackpressureMetrics{
		QueueDepth:     50, // Below queue threshold
		ProcessingRate: 10.0,
		ErrorRate:      0.4, // 40% error rate, above threshold
		SuccessCount:   60,
		FailureCount:   40,
	}

	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "backpressure_throttled"
	})).Return(nil).Once()

	err := service.RecordMetrics(ctx, instance, metrics)
	assert.NoError(t, err)

	shouldThrottle, factor, err := service.ShouldThrottle(ctx, instance)
	assert.NoError(t, err)
	assert.True(t, shouldThrottle, "high error rate should trigger throttling")
	assert.Less(t, factor, 1.0)

	mockHardening.AssertExpectations(t)
}

func TestBackpressureService_ConsecutiveErrors(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)

	config := BackpressureConfig{
		QueueThreshold:       100,
		ErrorRateThreshold:   0.5,
		ThrottleFactor:       0.5,
		RecoveryFactor:       1.2,
		MeasurementWindow:    time.Minute,
		CooldownPeriod:       100 * time.Millisecond,
		MaxQueueDepth:        1000,
		EmergencyStopEnabled: false,
	}

	service := NewBackpressureService(mockHardening, config)
	instance := "consecutive-error-instance"

	// Accumulate consecutive errors
	for i := 0; i < 4; i++ {
		metrics := BackpressureMetrics{
			QueueDepth:     50,
			ProcessingRate: 10.0,
			ErrorRate:      0.6, // Above threshold
			SuccessCount:   0,
			FailureCount:   10,
		}

		if i == 3 {
			// On 4th consecutive error, expect throttling
			mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
				return m.MetricType == "backpressure_throttled"
			})).Return(nil).Once()
		}

		err := service.RecordMetrics(ctx, instance, metrics)
		assert.NoError(t, err)
	}

	// Should be heavily throttled after consecutive errors
	shouldThrottle, factor, err := service.ShouldThrottle(ctx, instance)
	assert.NoError(t, err)
	assert.True(t, shouldThrottle)
	assert.LessOrEqual(t, factor, 0.25, "consecutive errors should heavily reduce throttle factor")

	mockHardening.AssertExpectations(t)
}

func TestBackpressureService_GetStatus(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)

	config := BackpressureConfig{
		QueueThreshold:       100,
		ErrorRateThreshold:   0.1,
		ThrottleFactor:       0.5,
		RecoveryFactor:       1.2,
		MeasurementWindow:    time.Minute,
		CooldownPeriod:       100 * time.Millisecond,
		MaxQueueDepth:        1000,
		EmergencyStopEnabled: true,
	}

	service := NewBackpressureService(mockHardening, config)
	instance := "status-instance"

	// Record some metrics
	metrics := BackpressureMetrics{
		QueueDepth:     150,
		ProcessingRate: 15.5,
		ErrorRate:      0.08,
		SuccessCount:   92,
		FailureCount:   8,
	}

	mockHardening.On("RecordMetric", ctx, mock.Anything).Return(nil).Maybe()

	err := service.RecordMetrics(ctx, instance, metrics)
	require.NoError(t, err)

	// Get status
	status, err := service.GetStatus(ctx, instance)
	require.NoError(t, err)

	assert.Equal(t, instance, status.Instance)
	assert.True(t, status.IsThrottled)
	assert.Less(t, status.ThrottleFactor, 1.0)
	assert.Equal(t, 150, status.QueueDepth)
	assert.Equal(t, 15.5, status.ProcessingRate)
	assert.InDelta(t, 0.08, status.ErrorRate, 0.01)
	assert.NotNil(t, status.ThrottledSince)
	assert.NotNil(t, status.RecoverAt)
	assert.GreaterOrEqual(t, status.ConsecutiveErrors, 0)
	assert.NotNil(t, status.Metadata)
	assert.Greater(t, status.Metadata["measurement_count"], 0)
}

func TestBackpressureService_Reset(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)

	config := BackpressureConfig{
		QueueThreshold:       100,
		ErrorRateThreshold:   0.1,
		ThrottleFactor:       0.5,
		RecoveryFactor:       1.2,
		MeasurementWindow:    time.Minute,
		CooldownPeriod:       100 * time.Millisecond,
		MaxQueueDepth:        1000,
		EmergencyStopEnabled: true,
	}

	service := NewBackpressureService(mockHardening, config)
	instance := "reset-instance"

	// First, cause throttling
	metrics := BackpressureMetrics{
		QueueDepth:     200,
		ProcessingRate: 10.0,
		ErrorRate:      0.3,
	}

	mockHardening.On("RecordMetric", ctx, mock.Anything).Return(nil).Maybe()

	err := service.RecordMetrics(ctx, instance, metrics)
	require.NoError(t, err)

	// Verify throttled
	shouldThrottle, _, _ := service.ShouldThrottle(ctx, instance)
	assert.True(t, shouldThrottle)

	// Reset
	mockHardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "backpressure_reset"
	})).Return(nil).Once()

	err = service.Reset(ctx, instance)
	assert.NoError(t, err)

	// Verify no longer throttled
	shouldThrottle, factor, err := service.ShouldThrottle(ctx, instance)
	assert.NoError(t, err)
	assert.False(t, shouldThrottle)
	assert.Equal(t, 1.0, factor)

	// Verify status is reset
	status, err := service.GetStatus(ctx, instance)
	assert.NoError(t, err)
	assert.False(t, status.IsThrottled)
	assert.Equal(t, 1.0, status.ThrottleFactor)
	assert.Equal(t, 0, status.QueueDepth)
	assert.Equal(t, 0.0, status.ProcessingRate)
	assert.Equal(t, 0.0, status.ErrorRate)
	assert.Equal(t, 0, status.ConsecutiveErrors)

	mockHardening.AssertExpectations(t)
}

func TestBackpressureService_GetAllStatuses(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)

	config := BackpressureConfig{
		QueueThreshold:       100,
		ErrorRateThreshold:   0.1,
		ThrottleFactor:       0.5,
		RecoveryFactor:       1.2,
		MeasurementWindow:    time.Minute,
		CooldownPeriod:       100 * time.Millisecond,
		MaxQueueDepth:        1000,
		EmergencyStopEnabled: true,
	}

	service := NewBackpressureService(mockHardening, config)

	instances := []string{"instance-1", "instance-2", "instance-3"}

	// Record metrics for multiple instances
	for i, instance := range instances {
		metrics := BackpressureMetrics{
			QueueDepth:     50 * (i + 1),
			ProcessingRate: 10.0 * float64(i+1),
			ErrorRate:      0.05 * float64(i),
		}

		if i > 0 {
			mockHardening.On("RecordMetric", ctx, mock.Anything).Return(nil).Maybe()
		}

		err := service.RecordMetrics(ctx, instance, metrics)
		require.NoError(t, err)
	}

	// Get all statuses
	statuses, err := service.GetAllStatuses(ctx)
	assert.NoError(t, err)
	assert.Len(t, statuses, 3)

	for _, instance := range instances {
		status, exists := statuses[instance]
		assert.True(t, exists, "status should exist for %s", instance)
		assert.NotNil(t, status)
		assert.Equal(t, instance, status.Instance)
	}

	mockHardening.AssertExpectations(t)
}

func TestBackpressureService_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)
	mockHardening.On("RecordMetric", ctx, mock.Anything).Return(nil).Maybe()

	config := BackpressureConfig{
		QueueThreshold:       100,
		ErrorRateThreshold:   0.1,
		ThrottleFactor:       0.5,
		RecoveryFactor:       1.2,
		MeasurementWindow:    time.Minute,
		CooldownPeriod:       100 * time.Millisecond,
		MaxQueueDepth:        1000,
		EmergencyStopEnabled: true,
	}

	service := NewBackpressureService(mockHardening, config)
	instance := "concurrent-instance"

	var wg sync.WaitGroup
	var recordCount int32
	var checkCount int32

	// Concurrent metric recording
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			metrics := BackpressureMetrics{
				QueueDepth:     50 + idx*10,
				ProcessingRate: 10.0,
				ErrorRate:      0.05,
			}

			if err := service.RecordMetrics(ctx, instance, metrics); err == nil {
				atomic.AddInt32(&recordCount, 1)
			}
		}(i)
	}

	// Concurrent throttle checking
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			if _, _, err := service.ShouldThrottle(ctx, instance); err == nil {
				atomic.AddInt32(&checkCount, 1)
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int32(10), atomic.LoadInt32(&recordCount), "all records should succeed")
	assert.Equal(t, int32(10), atomic.LoadInt32(&checkCount), "all checks should succeed")
}

func TestBackpressureService_MeasurementWindow(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)

	config := BackpressureConfig{
		QueueThreshold:       100,
		ErrorRateThreshold:   0.1,
		ThrottleFactor:       0.5,
		RecoveryFactor:       1.2,
		MeasurementWindow:    200 * time.Millisecond, // Short window for testing
		CooldownPeriod:       100 * time.Millisecond,
		MaxQueueDepth:        1000,
		EmergencyStopEnabled: false,
	}

	service := NewBackpressureService(mockHardening, config)
	instance := "window-instance"

	// Record high error rate
	metrics1 := BackpressureMetrics{
		QueueDepth:     50,
		ProcessingRate: 10.0,
		ErrorRate:      0.5, // High error rate
	}

	err := service.RecordMetrics(ctx, instance, metrics1)
	require.NoError(t, err)

	// Wait for measurement window to expire
	time.Sleep(250 * time.Millisecond)

	// Record good metrics
	metrics2 := BackpressureMetrics{
		QueueDepth:     50,
		ProcessingRate: 10.0,
		ErrorRate:      0.05, // Low error rate
	}

	err = service.RecordMetrics(ctx, instance, metrics2)
	require.NoError(t, err)

	// Status should reflect recent good metrics
	status, err := service.GetStatus(ctx, instance)
	assert.NoError(t, err)
	assert.LessOrEqual(t, status.ErrorRate, 0.1, "old measurements should be discarded")
}
