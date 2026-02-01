package video

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
)

func TestUpdateVideoHandler(t *testing.T) {
	// Common test data
	userID := uuid.NewString()
	videoID := uuid.NewString()
	now := time.Now()

	existingVideo := &domain.Video{
		ID:          videoID,
		UserID:      userID,
		Title:       "Original Title",
		Description: "Original Description",
		Privacy:     domain.PrivacyPublic,
		Status:      domain.StatusCompleted,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	t.Run("Successful Update", func(t *testing.T) {
		repo := new(MockVideoRepository)

		// Expect GetByID to check ownership
		repo.On("GetByID", mock.Anything, videoID).Return(existingVideo, nil).Once()

		// Expect Update with new values
		repo.On("Update", mock.Anything, mock.MatchedBy(func(v *domain.Video) bool {
			return v.ID == videoID && v.Title == "New Title" && v.Description == "New Desc"
		})).Return(nil).Once()

		// Expect GetByID to return updated video for response
		updatedVideo := *existingVideo
		updatedVideo.Title = "New Title"
		updatedVideo.Description = "New Desc"
		repo.On("GetByID", mock.Anything, videoID).Return(&updatedVideo, nil).Once()

		reqBody := map[string]interface{}{
			"title":       "New Title",
			"description": "New Desc",
			"privacy":     "public",
		}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("PUT", "/api/v1/videos/"+videoID, bytes.NewReader(body))
		req = withChiURLParam(req, "id", videoID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))

		w := httptest.NewRecorder()
		handler := UpdateVideoHandler(repo)
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var envelope shared.Response
		err := json.Unmarshal(w.Body.Bytes(), &envelope)
		require.NoError(t, err)

		// Convert Data to map
		dataBytes, _ := json.Marshal(envelope.Data)
		var resp map[string]interface{}
		json.Unmarshal(dataBytes, &resp)

		assert.Equal(t, "New Title", resp["title"])
		repo.AssertExpectations(t)
	})

	t.Run("Update Category by ID", func(t *testing.T) {
		repo := new(MockVideoRepository)
		categoryID := uuid.New()

		repo.On("GetByID", mock.Anything, videoID).Return(existingVideo, nil).Once()

		// Verify categoryID is updated
		repo.On("Update", mock.Anything, mock.MatchedBy(func(v *domain.Video) bool {
			return v.CategoryID != nil && *v.CategoryID == categoryID
		})).Return(nil).Once()

		updatedVideo := *existingVideo
		updatedVideo.CategoryID = &categoryID
		repo.On("GetByID", mock.Anything, videoID).Return(&updatedVideo, nil).Once()

		reqBody := map[string]interface{}{
			"title":       "Title",
			"description": "Desc",
			"privacy":     "public",
			"category_id": categoryID.String(),
		}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("PUT", "/api/v1/videos/"+videoID, bytes.NewReader(body))
		req = withChiURLParam(req, "id", videoID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))

		w := httptest.NewRecorder()
		handler := UpdateVideoHandler(repo)
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		repo.AssertExpectations(t)
	})

	t.Run("Category String Ignored", func(t *testing.T) {
		repo := new(MockVideoRepository)

		repo.On("GetByID", mock.Anything, videoID).Return(existingVideo, nil).Once()

		// Verify categoryID is NOT set/updated when only 'category' string is sent
		// This documents the current behavior where string is ignored
		repo.On("Update", mock.Anything, mock.MatchedBy(func(v *domain.Video) bool {
			return v.CategoryID == nil
		})).Return(nil).Once()

		updatedVideo := *existingVideo
		repo.On("GetByID", mock.Anything, videoID).Return(&updatedVideo, nil).Once()

		reqBody := map[string]interface{}{
			"title":       "Title",
			"description": "Desc",
			"privacy":     "public",
			"category":    "gaming", // Should be ignored by current implementation
		}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("PUT", "/api/v1/videos/"+videoID, bytes.NewReader(body))
		req = withChiURLParam(req, "id", videoID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))

		w := httptest.NewRecorder()
		handler := UpdateVideoHandler(repo)
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		// Also verify response contains the category string we sent back (backward compat logic in handler)
		var envelope shared.Response
		err := json.Unmarshal(w.Body.Bytes(), &envelope)
		require.NoError(t, err)

		dataBytes, _ := json.Marshal(envelope.Data)
		var resp map[string]interface{}
		json.Unmarshal(dataBytes, &resp)

		assert.Equal(t, "gaming", resp["category"])

		repo.AssertExpectations(t)
	})

	t.Run("Forbidden - Wrong User", func(t *testing.T) {
		repo := new(MockVideoRepository)
		otherUserID := uuid.NewString()

		// Video owned by otherUserID
		otherUserVideo := *existingVideo
		otherUserVideo.UserID = otherUserID

		repo.On("GetByID", mock.Anything, videoID).Return(&otherUserVideo, nil).Once()

		reqBody := map[string]interface{}{
			"title":   "Title",
			"privacy": "public",
		}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("PUT", "/api/v1/videos/"+videoID, bytes.NewReader(body))
		req = withChiURLParam(req, "id", videoID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))

		w := httptest.NewRecorder()
		handler := UpdateVideoHandler(repo)
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		repo.AssertExpectations(t)
	})

	t.Run("Not Found", func(t *testing.T) {
		repo := new(MockVideoRepository)

		repo.On("GetByID", mock.Anything, videoID).Return(nil, domain.NewDomainError("NOT_FOUND", "Video not found")).Once()

		reqBody := map[string]interface{}{
			"title":   "Title",
			"privacy": "public",
		}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("PUT", "/api/v1/videos/"+videoID, bytes.NewReader(body))
		req = withChiURLParam(req, "id", videoID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))

		w := httptest.NewRecorder()
		handler := UpdateVideoHandler(repo)
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		repo.AssertExpectations(t)
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		repo := new(MockVideoRepository)

		req := httptest.NewRequest("PUT", "/api/v1/videos/"+videoID, bytes.NewReader([]byte("invalid json")))
		req = withChiURLParam(req, "id", videoID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))

		w := httptest.NewRecorder()
		handler := UpdateVideoHandler(repo)
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}
