package video

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/middleware"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestUpdateVideoHandler(t *testing.T) {
	// Common test data
	userID := "user-123"
	otherUserID := "user-456"
	videoID := uuid.NewString()
	categoryID := uuid.New()

	existingVideo := &domain.Video{
		ID:          videoID,
		Title:       "Original Title",
		Description: "Original Description",
		Privacy:     domain.PrivacyPrivate,
		Status:      domain.StatusCompleted,
		UserID:      userID,
		Tags:        []string{"original"},
		CategoryID:  nil,
		Language:    "en",
		CreatedAt:   time.Now().Add(-24 * time.Hour),
		UpdatedAt:   time.Now().Add(-24 * time.Hour),
	}

	tests := []struct {
		name           string
		videoID        string
		requestBody    interface{} // map or struct
		userContextID  string
		setupMock      func(*MockVideoRepository)
		expectedStatus int
		verifyResponse func(*testing.T, *Response)
	}{
		{
			name:    "Success: Update basic fields",
			videoID: videoID,
			requestBody: map[string]interface{}{
				"title":       "New Title",
				"description": "New Description",
				"privacy":     "public",
				"tags":        []string{"new"},
				"language":    "es",
			},
			userContextID: userID,
			setupMock: func(m *MockVideoRepository) {
				m.On("GetByID", mock.Anything, videoID).Return(existingVideo, nil).Once()

				// Expect Update with new values
				m.On("Update", mock.Anything, mock.MatchedBy(func(v *domain.Video) bool {
					return v.ID == videoID &&
						v.Title == "New Title" &&
						v.Description == "New Description" &&
						v.Privacy == domain.PrivacyPublic &&
						len(v.Tags) == 1 && v.Tags[0] == "new" &&
						v.Language == "es" &&
						v.UserID == userID
				})).Return(nil).Once()

				// Handler fetches updated video to return it
				updatedVideo := *existingVideo
				updatedVideo.Title = "New Title"
				updatedVideo.Privacy = domain.PrivacyPublic
				m.On("GetByID", mock.Anything, videoID).Return(&updatedVideo, nil).Once()
			},
			expectedStatus: http.StatusOK,
			verifyResponse: func(t *testing.T, resp *Response) {
				require.True(t, resp.Success)
				dataBytes, _ := json.Marshal(resp.Data)
				var v domain.Video
				_ = json.Unmarshal(dataBytes, &v)
				assert.Equal(t, "New Title", v.Title)
				assert.Equal(t, domain.PrivacyPublic, v.Privacy)
			},
		},
		{
			name:    "Success: Update Category with UUID",
			videoID: videoID,
			requestBody: map[string]interface{}{
				"title":       "New Title",
				"privacy":     "public",
				"category_id": categoryID.String(),
			},
			userContextID: userID,
			setupMock: func(m *MockVideoRepository) {
				m.On("GetByID", mock.Anything, videoID).Return(existingVideo, nil).Once()

				m.On("Update", mock.Anything, mock.MatchedBy(func(v *domain.Video) bool {
					return v.CategoryID != nil && *v.CategoryID == categoryID
				})).Return(nil).Once()

				updatedVideo := *existingVideo
				updatedVideo.CategoryID = &categoryID
				m.On("GetByID", mock.Anything, videoID).Return(&updatedVideo, nil).Once()
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:    "Failure: Invalid JSON",
			videoID: videoID,
			requestBody: "invalid-json", // String instead of object
			userContextID: userID,
			setupMock: func(m *MockVideoRepository) {
				// No calls expected
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:    "Failure: Missing Title",
			videoID: videoID,
			requestBody: map[string]interface{}{
				"description": "Missing title",
				"privacy":     "public",
			},
			userContextID: userID,
			setupMock: func(m *MockVideoRepository) {
				// No calls expected
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:    "Failure: Invalid Privacy",
			videoID: videoID,
			requestBody: map[string]interface{}{
				"title":   "Title",
				"privacy": "invalid_privacy",
			},
			userContextID: userID,
			setupMock: func(m *MockVideoRepository) {
				// No calls expected
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:    "Failure: Video Not Found",
			videoID: videoID,
			requestBody: map[string]interface{}{
				"title":   "Title",
				"privacy": "public",
			},
			userContextID: userID,
			setupMock: func(m *MockVideoRepository) {
				m.On("GetByID", mock.Anything, videoID).Return(nil, domain.NewDomainError("VIDEO_NOT_FOUND", "not found")).Once()
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:    "Failure: Unauthorized (User Mismatch)",
			videoID: videoID,
			requestBody: map[string]interface{}{
				"title":   "Title",
				"privacy": "public",
			},
			userContextID: otherUserID,
			setupMock: func(m *MockVideoRepository) {
				m.On("GetByID", mock.Anything, videoID).Return(existingVideo, nil).Once()
			},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:    "Failure: Repository Update Error",
			videoID: videoID,
			requestBody: map[string]interface{}{
				"title":   "Title",
				"privacy": "public",
			},
			userContextID: userID,
			setupMock: func(m *MockVideoRepository) {
				m.On("GetByID", mock.Anything, videoID).Return(existingVideo, nil).Once()
				m.On("Update", mock.Anything, mock.Anything).Return(errors.New("db error")).Once()
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:    "Success (with Caveat): Category Slug Ignored (Current Behavior)",
			videoID: videoID,
			requestBody: map[string]interface{}{
				"title":    "New Title",
				"privacy":  "public",
				"category": "music", // Slug provided, but logic currently ignores it
			},
			userContextID: userID,
			setupMock: func(m *MockVideoRepository) {
				m.On("GetByID", mock.Anything, videoID).Return(existingVideo, nil).Once()

				// Assert that CategoryID is NIL even though "category": "music" was sent
				// This documents the current limitation/bug
				m.On("Update", mock.Anything, mock.MatchedBy(func(v *domain.Video) bool {
					return v.CategoryID == nil
				})).Return(nil).Once()

				updatedVideo := *existingVideo
				m.On("GetByID", mock.Anything, videoID).Return(&updatedVideo, nil).Once()
			},
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockVideoRepository)
			if tt.setupMock != nil {
				tt.setupMock(mockRepo)
			}

			// Prepare request body
			var body []byte
			if s, ok := tt.requestBody.(string); ok {
				body = []byte(s)
			} else {
				body, _ = json.Marshal(tt.requestBody)
			}

			// Create request
			req := httptest.NewRequest("PUT", "/api/v1/videos/"+tt.videoID, bytes.NewReader(body))

			// Set user context
			if tt.userContextID != "" {
				ctx := context.WithValue(req.Context(), middleware.UserIDKey, tt.userContextID)
				req = req.WithContext(ctx)
			}

			// Set Chi URL param
			req = withChiURLParam(req, "id", tt.videoID)

			w := httptest.NewRecorder()

			// Call Handler
			handler := UpdateVideoHandler(mockRepo)
			handler(w, req)

			// Assertions
			assert.Equal(t, tt.expectedStatus, w.Code)

			if w.Code == http.StatusOK {
				var resp Response
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err)

				if tt.verifyResponse != nil {
					tt.verifyResponse(t, &resp)
				}
			}

			mockRepo.AssertExpectations(t)
		})
	}
}

func TestDeleteVideoHandler(t *testing.T) {
	userID := "user-123"
	videoID := uuid.NewString()

	t.Run("Success: Delete video", func(t *testing.T) {
		mockRepo := new(MockVideoRepository)

		mockRepo.On("Delete", mock.Anything, videoID, userID).Return(nil).Once()

		req := httptest.NewRequest("DELETE", "/api/v1/videos/"+videoID, nil)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
		req = req.WithContext(ctx)
		req = withChiURLParam(req, "id", videoID)

		w := httptest.NewRecorder()
		DeleteVideoHandler(mockRepo)(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		mockRepo.AssertExpectations(t)
	})

	t.Run("Failure: Video not found", func(t *testing.T) {
		mockRepo := new(MockVideoRepository)

		mockRepo.On("Delete", mock.Anything, videoID, userID).Return(domain.NewDomainError("VIDEO_NOT_FOUND", "not found")).Once()

		req := httptest.NewRequest("DELETE", "/api/v1/videos/"+videoID, nil)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
		req = req.WithContext(ctx)
		req = withChiURLParam(req, "id", videoID)

		w := httptest.NewRecorder()
		DeleteVideoHandler(mockRepo)(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		mockRepo.AssertExpectations(t)
	})

	t.Run("Failure: Missing Authentication", func(t *testing.T) {
		mockRepo := new(MockVideoRepository)

		req := httptest.NewRequest("DELETE", "/api/v1/videos/"+videoID, nil)
		req = withChiURLParam(req, "id", videoID)

		w := httptest.NewRecorder()
		DeleteVideoHandler(mockRepo)(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		mockRepo.AssertExpectations(t)
	})
}
