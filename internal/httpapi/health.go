package httpapi

import (
	"athena/internal/health"
	"athena/internal/httpapi/shared"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"athena/internal/domain"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

type HealthResponse struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Version   string            `json:"version"`
	Uptime    string            `json:"uptime"`
	Checks    map[string]string `json:"checks,omitempty"`
}

var startTime = time.Now()

// HealthHandlers manages health check endpoints
type HealthHandlers struct {
	checkers []health.Checker
	db       *sqlx.DB
	redis    *redis.Client
	ipfsAPI  string
}

// NewHealthHandlers creates a new health handlers instance
func NewHealthHandlers(db *sqlx.DB, redisClient *redis.Client, ipfsAPI string) *HealthHandlers {
	// Initialize checkers
	checkers := []health.Checker{
		health.NewDatabaseChecker(db),
		health.NewRedisChecker(redisClient),
		health.NewIPFSChecker(ipfsAPI),
		health.NewQueueDepthChecker(
			func() (int, error) { return 5, nil },  // TODO: Replace with real queue service
			func() (int, error) { return 10, nil }, // TODO: Replace with real queue service
		),
	}

	return &HealthHandlers{
		checkers: checkers,
		db:       db,
		redis:    redisClient,
		ipfsAPI:  ipfsAPI,
	}
}

func HealthCheck(w http.ResponseWriter, r *http.Request) {
	health := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now(),
		Version:   "1.0.0",
		Uptime:    time.Since(startTime).String(),
	}

	shared.WriteJSON(w, http.StatusOK, health)
}

// ReadinessCheckHandler is a method on HealthHandlers that performs readiness checks
func (h *HealthHandlers) ReadinessCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	checks := make(map[string]string)
	overallStatus := "ok"
	statusCode := http.StatusOK

	// Perform all health checks
	for _, checker := range h.checkers {
		err := checker.Check(ctx)
		if err != nil {
			// SECURITY FIX: Log detailed errors server-side, return generic status to client
			log.Printf("ERROR: %s health check failed: %v", checker.Name(), err)
			checks[checker.Name()] = "fail"
			overallStatus = "fail"
			statusCode = http.StatusServiceUnavailable
		} else {
			checks[checker.Name()] = "ok"
		}
	}

	readiness := HealthResponse{
		Status:    overallStatus,
		Timestamp: time.Now(),
		Version:   "1.0.0",
		Uptime:    time.Since(startTime).String(),
		Checks:    checks,
	}

	shared.WriteJSON(w, statusCode, readiness)
}

// ReadinessCheck is a standalone function for backward compatibility
func ReadinessCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	checks := make(map[string]string)
	overallStatus := "ok"
	statusCode := http.StatusOK

	// Create temporary checkers for backward compatibility
	// This should be replaced with proper dependency injection
	if err := checkDatabase(); err != nil {
		log.Printf("ERROR: Database health check failed: %v", err)
		checks["database"] = "fail"
		overallStatus = "fail"
		statusCode = http.StatusServiceUnavailable
	} else {
		checks["database"] = "ok"
	}

	if err := checkRedis(); err != nil {
		log.Printf("ERROR: Redis health check failed: %v", err)
		checks["redis"] = "fail"
		overallStatus = "fail"
		statusCode = http.StatusServiceUnavailable
	} else {
		checks["redis"] = "ok"
	}

	if err := checkIPFS(); err != nil {
		log.Printf("ERROR: IPFS health check failed: %v", err)
		checks["ipfs"] = "fail"
		overallStatus = "fail"
		statusCode = http.StatusServiceUnavailable
	} else {
		checks["ipfs"] = "ok"
	}

	if err := checkQueueDepth(); err != nil {
		log.Printf("ERROR: Queue health check failed: %v", err)
		checks["queue"] = "fail"
		overallStatus = "fail"
		statusCode = http.StatusServiceUnavailable
	} else {
		checks["queue"] = "ok"
	}

	_ = ctx // Suppress unused warning

	readiness := HealthResponse{
		Status:    overallStatus,
		Timestamp: time.Now(),
		Version:   "1.0.0",
		Uptime:    time.Since(startTime).String(),
		Checks:    checks,
	}

	shared.WriteJSON(w, statusCode, readiness)
}

func checkDatabase() error {
	return nil
}

func checkRedis() error {
	return nil
}

func checkIPFS() error {
	return nil
}

func checkQueueDepth() error {
	queueDepth := 5
	maxQueueDepth := 1000

	if queueDepth > maxQueueDepth {
		return domain.NewDomainError("QUEUE_OVERLOAD", "Queue depth exceeds threshold")
	}

	return nil
}
