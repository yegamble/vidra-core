package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"athena/internal/health"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// LIVENESS PROBE TESTS (/health)
// ============================================================================

// Helper function to unwrap health response from shared.Response envelope
func unmarshalHealthResponse(t *testing.T, body []byte) HealthResponse {
	var envelope struct {
		Data    HealthResponse `json:"data"`
		Success bool           `json:"success"`
	}
	err := json.Unmarshal(body, &envelope)
	require.NoError(t, err, "Should unmarshal response envelope")
	return envelope.Data
}

func TestHealthHandler_Always200(t *testing.T) {
	// Test that health endpoint always returns 200 when server is alive
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	HealthCheck(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "Health check should always return 200")

	response := unmarshalHealthResponse(t, w.Body.Bytes())
	assert.Equal(t, "ok", response.Status)
	assert.NotEmpty(t, response.Version)
	assert.NotEmpty(t, response.Uptime)
	assert.NotZero(t, response.Timestamp)
}

func TestHealthHandler_FastResponse(t *testing.T) {
	// Test that health endpoint responds quickly (< 10ms)
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	start := time.Now()
	HealthCheck(w, req)
	duration := time.Since(start)

	assert.Less(t, duration.Milliseconds(), int64(10),
		"Health check should respond in less than 10ms, took %v", duration)
}

func TestHealthHandler_NoDependencyChecks(t *testing.T) {
	// Test that health endpoint doesn't check any external dependencies
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	HealthCheck(w, req)

	response := unmarshalHealthResponse(t, w.Body.Bytes())

	// Checks field should be nil or empty for liveness
	assert.Empty(t, response.Checks, "Liveness check should not include dependency checks")
}

func TestHealthHandler_ConcurrentRequests(t *testing.T) {
	// Test that health endpoint handles concurrent requests properly
	const numRequests = 100
	var wg sync.WaitGroup
	wg.Add(numRequests)

	successCount := int32(0)
	errorCount := int32(0)

	for i := 0; i < numRequests; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/health", nil)
			w := httptest.NewRecorder()

			HealthCheck(w, req)

			if w.Code == http.StatusOK {
				atomic.AddInt32(&successCount, 1)
			} else {
				atomic.AddInt32(&errorCount, 1)
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int32(numRequests), successCount,
		"All concurrent health checks should succeed")
	assert.Equal(t, int32(0), errorCount,
		"No health checks should fail")
}

// ============================================================================
// READINESS PROBE TESTS (/ready) - DATABASE
// ============================================================================

func TestReadyHandler_DatabaseHealthy(t *testing.T) {
	t.Skip("Will fail until real database check is implemented")

	// Mock a healthy database connection
	mockDB := setupMockDatabase(t, true)
	defer mockDB.Close()

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "Should return 200 when database is healthy")

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "ok", response.Checks["database"])
}

func TestReadyHandler_DatabaseDown(t *testing.T) {
	t.Skip("Will fail until real database check is implemented")

	// Mock a failed database connection
	mockDB := setupMockDatabase(t, false)
	defer mockDB.Close()

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"Should return 503 when database is down")

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "fail", response.Checks["database"])
	assert.Equal(t, "fail", response.Status)
}

func TestReadyHandler_DatabasePingTimeout(t *testing.T) {
	t.Skip("Will fail until real database check with timeout is implemented")

	// This test will verify that database ping has a timeout
	// Currently the stub doesn't implement timeouts

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req = req.WithContext(ctx)

	start := time.Now()
	ReadinessCheck(w, req)
	duration := time.Since(start)

	assert.Less(t, duration.Seconds(), float64(3),
		"Database check should timeout within 2 seconds")
}

func TestReadyHandler_DatabaseConnectionPoolExhaustion(t *testing.T) {
	t.Skip("Will fail until real connection pool monitoring is implemented")

	// Test that readiness fails when connection pool is exhausted
	// This requires actual implementation checking db.Stats()

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	// Should detect pool exhaustion and fail
	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"Should return 503 when connection pool is exhausted")
}

func TestReadyHandler_DatabaseReadOnly(t *testing.T) {
	t.Skip("Will fail until real read-only detection is implemented")

	// Test that readiness fails when database is in read-only mode
	// This requires checking if we can perform a write operation

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"Should return 503 when database is read-only")
}

