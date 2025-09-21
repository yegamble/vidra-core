package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/repository"
	"athena/internal/testutil"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModerationHandlers(t *testing.T) {
	// Setup test database
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return // Test was skipped
	}

	// Clean up any existing test data (but not users since we'll create them)
	ctx := context.Background()
	cleanupQueries := []string{
		"TRUNCATE TABLE abuse_reports CASCADE",
		"TRUNCATE TABLE blocklist CASCADE",
	}
	for _, q := range cleanupQueries {
		if _, err := testDB.DB.ExecContext(ctx, q); err != nil {
			t.Logf("Cleanup warning: %v", err)
		}
	}

	moderationRepo := repository.NewModerationRepository(testDB.DB)

	// Create test users
	adminUser := testutil.CreateTestUser(t, testDB.DB, "admin@test.com", string(domain.RoleAdmin))
	require.NotNil(t, adminUser, "Failed to create admin user")
	t.Logf("Created admin user with ID: %s", adminUser.ID)

	regularUser := testutil.CreateTestUser(t, testDB.DB, "user@test.com", string(domain.RoleUser))
	require.NotNil(t, regularUser, "Failed to create regular user")
	t.Logf("Created regular user with ID: %s", regularUser.ID)

	targetUser := testutil.CreateTestUser(t, testDB.DB, "target@test.com", string(domain.RoleUser))
	require.NotNil(t, targetUser, "Failed to create target user")
	t.Logf("Created target user with ID: %s", targetUser.ID)

	// Verify users exist in database
	var count int
	err := testDB.DB.Get(&count, "SELECT COUNT(*) FROM users WHERE id = $1", regularUser.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count, "Regular user not found in database")
	t.Logf("Verified regular user exists in database")

	// Try to insert a test abuse report directly
	_, err = testDB.DB.Exec(`
		INSERT INTO abuse_reports (
			reporter_id, reason, status, reported_entity_type, reported_user_id
		) VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, regularUser.ID, "Test reason", "pending", "user", targetUser.ID)
	if err != nil {
		t.Logf("Direct insert test failed: %v", err)
	} else {
		t.Logf("Direct insert test succeeded")
	}

	// Create handlers
	handlers := NewModerationHandlers(moderationRepo)

	t.Run("CreateAbuseReport", func(t *testing.T) {
		req := domain.CreateAbuseReportRequest{
			Reason:     "Spam content",
			Details:    "This user is posting spam",
			EntityType: domain.ReportedEntityUser,
			EntityID:   targetUser.ID,
		}

		body, _ := json.Marshal(req)
		r := httptest.NewRequest("POST", "/api/v1/abuse-reports", bytes.NewReader(body))
		t.Logf("Setting context with regularUser.ID: %s", regularUser.ID)
		r = r.WithContext(withUserID(r.Context(), regularUser.ID))
		w := httptest.NewRecorder()

		handlers.CreateAbuseReport(w, r)

		if w.Code != http.StatusCreated {
			t.Logf("CreateAbuseReport failed with status %d, body: %s", w.Code, w.Body.String())
		}
		assert.Equal(t, http.StatusCreated, w.Code)
		var resp struct {
			Data    domain.AbuseReport `json:"data"`
			Success bool               `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, regularUser.ID, resp.Data.ReporterID)
		assert.Equal(t, "Spam content", resp.Data.Reason)
		assert.Equal(t, domain.AbuseReportStatusPending, resp.Data.Status)
	})

	t.Run("ListAbuseReports", func(t *testing.T) {
		// Create a few test reports
		report1 := &domain.AbuseReport{
			ReporterID: regularUser.ID,
			Reason:     "Test report 1",
			Status:     domain.AbuseReportStatusPending,
			EntityType: domain.ReportedEntityUser,
			UserID:     testutil.NullString(targetUser.ID),
		}
		err := moderationRepo.CreateAbuseReport(context.Background(), report1)
		require.NoError(t, err)

		r := httptest.NewRequest("GET", "/api/v1/admin/abuse-reports", nil)
		r = r.WithContext(withUserID(r.Context(), adminUser.ID))
		w := httptest.NewRecorder()

		handlers.ListAbuseReports(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Data    []domain.AbuseReport `json:"data"`
			Total   int64                `json:"total"`
			Success bool                 `json:"success"`
		}
		err = json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.GreaterOrEqual(t, resp.Total, int64(1))
	})

	t.Run("UpdateAbuseReport", func(t *testing.T) {
		// Create a test report
		report := &domain.AbuseReport{
			ReporterID: regularUser.ID,
			Reason:     "Test report for update",
			Status:     domain.AbuseReportStatusPending,
			EntityType: domain.ReportedEntityUser,
			UserID:     testutil.NullString(targetUser.ID),
		}
		err := moderationRepo.CreateAbuseReport(context.Background(), report)
		require.NoError(t, err)

		updateReq := domain.UpdateAbuseReportRequest{
			Status:         domain.AbuseReportStatusAccepted,
			ModeratorNotes: "Confirmed violation",
		}

		body, _ := json.Marshal(updateReq)
		r := httptest.NewRequest("PUT", "/api/v1/admin/abuse-reports/"+report.ID, bytes.NewReader(body))
		r = r.WithContext(withUserID(r.Context(), adminUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx := chi.RouteContext(r.Context())
		rctx.URLParams.Add("id", report.ID)
		w := httptest.NewRecorder()

		handlers.UpdateAbuseReport(w, r)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify the report was updated
		updatedReport, err := moderationRepo.GetAbuseReport(context.Background(), report.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.AbuseReportStatusAccepted, updatedReport.Status)
		assert.Equal(t, "Confirmed violation", updatedReport.ModeratorNotes.String)
		assert.Equal(t, adminUser.ID, updatedReport.ModeratedBy.String)
	})

	t.Run("CreateBlocklistEntry", func(t *testing.T) {
		req := domain.CreateBlocklistEntryRequest{
			BlockType:    domain.BlockTypeEmail,
			BlockedValue: "spammer@example.com",
			Reason:       "Known spammer",
		}

		body, _ := json.Marshal(req)
		r := httptest.NewRequest("POST", "/api/v1/admin/blocklist", bytes.NewReader(body))
		r = r.WithContext(withUserID(r.Context(), adminUser.ID))
		w := httptest.NewRecorder()

		handlers.CreateBlocklistEntry(w, r)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp struct {
			Data    domain.BlocklistEntry `json:"data"`
			Success bool                  `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, domain.BlockTypeEmail, resp.Data.BlockType)
		assert.Equal(t, "spammer@example.com", resp.Data.BlockedValue)
		assert.True(t, resp.Data.IsActive)
	})

	t.Run("IsBlocked", func(t *testing.T) {
		// Create a blocklist entry
		entry := &domain.BlocklistEntry{
			BlockType:    domain.BlockTypeDomain,
			BlockedValue: "spam.com",
			BlockedBy:    adminUser.ID,
			IsActive:     true,
		}
		err := moderationRepo.CreateBlocklistEntry(context.Background(), entry)
		require.NoError(t, err)

		// Check if blocked
		isBlocked, err := moderationRepo.IsBlocked(context.Background(), domain.BlockTypeDomain, "spam.com")
		require.NoError(t, err)
		assert.True(t, isBlocked)

		// Check non-blocked
		isBlocked, err = moderationRepo.IsBlocked(context.Background(), domain.BlockTypeDomain, "legitimate.com")
		require.NoError(t, err)
		assert.False(t, isBlocked)
	})
}

func TestInstanceHandlers(t *testing.T) {
	// Setup test database
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return // Test was skipped
	}

	// Clean up any existing test data
	ctx := context.Background()
	if _, err := testDB.DB.ExecContext(ctx, "TRUNCATE TABLE instance_config CASCADE"); err != nil {
		t.Logf("Cleanup warning: %v", err)
	}

	moderationRepo := repository.NewModerationRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)

	// Create handlers
	handlers := NewInstanceHandlers(moderationRepo, userRepo, videoRepo)

	t.Run("GetInstanceAbout", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/v1/instance/about", nil)
		w := httptest.NewRecorder()

		handlers.GetInstanceAbout(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Data    domain.InstanceInfo `json:"data"`
			Success bool                `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		if w.Code != http.StatusOK {
			t.Logf("Response body: %s", w.Body.String())
		}
		t.Logf("Response: %+v", resp)
		t.Logf("Instance data - Name: %s, Version: %s", resp.Data.Name, resp.Data.Version)
		assert.True(t, resp.Success)
		// Check that name and version are not empty (either defaults or from config)
		assert.NotEmpty(t, resp.Data.Name)
		assert.NotEmpty(t, resp.Data.Version)
	})

	t.Run("UpdateInstanceConfig", func(t *testing.T) {
		adminUser := testutil.CreateTestUser(t, testDB.DB, "admin@test.com", string(domain.RoleAdmin))

		configValue := json.RawMessage(`"Updated Instance Name"`)
		req := domain.UpdateInstanceConfigRequest{
			Value:    configValue,
			IsPublic: true,
		}

		body, _ := json.Marshal(req)
		r := httptest.NewRequest("PUT", "/api/v1/admin/instance/config/instance_name", bytes.NewReader(body))
		r = r.WithContext(withUserID(r.Context(), adminUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx := chi.RouteContext(r.Context())
		rctx.URLParams.Add("key", "instance_name")
		w := httptest.NewRecorder()

		handlers.UpdateInstanceConfig(w, r)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify the config was updated
		config, err := moderationRepo.GetInstanceConfig(context.Background(), "instance_name")
		require.NoError(t, err)
		assert.Equal(t, configValue, config.Value)
		assert.True(t, config.IsPublic)
	})
}

func TestOEmbed(t *testing.T) {
	// Setup test database
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return // Test was skipped
	}

	// Clean up any existing test data
	ctx := context.Background()
	cleanupQueries := []string{
		"TRUNCATE TABLE videos CASCADE",
		"TRUNCATE TABLE users CASCADE",
	}
	for _, q := range cleanupQueries {
		if _, err := testDB.DB.ExecContext(ctx, q); err != nil {
			t.Logf("Cleanup warning: %v", err)
		}
	}

	moderationRepo := repository.NewModerationRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)

	// Create test data
	user := testutil.CreateTestUser(t, testDB.DB, "video@test.com", string(domain.RoleUser))
	video := testutil.CreateTestVideo(t, testDB.DB, user.ID, "Test Video for oEmbed")

	// Create handlers
	handlers := NewInstanceHandlers(moderationRepo, userRepo, videoRepo)

	t.Run("OEmbed_JSON", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/oembed?url=https://example.com/videos/"+video.ID, nil)
		w := httptest.NewRecorder()

		handlers.OEmbed(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

		var resp map[string]interface{}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, "1.0", resp["version"])
		assert.Equal(t, "video", resp["type"])
		assert.Equal(t, "Test Video for oEmbed", resp["title"])
		assert.Contains(t, resp["html"], "<iframe")
		assert.Contains(t, resp["html"], video.ID)
	})

	t.Run("OEmbed_XML", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/oembed?url=https://example.com/videos/"+video.ID+"&format=xml", nil)
		w := httptest.NewRecorder()

		handlers.OEmbed(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "application/xml")
		body := w.Body.String()
		assert.Contains(t, body, "<?xml version")
		assert.Contains(t, body, "<oembed>")
		assert.Contains(t, body, "<title>Test Video for oEmbed</title>")
		assert.Contains(t, body, "</oembed>")
	})

	t.Run("OEmbed_InvalidURL", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/oembed?url=https://example.com/invalid", nil)
		w := httptest.NewRecorder()

		handlers.OEmbed(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("OEmbed_MissingURL", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/oembed", nil)
		w := httptest.NewRecorder()

		handlers.OEmbed(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}
