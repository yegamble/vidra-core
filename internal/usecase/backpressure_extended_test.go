package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
)

func TestShouldThrottle_EmergencyStop(t *testing.T) {
	mockH := new(MockHardeningRepository)
	mockH.On("RecordMetric", mock.Anything, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "backpressure_throttled"
	})).Return(nil)
	mockH.On("RecordMetric", mock.Anything, mock.MatchedBy(func(m *domain.FederationMetric) bool {
		return m.MetricType == "backpressure_emergency_stop"
	})).Return(nil)

	cfg := BackpressureConfig{
		QueueThreshold:       1000,
		ErrorRateThreshold:   0.1,
		ThrottleFactor:       0.5,
		MaxQueueDepth:        10000,
		EmergencyStopEnabled: true,
		MeasurementWindow:    5 * time.Minute,
		CooldownPeriod:       2 * time.Minute,
	}
	svc := NewBackpressureService(mockH, cfg)
	ctx := context.Background()

	err := svc.RecordMetrics(ctx, "test-instance", BackpressureMetrics{
		QueueDepth:     15000,
		ProcessingRate: 10,
		ErrorRate:      0.0,
	})
	assert.NoError(t, err)

	throttled, factor, err := svc.ShouldThrottle(ctx, "test-instance")
	assert.NoError(t, err)
	assert.True(t, throttled)
	assert.Equal(t, 0.0, factor)
}

func TestShouldThrottle_Cooldown(t *testing.T) {
	cfg := BackpressureConfig{
		QueueThreshold:     100,
		ErrorRateThreshold: 0.1,
		ThrottleFactor:     0.5,
		RecoveryFactor:     1.2,
		MaxQueueDepth:      10000,
		MeasurementWindow:  5 * time.Minute,
		CooldownPeriod:     1 * time.Hour,
	}
	svc := NewBackpressureService(nil, cfg)
	ctx := context.Background()

	err := svc.RecordMetrics(ctx, "instance1", BackpressureMetrics{
		QueueDepth:     500,
		ProcessingRate: 10,
		ErrorRate:      0.0,
	})
	assert.NoError(t, err)

	throttled, factor, err := svc.ShouldThrottle(ctx, "instance1")
	assert.NoError(t, err)
	assert.True(t, throttled)
	assert.True(t, factor > 0 && factor < 1.0)
}

func TestShouldThrottle_QueueOverThreshold(t *testing.T) {
	cfg := BackpressureConfig{
		QueueThreshold:     100,
		ErrorRateThreshold: 0.1,
		ThrottleFactor:     0.5,
		MaxQueueDepth:      10000,
		MeasurementWindow:  5 * time.Minute,
		CooldownPeriod:     2 * time.Minute,
	}
	svc := NewBackpressureService(nil, cfg).(*backpressureService)
	instance := "test"

	bp := svc.getOrCreateInstance(instance)
	bp.mu.Lock()
	bp.queueDepth = 200
	bp.isThrottled = false
	bp.mu.Unlock()

	throttled, factor, err := svc.ShouldThrottle(context.Background(), instance)
	assert.NoError(t, err)
	assert.True(t, throttled)
	assert.True(t, factor > 0)
}

func TestShouldThrottle_HighErrorRate(t *testing.T) {
	cfg := BackpressureConfig{
		QueueThreshold:     1000,
		ErrorRateThreshold: 0.1,
		ThrottleFactor:     0.5,
		MaxQueueDepth:      10000,
		MeasurementWindow:  5 * time.Minute,
		CooldownPeriod:     2 * time.Minute,
	}
	svc := NewBackpressureService(nil, cfg).(*backpressureService)
	instance := "test"

	bp := svc.getOrCreateInstance(instance)
	bp.mu.Lock()
	bp.errorRate = 0.5
	bp.isThrottled = false
	bp.mu.Unlock()

	throttled, _, err := svc.ShouldThrottle(context.Background(), instance)
	assert.NoError(t, err)
	assert.True(t, throttled)
}

func TestShouldThrottle_ConsecutiveErrors(t *testing.T) {
	cfg := BackpressureConfig{
		QueueThreshold:     100,
		ErrorRateThreshold: 0.1,
		ThrottleFactor:     0.5,
		MaxQueueDepth:      10000,
		MeasurementWindow:  5 * time.Minute,
		CooldownPeriod:     2 * time.Minute,
	}
	svc := NewBackpressureService(nil, cfg).(*backpressureService)
	instance := "test"

	bp := svc.getOrCreateInstance(instance)
	bp.mu.Lock()
	bp.queueDepth = 200
	bp.consecutiveErrors = 5
	bp.isThrottled = false
	bp.mu.Unlock()

	throttled, factor, err := svc.ShouldThrottle(context.Background(), instance)
	assert.NoError(t, err)
	assert.True(t, throttled)
	assert.True(t, factor > 0)
}

func TestShouldThrottle_NotThrottled(t *testing.T) {
	cfg := BackpressureConfig{
		QueueThreshold:     1000,
		ErrorRateThreshold: 0.1,
		ThrottleFactor:     0.5,
		MaxQueueDepth:      10000,
		MeasurementWindow:  5 * time.Minute,
		CooldownPeriod:     2 * time.Minute,
	}
	svc := NewBackpressureService(nil, cfg).(*backpressureService)
	instance := "healthy"

	bp := svc.getOrCreateInstance(instance)
	bp.mu.Lock()
	bp.queueDepth = 50
	bp.errorRate = 0.01
	bp.consecutiveErrors = 0
	bp.isThrottled = false
	bp.mu.Unlock()

	throttled, factor, err := svc.ShouldThrottle(context.Background(), instance)
	assert.NoError(t, err)
	assert.False(t, throttled)
	assert.Equal(t, 1.0, factor)
}

func TestNewBackpressureService_Defaults(t *testing.T) {
	svc := NewBackpressureService(nil, BackpressureConfig{})
	require.NotNil(t, svc)

	concrete := svc.(*backpressureService)
	assert.Equal(t, 1000, concrete.config.QueueThreshold)
	assert.Equal(t, 0.1, concrete.config.ErrorRateThreshold)
	assert.Equal(t, 0.5, concrete.config.ThrottleFactor)
	assert.Equal(t, 1.2, concrete.config.RecoveryFactor)
	assert.Equal(t, 5*time.Minute, concrete.config.MeasurementWindow)
	assert.Equal(t, 2*time.Minute, concrete.config.CooldownPeriod)
	assert.Equal(t, 10000, concrete.config.MaxQueueDepth)
}
