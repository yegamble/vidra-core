package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yegamble/athena/tests/e2e"
)

// TestVideoUploadWorkflow tests the complete video upload, view, and delete workflow
func TestVideoUploadWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	cfg := e2e.DefaultConfig()

	// Wait for API to be ready
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	err := e2e.WaitForService(ctx, cfg.BaseURL+"/health", 2*time.Minute)
	require.NoError(t, err, "API service did not become available")

	// Create test client
	client := e2e.NewTestClient(cfg.BaseURL)

	// Step 1: Register a new user
	username := "testuser_" + time.Now().Format("20060102150405")
	email := username + "@example.com"
	password := "SecurePass123!"

	userID, token := client.RegisterUser(t, username, email, password)
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
	client := e2e.NewTestClient(cfg.BaseURL)

	// Step 1: Register a new user
	username := "authtest_" + time.Now().Format("20060102150405")
	email := username + "@example.com"
	password := "SecurePass123!"

	userID, token := client.RegisterUser(t, username, email, password)
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
	client := e2e.NewTestClient(cfg.BaseURL)

	// Register user
	username := "searchtest_" + time.Now().Format("20060102150405")
	email := username + "@example.com"
	client.RegisterUser(t, username, email, "SecurePass123!")

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
	// TODO: Generate a real test video file using ffmpeg or use a fixture
	// For now, this is a placeholder stub
	t.Skip("Test video file generation not implemented - requires ffmpeg or test fixtures")
	return ""
}

// Helper: Clean up test files
func cleanupTestFile(t *testing.T, path string) {
	if path != "" {
		// os.Remove(path)
	}
}