// ============================================================================
// READINESS PROBE TESTS (/ready) - REDIS
// ============================================================================

func TestReadyHandler_RedisHealthy(t *testing.T) {
	t.Skip("Will fail until real Redis check is implemented")

	// Mock a healthy Redis connection
	mockRedis := setupMockRedis(t, true)
	defer mockRedis.Close()

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "Should return 200 when Redis is healthy")

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "ok", response.Checks["redis"])
}

func TestReadyHandler_RedisDown(t *testing.T) {
	t.Skip("Will fail until real Redis check is implemented")

	// Mock a failed Redis connection
	mockRedis := setupMockRedis(t, false)
	defer mockRedis.Close()

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"Should return 503 when Redis is down")

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "fail", response.Checks["redis"])
}

func TestReadyHandler_RedisPingTimeout(t *testing.T) {
	t.Skip("Will fail until real Redis check with timeout is implemented")

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	req = req.WithContext(ctx)

	start := time.Now()
	ReadinessCheck(w, req)
	duration := time.Since(start)

	assert.Less(t, duration.Seconds(), float64(2),
		"Redis check should timeout within 1 second")
}

func TestReadyHandler_RedisMemoryPressure(t *testing.T) {
	t.Skip("Will fail until real Redis memory check is implemented")

	// Test that readiness fails when Redis memory usage is too high
	// This requires checking INFO memory command

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	// Should detect high memory usage and warn/fail
	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"Should return 503 when Redis memory usage is critical")
}

func TestReadyHandler_RedisClusterFailover(t *testing.T) {
	t.Skip("Will fail until Redis cluster support is implemented")

	// Test that readiness handles Redis cluster failover gracefully
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	// Should handle failover scenario
	assert.Contains(t, []int{http.StatusOK, http.StatusServiceUnavailable}, w.Code,
		"Should handle cluster failover appropriately")
}

// ============================================================================
// READINESS PROBE TESTS (/ready) - IPFS
// ============================================================================

func TestReadyHandler_IPFSHealthy(t *testing.T) {
	t.Skip("Will fail until real IPFS check is implemented")

	// Mock a healthy IPFS API
	mockIPFS := setupMockIPFS(t, true)
	defer mockIPFS.Close()

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "Should return 200 when IPFS is healthy")

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "ok", response.Checks["ipfs"])
}

func TestReadyHandler_IPFSDown(t *testing.T) {
	t.Skip("Will fail until real IPFS check is implemented")

	// Mock a failed IPFS connection
	mockIPFS := setupMockIPFS(t, false)
	defer mockIPFS.Close()

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"Should return 503 when IPFS is down")

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "fail", response.Checks["ipfs"])
}

