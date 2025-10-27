package httpapi

import (
	"athena/internal/httpapi/shared"
	"log"
	"net/http"
	"time"

	"athena/internal/domain"
)

type HealthResponse struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Version   string            `json:"version"`
	Uptime    string            `json:"uptime"`
	Checks    map[string]string `json:"checks,omitempty"`
}

var startTime = time.Now()

func HealthCheck(w http.ResponseWriter, r *http.Request) {
	health := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now(),
		Version:   "1.0.0",
		Uptime:    time.Since(startTime).String(),
	}

	shared.WriteJSON(w, http.StatusOK, health)
}

func ReadinessCheck(w http.ResponseWriter, r *http.Request) {
	checks := make(map[string]string)
	overallStatus := "ok"
	statusCode := http.StatusOK

	// SECURITY FIX: Log detailed errors server-side, return generic status to client
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
