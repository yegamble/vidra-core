//go:build load

package video

import (
	"athena/internal/repository"
	"athena/internal/usecase"
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/testutil"
)

// TestLoadScenarios tests various high-volume scenarios to ensure the system
// can handle genuine traffic patterns without degradation
func TestLoadScenarios(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load tests in short mode")
	}

	db := testutil.SetupTestDB(t)
	defer func(DB *sqlx.DB) {
		err := DB.Close()
		if err != nil {

		}
	}(db.DB)

	// Setup repositories and services
	viewsRepo := repository.NewViewsRepository(db.DB)
	videoRepo := repository.NewVideoRepository(db.DB)
	viewsService := usecase.NewViewsService(viewsRepo, videoRepo)
	viewsHandler := NewViewsHandler(viewsService)

	// Create HTTP mux for load testing
	mux := http.NewServeMux()
	mux.HandleFunc("/track", viewsHandler.TrackView)
	mux.HandleFunc("/analytics", viewsHandler.GetVideoAnalytics)
	mux.HandleFunc("/trending", viewsHandler.GetTrendingVideos)

	// Create test videos for load testing
	videoIDs := createTestVideos(t, db, 10)

	t.Run("SimulateRealWorldTrafficPattern", func(t *testing.T) {
		testRealWorldTraffic(t, mux, videoIDs)
	})

	t.Run("StressTestViewTracking", func(t *testing.T) {
		testStressViewTracking(t, mux, videoIDs)
	})

	t.Run("SustainedHighThroughput", func(t *testing.T) {
		testSustainedThroughput(t, mux, videoIDs)
	})

	t.Run("ConcurrentAnalyticsQueries", func(t *testing.T) {
		testConcurrentAnalytics(t, mux, videoIDs)
	})

	t.Run("PeakTrafficSimulation", func(t *testing.T) {
		testPeakTraffic(t, mux, videoIDs)
	})
}

// testRealWorldTraffic simulates realistic user behavior patterns
func testRealWorldTraffic(t *testing.T, mux http.Handler, videoIDs []uuid.UUID) {
	const (
		totalUsers     = 200
		testDurationMs = 5000
		avgViewTimeMs  = 30000 // 30 seconds average view time
	)

	var wg sync.WaitGroup
	startTime := time.Now()
	endTime := startTime.Add(time.Duration(testDurationMs) * time.Millisecond)

	// Channel to collect metrics
	metrics := make(chan loadTestMetric, totalUsers*10)

	// Simulate different user behaviors
	userBehaviors := []userBehaviorPattern{
		{name: "casual_viewer", weight: 60, avgSessions: 2, avgViewTime: 15 * time.Second},
		{name: "engaged_viewer", weight: 30, avgSessions: 5, avgViewTime: 45 * time.Second},
		{name: "power_user", weight: 10, avgSessions: 15, avgViewTime: 120 * time.Second},
	}

	for i := 0; i < totalUsers; i++ {
		wg.Add(1)
		go func(userID int) {
			defer wg.Done()

			behavior := selectUserBehavior(userBehaviors, rand.Intn(100))
			simulateUser(t, mux, videoIDs, userID, behavior, endTime, metrics)
		}(i)
	}

	// Collect metrics in background
	var collectedMetrics []loadTestMetric
	go func() {
		for metric := range metrics {
			collectedMetrics = append(collectedMetrics, metric)
		}
	}()

	wg.Wait()
	close(metrics)
	time.Sleep(100 * time.Millisecond) // Allow metric collection to complete

	// Analyze results
	analyzeLoadTestResults(t, collectedMetrics, time.Since(startTime))
}