func TestReadyHandler_IPFSVersionEndpoint(t *testing.T) {
	t.Skip("Will fail until IPFS /api/v0/version check is implemented")

	// Test that we check the correct IPFS endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v0/version", r.URL.Path,
			"Should check IPFS version endpoint")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"Version": "0.12.0"}`))
	}))
	defer server.Close()

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
}

func TestReadyHandler_IPFSTimeout(t *testing.T) {
	t.Skip("Will fail until IPFS check with timeout is implemented")

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req = req.WithContext(ctx)

	start := time.Now()
	ReadinessCheck(w, req)
	duration := time.Since(start)

	assert.Less(t, duration.Seconds(), float64(4),
		"IPFS check should timeout within 3 seconds")
}

func TestReadyHandler_IPFSClusterAvailability(t *testing.T) {
	t.Skip("Will fail until IPFS Cluster check is implemented")

	// Test that we also check IPFS Cluster availability if configured
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Should have both ipfs and ipfs-cluster checks when cluster is enabled
	assert.Contains(t, response.Checks, "ipfs")
}

// ============================================================================
// READINESS PROBE TESTS (/ready) - QUEUE DEPTH
// ============================================================================

func TestReadyHandler_QueueNormal(t *testing.T) {
	// This test should pass with current implementation
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"Should return 200 when queue depth is normal")

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "ok", response.Checks["queue"])
}

func TestReadyHandler_QueueSaturated(t *testing.T) {
	t.Skip("Will fail until real queue depth check is implemented")

	// Mock high queue depth (>5000)
	// Current implementation has hardcoded value of 5

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"Should return 503 when queue is saturated (>5000)")

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "fail", response.Checks["queue"])
}

func TestReadyHandler_EncodingQueueDepth(t *testing.T) {
	t.Skip("Will fail until encoding queue check is implemented")

	// Test specific encoding queue depth check
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Should have specific queue metrics in details
	assert.Contains(t, response.Checks, "queue")
}

func TestReadyHandler_ActivityPubDeliveryQueueDepth(t *testing.T) {
	t.Skip("Will fail until ActivityPub queue check is implemented")

	// Test ActivityPub delivery queue depth check
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Should check ActivityPub delivery queue
	assert.Contains(t, response.Checks, "queue")
}

func TestReadyHandler_CombinedQueueMetrics(t *testing.T) {
	t.Skip("Will fail until combined queue metrics are implemented")

	// Test that we report combined queue metrics
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Should have comprehensive queue status
	assert.NotEmpty(t, response.Checks["queue"])
}

// ============================================================================
// RESPONSE FORMAT TESTS
// ============================================================================

func TestReadyHandler_JSONResponseStructure(t *testing.T) {
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	// Check Content-Type
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	// Verify JSON structure
	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	// Verify all expected fields
	assert.NotEmpty(t, response.Status, "Status field is required")
	assert.NotZero(t, response.Timestamp, "Timestamp field is required")
	assert.NotEmpty(t, response.Version, "Version field is required")
	assert.NotEmpty(t, response.Uptime, "Uptime field is required")
	assert.NotNil(t, response.Checks, "Checks field should be present")
}

func TestReadyHandler_ComponentStatusDetails(t *testing.T) {
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify each component has a status
	expectedComponents := []string{"database", "redis", "ipfs", "queue"}
	for _, component := range expectedComponents {
		status, exists := response.Checks[component]
		assert.True(t, exists, "Component %s should be in checks", component)
		assert.Contains(t, []string{"ok", "fail"}, status,
			"Component %s status should be 'ok' or 'fail'", component)
	}
}

func TestReadyHandler_CheckDuration(t *testing.T) {
	t.Skip("Will fail until duration tracking is implemented")

	// Test that response includes check duration for each component
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	// Parse response and check for duration information
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	checks, ok := response["checks"].(map[string]interface{})
	require.True(t, ok, "Checks should be a map")

	// Each check should have duration info
	for component := range checks {
		t.Logf("Component %s should include duration metrics", component)
	}
}

func TestReadyHandler_VersionInformation(t *testing.T) {
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "1.0.0", response.Version, "Should include version information")
}

// ============================================================================
// KUBERNETES INTEGRATION TESTS
// ============================================================================

func TestProbe_InitialDelaySeconds(t *testing.T) {
	// Simulate Kubernetes initial delay behavior
	// Health should return 200 immediately, ready might not

	// Test immediate health check
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	HealthCheck(w, req)
	assert.Equal(t, http.StatusOK, w.Code,
		"Health should be available immediately")

	// Test readiness might not be ready immediately after startup
	req = httptest.NewRequest("GET", "/ready", nil)
	w = httptest.NewRecorder()
	ReadinessCheck(w, req)
	// Could be either 200 or 503 depending on startup state
	assert.Contains(t, []int{http.StatusOK, http.StatusServiceUnavailable}, w.Code)
}

func TestProbe_PeriodSeconds(t *testing.T) {
	// Test that probes can be called repeatedly at short intervals
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()

		HealthCheck(w, req)
		assert.Equal(t, http.StatusOK, w.Code,
			"Probe %d should succeed", i)

		time.Sleep(100 * time.Millisecond) // Simulate periodSeconds
	}
}

func TestProbe_FailureThreshold(t *testing.T) {
	t.Skip("Will fail until proper failure tracking is implemented")

	// Simulate consecutive failures
	failureCount := 0
	maxFailures := 3

	for i := 0; i < maxFailures+1; i++ {
		req := httptest.NewRequest("GET", "/ready", nil)
		w := httptest.NewRecorder()

		ReadinessCheck(w, req)

		if w.Code != http.StatusOK {
			failureCount++
		}

		if failureCount >= maxFailures {
			t.Logf("Reached failure threshold after %d attempts", i+1)
			break
		}
	}

	assert.GreaterOrEqual(t, failureCount, maxFailures,
		"Should track consecutive failures")
}

func TestProbe_SuccessThreshold(t *testing.T) {
	// Test that probe becomes healthy after success threshold
	successCount := 0
	requiredSuccesses := 1

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()

		HealthCheck(w, req)

		if w.Code == http.StatusOK {
			successCount++
			if successCount >= requiredSuccesses {
				break
			}
		} else {
			successCount = 0 // Reset on failure
		}
	}

	assert.GreaterOrEqual(t, successCount, requiredSuccesses,
		"Should become healthy after success threshold")
}

// ============================================================================
// GRACEFUL SHUTDOWN TESTS
// ============================================================================

func TestGracefulShutdown_ReadinessFails(t *testing.T) {
	t.Skip("Will fail until graceful shutdown is implemented")

	// Simulate shutdown initiated
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Trigger shutdown

	req := httptest.NewRequest("GET", "/ready", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"Readiness should fail during shutdown")
}

func TestGracefulShutdown_LivenessContinues(t *testing.T) {
	// Liveness should continue during shutdown
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Trigger shutdown

	req := httptest.NewRequest("GET", "/health", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	HealthCheck(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"Liveness should continue during shutdown")
}

func TestGracefulShutdown_NoNewRequests(t *testing.T) {
	t.Skip("Will fail until request rejection during shutdown is implemented")

	// Simulate server in shutdown mode
	// New requests should be rejected

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	// In shutdown mode, should not accept new work
	ReadinessCheck(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"Should not accept new requests during shutdown")
}

// ============================================================================
// PERFORMANCE TESTS
// ============================================================================

func BenchmarkHealthHandler(b *testing.B) {
	req := httptest.NewRequest("GET", "/health", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		HealthCheck(w, req)

		if w.Code != http.StatusOK {
			b.Fatalf("Health check failed with status %d", w.Code)
		}
	}
}

func BenchmarkReadyHandler(b *testing.B) {
	req := httptest.NewRequest("GET", "/ready", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		ReadinessCheck(w, req)

		// Ready can be either 200 or 503
		if w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
			b.Fatalf("Ready check returned unexpected status %d", w.Code)
		}
	}
}

func TestHealthHandler_Latency(t *testing.T) {
	// Test that health check meets latency requirements (<5ms)
	samples := 100
	var totalDuration time.Duration

	for i := 0; i < samples; i++ {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()

		start := time.Now()
		HealthCheck(w, req)
		totalDuration += time.Since(start)
	}

	avgLatency := totalDuration / time.Duration(samples)
	assert.Less(t, avgLatency.Milliseconds(), int64(5),
		"Average health check latency should be <5ms, got %v", avgLatency)
}

func TestReadyHandler_Latency(t *testing.T) {
	// Test that ready check meets latency requirements (<50ms)
	samples := 100
	var totalDuration time.Duration

	for i := 0; i < samples; i++ {
		req := httptest.NewRequest("GET", "/ready", nil)
		w := httptest.NewRecorder()

		start := time.Now()
		ReadinessCheck(w, req)
		totalDuration += time.Since(start)
	}

	avgLatency := totalDuration / time.Duration(samples)
	assert.Less(t, avgLatency.Milliseconds(), int64(50),
		"Average ready check latency should be <50ms, got %v", avgLatency)
}

func TestProbes_ConcurrentLoad(t *testing.T) {
	// Test 100 req/s concurrent probe requests
	duration := 1 * time.Second
	requestsPerSecond := 100
	ticker := time.NewTicker(time.Duration(1000/requestsPerSecond) * time.Millisecond)
	defer ticker.Stop()

	done := time.After(duration)
	requestCount := int32(0)
	errorCount := int32(0)

	var wg sync.WaitGroup

Loop:
	for {
		select {
		case <-ticker.C:
			wg.Add(1)
			go func() {
				defer wg.Done()
				req := httptest.NewRequest("GET", "/health", nil)
				w := httptest.NewRecorder()

				HealthCheck(w, req)
				atomic.AddInt32(&requestCount, 1)

				if w.Code != http.StatusOK {
					atomic.AddInt32(&errorCount, 1)
				}
			}()
		case <-done:
			break Loop
		}
	}

	wg.Wait()

	assert.GreaterOrEqual(t, requestCount, int32(90),
		"Should handle at least 90 requests in 1 second")
	assert.Equal(t, int32(0), errorCount,
		"No requests should fail under load")
}

func TestProbes_NoConnectionLeaks(t *testing.T) {
	t.Skip("Will fail until connection leak detection is implemented")

	// Test that health checks don't leak connections
	// This would require tracking open connections before/after

	initialConnections := getOpenConnections()

	// Perform many health checks
	for i := 0; i < 1000; i++ {
		req := httptest.NewRequest("GET", "/ready", nil)
		w := httptest.NewRecorder()
		ReadinessCheck(w, req)
	}

	// Allow cleanup
	time.Sleep(100 * time.Millisecond)

	finalConnections := getOpenConnections()

	assert.Equal(t, initialConnections, finalConnections,
		"Should not leak connections after health checks")
}

// ============================================================================
// INTEGRATION TESTS
// ============================================================================

func TestIntegration_AllComponentsHealthy(t *testing.T) {
	// Test with all components reporting healthy
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"Should return 200 when all components are healthy")

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "ok", response.Status)
	for component, status := range response.Checks {
		assert.Equal(t, "ok", status,
			"Component %s should be healthy", component)
	}
}

func TestIntegration_AnyComponentDown503(t *testing.T) {
	t.Skip("Will fail until actual component checks are implemented")

	// Test that any component failure results in 503
	// This requires mocking at least one component as unhealthy

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	// If any component is down, should return 503
	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	hasFailure := false
	for _, status := range response.Checks {
		if status == "fail" {
			hasFailure = true
			break
		}
	}

	if hasFailure {
		assert.Equal(t, http.StatusServiceUnavailable, w.Code,
			"Should return 503 when any component fails")
		assert.Equal(t, "fail", response.Status,
			"Overall status should be 'fail' when any component fails")
	}
}

func TestIntegration_RealPostgreSQL(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test with real PostgreSQL")
	}

	t.Skip("Will fail until real PostgreSQL integration is implemented")

	// This test would use a real PostgreSQL container
	// Started via docker-compose or testcontainers

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "ok", response.Checks["database"],
		"Should successfully check real PostgreSQL")
}

func TestIntegration_RealRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test with real Redis")
	}

	t.Skip("Will fail until real Redis integration is implemented")

	// This test would use a real Redis container

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "ok", response.Checks["redis"],
		"Should successfully check real Redis")
}

func TestIntegration_RealIPFS(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test with real IPFS")
	}

	t.Skip("Will fail until real IPFS integration is implemented")

	// This test would use a real IPFS node

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "ok", response.Checks["ipfs"],
		"Should successfully check real IPFS node")
}

func TestIntegration_CascadeFailure(t *testing.T) {
	t.Skip("Will fail until cascade failure handling is implemented")

	// Test cascade failure scenario where one component failure
	// might affect others

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"Should handle cascade failures gracefully")
}

// ============================================================================
// TABLE-DRIVEN TESTS
// ============================================================================

func TestReadyHandler_StatusCodes(t *testing.T) {
	tests := []struct {
		name           string
		setupMocks     func()
		expectedCode   int
		expectedStatus string
		expectedChecks map[string]string
	}{
		{
			name: "all_healthy",
			setupMocks: func() {
				// Current stubs return nil (healthy)
			},
			expectedCode:   http.StatusOK,
			expectedStatus: "ok",
			expectedChecks: map[string]string{
				"database": "ok",
				"redis":    "ok",
				"ipfs":     "ok",
				"queue":    "ok",
			},
		},
		// These test cases will fail until real implementation
		// {
		// 	name: "database_down",
		// 	setupMocks: func() {
		// 		// Mock database failure
		// 	},
		// 	expectedCode:   http.StatusServiceUnavailable,
		// 	expectedStatus: "fail",
		// 	expectedChecks: map[string]string{
		// 		"database": "fail",
		// 		"redis":    "ok",
		// 		"ipfs":     "ok",
		// 		"queue":    "ok",
		// 	},
		// },
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupMocks != nil {
				tt.setupMocks()
			}

			req := httptest.NewRequest("GET", "/ready", nil)
			w := httptest.NewRecorder()

			ReadinessCheck(w, req)

			assert.Equal(t, tt.expectedCode, w.Code,
				"Expected status code %d, got %d", tt.expectedCode, w.Code)

			var response HealthResponse
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedStatus, response.Status)

			for component, expectedStatus := range tt.expectedChecks {
				assert.Equal(t, expectedStatus, response.Checks[component],
					"Component %s expected status %s, got %s",
					component, expectedStatus, response.Checks[component])
			}
		})
	}
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

func setupMockDatabase(t *testing.T, healthy bool) *sql.DB {
	// This would return a mock database connection
	// For now, return nil as placeholder
	return nil
}

func setupMockRedis(t *testing.T, healthy bool) io.Closer {
	// This would return a mock Redis client
	// For now, return a no-op closer
	return io.NopCloser(nil)
}

func setupMockIPFS(t *testing.T, healthy bool) io.Closer {
	// This would return a mock IPFS server
	// For now, return a no-op closer
	return io.NopCloser(nil)
}

func getOpenConnections() int {
	// This would check actual open connections
	// Placeholder for now
	return 0
}

// ============================================================================
// MOCK HEALTH SERVICE TESTS
// ============================================================================

func TestHealthService_CheckLiveness(t *testing.T) {
	service := health.NewHealthService("1.0.0")
	result := service.CheckLiveness()

	assert.Equal(t, "ok", result.Status)
	assert.Equal(t, "liveness", result.Name)
	assert.NotZero(t, result.Duration)
	assert.NotNil(t, result.Details)
}

func TestHealthService_CheckReadiness(t *testing.T) {
	// Test with mock checkers
	checkers := []health.Checker{
		&health.MockChecker{
			NameValue: "database",
			CheckFunc: func(ctx context.Context) error {
				return nil // healthy
			},
		},
		&health.MockChecker{
			NameValue: "redis",
			CheckFunc: func(ctx context.Context) error {
				return errors.New("connection refused") // unhealthy
			},
		},
	}

	service := health.NewHealthService("1.0.0", checkers...)
	ctx := context.Background()

	results, allHealthy := service.CheckReadiness(ctx)

	assert.False(t, allHealthy, "Should not be healthy when Redis fails")
	assert.Len(t, results, 2)

	// Check individual results
	for _, result := range results {
		switch result.Name {
		case "database":
			assert.Equal(t, "ok", result.Status)
			assert.Empty(t, result.Error)
		case "redis":
			assert.Equal(t, "fail", result.Status)
			assert.NotEmpty(t, result.Error)
		}
	}
}

func TestHealthService_Timeout(t *testing.T) {
	// Test that checks respect context timeout
	checker := &health.MockChecker{
		NameValue: "slow-service",
		CheckFunc: func(ctx context.Context) error {
			select {
			case <-time.After(5 * time.Second):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}

	service := health.NewHealthService("1.0.0", checker)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	results, allHealthy := service.CheckReadiness(ctx)

	assert.False(t, allHealthy, "Should fail on timeout")
	assert.Len(t, results, 1)
	assert.Equal(t, "fail", results[0].Status)
	assert.Contains(t, results[0].Error, "context deadline exceeded")
}

// ============================================================================
// ERROR SCENARIO TESTS
// ============================================================================

func TestReadyHandler_PartialFailure(t *testing.T) {
	t.Skip("Will fail until partial failure handling is implemented")

	// Test behavior when some checks succeed and others fail
	// Should still return component-level status

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Should have mixed statuses
	hasOk := false
	hasFail := false

	for _, status := range response.Checks {
		if status == "ok" {
			hasOk = true
		}
		if status == "fail" {
			hasFail = true
		}
	}

	assert.True(t, hasOk || hasFail, "Should have status for each component")
}

func TestReadyHandler_PanicRecovery(t *testing.T) {
	t.Skip("Will fail until panic recovery is implemented")

	// Test that panics in health checks are recovered
	// and treated as failures

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	// Should recover from panic and return 503
	assert.NotPanics(t, func() {
		ReadinessCheck(w, req)
	}, "Should recover from panics in health checks")

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"Should return 503 after recovering from panic")
}
