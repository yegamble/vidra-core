package usecase

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"athena/internal/domain"
)

// CircuitState represents the state of a circuit breaker
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

// CircuitBreakerConfig defines circuit breaker settings
type CircuitBreakerConfig struct {
	FailureThreshold   int           // Number of failures to open circuit
	SuccessThreshold   int           // Number of successes to close circuit
	Timeout            time.Duration // Time before attempting to half-open
	HalfOpenMaxCalls   int           // Max calls allowed in half-open state
	ErrorRateThreshold float64       // Error rate to open circuit (0.0-1.0)
	WindowSize         time.Duration // Time window for error rate calculation
}

// CircuitBreaker interface for managing circuit breakers
type CircuitBreaker interface {
	// Call executes a function with circuit breaker protection
	Call(ctx context.Context, endpoint string, fn func() error) error
	// GetState returns the current state of a circuit
	GetState(ctx context.Context, endpoint string) (CircuitState, error)
	// Reset manually resets a circuit breaker
	Reset(ctx context.Context, endpoint string) error
	// GetStats returns statistics for a circuit
	GetStats(ctx context.Context, endpoint string) (*CircuitStats, error)
}

// CircuitStats holds circuit breaker statistics
type CircuitStats struct {
	State               CircuitState
	FailureCount        int
	SuccessCount        int
	ConsecutiveFailures int
	ConsecutiveSuccess  int
	ErrorRate           float64
	LastFailure         *time.Time
	LastSuccess         *time.Time
}

type circuitBreakerService struct {
	hardening HardeningRepository
	config    CircuitBreakerConfig
	breakers  map[string]*circuitBreaker
	mu        sync.RWMutex
}

// Individual circuit breaker instance
type circuitBreaker struct {
	endpoint            string
	state               CircuitState
	failures            int
	successes           int
	consecutiveFailures int
	consecutiveSuccess  int
	lastFailure         *time.Time
	lastSuccess         *time.Time
	halfOpenCalls       int
	openedAt            *time.Time
	halfOpenAt          *time.Time
	errorCount          int
	totalCount          int
	windowStart         time.Time
	mu                  sync.RWMutex
}

// NewCircuitBreakerService creates a new circuit breaker service
func NewCircuitBreakerService(hardening HardeningRepository, config CircuitBreakerConfig) CircuitBreaker {
	// Set defaults
	if config.FailureThreshold == 0 {
		config.FailureThreshold = 5
	}
	if config.SuccessThreshold == 0 {
		config.SuccessThreshold = 2
	}
	if config.Timeout == 0 {
		config.Timeout = 60 * time.Second
	}
	if config.HalfOpenMaxCalls == 0 {
		config.HalfOpenMaxCalls = 3
	}
	if config.ErrorRateThreshold == 0 {
		config.ErrorRateThreshold = 0.5
	}
	if config.WindowSize == 0 {
		config.WindowSize = 5 * time.Minute
	}

	return &circuitBreakerService{
		hardening: hardening,
		config:    config,
		breakers:  make(map[string]*circuitBreaker),
	}
}

// Call executes a function with circuit breaker protection
func (s *circuitBreakerService) Call(ctx context.Context, endpoint string, fn func() error) error {
	cb := s.getOrCreateBreaker(endpoint)

	// Check if we can proceed
	if err := cb.canProceed(); err != nil {
		// Record blocked call metric
		if s.hardening != nil {
			_ = s.hardening.RecordMetric(ctx, &domain.FederationMetric{
				MetricType:     "circuit_breaker_blocked",
				MetricValue:    1,
				InstanceDomain: &endpoint,
				Timestamp:      time.Now(),
			})
		}
		return err
	}

	// Execute the function
	err := fn()

	// Update circuit breaker state based on result
	cb.recordResult(err == nil)

	// Persist state if hardening is available
	if s.hardening != nil {
		s.persistState(ctx, cb)
	}

	// Check if state transition is needed
	s.checkStateTransition(ctx, cb)

	return err
}

