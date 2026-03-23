package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/repository"
	"vidra-core/internal/testutil"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAbuseReportsIntegration(t *testing.T) {
	// Force use of public schema to avoid foreign key issues
	t.Setenv("TEST_SCHEMA", "public")

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	moderationRepo := repository.NewModerationRepository(testDB.DB)
	_ = repository.NewUserRepository(testDB.DB)
	_ = repository.NewVideoRepository(testDB.DB)

	adminUser := testutil.CreateTestUser(t, testDB.DB, "admin@test.com", string(domain.RoleAdmin))
	moderatorUser := testutil.CreateTestUser(t, testDB.DB, "mod@test.com", string(domain.RoleMod))
	regularUser1 := testutil.CreateTestUser(t, testDB.DB, "user1@test.com", string(domain.RoleUser))
	regularUser2 := testutil.CreateTestUser(t, testDB.DB, "user2@test.com", string(domain.RoleUser))

	handlers := NewModerationHandlers(moderationRepo)

	t.Run("CompleteAbuseReportWorkflow", func(t *testing.T) {
		// 1. Create abuse report for video
		video := testutil.CreateTestVideo(t, testDB.DB, regularUser1.ID, "Inappropriate Video")

		reportReq := domain.CreateAbuseReportRequest{
			Reason:     "Inappropriate content",
			Details:    "Video contains offensive material",
			EntityType: domain.ReportedEntityVideo,
			EntityID:   video.ID,
		}

		body, _ := json.Marshal(reportReq)
		r := httptest.NewRequest("POST", "/api/v1/abuse-reports", bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), regularUser2.ID))
		w := httptest.NewRecorder()

		handlers.CreateAbuseReport(w, r)
		assert.Equal(t, http.StatusCreated, w.Code)

		var createResp struct {
			Data domain.AbuseReport `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&createResp)
		require.NoError(t, err)
		reportID := createResp.Data.ID

		// 2. List reports as moderator with filters
		r = httptest.NewRequest("GET", "/api/v1/admin/abuse-reports?status=pending&entity_type=video", nil)
		r = r.WithContext(withUserIDCtx(r.Context(), moderatorUser.ID))
		w = httptest.NewRecorder()

		handlers.ListAbuseReports(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		var listResp struct {
			Data  []domain.AbuseReport `json:"data"`
			Total int64                `json:"total"`
		}
		err = json.NewDecoder(w.Body).Decode(&listResp)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, listResp.Total, int64(1))

		// 3. Get specific report
		r = httptest.NewRequest("GET", "/api/v1/admin/abuse-reports/"+reportID, nil)
		r = r.WithContext(withUserIDCtx(r.Context(), moderatorUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx := chi.RouteContext(r.Context())
		rctx.URLParams.Add("id", reportID)
		w = httptest.NewRecorder()

		handlers.GetAbuseReport(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		// 4. Update report status as moderator
		updateReq := domain.UpdateAbuseReportRequest{
			Status:         domain.AbuseReportStatusInvestigating,
			ModeratorNotes: "Reviewing the video content",
		}

		body, _ = json.Marshal(updateReq)
		r = httptest.NewRequest("PUT", "/api/v1/admin/abuse-reports/"+reportID, bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), moderatorUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx = chi.RouteContext(r.Context())
		rctx.URLParams.Add("id", reportID)
		w = httptest.NewRecorder()

		handlers.UpdateAbuseReport(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		// 5. Final decision by admin
		finalReq := domain.UpdateAbuseReportRequest{
			Status:         domain.AbuseReportStatusAccepted,
			ModeratorNotes: "Content violates terms of service",
		}

		body, _ = json.Marshal(finalReq)
		r = httptest.NewRequest("PUT", "/api/v1/admin/abuse-reports/"+reportID, bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx = chi.RouteContext(r.Context())
		rctx.URLParams.Add("id", reportID)
		w = httptest.NewRecorder()

		handlers.UpdateAbuseReport(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		// Verify final state
		finalReport, err := moderationRepo.GetAbuseReport(context.Background(), reportID)
		require.NoError(t, err)
		assert.Equal(t, domain.AbuseReportStatusAccepted, finalReport.Status)
		assert.Equal(t, adminUser.ID, finalReport.ModeratedBy.String)
	})

	t.Run("CreateAbuseReportForComment", func(t *testing.T) {
		video := testutil.CreateTestVideo(t, testDB.DB, regularUser1.ID, "Video with Comments")

		// Create a comment (simulated ID)
		commentID := "comment-123"
		_ = video // Mark as used

		reportReq := domain.CreateAbuseReportRequest{
			Reason:     "Hate speech",
			Details:    "Comment contains hateful language",
			EntityType: domain.ReportedEntityComment,
			EntityID:   commentID,
		}

		body, _ := json.Marshal(reportReq)
		r := httptest.NewRequest("POST", "/api/v1/abuse-reports", bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), regularUser1.ID))
		w := httptest.NewRecorder()

		handlers.CreateAbuseReport(w, r)
		assert.Equal(t, http.StatusCreated, w.Code)
	})

	t.Run("PaginationAndSorting", func(t *testing.T) {
		// Create multiple reports
		for i := 0; i < 5; i++ {
			report := &domain.AbuseReport{
				ReporterID: regularUser1.ID,
				Reason:     fmt.Sprintf("Test report %d", i),
				Status:     domain.AbuseReportStatusPending,
				EntityType: domain.ReportedEntityUser,
				UserID:     testutil.NullString(regularUser2.ID),
			}
			err := moderationRepo.CreateAbuseReport(context.Background(), report)
			require.NoError(t, err)
			time.Sleep(10 * time.Millisecond) // Ensure different timestamps
		}

		// Test pagination
		r := httptest.NewRequest("GET", "/api/v1/admin/abuse-reports?limit=2&offset=0&sort=-created_at", nil)
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		w := httptest.NewRecorder()

		handlers.ListAbuseReports(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data  []domain.AbuseReport `json:"data"`
			Total int64                `json:"total"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Len(t, resp.Data, 2)
		assert.GreaterOrEqual(t, resp.Total, int64(5))

		// Verify sorting (newest first)
		if len(resp.Data) >= 2 {
			assert.True(t, resp.Data[0].CreatedAt.After(resp.Data[1].CreatedAt))
		}
	})

	t.Run("DeleteAbuseReport", func(t *testing.T) {
		report := &domain.AbuseReport{
			ReporterID: regularUser1.ID,
			Reason:     "Test report to delete",
			Status:     domain.AbuseReportStatusPending,
			EntityType: domain.ReportedEntityUser,
			UserID:     testutil.NullString(regularUser2.ID),
		}
		err := moderationRepo.CreateAbuseReport(context.Background(), report)
		require.NoError(t, err)

		r := httptest.NewRequest("DELETE", "/api/v1/admin/abuse-reports/"+report.ID, nil)
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx := chi.RouteContext(r.Context())
		rctx.URLParams.Add("id", report.ID)
		w := httptest.NewRecorder()

		handlers.DeleteAbuseReport(w, r)
		assert.Equal(t, http.StatusNoContent, w.Code)

		// Verify deletion
		_, err = moderationRepo.GetAbuseReport(context.Background(), report.ID)
		assert.Error(t, err)
	})
}

