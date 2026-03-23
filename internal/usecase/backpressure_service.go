package usecase

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"athena/internal/domain"
)

// BackpressureConfig defines backpressure settings
type BackpressureConfig struct {
	QueueThreshold       int           // Queue depth before throttling
	ErrorRateThreshold   float64       // Error rate threshold (0.0-1.0)
	ThrottleFactor       float64       // Rate reduction factor when throttled
	RecoveryFactor       float64       // Rate increase factor during recovery
	MeasurementWindow    time.Duration // Time window for measurements
	CooldownPeriod       time.Duration // Time to wait before recovery
	MaxQueueDepth        int           // Maximum allowed queue depth
	EmergencyStopEnabled bool          // Enable emergency stop on critical load
}

// BackpressureService manages federation backpressure
type BackpressureService interface {
	// ShouldThrottle checks if an instance should be throttled
	ShouldThrottle(ctx context.Context, instance string) (bool, float64, error)
	// RecordMetrics updates backpressure metrics for an instance
	RecordMetrics(ctx context.Context, instance string, metrics BackpressureMetrics) error
	// GetStatus returns backpressure status for an instance
	GetStatus(ctx context.Context, instance string) (*BackpressureStatus, error)
	// Reset manually resets backpressure for an instance
	Reset(ctx context.Context, instance string) error
	// GetAllStatuses returns status for all monitored instances
	GetAllStatuses(ctx context.Context) (map[string]*BackpressureStatus, error)
}

// BackpressureMetrics contains metrics for backpressure calculation
type BackpressureMetrics struct {
	QueueDepth     int
	ProcessingRate float64 // items per second
	ErrorRate      float64 // percentage (0.0-1.0)
	SuccessCount   int
	FailureCount   int
	AverageLatency time.Duration
	P99Latency     time.Duration
}

// BackpressureStatus represents the current backpressure state
type BackpressureStatus struct {
	Instance          string
	IsThrottled       bool
	ThrottleFactor    float64
	QueueDepth        int
	ProcessingRate    float64
	ErrorRate         float64
	LastMeasurement   time.Time
	ThrottledSince    *time.Time
	RecoverAt         *time.Time
	ConsecutiveErrors int
	Metadata          map[string]interface{}
}

type backpressureService struct {
	hardening HardeningRepository
	config    BackpressureConfig
	instances map[string]*instanceBackpressure
	mu        sync.RWMutex
}

// Per-instance backpressure tracking
type instanceBackpressure struct {
	instance          string
	queueDepth        int
	processingRate    float64
	errorRate         float64
	throttleFactor    float64
	isThrottled       bool
	throttledAt       *time.Time
	recoverAt         *time.Time
	lastMeasurement   time.Time
	consecutiveErrors int
	measurements      []BackpressureMetrics
	measurementWindow []time.Time
	mu                sync.RWMutex
}

// NewBackpressureService creates a new backpressure service
func NewBackpressureService(hardening HardeningRepository, config BackpressureConfig) BackpressureService {
	// Set defaults
	if config.QueueThreshold == 0 {
		config.QueueThreshold = 1000
	}
	if config.ErrorRateThreshold == 0 {
		config.ErrorRateThreshold = 0.1 // 10% error rate
	}
	if config.ThrottleFactor == 0 {
		config.ThrottleFactor = 0.5 // Reduce rate by 50%
	}
	if config.RecoveryFactor == 0 {
		config.RecoveryFactor = 1.2 // Increase rate by 20%
	}
	if config.MeasurementWindow == 0 {
		config.MeasurementWindow = 5 * time.Minute
	}
	if config.CooldownPeriod == 0 {
		config.CooldownPeriod = 2 * time.Minute
	}
	if config.MaxQueueDepth == 0 {
		config.MaxQueueDepth = 10000
	}

	return &backpressureService{
		hardening: hardening,
		config:    config,
		instances: make(map[string]*instanceBackpressure),
	}
}

// ShouldThrottle determines if an instance should be throttled
func (s *backpressureService) ShouldThrottle(ctx context.Context, instance string) (bool, float64, error) {
	bp := s.getOrCreateInstance(instance)

	bp.mu.RLock()
	defer bp.mu.RUnlock()

	// Check for emergency stop first (highest priority)
	if s.config.EmergencyStopEnabled && bp.queueDepth > s.config.MaxQueueDepth {
		// Complete stop - queue is critically full
		if s.hardening != nil {
			_ = s.hardening.RecordMetric(ctx, &domain.FederationMetric{
				MetricType:     "backpressure_emergency_stop",
				MetricValue:    1,
				InstanceDomain: &instance,
				Timestamp:      time.Now(),
			})
		}
		return true, 0.0, nil // Complete stop
	}

	// Check if already throttled and still in cooldown
	if bp.isThrottled && bp.recoverAt != nil && time.Now().Before(*bp.recoverAt) {
		return true, bp.throttleFactor, nil
	}

	// Calculate if throttling is needed
	shouldThrottle := false
	throttleFactor := 1.0

	// Check queue depth
	if bp.queueDepth > s.config.QueueThreshold {
		shouldThrottle = true
		// Progressive throttling based on how far over threshold
		overageRatio := float64(bp.queueDepth-s.config.QueueThreshold) / float64(s.config.QueueThreshold)
		throttleFactor = s.config.ThrottleFactor * (1.0 - overageRatio*0.5) // More aggressive with higher overage
		if throttleFactor < 0.1 {
			throttleFactor = 0.1 // Minimum 10% throughput
		}
	}

	// Check error rate
	if bp.errorRate > s.config.ErrorRateThreshold {
		shouldThrottle = true
		// Further reduce rate based on error severity
		errorFactor := 1.0 - (bp.errorRate - s.config.ErrorRateThreshold)
		if errorFactor < throttleFactor {
			throttleFactor = errorFactor
		}
	}

	// Check consecutive errors (circuit breaker-like behavior)
	if bp.consecutiveErrors > 3 {
		shouldThrottle = true
		throttleFactor = throttleFactor * 0.5 // Halve the rate on consecutive errors
	}

	return shouldThrottle, throttleFactor, nil
}