// getOrCreateBreaker gets or creates a circuit breaker for an endpoint
func (s *circuitBreakerService) getOrCreateBreaker(endpoint string) *circuitBreaker {
	s.mu.RLock()
	cb, exists := s.breakers[endpoint]
	s.mu.RUnlock()

	if exists {
		return cb
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock
	if cb, exists = s.breakers[endpoint]; exists {
		return cb
	}

	// Create new circuit breaker
	cb = &circuitBreaker{
		endpoint:    endpoint,
		state:       CircuitClosed,
		windowStart: time.Now(),
	}
	s.breakers[endpoint] = cb
	return cb
}

// canProceed checks if a call can proceed through the circuit
func (cb *circuitBreaker) canProceed() error {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	switch cb.state {
	case CircuitOpen:
		// Check if it's time to transition to half-open
		if cb.halfOpenAt != nil && time.Now().After(*cb.halfOpenAt) {
			return nil // Allow one call to test
		}
		return fmt.Errorf("circuit breaker is open for endpoint: %s", cb.endpoint)

	case CircuitHalfOpen:
		// Limit calls in half-open state
		if cb.halfOpenCalls >= 3 { // Hardcoded limit for half-open
			return fmt.Errorf("circuit breaker half-open limit reached for endpoint: %s", cb.endpoint)
		}
		return nil

	case CircuitClosed:
		return nil

	default:
		return nil
	}
}

// recordResult updates circuit breaker metrics based on call result
func (cb *circuitBreaker) recordResult(success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	// Reset window if needed
	if now.Sub(cb.windowStart) > 5*time.Minute {
		cb.errorCount = 0
		cb.totalCount = 0
		cb.windowStart = now
	}

	cb.totalCount++

	if success {
		cb.successes++
		cb.consecutiveSuccess++
		cb.consecutiveFailures = 0
		cb.lastSuccess = &now

		if cb.state == CircuitHalfOpen {
			cb.halfOpenCalls++
		}
	} else {
		cb.failures++
		cb.errorCount++
		cb.consecutiveFailures++
		cb.consecutiveSuccess = 0
		cb.lastFailure = &now

		if cb.state == CircuitHalfOpen {
			cb.halfOpenCalls++
		}
	}
}

// checkStateTransition determines if circuit breaker state should change
func (s *circuitBreakerService) checkStateTransition(ctx context.Context, cb *circuitBreaker) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	switch cb.state {
	case CircuitClosed:
		// Calculate error rate
		errorRate := float64(cb.errorCount) / float64(cb.totalCount)

		// Check if we should open the circuit
		if cb.consecutiveFailures >= s.config.FailureThreshold ||
			(cb.totalCount >= 10 && errorRate > s.config.ErrorRateThreshold) {
			// Open the circuit
			cb.state = CircuitOpen
			cb.openedAt = &now
			halfOpenTime := now.Add(s.config.Timeout)
			cb.halfOpenAt = &halfOpenTime
			cb.halfOpenCalls = 0

			// Record metric
			if s.hardening != nil {
				_ = s.hardening.RecordMetric(ctx, &domain.FederationMetric{
					MetricType:     "circuit_breaker_opened",
					MetricValue:    1,
					InstanceDomain: &cb.endpoint,
					Metadata: map[string]interface{}{
						"consecutive_failures": cb.consecutiveFailures,
						"error_rate":           errorRate,
					},
					Timestamp: now,
				})
			}
		}

	case CircuitOpen:
		// Check if it's time to try half-open
		if cb.halfOpenAt != nil && now.After(*cb.halfOpenAt) {
			cb.state = CircuitHalfOpen
			cb.halfOpenCalls = 0
			cb.consecutiveSuccess = 0
			cb.consecutiveFailures = 0
		}

	case CircuitHalfOpen:
		// Check if we should close or re-open
		if cb.consecutiveSuccess >= s.config.SuccessThreshold {
			// Close the circuit
			cb.state = CircuitClosed
			cb.openedAt = nil
			cb.halfOpenAt = nil
			cb.consecutiveFailures = 0
			cb.errorCount = 0
			cb.totalCount = 0
			cb.windowStart = now

			// Record metric
			if s.hardening != nil {
				_ = s.hardening.RecordMetric(ctx, &domain.FederationMetric{
					MetricType:     "circuit_breaker_closed",
					MetricValue:    1,
					InstanceDomain: &cb.endpoint,
					Timestamp:      now,
				})
			}
		} else if cb.consecutiveFailures > 0 {
			// Re-open the circuit
			cb.state = CircuitOpen
			cb.openedAt = &now
			halfOpenTime := now.Add(s.config.Timeout * time.Duration(math.Min(float64(cb.failures/s.config.FailureThreshold), 5)))
			cb.halfOpenAt = &halfOpenTime
			cb.halfOpenCalls = 0
		}
	}
}

// GetState returns the current state of a circuit
func (s *circuitBreakerService) GetState(ctx context.Context, endpoint string) (CircuitState, error) {
	cb := s.getOrCreateBreaker(endpoint)
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state, nil
}

// Reset manually resets a circuit breaker
func (s *circuitBreakerService) Reset(ctx context.Context, endpoint string) error {
	cb := s.getOrCreateBreaker(endpoint)
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = CircuitClosed
	cb.failures = 0
	cb.successes = 0
	cb.consecutiveFailures = 0
	cb.consecutiveSuccess = 0
	cb.errorCount = 0
	cb.totalCount = 0
	cb.windowStart = time.Now()
	cb.openedAt = nil
	cb.halfOpenAt = nil
	cb.halfOpenCalls = 0

	// Persist reset
	if s.hardening != nil {
		_ = s.hardening.RecordMetric(ctx, &domain.FederationMetric{
			MetricType:     "circuit_breaker_reset",
			MetricValue:    1,
			InstanceDomain: &endpoint,
			Timestamp:      time.Now(),
		})
	}

	return nil
}

// GetStats returns statistics for a circuit
func (s *circuitBreakerService) GetStats(ctx context.Context, endpoint string) (*CircuitStats, error) {
	cb := s.getOrCreateBreaker(endpoint)
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	errorRate := float64(0)
	if cb.totalCount > 0 {
		errorRate = float64(cb.errorCount) / float64(cb.totalCount)
	}

	return &CircuitStats{
		State:               cb.state,
		FailureCount:        cb.failures,
		SuccessCount:        cb.successes,
		ConsecutiveFailures: cb.consecutiveFailures,
		ConsecutiveSuccess:  cb.consecutiveSuccess,
		ErrorRate:           errorRate,
		LastFailure:         cb.lastFailure,
		LastSuccess:         cb.lastSuccess,
	}, nil
}

// persistState saves circuit breaker state to database
func (s *circuitBreakerService) persistState(ctx context.Context, cb *circuitBreaker) {
	// This could be implemented to persist state to federation_circuit_breakers table
	// For now, we keep state in memory only
}
