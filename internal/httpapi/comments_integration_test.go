package httpapi

import (
	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/testutil"
	"athena/internal/usecase"
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
	"golang.org/x/crypto/bcrypt"
)

func TestComments_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}

	td := testutil.SetupTestDB(t)

	// Setup repositories and services
	userRepo := repository.NewUserRepository(td.DB)
	channelRepo := repository.NewChannelRepository(td.DB)
	videoRepo := repository.NewVideoRepository(td.DB)
	commentRepo := repository.NewCommentRepository(td.DB)
	commentService := usecase.NewCommentService(commentRepo, videoRepo, userRepo, channelRepo)
	authRepo := repository.NewAuthRepository(td.DB)

	// Create handlers
	commentHandlers := NewCommentHandlers(commentService)

	// Create test server for auth
	s := NewServer(userRepo, authRepo, "test-secret", nil, 0, "", "", 0, nil)

	// Setup router
	router := chi.NewRouter()
	router.Use(middleware.RequestID())

	// Register comment routes
	router.Route("/api/v1", func(r chi.Router) {
		r.Route("/videos/{videoId}/comments", func(r chi.Router) {
			r.Get("/", commentHandlers.GetComments)
			r.With(middleware.Auth("test-secret")).Post("/", commentHandlers.CreateComment)
		})

		r.Route("/comments", func(r chi.Router) {
			r.Get("/{commentId}", commentHandlers.GetComment)
			r.With(middleware.Auth("test-secret")).Put("/{commentId}", commentHandlers.UpdateComment)
			r.With(middleware.Auth("test-secret")).Delete("/{commentId}", commentHandlers.DeleteComment)
			r.With(middleware.Auth("test-secret")).Post("/{commentId}/flag", commentHandlers.FlagComment)
			r.With(middleware.Auth("test-secret")).Delete("/{commentId}/flag", commentHandlers.UnflagComment)
			r.With(middleware.Auth("test-secret")).Post("/{commentId}/moderate", commentHandlers.ModerateComment)
		})
	})

	// Helper function to create authenticated user
	createUser := func(t *testing.T, username, email string) (*domain.User, string) {
		pw := "password123"
		hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		require.NoError(t, err)

		user := &domain.User{
			ID:        uuid.NewString(),
			Username:  username,
			Email:     email,
			Role:      domain.RoleUser,
			IsActive:  true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		err = userRepo.Create(context.Background(), user, string(hash))
		require.NoError(t, err)

		// Login to get token
		body := map[string]any{"email": email, "password": pw}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		s.Login(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var resp struct {
			Data json.RawMessage `json:"data"`
		}
		err = json.NewDecoder(rr.Body).Decode(&resp)
		require.NoError(t, err)

		var authResp struct {
			AccessToken string `json:"access_token"`
		}
		err = json.Unmarshal(resp.Data, &authResp)
		require.NoError(t, err)

		return user, authResp.AccessToken
	}

	// Helper to create channel
	createChannel := func(t *testing.T, userID string) *domain.Channel {
		channel := &domain.Channel{
			ID:          uuid.New(),
			AccountID:   uuid.MustParse(userID),
			Handle:      fmt.Sprintf("channel_%s", uuid.New()),
			DisplayName: "Test Channel",
			IsLocal:     true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err := channelRepo.Create(context.Background(), channel)
		require.NoError(t, err)
		return channel
	}

	// Helper to create video
	createVideo := func(t *testing.T, channelID uuid.UUID) *domain.Video {
		video := &domain.Video{
			ID:          uuid.NewString(),
			ChannelID:   channelID,
			Title:       "Test Video",
			Description: "Test Description",
			Duration:    120,
			Privacy:     domain.PrivacyPublic,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err := videoRepo.Create(context.Background(), video)
		require.NoError(t, err)
		return video
	}

	// Helper to make authenticated requests
	makeRequest := func(method, path string, body interface{}, token string) *httptest.ResponseRecorder {
		var bodyReader *bytes.Reader
		if body != nil {
			bodyBytes, _ := json.Marshal(body)
			bodyReader = bytes.NewReader(bodyBytes)
		} else {
			bodyReader = bytes.NewReader([]byte{})
		}

		req := httptest.NewRequest(method, path, bodyReader)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		}

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// Create test users
	user1, token1 := createUser(t, "user1", "user1@example.com")
	user2, token2 := createUser(t, "user2", "user2@example.com")

	// Create a test video
	channel1 := createChannel(t, user1.ID)
	video := createVideo(t, channel1.ID)

	t.Run("CreateComment", func(t *testing.T) {
		body := map[string]interface{}{
			"body": "This is a test comment",
		}

		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), body, token1)
		assert.Equal(t, http.StatusCreated, w.Code)

		var resp struct {
			Data domain.Comment `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, "This is a test comment", resp.Data.Body)
		assert.Equal(t, uuid.MustParse(user1.ID), resp.Data.UserID)
		assert.Equal(t, uuid.MustParse(video.ID), resp.Data.VideoID)
		assert.Nil(t, resp.Data.ParentID)
	})

	t.Run("CreateReply", func(t *testing.T) {
		// First create a parent comment
		parentBody := map[string]interface{}{
			"body": "Parent comment",
		}
		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), parentBody, token1)
		require.Equal(t, http.StatusCreated, w.Code)

		var parentResp struct {
			Data domain.Comment `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&parentResp)
		require.NoError(t, err)

		// Create a reply
		replyBody := map[string]interface{}{
			"body":     "This is a reply",
			"parentId": parentResp.Data.ID,
		}
		w = makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), replyBody, token2)
		assert.Equal(t, http.StatusCreated, w.Code)

		var replyResp struct {
			Data domain.Comment `json:"data"`
		}
		err = json.NewDecoder(w.Body).Decode(&replyResp)
		require.NoError(t, err)
		assert.Equal(t, "This is a reply", replyResp.Data.Body)
		assert.Equal(t, uuid.MustParse(user2.ID), replyResp.Data.UserID)
		assert.NotNil(t, replyResp.Data.ParentID)
		assert.Equal(t, parentResp.Data.ID, *replyResp.Data.ParentID)
	})

	t.Run("ListComments", func(t *testing.T) {
		// Create multiple comments
		for i := 0; i < 5; i++ {
			body := map[string]interface{}{
				"body": fmt.Sprintf("Comment %d", i),
			}
			w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), body, token1)
			require.Equal(t, http.StatusCreated, w.Code)
		}

		// Get comments without auth
		w := makeRequest("GET", fmt.Sprintf("/api/v1/videos/%s/comments?limit=10", video.ID), nil, "")
		assert.Equal(t, http.StatusOK, w.Code)

		var response struct {
			Data []domain.CommentWithUser `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(response.Data), 5)
	})

	t.Run("UpdateComment", func(t *testing.T) {
		// Create a comment
		createBody := map[string]interface{}{
			"body": "Original comment",
		}
		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), createBody, token1)
		require.Equal(t, http.StatusCreated, w.Code)

		var createResp struct {
			Data domain.Comment `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&createResp)
		require.NoError(t, err)

		// Update the comment
		updateBody := map[string]interface{}{
			"body": "Updated comment",
		}
		w = makeRequest("PUT", fmt.Sprintf("/api/v1/comments/%s", createResp.Data.ID), updateBody, token1)
		assert.Equal(t, http.StatusNoContent, w.Code)

		// Verify update
		w = makeRequest("GET", fmt.Sprintf("/api/v1/comments/%s", createResp.Data.ID), nil, "")
		assert.Equal(t, http.StatusOK, w.Code)

		var updatedComment struct {
			Data domain.CommentWithUser `json:"data"`
		}
		err = json.NewDecoder(w.Body).Decode(&updatedComment)
		require.NoError(t, err)
		assert.Equal(t, "Updated comment", updatedComment.Data.Body)
		assert.NotNil(t, updatedComment.Data.EditedAt)
	})

	t.Run("DeleteComment", func(t *testing.T) {
		// Create a comment
		createBody := map[string]interface{}{
			"body": "Comment to delete",
		}
		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), createBody, token1)
		require.Equal(t, http.StatusCreated, w.Code)

		var createResp struct {
			Data domain.Comment `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&createResp)
		require.NoError(t, err)

		// Delete the comment
		w = makeRequest("DELETE", fmt.Sprintf("/api/v1/comments/%s", createResp.Data.ID), nil, token1)
		assert.Equal(t, http.StatusNoContent, w.Code)

		// Verify deletion (should return 404)
		w = makeRequest("GET", fmt.Sprintf("/api/v1/comments/%s", createResp.Data.ID), nil, "")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("FlagComment", func(t *testing.T) {
		// Create a comment by user1
		createBody := map[string]interface{}{
			"body": "Inappropriate comment",
		}
		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), createBody, token1)
		require.Equal(t, http.StatusCreated, w.Code)

		var createResp struct {
			Data domain.Comment `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&createResp)
		require.NoError(t, err)

		// Flag the comment as user2
		flagBody := map[string]interface{}{
			"reason":  "inappropriate",
			"details": "This comment violates community guidelines",
		}
		w = makeRequest("POST", fmt.Sprintf("/api/v1/comments/%s/flag", createResp.Data.ID), flagBody, token2)
		assert.Equal(t, http.StatusCreated, w.Code)

		// Can't flag your own comment
		w = makeRequest("POST", fmt.Sprintf("/api/v1/comments/%s/flag", createResp.Data.ID), flagBody, token1)
		assert.Equal(t, http.StatusInternalServerError, w.Code)

		// Unflag the comment
		w = makeRequest("DELETE", fmt.Sprintf("/api/v1/comments/%s/flag", createResp.Data.ID), nil, token2)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("ModerateComment", func(t *testing.T) {
		// Create a comment
		createBody := map[string]interface{}{
			"body": "Comment to moderate",
		}
		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), createBody, token2)
		require.Equal(t, http.StatusCreated, w.Code)

		var createResp struct {
			Data domain.Comment `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&createResp)
		require.NoError(t, err)

		// Video owner can moderate
		moderateBody := map[string]interface{}{
			"status": "hidden",
		}
		w = makeRequest("POST", fmt.Sprintf("/api/v1/comments/%s/moderate", createResp.Data.ID), moderateBody, token1)
		assert.Equal(t, http.StatusNoContent, w.Code)

		// Non-owner can't moderate (create another video for user2)
		channel2 := createChannel(t, user2.ID)
		video2 := createVideo(t, channel2.ID)

		// Create comment on video2 by user1
		createBody2 := map[string]interface{}{
			"body": "Comment on video2",
		}
		w = makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video2.ID), createBody2, token1)
		require.Equal(t, http.StatusCreated, w.Code)

		var createResp2 struct {
			Data domain.Comment `json:"data"`
		}
		err = json.NewDecoder(w.Body).Decode(&createResp2)
		require.NoError(t, err)

		// User1 can't moderate comment on user2's video
		w = makeRequest("POST", fmt.Sprintf("/api/v1/comments/%s/moderate", createResp2.Data.ID), moderateBody, token1)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("Threading", func(t *testing.T) {
		// Create parent comment
		parentBody := map[string]interface{}{
			"body": "Parent comment for threading test",
		}
		w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), parentBody, token1)
		require.Equal(t, http.StatusCreated, w.Code)

		var parentResp struct {
			Data domain.Comment `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&parentResp)
		require.NoError(t, err)

		// Create multiple replies
		for i := 0; i < 3; i++ {
			replyBody := map[string]interface{}{
				"body":     fmt.Sprintf("Reply %d", i),
				"parentId": parentResp.Data.ID,
			}
			w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), replyBody, token2)
			require.Equal(t, http.StatusCreated, w.Code)
		}

		// Get top-level comments (should include parent with some replies)
		w = makeRequest("GET", fmt.Sprintf("/api/v1/videos/%s/comments", video.ID), nil, "")
		assert.Equal(t, http.StatusOK, w.Code)

		var response struct {
			Data []domain.CommentWithUser `json:"data"`
		}
		err = json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)

		// Find our parent comment
		var foundParent *domain.CommentWithUser
		for i := range response.Data {
			if response.Data[i].ID == parentResp.Data.ID {
				foundParent = &response.Data[i]
				break
			}
		}
		require.NotNil(t, foundParent)
		assert.Equal(t, 3, len(foundParent.Replies))

		// Get replies directly
		w = makeRequest("GET", fmt.Sprintf("/api/v1/videos/%s/comments?parentId=%s", video.ID, parentResp.Data.ID), nil, "")
		assert.Equal(t, http.StatusOK, w.Code)

		err = json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)
		assert.Equal(t, 3, len(response.Data))
	})

	t.Run("Pagination", func(t *testing.T) {
		// Create a new video for clean pagination test
		video3 := createVideo(t, channel1.ID)

		// Create many comments
		for i := 0; i < 25; i++ {
			body := map[string]interface{}{
				"body": fmt.Sprintf("Pagination test comment %d", i),
			}
			w := makeRequest("POST", fmt.Sprintf("/api/v1/videos/%s/comments", video3.ID), body, token1)
			require.Equal(t, http.StatusCreated, w.Code)
			time.Sleep(10 * time.Millisecond) // Small delay to ensure different timestamps
		}

		// Get first page
		w := makeRequest("GET", fmt.Sprintf("/api/v1/videos/%s/comments?limit=10&offset=0", video3.ID), nil, "")
		assert.Equal(t, http.StatusOK, w.Code)

		var page1 struct {
			Data []domain.CommentWithUser `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&page1)
		require.NoError(t, err)
		assert.Equal(t, 10, len(page1.Data))

		// Get second page
		w = makeRequest("GET", fmt.Sprintf("/api/v1/videos/%s/comments?limit=10&offset=10", video3.ID), nil, "")
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
