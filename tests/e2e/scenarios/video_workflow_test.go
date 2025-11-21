package scenarios

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"testing"
	"time"

	"athena/tests/e2e"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVideoUploadWorkflow tests the complete video upload, view, and delete workflow
func TestVideoUploadWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	cfg := e2e.DefaultConfig()

	// Wait for API to be ready - skip if service is not available
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	err := e2e.WaitForService(ctx, cfg.BaseURL+"/health", 2*time.Minute)
	if err != nil {
		t.Skipf("Skipping E2E test: API service not available (%v)", err)
	}

	// Create test client
	client := e2e.NewTestClient(cfg.BaseURL)

	// Step 1: Register a new user with unique username (hash + short timestamp)
	// Keep username under 50 chars (database constraint: VARCHAR(50))
	timestamp := time.Now().UnixNano() % 10000000000             // 10 digits
	testHash := fmt.Sprintf("%x", md5.Sum([]byte(t.Name())))[:8] // 8-char hash
	username := fmt.Sprintf("e2e_%s_%d", testHash, timestamp)    // ~23 chars total
	email := username + "@example.com"
	password := "SecurePass123!"

	userID, token, username := client.RegisterUser(t, username, email, password)
	assert.NotEmpty(t, userID, "User ID should not be empty")
	assert.NotEmpty(t, token, "Access token should not be empty")

	t.Logf("Registered user: %s (ID: %s)", username, userID)

	// Step 2: Create a test video file
	testVideoPath := createTestVideoFile(t)
	defer cleanupTestFile(t, testVideoPath)

	// Step 3: Upload video
	videoID := client.UploadVideo(t, testVideoPath, "Test Video", "This is a test video for E2E testing")
	assert.NotEmpty(t, videoID, "Video ID should not be empty")

	t.Logf("Uploaded video: %s", videoID)

	// Step 4: Retrieve video details
	video := client.GetVideo(t, videoID)
	assert.Equal(t, "Test Video", video["title"])
	assert.Equal(t, "This is a test video for E2E testing", video["description"])
	assert.Equal(t, userID, video["user_id"])

	// Step 5: Verify video appears in list
	videos := client.ListVideos(t)
	assert.NotEmpty(t, videos, "Video list should not be empty")

	found := false
	for _, v := range videos {
		if v["id"] == videoID {
			found = true
			break
		}
	}
	assert.True(t, found, "Uploaded video should appear in video list")

	// Step 6: Search for the video
	searchResults := client.SearchVideos(t, "Test Video")
	assert.NotEmpty(t, searchResults, "Search should return results")

	// Step 7: Delete the video
	client.DeleteVideo(t, videoID)

	// Step 8: Verify video is deleted (should return 404)
	resp, err := client.Get("/api/v1/videos/" + videoID)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 404, resp.StatusCode, "Deleted video should return 404")

	t.Log("Video workflow completed successfully")
}

// TestUserAuthenticationFlow tests user registration, login, and token usage
func TestUserAuthenticationFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	cfg := e2e.DefaultConfig()

	// Check if service is available - skip if not
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := e2e.WaitForService(ctx, cfg.BaseURL+"/health", 10*time.Second)
	if err != nil {
		t.Skipf("Skipping E2E test: API service not available (%v)", err)
	}

	client := e2e.NewTestClient(cfg.BaseURL)

	// Step 1: Register a new user with unique username (hash + short timestamp)
	// Keep username under 50 chars (database constraint: VARCHAR(50))
	timestamp := time.Now().UnixNano() % 10000000000             // 10 digits
	testHash := fmt.Sprintf("%x", md5.Sum([]byte(t.Name())))[:8] // 8-char hash
	username := fmt.Sprintf("e2e_%s_%d", testHash, timestamp)    // ~23 chars total
	email := username + "@example.com"
	password := "SecurePass123!"

	userID, token, username := client.RegisterUser(t, username, email, password)
	assert.NotEmpty(t, userID)
	assert.NotEmpty(t, token)

	t.Logf("Registered user: %s", username)

	// Step 2: Create a new client and login
	client2 := e2e.NewTestClient(cfg.BaseURL)
	userID2, token2 := client2.Login(t, username, password)

	assert.Equal(t, userID, userID2, "User ID should match")
	assert.NotEmpty(t, token2, "Login should return access token")
	assert.NotEqual(t, token, token2, "Each login should generate a new token")

	// Step 3: Use token to access protected endpoint
	resp, err := client2.Get("/api/v1/users/me")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode, "Token should allow access to protected endpoints")

	t.Log("Authentication flow completed successfully")
}

// TestVideoSearchFunctionality tests video search across different fields
func TestVideoSearchFunctionality(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	cfg := e2e.DefaultConfig()

	// Check if service is available - skip if not
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := e2e.WaitForService(ctx, cfg.BaseURL+"/health", 10*time.Second)
	if err != nil {
		t.Skipf("Skipping E2E test: API service not available (%v)", err)
	}

	client := e2e.NewTestClient(cfg.BaseURL)

	// Register user with unique username (hash + short timestamp)
	// Keep username under 50 chars (database constraint: VARCHAR(50))
	timestamp := time.Now().UnixNano() % 10000000000             // 10 digits
	testHash := fmt.Sprintf("%x", md5.Sum([]byte(t.Name())))[:8] // 8-char hash
	username := fmt.Sprintf("e2e_%s_%d", testHash, timestamp)    // ~23 chars total
	email := username + "@example.com"
	_, _, username = client.RegisterUser(t, username, email, "SecurePass123!")

	// Create test video file
	testVideoPath := createTestVideoFile(t)
	defer cleanupTestFile(t, testVideoPath)

	// Upload video with searchable title
	uniqueTitle := "Searchable Video " + time.Now().Format("20060102150405")
	videoID := client.UploadVideo(t, testVideoPath, uniqueTitle, "Testing search functionality")

	// Wait a moment for indexing
	time.Sleep(2 * time.Second)

	// Search for the video
	results := client.SearchVideos(t, uniqueTitle)
	assert.NotEmpty(t, results, "Search should return results")

	found := false
	for _, v := range results {
		if v["id"] == videoID {
			found = true
			break
		}
	}
	assert.True(t, found, "Uploaded video should appear in search results")

	// Cleanup
	client.DeleteVideo(t, videoID)

	t.Log("Search functionality test completed successfully")
}

// Helper: Create a minimal test video file
func createTestVideoFile(t *testing.T) string {
	// Use existing test video from postman test files
	// This allows E2E tests to run without requiring ffmpeg

	// Use environment variable if set, otherwise use relative path
	testVideoPath := os.Getenv("E2E_TEST_VIDEO_PATH")
	if testVideoPath == "" {
		// Fallback to relative path from test directory
		testVideoPath = "../../postman/test-files/videos/test-video.mp4"
	}

	// Check if the test video exists
	if _, err := os.Stat(testVideoPath); err != nil {
		t.Fatalf("Test video file not found at %s: %v", testVideoPath, err)
	}

	return testVideoPath
}

// Helper: Clean up test files
// Note: This is a no-op for E2E tests since we use a shared test video file
// from the postman test-files directory. Individual test resources (uploaded videos,
// users) are cleaned up via API calls (DeleteVideo, DeleteUser) rather than file deletion.
func cleanupTestFile(t *testing.T, path string) {
	// No-op: Shared test video files should not be deleted
}
