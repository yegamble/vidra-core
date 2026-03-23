package social

import (
	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/repository"
	"vidra-core/internal/testutil"
	"vidra-core/internal/usecase"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptionsIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Setup test database
	db := testutil.SetupTestDB(t)

	// Initialize repositories
	userRepo := repository.NewUserRepository(db.DB)
	channelRepo := repository.NewChannelRepository(db.DB)
	videoRepo := repository.NewVideoRepository(db.DB)
	captionRepo := repository.NewCaptionRepository(db.DB)

	// Initialize test config
	cfg := &config.Config{
		JWTSecret:  "test-secret",
		StorageDir: t.TempDir(),
	}

	// Initialize services
	captionService := usecase.NewCaptionService(captionRepo, videoRepo, cfg)

	// Initialize handlers
	captionHandlers := NewCaptionHandlers(captionService, videoRepo)

	// Create test user
	userID := uuid.New()
	user := &domain.User{
		ID:       userID.String(),
		Email:    "test@example.com",
		Username: "testuser",
		Role:     domain.RoleUser,
	}
	err := userRepo.Create(context.Background(), user, "hashedpassword")
	require.NoError(t, err)

	channel := &domain.Channel{
		AccountID:   userID,
		Handle:      fmt.Sprintf("captions_%s", userID.String()[:8]),
		DisplayName: "Captions Channel",
		IsLocal:     true,
	}
	err = channelRepo.Create(context.Background(), channel)
	require.NoError(t, err)

	// Create test video
	videoID := uuid.New()
	video := &domain.Video{
		ID:            videoID.String(),
		ThumbnailID:   uuid.NewString(),
		ChannelID:     channel.ID,
		Title:         "Test Video",
		Description:   "Test Description",
		Privacy:       domain.PrivacyPublic,
		Status:        domain.StatusCompleted,
		UserID:        userID.String(),
		Tags:          []string{},
		FileSize:      1024,
		Metadata:      domain.VideoMetadata{},
		ProcessedCIDs: map[string]string{},
		OutputPaths:   map[string]string{},
	}
	err = videoRepo.Create(context.Background(), video)
	require.NoError(t, err)

	// Setup router
	r := chi.NewRouter()
	r.Route("/api/v1/videos/{id}/captions", func(r chi.Router) {
		r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", captionHandlers.GetCaptions)
		r.With(middleware.Auth(cfg.JWTSecret)).Post("/", captionHandlers.CreateCaption)
		r.Route("/{captionId}", func(r chi.Router) {
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/content", captionHandlers.GetCaptionContent)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/", captionHandlers.UpdateCaption)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/", captionHandlers.DeleteCaption)
		})
	})

	// Generate test JWT token
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": userID.String(),
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
	tokenObj := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token, err := tokenObj.SignedString([]byte(cfg.JWTSecret))
	require.NoError(t, err)

	t.Run("CreateCaption", func(t *testing.T) {
		// Create a test VTT file
		vttContent := `WEBVTT

00:00:00.000 --> 00:00:02.000
Hello World

00:00:02.000 --> 00:00:04.000
This is a test caption`

		// Create multipart form
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		// Add form fields
		_ = writer.WriteField("language_code", "en")
		_ = writer.WriteField("label", "English")
		_ = writer.WriteField("file_format", "vtt")

		// Add file
		part, err := writer.CreateFormFile("caption_file", "test.vtt")
		require.NoError(t, err)
		_, err = part.Write([]byte(vttContent))
		require.NoError(t, err)

		err = writer.Close()
		require.NoError(t, err)

		// Create request
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/captions", videoID), body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+token)

		// Execute request
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		// Assert response
		assert.Equal(t, http.StatusCreated, rec.Code)

		var response struct {
			Data domain.Caption `json:"data"`
		}
		err = json.NewDecoder(rec.Body).Decode(&response)
		require.NoError(t, err)

		assert.Equal(t, "en", response.Data.LanguageCode)
		assert.Equal(t, "English", response.Data.Label)
		assert.Equal(t, domain.CaptionFormatVTT, response.Data.FileFormat)
		assert.Equal(t, videoID, response.Data.VideoID)
	})

	t.Run("GetCaptions", func(t *testing.T) {
		// Create request
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/videos/%s/captions", videoID), nil)

		// Execute request
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		// Assert response
		assert.Equal(t, http.StatusOK, rec.Code)

		var response struct {
			Data domain.CaptionListResponse `json:"data"`
		}
		err = json.NewDecoder(rec.Body).Decode(&response)
		require.NoError(t, err)

		assert.Equal(t, 1, response.Data.Count)
		if len(response.Data.Captions) > 0 {
			assert.Len(t, response.Data.Captions, 1)
			assert.Equal(t, "en", response.Data.Captions[0].LanguageCode)
		} else {
			t.Log("Warning: No captions returned, CreateCaption test may not have persisted data")
		}
	})

	t.Run("GetCaptionContent", func(t *testing.T) {
		// First get the caption ID
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/videos/%s/captions", videoID), nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		var listResponse struct {
			Data domain.CaptionListResponse `json:"data"`
		}
		err = json.NewDecoder(rec.Body).Decode(&listResponse)
		require.NoError(t, err)

		if len(listResponse.Data.Captions) == 0 {
			t.Skip("No captions available, skipping content test")
			return
		}

		captionID := listResponse.Data.Captions[0].ID

		// Get caption content
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/videos/%s/captions/%s/content", videoID, captionID), nil)
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		// Assert response
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "text/vtt", rec.Header().Get("Content-Type"))

		content, err := io.ReadAll(rec.Body)
		require.NoError(t, err)
		assert.Contains(t, string(content), "WEBVTT")
		assert.Contains(t, string(content), "Hello World")
	})

	t.Run("UpdateCaption", func(t *testing.T) {
		// First get the caption ID
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/videos/%s/captions", videoID), nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		var listResponse struct {
			Data domain.CaptionListResponse `json:"data"`
		}
		err = json.NewDecoder(rec.Body).Decode(&listResponse)
		require.NoError(t, err)

		if len(listResponse.Data.Captions) == 0 {
			t.Skip("No captions available, skipping update test")
			return
		}

		captionID := listResponse.Data.Captions[0].ID

		// Update caption
		updateReq := domain.UpdateCaptionRequest{
			Label: captionStrPtr("English (US)"),
		}
		body, err := json.Marshal(updateReq)
		require.NoError(t, err)

		req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/videos/%s/captions/%s", videoID, captionID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		// Assert response
		assert.Equal(t, http.StatusOK, rec.Code)

		var updateResp struct {
			Data domain.Caption `json:"data"`
		}
		err = json.NewDecoder(rec.Body).Decode(&updateResp)
		require.NoError(t, err)

		assert.Equal(t, "English (US)", updateResp.Data.Label)
	})

	t.Run("DeleteCaption", func(t *testing.T) {
		// Create another caption to delete
		vttContent := `WEBVTT

00:00:00.000 --> 00:00:02.000
Hola Mundo`

		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		_ = writer.WriteField("language_code", "es")
		_ = writer.WriteField("label", "Spanish")
		_ = writer.WriteField("file_format", "vtt")
		part, err := writer.CreateFormFile("caption_file", "test_es.vtt")
		require.NoError(t, err)
		_, err = part.Write([]byte(vttContent))
		require.NoError(t, err)
		err = writer.Close()
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/captions", videoID), body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+token)

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusCreated, rec.Code)

		var createResp struct {
			Data domain.Caption `json:"data"`
		}
		err = json.NewDecoder(rec.Body).Decode(&createResp)
		require.NoError(t, err)

		// Delete the caption
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/videos/%s/captions/%s", videoID, createResp.Data.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)

		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		// Assert response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Verify caption is deleted
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/videos/%s/captions/%s/content", videoID, createResp.Data.ID), nil)
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("PrivateVideoAccessControl", func(t *testing.T) {
		privateOwnerID := uuid.New()
		privateOwner := &domain.User{
			ID:       privateOwnerID.String(),
			Email:    "private-owner@example.com",
			Username: "private_owner",
			Role:     domain.RoleUser,
		}
		err = userRepo.Create(context.Background(), privateOwner, "hashedpassword")
		require.NoError(t, err)

		privateChannel := &domain.Channel{
			AccountID:   privateOwnerID,
			Handle:      fmt.Sprintf("private_%s", privateOwnerID.String()[:8]),
			DisplayName: "Private Channel",
			IsLocal:     true,
		}
		err = channelRepo.Create(context.Background(), privateChannel)
		require.NoError(t, err)

		// Create private video
		privateVideoID := uuid.New()
		privateVideo := &domain.Video{
			ID:            privateVideoID.String(),
			ThumbnailID:   uuid.NewString(),
			ChannelID:     privateChannel.ID,
			Title:         "Private Video",
			Description:   "Private Test",
			Privacy:       domain.PrivacyPrivate,
			Status:        domain.StatusCompleted,
			UserID:        privateOwnerID.String(), // Different user
			Tags:          []string{},
			FileSize:      1024,
			Metadata:      domain.VideoMetadata{},
			ProcessedCIDs: map[string]string{},
			OutputPaths:   map[string]string{},
		}
		err = videoRepo.Create(context.Background(), privateVideo)
		require.NoError(t, err)

		// Try to get captions without auth (should fail)
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/videos/%s/captions", privateVideoID), nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code)

		// Try to add caption with wrong user (should fail)
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		_ = writer.WriteField("language_code", "fr")
		_ = writer.WriteField("label", "French")
		_ = writer.WriteField("file_format", "vtt")
		part, err := writer.CreateFormFile("caption_file", "test_fr.vtt")
		require.NoError(t, err)
		_, err = part.Write([]byte("WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nBonjour"))
		require.NoError(t, err)
		err = writer.Close()
		require.NoError(t, err)

		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/captions", privateVideoID), body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+token)

		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
}

func captionStrPtr(s string) *string {
	return &s
}