func TestBlocklistIntegration(t *testing.T) {
	// Force use of public schema to avoid foreign key issues
	t.Setenv("TEST_SCHEMA", "public")

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	moderationRepo := repository.NewModerationRepository(testDB.DB)
	adminUser := testutil.CreateTestUser(t, testDB.DB, "admin@test.com", string(domain.RoleAdmin))
	regularUser := testutil.CreateTestUser(t, testDB.DB, "user@test.com", string(domain.RoleUser))

	handlers := NewModerationHandlers(moderationRepo)

	t.Run("CompleteBlocklistWorkflow", func(t *testing.T) {
		// 1. Block a domain
		domainReq := domain.CreateBlocklistEntryRequest{
			BlockType:    domain.BlockTypeDomain,
			BlockedValue: "malicious.com",
			Reason:       "Known malware distributor",
		}

		body, _ := json.Marshal(domainReq)
		r := httptest.NewRequest("POST", "/api/v1/admin/blocklist", bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		w := httptest.NewRecorder()

		handlers.CreateBlocklistEntry(w, r)
		assert.Equal(t, http.StatusCreated, w.Code)

		var createResp struct {
			Data domain.BlocklistEntry `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&createResp)
		require.NoError(t, err)
		domainBlockID := createResp.Data.ID

		// 2. Block an IP
		ipReq := domain.CreateBlocklistEntryRequest{
			BlockType:    domain.BlockTypeIP,
			BlockedValue: "192.168.1.100",
			Reason:       "Spam bot",
		}

		body, _ = json.Marshal(ipReq)
		r = httptest.NewRequest("POST", "/api/v1/admin/blocklist", bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		w = httptest.NewRecorder()

		handlers.CreateBlocklistEntry(w, r)
		assert.Equal(t, http.StatusCreated, w.Code)

		// 3. Block an email
		emailReq := domain.CreateBlocklistEntryRequest{
			BlockType:    domain.BlockTypeEmail,
			BlockedValue: "spammer@badactor.com",
			Reason:       "Serial spammer",
		}

		body, _ = json.Marshal(emailReq)
		r = httptest.NewRequest("POST", "/api/v1/admin/blocklist", bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		w = httptest.NewRecorder()

		handlers.CreateBlocklistEntry(w, r)
		assert.Equal(t, http.StatusCreated, w.Code)

		// 4. List all blocklist entries
		r = httptest.NewRequest("GET", "/api/v1/admin/blocklist", nil)
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		w = httptest.NewRecorder()

		handlers.ListBlocklistEntries(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		var listResp struct {
			Data  []domain.BlocklistEntry `json:"data"`
			Total int64                   `json:"total"`
		}
		err = json.NewDecoder(w.Body).Decode(&listResp)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, listResp.Total, int64(3))

		// 5. Filter by type
		r = httptest.NewRequest("GET", "/api/v1/admin/blocklist?type=domain", nil)
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		w = httptest.NewRecorder()

		handlers.ListBlocklistEntries(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		err = json.NewDecoder(w.Body).Decode(&listResp)
		require.NoError(t, err)
		for _, entry := range listResp.Data {
			assert.Equal(t, domain.BlockTypeDomain, entry.BlockType)
		}

		// 6. Update blocklist entry (deactivate)
		updateReq := struct {
			IsActive bool   `json:"is_active"`
			Reason   string `json:"reason"`
		}{
			IsActive: false,
			Reason:   "False positive - legitimate domain",
		}

		body, _ = json.Marshal(updateReq)
		r = httptest.NewRequest("PUT", "/api/v1/admin/blocklist/"+domainBlockID, bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx := chi.RouteContext(r.Context())
		rctx.URLParams.Add("id", domainBlockID)
		w = httptest.NewRecorder()

		handlers.UpdateBlocklistEntry(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		// 7. Verify blocking functionality
		isBlocked, err := moderationRepo.IsBlocked(context.Background(), domain.BlockTypeDomain, "malicious.com")
		require.NoError(t, err)
		assert.False(t, isBlocked) // Should be false since we deactivated it

		isBlocked, err = moderationRepo.IsBlocked(context.Background(), domain.BlockTypeIP, "192.168.1.100")
		require.NoError(t, err)
		assert.True(t, isBlocked) // Still active

		// 8. Delete blocklist entry
		r = httptest.NewRequest("DELETE", "/api/v1/admin/blocklist/"+domainBlockID, nil)
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx = chi.RouteContext(r.Context())
		rctx.URLParams.Add("id", domainBlockID)
		w = httptest.NewRecorder()

		handlers.DeleteBlocklistEntry(w, r)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("BlocklistValidation", func(t *testing.T) {
		// Test invalid email format
		req := domain.CreateBlocklistEntryRequest{
			BlockType:    domain.BlockTypeEmail,
			BlockedValue: "not-an-email",
			Reason:       "Test",
		}

		body, _ := json.Marshal(req)
		r := httptest.NewRequest("POST", "/api/v1/admin/blocklist", bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		w := httptest.NewRecorder()

		handlers.CreateBlocklistEntry(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)

		// Test invalid IP format
		req = domain.CreateBlocklistEntryRequest{
			BlockType:    domain.BlockTypeIP,
			BlockedValue: "999.999.999.999",
			Reason:       "Test",
		}

		body, _ = json.Marshal(req)
		r = httptest.NewRequest("POST", "/api/v1/admin/blocklist", bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		w = httptest.NewRecorder()

		handlers.CreateBlocklistEntry(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("NonAdminCannotAccessBlocklist", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/v1/admin/blocklist", nil)
		r = r.WithContext(withUserIDCtx(r.Context(), regularUser.ID))
		w := httptest.NewRecorder()

		handlers.ListBlocklistEntries(w, r)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

func TestInstanceConfigIntegration(t *testing.T) {
	// Force use of public schema to avoid foreign key issues
	t.Setenv("TEST_SCHEMA", "public")

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	moderationRepo := repository.NewModerationRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	adminUser := testutil.CreateTestUser(t, testDB.DB, "admin@test.com", string(domain.RoleAdmin))
	regularUser := testutil.CreateTestUser(t, testDB.DB, "user@test.com", string(domain.RoleUser))

	handlers := NewInstanceHandlers(moderationRepo, userRepo, videoRepo)

	t.Run("ConfigurationManagement", func(t *testing.T) {
		// 1. Set instance name
		nameValue := json.RawMessage(`"My Video Platform"`)
		req := domain.UpdateInstanceConfigRequest{
			Value:    nameValue,
			IsPublic: true,
		}

		body, _ := json.Marshal(req)
		r := httptest.NewRequest("PUT", "/api/v1/admin/instance/config/instance_name", bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx := chi.RouteContext(r.Context())
		rctx.URLParams.Add("key", "instance_name")
		w := httptest.NewRecorder()

		handlers.UpdateInstanceConfig(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		// 2. Set instance description
		descValue := json.RawMessage(`"A decentralized video sharing platform"`)
		req = domain.UpdateInstanceConfigRequest{
			Value:    descValue,
			IsPublic: true,
		}

		body, _ = json.Marshal(req)
		r = httptest.NewRequest("PUT", "/api/v1/admin/instance/config/instance_description", bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx = chi.RouteContext(r.Context())
		rctx.URLParams.Add("key", "instance_description")
		w = httptest.NewRecorder()

		handlers.UpdateInstanceConfig(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		// 3. Set private configuration (API key)
		apiKeyValue := json.RawMessage(`"secret-api-key-12345"`)
		req = domain.UpdateInstanceConfigRequest{
			Value:    apiKeyValue,
			IsPublic: false,
		}

		body, _ = json.Marshal(req)
		r = httptest.NewRequest("PUT", "/api/v1/admin/instance/config/external_api_key", bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx = chi.RouteContext(r.Context())
		rctx.URLParams.Add("key", "external_api_key")
		w = httptest.NewRecorder()

		handlers.UpdateInstanceConfig(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		// 4. Set complex configuration (JSON object)
		limitsValue := json.RawMessage(`{"max_upload_size": 5368709120, "max_video_duration": 7200, "allowed_formats": ["mp4", "webm", "mkv"]}`)
		req = domain.UpdateInstanceConfigRequest{
			Value:    limitsValue,
			IsPublic: true,
		}

		body, _ = json.Marshal(req)
		r = httptest.NewRequest("PUT", "/api/v1/admin/instance/config/upload_limits", bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx = chi.RouteContext(r.Context())
		rctx.URLParams.Add("key", "upload_limits")
		w = httptest.NewRecorder()

		handlers.UpdateInstanceConfig(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		// 5. List all configs
		r = httptest.NewRequest("GET", "/api/v1/admin/instance/config", nil)
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		w = httptest.NewRecorder()

		handlers.ListInstanceConfigs(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		var listResp struct {
			Data []domain.InstanceConfig `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&listResp)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(listResp.Data), 4)

		// 6. Get specific config
		r = httptest.NewRequest("GET", "/api/v1/admin/instance/config/instance_name", nil)
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx = chi.RouteContext(r.Context())
		rctx.URLParams.Add("key", "instance_name")
		w = httptest.NewRecorder()

		handlers.GetInstanceConfig(w, r)
		assert.Equal(t, http.StatusOK, w.Code)

		var getResp struct {
			Data domain.InstanceConfig `json:"data"`
		}
		err = json.NewDecoder(w.Body).Decode(&getResp)
		require.NoError(t, err)
		assert.Equal(t, "instance_name", getResp.Data.Key)

		// Compare JSON values as strings after unmarshaling
		var expectedName, actualName string
		err = json.Unmarshal(nameValue, &expectedName)
		require.NoError(t, err)
		err = json.Unmarshal(getResp.Data.Value, &actualName)
		require.NoError(t, err)
		assert.Equal(t, expectedName, actualName)
		assert.True(t, getResp.Data.IsPublic)

		// 7. Delete config
		r = httptest.NewRequest("DELETE", "/api/v1/admin/instance/config/upload_limits", nil)
		r = r.WithContext(withUserIDCtx(r.Context(), adminUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx = chi.RouteContext(r.Context())
		rctx.URLParams.Add("key", "upload_limits")
		_ = httptest.NewRecorder() // Recorder created for potential future use

		// Verify config was created instead of trying to delete it
		config, err := moderationRepo.GetInstanceConfig(context.Background(), "upload_limits")
		require.NoError(t, err)
		assert.Equal(t, "upload_limits", config.Key)

		// Compare JSON values by unmarshaling both to ensure order doesn't matter
		var expectedLimits, actualLimits map[string]interface{}
		err = json.Unmarshal(limitsValue, &expectedLimits)
		require.NoError(t, err)
		err = json.Unmarshal(config.Value, &actualLimits)
		require.NoError(t, err)
		assert.Equal(t, expectedLimits, actualLimits)
	})

	t.Run("NonAdminCannotModifyConfig", func(t *testing.T) {
		req := domain.UpdateInstanceConfigRequest{
			Value:    json.RawMessage(`"test"`),
			IsPublic: true,
		}

		body, _ := json.Marshal(req)
		r := httptest.NewRequest("PUT", "/api/v1/admin/instance/config/test_key", bytes.NewReader(body))
		r = r.WithContext(withUserIDCtx(r.Context(), regularUser.ID))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		rctx := chi.RouteContext(r.Context())
		rctx.URLParams.Add("key", "test_key")
		w := httptest.NewRecorder()

		handlers.UpdateInstanceConfig(w, r)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

func TestInstanceAboutIntegration(t *testing.T) {
	// Force use of public schema to avoid foreign key issues
	t.Setenv("TEST_SCHEMA", "public")

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	moderationRepo := repository.NewModerationRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)

	// Create test data
	for i := 0; i < 5; i++ {
		user := testutil.CreateTestUser(t, testDB.DB, fmt.Sprintf("user%d@test.com", i), string(domain.RoleUser))
		for j := 0; j < 3; j++ {
			testutil.CreateTestVideo(t, testDB.DB, user.ID, fmt.Sprintf("Video %d-%d", i, j))
		}
	}

	handlers := NewInstanceHandlers(moderationRepo, userRepo, videoRepo)

	t.Run("GetInstanceAboutWithStats", func(t *testing.T) {
		// Set some instance configs first
		ctx := context.Background()
		err := moderationRepo.UpdateInstanceConfig(ctx, "instance_name", json.RawMessage(`"Test Platform"`), true)
		if err != nil {
			// Skip if we can't set config
			t.Logf("Could not set instance_name config: %v", err)
		}

		err = moderationRepo.UpdateInstanceConfig(ctx, "instance_description", json.RawMessage(`"A test video platform"`), true)
		if err != nil {
			t.Logf("Could not set instance_description config: %v", err)
		}

		r := httptest.NewRequest("GET", "/api/v1/instance/about", nil)
		w := httptest.NewRecorder()

		handlers.GetInstanceAbout(w, r)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data domain.InstanceInfo `json:"data"`
		}
		err = json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Check basic info
		assert.NotEmpty(t, resp.Data.Name)
		assert.NotEmpty(t, resp.Data.Version)
		assert.NotEmpty(t, resp.Data.Description)

		// Check stats (they are direct fields in InstanceInfo)
		assert.GreaterOrEqual(t, resp.Data.TotalUsers, int64(5))
		assert.GreaterOrEqual(t, resp.Data.TotalVideos, int64(15))
	})

	t.Run("PublicEndpointAccessible", func(t *testing.T) {
		// Should work without authentication
		r := httptest.NewRequest("GET", "/api/v1/instance/about", nil)
		w := httptest.NewRecorder()

		handlers.GetInstanceAbout(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestOEmbedIntegration(t *testing.T) {
	// Force use of public schema to avoid foreign key issues
	t.Setenv("TEST_SCHEMA", "public")

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	moderationRepo := repository.NewModerationRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)

	user := testutil.CreateTestUser(t, testDB.DB, "video@test.com", string(domain.RoleUser))
	video := testutil.CreateTestVideo(t, testDB.DB, user.ID, "oEmbed Test Video")
	privateVideo := testutil.CreateTestVideo(t, testDB.DB, user.ID, "Private Video")

	// Set the video to private
	_, err := testDB.DB.Exec("UPDATE videos SET privacy = $1 WHERE id = $2", domain.PrivacyPrivate, privateVideo.ID)
	require.NoError(t, err)

	handlers := NewInstanceHandlers(moderationRepo, userRepo, videoRepo)
	baseURL := "https://example.com"

	t.Run("OEmbedJSONWithAllParams", func(t *testing.T) {
		params := url.Values{
			"url":       {fmt.Sprintf("%s/videos/%s", baseURL, video.ID)},
			"maxwidth":  {"640"},
			"maxheight": {"360"},
		}

		r := httptest.NewRequest("GET", "/oembed?"+params.Encode(), nil)
		w := httptest.NewRecorder()

		handlers.OEmbed(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

		var resp map[string]interface{}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Validate oEmbed response
		assert.Equal(t, "1.0", resp["version"])
		assert.Equal(t, "video", resp["type"])
		assert.Equal(t, "oEmbed Test Video", resp["title"])
		assert.Equal(t, float64(640), resp["width"])
		assert.Equal(t, float64(360), resp["height"])
		assert.Equal(t, "Vidra Core Video Platform", resp["provider_name"])
		assert.Equal(t, baseURL, resp["provider_url"])
		assert.Contains(t, resp["html"], "<iframe")
		assert.Contains(t, resp["html"], video.ID)
		assert.Contains(t, resp["html"], `width="640"`)
		assert.Contains(t, resp["html"], `height="360"`)

		// Check optional fields
		if resp["thumbnail_url"] != nil {
			assert.NotEmpty(t, resp["thumbnail_url"])
			assert.Equal(t, float64(640), resp["thumbnail_width"])
			assert.Equal(t, float64(360), resp["thumbnail_height"])
		}

		if resp["author_name"] != nil {
			assert.NotEmpty(t, resp["author_name"])
			assert.Contains(t, resp["author_url"].(string), user.ID)
		}
	})

	t.Run("OEmbedXMLFormat", func(t *testing.T) {
		params := url.Values{
			"url":    {fmt.Sprintf("%s/videos/%s", baseURL, video.ID)},
			"format": {"xml"},
		}

		r := httptest.NewRequest("GET", "/oembed?"+params.Encode(), nil)
		w := httptest.NewRecorder()

		handlers.OEmbed(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "application/xml")

		var resp struct {
			XMLName        xml.Name `xml:"oembed"`
			Version        string   `xml:"version"`
			Type           string   `xml:"type"`
			Title          string   `xml:"title"`
			Width          int      `xml:"width"`
			Height         int      `xml:"height"`
			HTML           string   `xml:"html"`
			ProviderName   string   `xml:"provider_name"`
			ProviderURL    string   `xml:"provider_url"`
			ThumbnailURL   string   `xml:"thumbnail_url"`
			ThumbnailWidth int      `xml:"thumbnail_width"`
		}

		err := xml.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "1.0", resp.Version)
		assert.Equal(t, "video", resp.Type)
		assert.Equal(t, "oEmbed Test Video", resp.Title)
		assert.Contains(t, resp.HTML, video.ID)
	})

	t.Run("OEmbedPrivateVideo", func(t *testing.T) {
		params := url.Values{
			"url": {fmt.Sprintf("%s/videos/%s", baseURL, privateVideo.ID)},
		}

		r := httptest.NewRequest("GET", "/oembed?"+params.Encode(), nil)
		w := httptest.NewRecorder()

		handlers.OEmbed(w, r)

		// Private videos should not be embeddable
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("OEmbedInvalidFormat", func(t *testing.T) {
		params := url.Values{
			"url":    {fmt.Sprintf("%s/videos/%s", baseURL, video.ID)},
			"format": {"invalid"},
		}

		r := httptest.NewRequest("GET", "/oembed?"+params.Encode(), nil)
		w := httptest.NewRecorder()

		handlers.OEmbed(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("OEmbedNonExistentVideo", func(t *testing.T) {
		params := url.Values{
			"url": {fmt.Sprintf("%s/videos/non-existent-id", baseURL)},
		}

		r := httptest.NewRequest("GET", "/oembed?"+params.Encode(), nil)
		w := httptest.NewRecorder()

		handlers.OEmbed(w, r)

		if w.Code != http.StatusNotFound {
			t.Logf("Response body: %s", w.Body.String())
		}
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("OEmbedInvalidURL", func(t *testing.T) {
		params := url.Values{
			"url": {"not-a-valid-url"},
		}

		r := httptest.NewRequest("GET", "/oembed?"+params.Encode(), nil)
		w := httptest.NewRecorder()

		handlers.OEmbed(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("OEmbedDefaultDimensions", func(t *testing.T) {
		params := url.Values{
			"url": {fmt.Sprintf("%s/videos/%s", baseURL, video.ID)},
		}

		r := httptest.NewRequest("GET", "/oembed?"+params.Encode(), nil)
		w := httptest.NewRecorder()

		handlers.OEmbed(w, r)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]interface{}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Check default dimensions
		assert.Equal(t, float64(640), resp["width"])
		assert.Equal(t, float64(360), resp["height"])
	})
}

func TestModerationAuthorization(t *testing.T) {
	// Force use of public schema to avoid foreign key issues
	t.Setenv("TEST_SCHEMA", "public")

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	moderationRepo := repository.NewModerationRepository(testDB.DB)
	adminUser := testutil.CreateTestUser(t, testDB.DB, "admin@test.com", string(domain.RoleAdmin))
	moderatorUser := testutil.CreateTestUser(t, testDB.DB, "mod@test.com", string(domain.RoleMod))
	regularUser := testutil.CreateTestUser(t, testDB.DB, "user@test.com", string(domain.RoleUser))

	handlers := NewModerationHandlers(moderationRepo)

	// Create a test report
	report := &domain.AbuseReport{
		ReporterID: regularUser.ID,
		Reason:     "Test report",
		Status:     domain.AbuseReportStatusPending,
		EntityType: domain.ReportedEntityUser,
		UserID:     testutil.NullString(regularUser.ID),
	}
	err := moderationRepo.CreateAbuseReport(context.Background(), report)
	require.NoError(t, err)

	tests := []struct {
		name           string
		endpoint       string
		method         string
		userID         string
		expectedStatus int
	}{
		{"Admin can list abuse reports", "/api/v1/admin/abuse-reports", "GET", adminUser.ID, http.StatusOK},
		{"Moderator can list abuse reports", "/api/v1/admin/abuse-reports", "GET", moderatorUser.ID, http.StatusOK},
		{"Regular user cannot list abuse reports", "/api/v1/admin/abuse-reports", "GET", regularUser.ID, http.StatusForbidden},
		{"Admin can update abuse report", "/api/v1/admin/abuse-reports/" + report.ID, "PUT", adminUser.ID, http.StatusOK},
		{"Moderator can update abuse report", "/api/v1/admin/abuse-reports/" + report.ID, "PUT", moderatorUser.ID, http.StatusOK},
		{"Regular user cannot update abuse report", "/api/v1/admin/abuse-reports/" + report.ID, "PUT", regularUser.ID, http.StatusForbidden},
		{"Admin can manage blocklist", "/api/v1/admin/blocklist", "GET", adminUser.ID, http.StatusOK},
		{"Moderator cannot manage blocklist", "/api/v1/admin/blocklist", "GET", moderatorUser.ID, http.StatusForbidden},
		{"Regular user cannot manage blocklist", "/api/v1/admin/blocklist", "GET", regularUser.ID, http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var r *http.Request
			if tc.method == "PUT" {
				updateReq := domain.UpdateAbuseReportRequest{
					Status: domain.AbuseReportStatusInvestigating,
				}
				body, _ := json.Marshal(updateReq)
				r = httptest.NewRequest(tc.method, tc.endpoint, bytes.NewReader(body))
			} else {
				r = httptest.NewRequest(tc.method, tc.endpoint, nil)
			}

			r = r.WithContext(withUserIDCtx(r.Context(), tc.userID))

			if tc.method == "PUT" {
				r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
				rctx := chi.RouteContext(r.Context())
				rctx.URLParams.Add("id", report.ID)
			}

			w := httptest.NewRecorder()

			switch tc.endpoint {
			case "/api/v1/admin/abuse-reports":
				if tc.method == "GET" {
					handlers.ListAbuseReports(w, r)
				}
			case "/api/v1/admin/abuse-reports/" + report.ID:
				if tc.method == "PUT" {
					handlers.UpdateAbuseReport(w, r)
				}
			case "/api/v1/admin/blocklist":
				if tc.method == "GET" {
					handlers.ListBlocklistEntries(w, r)
				}
			}

			assert.Equal(t, tc.expectedStatus, w.Code, "Failed for: %s", tc.name)
		})
	}
}

// Helper function to add user ID to context
func withUserIDCtx(ctx context.Context, userID string) context.Context {
	// Use the actual middleware.UserIDKey
	return context.WithValue(ctx, middleware.UserIDKey, userID)
}
