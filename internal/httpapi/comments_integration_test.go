package httpapi

import (
	"athena/internal/domain"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComments_Integration(t *testing.T) {
	ctx := context.Background()
	server := setupTestServer(t)
	defer server.cleanup()

	// Create test users
	user1 := createTestUser(t, server)
	user2 := createTestUser(t, server)
	adminUser := createTestUser(t, server)

	// Create a test video
	channel1 := createTestChannel(t, server, user1.ID)
	video := createTestVideo(t, server, channel1.ID, user1.ID)

	// Helper to make authenticated requests
	makeRequest := func(method, path string, body interface{}, userID uuid.UUID) *httptest.ResponseRecorder {
		var bodyReader *bytes.Reader
		if body != nil {
			bodyBytes, _ := json.Marshal(body)
			bodyReader = bytes.NewReader(bodyBytes)
		} else {
			bodyReader = bytes.NewReader([]byte{})
		}

		req := httptest.NewRequest(method, path, bodyReader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", generateTestToken(userID)))

		w := httptest.NewRecorder()
		server.router.ServeHTTP(w, req)
		return w
	}

	t.Run("CreateComment", func(t *testing.T) {
		body := map[string]interface{}{
			"body": "This is a test comment",
		}

		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), body, user1.ID)
		assert.Equal(t, http.StatusCreated, w.Code)

		var comment domain.Comment
		err := json.NewDecoder(w.Body).Decode(&comment)
		require.NoError(t, err)
		assert.Equal(t, "This is a test comment", comment.Body)
		assert.Equal(t, user1.ID, comment.UserID)
		assert.Equal(t, video.ID, comment.VideoID)
		assert.Nil(t, comment.ParentID)
	})

	t.Run("CreateReply", func(t *testing.T) {
		// First create a parent comment
		parentBody := map[string]interface{}{
			"body": "Parent comment",
		}
		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), parentBody, user1.ID)
		require.Equal(t, http.StatusCreated, w.Code)

		var parentComment domain.Comment
		err := json.NewDecoder(w.Body).Decode(&parentComment)
		require.NoError(t, err)

		// Create a reply
		replyBody := map[string]interface{}{
			"body":     "This is a reply",
			"parentId": parentComment.ID,
		}
		w = makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), replyBody, user2.ID)
		assert.Equal(t, http.StatusCreated, w.Code)

		var replyComment domain.Comment
		err = json.NewDecoder(w.Body).Decode(&replyComment)
		require.NoError(t, err)
		assert.Equal(t, "This is a reply", replyComment.Body)
		assert.Equal(t, user2.ID, replyComment.UserID)
		assert.NotNil(t, replyComment.ParentID)
		assert.Equal(t, parentComment.ID, *replyComment.ParentID)
	})

	t.Run("ListComments", func(t *testing.T) {
		// Create multiple comments
		for i := 0; i < 5; i++ {
			body := map[string]interface{}{
				"body": fmt.Sprintf("Comment %d", i),
			}
			w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), body, user1.ID)
			require.Equal(t, http.StatusCreated, w.Code)
		}

		// Get comments without auth
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/videos/%s/comments?limit=10", video.ID), nil)
		w := httptest.NewRecorder()
		server.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var response struct {
			Data       []domain.CommentWithUser `json:"data"`
			Pagination struct {
				Limit  int `json:"limit"`
				Offset int `json:"offset"`
			} `json:"pagination"`
		}
		err := json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(response.Data), 5)
		assert.Equal(t, 10, response.Pagination.Limit)
	})

	t.Run("UpdateComment", func(t *testing.T) {
		// Create a comment
		createBody := map[string]interface{}{
			"body": "Original comment",
		}
		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), createBody, user1.ID)
		require.Equal(t, http.StatusCreated, w.Code)

		var comment domain.Comment
		err := json.NewDecoder(w.Body).Decode(&comment)
		require.NoError(t, err)

		// Update the comment
		updateBody := map[string]interface{}{
			"body": "Updated comment",
		}
		w = makeRequest("PUT", fmt.Sprintf("/api/v1/comments/%s", comment.ID), updateBody, user1.ID)
		assert.Equal(t, http.StatusNoContent, w.Code)

		// Verify update
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/comments/%s", comment.ID), nil)
		w = httptest.NewRecorder()
		server.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var updatedComment domain.CommentWithUser
		err = json.NewDecoder(w.Body).Decode(&updatedComment)
		require.NoError(t, err)
		assert.Equal(t, "Updated comment", updatedComment.Body)
		assert.NotNil(t, updatedComment.EditedAt)
	})

	t.Run("DeleteComment", func(t *testing.T) {
		// Create a comment
		createBody := map[string]interface{}{
			"body": "Comment to delete",
		}
		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), createBody, user1.ID)
		require.Equal(t, http.StatusCreated, w.Code)

		var comment domain.Comment
		err := json.NewDecoder(w.Body).Decode(&comment)
		require.NoError(t, err)

		// Delete the comment
		w = makeRequest("DELETE", fmt.Sprintf("/api/v1/comments/%s", comment.ID), nil, user1.ID)
		assert.Equal(t, http.StatusNoContent, w.Code)

		// Verify deletion (should return 404)
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/comments/%s", comment.ID), nil)
		w = httptest.NewRecorder()
		server.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("FlagComment", func(t *testing.T) {
		// Create a comment by user1
		createBody := map[string]interface{}{
			"body": "Inappropriate comment",
		}
		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), createBody, user1.ID)
		require.Equal(t, http.StatusCreated, w.Code)

		var comment domain.Comment
		err := json.NewDecoder(w.Body).Decode(&comment)
		require.NoError(t, err)

		// Flag the comment as user2
		flagBody := map[string]interface{}{
			"reason":  "inappropriate",
			"details": "This comment violates community guidelines",
		}
		w = makeRequest("POST", fmt.Sprintf("/api/v1/comments/%s/flag", comment.ID), flagBody, user2.ID)
		assert.Equal(t, http.StatusCreated, w.Code)

		// Can't flag your own comment
		w = makeRequest("POST", fmt.Sprintf("/api/v1/comments/%s/flag", comment.ID), flagBody, user1.ID)
		assert.Equal(t, http.StatusInternalServerError, w.Code)

		// Unflag the comment
		w = makeRequest("DELETE", fmt.Sprintf("/api/v1/comments/%s/flag", comment.ID), nil, user2.ID)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("ModerateComment", func(t *testing.T) {
		// Create a comment
		createBody := map[string]interface{}{
			"body": "Comment to moderate",
		}
		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), createBody, user2.ID)
		require.Equal(t, http.StatusCreated, w.Code)

		var comment domain.Comment
		err := json.NewDecoder(w.Body).Decode(&comment)
		require.NoError(t, err)

		// Video owner can moderate
		moderateBody := map[string]interface{}{
			"status": "hidden",
		}
		w = makeRequest("POST", fmt.Sprintf("/api/v1/comments/%s/moderate", comment.ID), moderateBody, user1.ID)
		assert.Equal(t, http.StatusNoContent, w.Code)

		// Non-owner can't moderate
		w = makeRequest("POST", fmt.Sprintf("/api/v1/comments/%s/moderate", comment.ID), moderateBody, user2.ID)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("Threading", func(t *testing.T) {
		// Create parent comment
		parentBody := map[string]interface{}{
			"body": "Parent comment for threading test",
		}
		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), parentBody, user1.ID)
		require.Equal(t, http.StatusCreated, w.Code)

		var parentComment domain.Comment
		err := json.NewDecoder(w.Body).Decode(&parentComment)
		require.NoError(t, err)

		// Create multiple replies
		for i := 0; i < 3; i++ {
			replyBody := map[string]interface{}{
				"body":     fmt.Sprintf("Reply %d", i),
				"parentId": parentComment.ID,
			}
			w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), replyBody, user2.ID)
			require.Equal(t, http.StatusCreated, w.Code)
		}

		// Get top-level comments (should include parent with some replies)
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), nil)
		w = httptest.NewRecorder()
		server.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var response struct {
			Data []domain.CommentWithUser `json:"data"`
		}
		err = json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)

		// Find our parent comment
		var foundParent *domain.CommentWithUser
		for i := range response.Data {
			if response.Data[i].ID == parentComment.ID {
				foundParent = &response.Data[i]
				break
			}
		}
		require.NotNil(t, foundParent)
		assert.Equal(t, 3, len(foundParent.Replies))

		// Get replies directly
		req = httptest.NewRequest("GET", fmt.Sprintf("/api/v1/videos/%s/comments?parentId=%s", video.ID, parentComment.ID), nil)
		w = httptest.NewRecorder()
		server.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		err = json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)
		assert.Equal(t, 3, len(response.Data))
	})

	t.Run("Pagination", func(t *testing.T) {
		// Create many comments
		for i := 0; i < 25; i++ {
			body := map[string]interface{}{
				"body": fmt.Sprintf("Pagination test comment %d", i),
			}
			w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), body, user1.ID)
			require.Equal(t, http.StatusCreated, w.Code)
			time.Sleep(10 * time.Millisecond) // Small delay to ensure different timestamps
		}

		// Get first page
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/videos/%s/comments?limit=10&offset=0", video.ID), nil)
		w := httptest.NewRecorder()
		server.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var page1 struct {
			Data []domain.CommentWithUser `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&page1)
		require.NoError(t, err)
		assert.Equal(t, 10, len(page1.Data))

		// Get second page
		req = httptest.NewRequest("GET", fmt.Sprintf("/api/v1/videos/%s/comments?limit=10&offset=10", video.ID), nil)
		w = httptest.NewRecorder()
		server.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var page2 struct {
			Data []domain.CommentWithUser `json:"data"`
		}
		err = json.NewDecoder(w.Body).Decode(&page2)
		require.NoError(t, err)
		assert.Equal(t, 10, len(page2.Data))

		// Ensure pages don't overlap
		for _, c1 := range page1.Data {
			for _, c2 := range page2.Data {
				assert.NotEqual(t, c1.ID, c2.ID)
			}
		}
	})
}

// Helper functions for test setup
func createTestChannel(t *testing.T, server *testServer, userID uuid.UUID) *domain.Channel {
	channel := &domain.Channel{
		AccountID:   userID,
		Handle:      fmt.Sprintf("testchannel_%s", uuid.New()),
		DisplayName: "Test Channel",
	}
	err := server.channelRepo.Create(context.Background(), channel)
	require.NoError(t, err)
	return channel
}

func createTestVideo(t *testing.T, server *testServer, channelID, userID uuid.UUID) *domain.Video {
	video := &domain.Video{
		ID:          uuid.New(),
		ChannelID:   channelID,
		Title:       "Test Video",
		Description: "Test Description",
		Duration:    120,
		Privacy:     domain.PublicPrivacy,
	}
	err := server.videoRepo.Create(context.Background(), video)
	require.NoError(t, err)
	return video
}

func generateTestToken(userID uuid.UUID) string {
	// Implementation would generate a valid JWT token for testing
	// This is a placeholder
	return fmt.Sprintf("test-token-%s", userID)
}
