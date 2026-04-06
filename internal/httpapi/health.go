package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
	"vidra-core/internal/health"
	"vidra-core/internal/httpapi/shared"

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

type HealthHandlers struct {
	checkers    []health.Checker
	db          *sqlx.DB
	redis       *redis.Client
	ipfsAPI     string
	iotaNodeURL string
}

// QueueDepthFunc returns the current depth of a queue. Nil functions are treated as
// unavailable queues and the queue health check will be skipped.
type QueueDepthFunc func() (int, error)

func NewHealthHandlers(db *sqlx.DB, redisClient *redis.Client, ipfsAPI string, iotaNodeURL string, encodingQueueDepth, activityQueueDepth QueueDepthFunc) *HealthHandlers {
	checkers := []health.Checker{
		health.NewDatabaseChecker(db),
		health.NewRedisChecker(redisClient),
		health.NewIPFSChecker(ipfsAPI),
	}

	if encodingQueueDepth != nil && activityQueueDepth != nil {
		checkers = append(checkers, health.NewQueueDepthChecker(encodingQueueDepth, activityQueueDepth))
	}

	if iotaNodeURL != "" {
		checkers = append(checkers, health.NewIOTAChecker(iotaNodeURL))
	}

	return &HealthHandlers{
		checkers:    checkers,
		db:          db,
		redis:       redisClient,
		ipfsAPI:     ipfsAPI,
		iotaNodeURL: iotaNodeURL,
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

func (h *HealthHandlers) ReadinessCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	checks := make(map[string]string)
	overallStatus := "ok"
	statusCode := http.StatusOK

	for _, checker := range h.checkers {
		err := checker.Check(ctx)
		if err != nil {
			slog.Info(fmt.Sprintf("ERROR: %s health check failed: %v", checker.Name(), err))
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

func ReadinessCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	checks := make(map[string]string)
	overallStatus := "ok"
	statusCode := http.StatusOK

	if err := checkDatabase(); err != nil {
		slog.Info(fmt.Sprintf("ERROR: Database health check failed: %v", err))
		checks["database"] = "fail"
		overallStatus = "fail"
		statusCode = http.StatusServiceUnavailable
	} else {
		checks["database"] = "ok"
	}

	if err := checkRedis(); err != nil {
		slog.Info(fmt.Sprintf("ERROR: Redis health check failed: %v", err))
		checks["redis"] = "fail"
		overallStatus = "fail"
		statusCode = http.StatusServiceUnavailable
	} else {
		checks["redis"] = "ok"
	}

	if err := checkIPFS(); err != nil {
		slog.Info(fmt.Sprintf("ERROR: IPFS health check failed: %v", err))
		checks["ipfs"] = "fail"
		overallStatus = "fail"
		statusCode = http.StatusServiceUnavailable
	} else {
		checks["ipfs"] = "ok"
	}

	if err := checkQueueDepth(); err != nil {
		slog.Info(fmt.Sprintf("ERROR: Queue health check failed: %v", err))
		checks["queue"] = "fail"
		overallStatus = "fail"
		statusCode = http.StatusServiceUnavailable
	} else {
		checks["queue"] = "ok"
	}

	_ = ctx

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
	// Queue depth monitoring is handled by the HealthHandlers.ReadinessCheck path
	// via injected QueueDepthFunc providers. This standalone function is a no-op
	// placeholder for the legacy ReadinessCheck handler.
	return nil
}