// RecordMetrics updates backpressure metrics for an instance
func (s *backpressureService) RecordMetrics(ctx context.Context, instance string, metrics BackpressureMetrics) error {
	bp := s.getOrCreateInstance(instance)

	bp.mu.Lock()
	defer bp.mu.Unlock()

	now := time.Now()
	bp.lastMeasurement = now

	s.updateCurrentMetrics(bp, metrics)
	s.pruneAndAverageMeasurements(bp, now)

	shouldThrottle, throttleFactor := s.calculateThrottleState(bp)
	s.applyThrottleTransition(ctx, bp, instance, now, shouldThrottle, throttleFactor)

	// Persist state if hardening is available
	if s.hardening != nil {
		s.persistBackpressureState(ctx, bp)
	}

	return nil
}

// updateCurrentMetrics applies incoming metrics and tracks consecutive errors.
func (s *backpressureService) updateCurrentMetrics(bp *instanceBackpressure, metrics BackpressureMetrics) {
	bp.queueDepth = metrics.QueueDepth
	bp.processingRate = metrics.ProcessingRate
	bp.errorRate = metrics.ErrorRate

	if metrics.ErrorRate > s.config.ErrorRateThreshold {
		bp.consecutiveErrors++
	} else {
		bp.consecutiveErrors = 0
	}
}

// pruneAndAverageMeasurements adds the current timestamp, removes expired entries,
// and recalculates windowed averages for error rate and processing rate.
func (s *backpressureService) pruneAndAverageMeasurements(bp *instanceBackpressure, now time.Time) {
	bp.measurements = append(bp.measurements, BackpressureMetrics{
		QueueDepth:     bp.queueDepth,
		ProcessingRate: bp.processingRate,
		ErrorRate:      bp.errorRate,
	})
	bp.measurementWindow = append(bp.measurementWindow, now)

	// Clean old measurements
	cutoff := now.Add(-s.config.MeasurementWindow)
	validIdx := 0
	for i, t := range bp.measurementWindow {
		if t.After(cutoff) {
			validIdx = i
			break
		}
	}
	if validIdx > 0 {
		bp.measurements = bp.measurements[validIdx:]
		bp.measurementWindow = bp.measurementWindow[validIdx:]
	}

	// Calculate average metrics over window
	if len(bp.measurements) > 0 {
		var totalErrorRate float64
		var totalProcessingRate float64
		for _, m := range bp.measurements {
			totalErrorRate += m.ErrorRate
			totalProcessingRate += m.ProcessingRate
		}
		bp.errorRate = totalErrorRate / float64(len(bp.measurements))
		bp.processingRate = totalProcessingRate / float64(len(bp.measurements))
	}
}

// applyThrottleTransition transitions the instance between throttled and recovered states.
func (s *backpressureService) applyThrottleTransition(ctx context.Context, bp *instanceBackpressure, instance string, now time.Time, shouldThrottle bool, throttleFactor float64) {
	if shouldThrottle && !bp.isThrottled {
		// Start throttling
		bp.isThrottled = true
		bp.throttledAt = &now
		recoverTime := now.Add(s.config.CooldownPeriod)
		bp.recoverAt = &recoverTime
		bp.throttleFactor = throttleFactor

		if s.hardening != nil {
			metadata, _ := json.Marshal(map[string]interface{}{
				"queue_depth":        bp.queueDepth,
				"error_rate":         bp.errorRate,
				"consecutive_errors": bp.consecutiveErrors,
			})
			_ = s.hardening.RecordMetric(ctx, &domain.FederationMetric{
				MetricType:     "backpressure_throttled",
				MetricValue:    throttleFactor,
				InstanceDomain: &instance,
				Metadata:       metadata,
				Timestamp:      now,
			})
		}
	} else if !shouldThrottle && bp.isThrottled && bp.recoverAt != nil && now.After(*bp.recoverAt) {
		// Stop throttling - recovery period complete
		bp.isThrottled = false
		bp.throttledAt = nil
		bp.recoverAt = nil
		bp.throttleFactor = 1.0
		bp.consecutiveErrors = 0

		if s.hardening != nil {
			_ = s.hardening.RecordMetric(ctx, &domain.FederationMetric{
				MetricType:     "backpressure_recovered",
				MetricValue:    1,
				InstanceDomain: &instance,
				Timestamp:      now,
			})
		}
	} else if bp.isThrottled {
		// Update throttle factor if still throttled
		bp.throttleFactor = throttleFactor
	}
}