// testStressViewTracking pushes the view tracking system to its limits
func testStressViewTracking(t *testing.T, mux http.Handler, videoIDs []uuid.UUID) {
	const (
		concurrentUsers = 500
		viewsPerUser    = 20
		maxLatencyMs    = 1000 // 1 second max acceptable latency
	)

	var wg sync.WaitGroup
	latencies := make(chan time.Duration, concurrentUsers*viewsPerUser)
	errors := make(chan error, concurrentUsers*viewsPerUser)

	startTime := time.Now()

	for i := 0; i < concurrentUsers; i++ {
		wg.Add(1)
		go func(userID int) {
			defer wg.Done()

			fingerprint := generateFingerprint(userID)

			for j := 0; j < viewsPerUser; j++ {
				videoID := videoIDs[rand.Intn(len(videoIDs))]

				requestStart := time.Now()
				err := trackView(mux, videoID, fingerprint)
				latency := time.Since(requestStart)

				latencies <- latency
				if err != nil {
					errors <- err
				}

				// Small delay to simulate realistic user behavior
				time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
	close(latencies)
	close(errors)

	totalDuration := time.Since(startTime)

	// Analyze latencies
	var totalLatency time.Duration
	var maxLatency time.Duration
	latencyCount := 0

	for latency := range latencies {
		totalLatency += latency
		if latency > maxLatency {
			maxLatency = latency
		}
		latencyCount++
	}

	// Check for errors
	errorCount := 0
	for range errors {
		errorCount++
	}

	avgLatency := totalLatency / time.Duration(latencyCount)
	throughput := float64(latencyCount) / totalDuration.Seconds()

	t.Logf("Stress test results:")
	t.Logf("  Total requests: %d", latencyCount)
	t.Logf("  Total duration: %v", totalDuration)
	t.Logf("  Throughput: %.2f requests/second", throughput)
	t.Logf("  Average latency: %v", avgLatency)
	t.Logf("  Max latency: %v", maxLatency)
	t.Logf("  Errors: %d", errorCount)

	// Assertions
	assert.Less(t, maxLatency, time.Duration(maxLatencyMs)*time.Millisecond,
		"Maximum latency should be under %dms", maxLatencyMs)
	assert.Less(t, float64(errorCount)/float64(latencyCount), 0.01,
		"Error rate should be less than 1%")
	assert.Greater(t, throughput, 100.0,
		"System should handle at least 100 requests/second")
}

// testSustainedThroughput tests system behavior under sustained load
func testSustainedThroughput(t *testing.T, mux http.Handler, videoIDs []uuid.UUID) {
	const (
		testDurationSeconds = 30
		targetThroughput    = 50 // requests per second
		tolerancePercent    = 20 // 20% tolerance
	)

	var wg sync.WaitGroup
	var requestCount int64
	var errorCount int64

	startTime := time.Now()
	endTime := startTime.Add(testDurationSeconds * time.Second)

	// Start workers
	workers := 10
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			fingerprint := generateFingerprint(workerID)

			for time.Now().Before(endTime) {
				videoID := videoIDs[rand.Intn(len(videoIDs))]

				if err := trackView(mux, videoID, fingerprint); err != nil {
					errorCount++
				}
				requestCount++

				// Control throughput
				time.Sleep(time.Duration(1000/targetThroughput/workers) * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
	actualDuration := time.Since(startTime)
	actualThroughput := float64(requestCount) / actualDuration.Seconds()

	t.Logf("Sustained throughput test results:")
	t.Logf("  Duration: %v", actualDuration)
	t.Logf("  Total requests: %d", requestCount)
	t.Logf("  Target throughput: %d req/s", targetThroughput)
	t.Logf("  Actual throughput: %.2f req/s", actualThroughput)
	t.Logf("  Errors: %d", errorCount)

	// Verify throughput is within tolerance
	minAcceptable := float64(targetThroughput) * (1 - float64(tolerancePercent)/100)
	maxAcceptable := float64(targetThroughput) * (1 + float64(tolerancePercent)/100)

	assert.GreaterOrEqual(t, actualThroughput, minAcceptable,
		"Actual throughput should be within tolerance")
	assert.LessOrEqual(t, actualThroughput, maxAcceptable,
		"Actual throughput should be within tolerance")
	assert.Less(t, float64(errorCount)/float64(requestCount), 0.01,
		"Error rate should be less than 1%")
}

// testConcurrentAnalytics tests analytics queries under concurrent load
func testConcurrentAnalytics(t *testing.T, mux http.Handler, videoIDs []uuid.UUID) {
	const concurrentQueries = 50

	var wg sync.WaitGroup
	latencies := make(chan time.Duration, concurrentQueries*3) // 3 types of queries

	for i := 0; i < concurrentQueries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Test different analytics endpoints concurrently
			endpoints := []string{
				fmt.Sprintf("/api/v1/videos/%s/analytics", videoIDs[0]),
				"/api/v1/analytics/trending",
				"/api/v1/analytics/dashboard",
			}

			for _, endpoint := range endpoints {
				start := time.Now()
				req := httptest.NewRequest(http.MethodGet, endpoint, nil)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, req)
				latency := time.Since(start)

				latencies <- latency

				// Analytics queries should not fail under load
				if w.Code != http.StatusOK {
					t.Errorf("Analytics query failed: %s returned %d", endpoint, w.Code)
				}
			}
		}()
	}

	wg.Wait()
	close(latencies)

	var totalLatency time.Duration
	var maxLatency time.Duration
	count := 0

	for latency := range latencies {
		totalLatency += latency
		if latency > maxLatency {
			maxLatency = latency
		}
		count++
	}

	avgLatency := totalLatency / time.Duration(count)

	t.Logf("Concurrent analytics test results:")
	t.Logf("  Total queries: %d", count)
	t.Logf("  Average latency: %v", avgLatency)
	t.Logf("  Max latency: %v", maxLatency)

	// Analytics queries should complete within reasonable time
	assert.Less(t, avgLatency, 2*time.Second, "Average analytics latency should be under 2s")
	assert.Less(t, maxLatency, 5*time.Second, "Max analytics latency should be under 5s")
}

// testPeakTraffic simulates traffic spikes (viral video scenario)
func testPeakTraffic(t *testing.T, mux http.Handler, videoIDs []uuid.UUID) {
	// Simulate a viral video getting sudden attention
	viralVideoID := videoIDs[0]

	phases := []trafficPhase{
		{name: "baseline", duration: 2 * time.Second, concurrency: 10},
		{name: "spike_start", duration: 1 * time.Second, concurrency: 100},
		{name: "peak", duration: 3 * time.Second, concurrency: 200},
		{name: "cooldown", duration: 2 * time.Second, concurrency: 50},
	}

	var totalRequests int64
	var totalErrors int64

	for _, phase := range phases {
		t.Logf("Starting phase: %s (concurrency: %d, duration: %v)",
			phase.name, phase.concurrency, phase.duration)

		requests, errors := runTrafficPhase(t, mux, viralVideoID, phase)
		totalRequests += requests
		totalErrors += errors

		t.Logf("Phase %s completed: %d requests, %d errors",
			phase.name, requests, errors)
	}

	errorRate := float64(totalErrors) / float64(totalRequests)
	t.Logf("Peak traffic test completed: %d total requests, error rate: %.2f%%",
		totalRequests, errorRate*100)

	// System should handle viral traffic with low error rates
	assert.Less(t, errorRate, 0.05, "Error rate should be less than 5% during peak traffic")
}

// Helper types and functions

type loadTestMetric struct {
	UserID    int
	VideoID   uuid.UUID
	Latency   time.Duration
	Success   bool
	Timestamp time.Time
}

type userBehaviorPattern struct {
	name        string
	weight      int
	avgSessions int
	avgViewTime time.Duration
}

type trafficPhase struct {
	name        string
	duration    time.Duration
	concurrency int
}

func selectUserBehavior(behaviors []userBehaviorPattern, roll int) userBehaviorPattern {
	cumulative := 0
	for _, behavior := range behaviors {
		cumulative += behavior.weight
		if roll < cumulative {
			return behavior
		}
	}
	return behaviors[len(behaviors)-1] // fallback
}

func simulateUser(t *testing.T, mux http.Handler, videoIDs []uuid.UUID,
	userID int, behavior userBehaviorPattern, endTime time.Time, metrics chan<- loadTestMetric) {

	fingerprint := generateFingerprint(userID)

	for time.Now().Before(endTime) {
		// Select random video
		videoID := videoIDs[rand.Intn(len(videoIDs))]

		start := time.Now()
		err := trackView(mux, videoID, fingerprint)
		latency := time.Since(start)

		metrics <- loadTestMetric{
			UserID:    userID,
			VideoID:   videoID,
			Latency:   latency,
			Success:   err == nil,
			Timestamp: time.Now(),
		}

		// Simulate view duration based on behavior pattern
		viewDuration := time.Duration(rand.Int63n(int64(behavior.avgViewTime * 2)))
		time.Sleep(viewDuration)

		if time.Now().After(endTime) {
			break
		}
	}
}

func runTrafficPhase(t *testing.T, mux http.Handler, videoID uuid.UUID, phase trafficPhase) (int64, int64) {
	var wg sync.WaitGroup
	var requests int64
	var errors int64
	var mu sync.Mutex

	endTime := time.Now().Add(phase.duration)

	for i := 0; i < phase.concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			fingerprint := generateFingerprint(workerID + int(time.Now().UnixNano()))

			for time.Now().Before(endTime) {
				err := trackView(mux, videoID, fingerprint)

				mu.Lock()
				requests++
				if err != nil {
					errors++
				}
				mu.Unlock()

				// Small delay to avoid overwhelming
				time.Sleep(10 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
	return requests, errors
}

func analyzeLoadTestResults(t *testing.T, metrics []loadTestMetric, duration time.Duration) {
	if len(metrics) == 0 {
		t.Error("No metrics collected")
		return
	}

	var totalLatency time.Duration
	var maxLatency time.Duration
	successCount := 0

	for _, metric := range metrics {
		totalLatency += metric.Latency
		if metric.Latency > maxLatency {
			maxLatency = metric.Latency
		}
		if metric.Success {
			successCount++
		}
	}

	avgLatency := totalLatency / time.Duration(len(metrics))
	successRate := float64(successCount) / float64(len(metrics))
	throughput := float64(len(metrics)) / duration.Seconds()

	t.Logf("Load test analysis:")
	t.Logf("  Total requests: %d", len(metrics))
	t.Logf("  Duration: %v", duration)
	t.Logf("  Throughput: %.2f req/s", throughput)
	t.Logf("  Success rate: %.2f%%", successRate*100)
	t.Logf("  Average latency: %v", avgLatency)
	t.Logf("  Max latency: %v", maxLatency)

	// Assertions for acceptable performance
	assert.Greater(t, successRate, 0.95, "Success rate should be above 95%")
	assert.Less(t, avgLatency, 500*time.Millisecond, "Average latency should be under 500ms")
	assert.Less(t, maxLatency, 2*time.Second, "Max latency should be under 2s")
}

func trackView(mux http.Handler, videoID uuid.UUID, fingerprint string) error {
	requestBody := map[string]interface{}{
		"video_id":    videoID.String(),
		"fingerprint": fingerprint,
		"timestamp":   time.Now().Unix(),
	}

	body, _ := json.Marshal(requestBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/views", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		return fmt.Errorf("request failed with status %d", w.Code)
	}

	return nil
}

func createTestVideos(t *testing.T, db *testutil.TestDB, count int) []uuid.UUID {
	// First create a test user
	userID := uuid.New()
	_, err := db.DB.Exec(`
		INSERT INTO users (id, username, email, display_name, role, password_hash, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'user', 'test_hash', true, NOW(), NOW())
	`, userID, "loadtest_user_"+uuid.New().String()[:8], "loadtest_"+uuid.New().String()[:8]+"@example.com", "Load Test User")
	require.NoError(t, err)

	var videoIDs []uuid.UUID

	for i := 0; i < count; i++ {
		videoID := uuid.New()
		thumbnailID := uuid.New()

		_, err := db.DB.Exec(`
			INSERT INTO videos (id, thumbnail_id, title, description, duration, upload_date, privacy, status, user_id, views, file_size, mime_type, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, NOW(), 'public', 'completed', $6, 0, 1024000, 'video/mp4', NOW(), NOW())
		`, videoID, thumbnailID, fmt.Sprintf("Load Test Video %d", i+1),
			fmt.Sprintf("Test video for load testing %d", i+1), 120, userID)

		require.NoError(t, err)
		videoIDs = append(videoIDs, videoID)
	}

	return videoIDs
}

func generateFingerprint(seed int) string {
	// Generate a deterministic but unique fingerprint for testing
	return fmt.Sprintf("test_fingerprint_%d_%d", seed, time.Now().UnixNano()%1000000)
}