// getOrCreateInstance gets or creates backpressure tracking for an instance
func (s *backpressureService) getOrCreateInstance(instance string) *instanceBackpressure {
	s.mu.RLock()
	bp, exists := s.instances[instance]
	s.mu.RUnlock()

	if exists {
		return bp
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock
	if bp, exists = s.instances[instance]; exists {
		return bp
	}

	// Create new instance tracking
	bp = &instanceBackpressure{
		instance:          instance,
		throttleFactor:    1.0,
		lastMeasurement:   time.Now(),
		measurements:      make([]BackpressureMetrics, 0),
		measurementWindow: make([]time.Time, 0),
	}
	s.instances[instance] = bp
	return bp
}

// calculateThrottleState determines if throttling is needed
func (s *backpressureService) calculateThrottleState(bp *instanceBackpressure) (bool, float64) {
	// Already handled in ShouldThrottle, but separated for clarity
	shouldThrottle := false
	throttleFactor := 1.0

	if bp.queueDepth > s.config.QueueThreshold {
		shouldThrottle = true
		overageRatio := float64(bp.queueDepth-s.config.QueueThreshold) / float64(s.config.QueueThreshold)
		throttleFactor = s.config.ThrottleFactor * (1.0 - overageRatio*0.5)
		if throttleFactor < 0.1 {
			throttleFactor = 0.1
		}
	}

	if bp.errorRate > s.config.ErrorRateThreshold {
		shouldThrottle = true
		errorFactor := 1.0 - (bp.errorRate - s.config.ErrorRateThreshold)
		if errorFactor < throttleFactor {
			throttleFactor = errorFactor
		}
	}

	if bp.consecutiveErrors > 3 {
		shouldThrottle = true
		throttleFactor = throttleFactor * 0.5
	}

	return shouldThrottle, throttleFactor
}

// GetStatus returns backpressure status for an instance
func (s *backpressureService) GetStatus(ctx context.Context, instance string) (*BackpressureStatus, error) {
	bp := s.getOrCreateInstance(instance)
	bp.mu.RLock()
	defer bp.mu.RUnlock()

	return &BackpressureStatus{
		Instance:          bp.instance,
		IsThrottled:       bp.isThrottled,
		ThrottleFactor:    bp.throttleFactor,
		QueueDepth:        bp.queueDepth,
		ProcessingRate:    bp.processingRate,
		ErrorRate:         bp.errorRate,
		LastMeasurement:   bp.lastMeasurement,
		ThrottledSince:    bp.throttledAt,
		RecoverAt:         bp.recoverAt,
		ConsecutiveErrors: bp.consecutiveErrors,
		Metadata: map[string]interface{}{
			"measurement_count": len(bp.measurements),
		},
	}, nil
}

// Reset manually resets backpressure for an instance
func (s *backpressureService) Reset(ctx context.Context, instance string) error {
	bp := s.getOrCreateInstance(instance)
	bp.mu.Lock()
	defer bp.mu.Unlock()

	bp.queueDepth = 0
	bp.processingRate = 0
	bp.errorRate = 0
	bp.throttleFactor = 1.0
	bp.isThrottled = false
	bp.throttledAt = nil
	bp.recoverAt = nil
	bp.consecutiveErrors = 0
	bp.measurements = make([]BackpressureMetrics, 0)
	bp.measurementWindow = make([]time.Time, 0)
	bp.lastMeasurement = time.Now()

	// Record reset
	if s.hardening != nil {
		_ = s.hardening.RecordMetric(ctx, &domain.FederationMetric{
			MetricType:     "backpressure_reset",
			MetricValue:    1,
			InstanceDomain: &instance,
			Timestamp:      time.Now(),
		})
	}

	return nil
}

// GetAllStatuses returns status for all monitored instances
func (s *backpressureService) GetAllStatuses(ctx context.Context) (map[string]*BackpressureStatus, error) {
	s.mu.RLock()
	instanceList := make([]string, 0, len(s.instances))
	for instance := range s.instances {
		instanceList = append(instanceList, instance)
	}
	s.mu.RUnlock()

	statuses := make(map[string]*BackpressureStatus)
	for _, instance := range instanceList {
		status, err := s.GetStatus(ctx, instance)
		if err == nil {
			statuses[instance] = status
		}
	}

	return statuses, nil
}

// persistBackpressureState saves backpressure state to database
func (s *backpressureService) persistBackpressureState(ctx context.Context, bp *instanceBackpressure) {
	// This could be implemented to persist to federation_backpressure table
	// For now, we keep state in memory only
}
