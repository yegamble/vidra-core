package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTest creates the handler and mock db
func setupTest(t *testing.T) (*InstanceHandlers, *sql.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	sqlxDB := sqlx.NewDb(db, "postgres")
	modRepo := repository.NewModerationRepository(sqlxDB)

	// We can use the mocks defined in security_test.go for other repos
	// as they are in the same package
	userRepo := &MockUserRepo{}
	videoRepo := &MockVideoRepo{}

	h := NewInstanceHandlers(modRepo, userRepo, videoRepo)
	return h, db, mock
}

type JSONResponse struct {
	Data    interface{} `json:"data"`
	Success bool        `json:"success"`
	Error   interface{} `json:"error,omitempty"`
}

func TestUpdateInstanceConfig(t *testing.T) {
	type testCase struct {
		name           string
		key            string
		role           domain.UserRole
		requestBody    interface{} // string or struct
		mockSetup      func(mock sqlmock.Sqlmock)
		expectedStatus int
		expectedSuccess bool
	}

	validBody := domain.UpdateInstanceConfigRequest{
		Value:    json.RawMessage(`"My Awesome Site"`),
		IsPublic: true,
	}

	testCases := []testCase{
		{
			name:        "Success",
			key:         "site_name",
			role:        domain.RoleAdmin,
			requestBody: validBody,
			mockSetup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO instance_config").
					WithArgs("site_name", validBody.Value, validBody.IsPublic).
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
			expectedStatus: http.StatusOK,
			expectedSuccess: true,
		},
		{
			name:        "Forbidden",
			key:         "site_name",
			role:        domain.RoleUser,
			requestBody: validBody,
			mockSetup:   func(mock sqlmock.Sqlmock) {}, // No DB calls
			expectedStatus: http.StatusForbidden,
			expectedSuccess: false,
		},
		{
			name:        "InvalidBody",
			key:         "site_name",
			role:        domain.RoleAdmin,
			requestBody: "{invalid_json",
			mockSetup:   func(mock sqlmock.Sqlmock) {},
			expectedStatus: http.StatusBadRequest,
			expectedSuccess: false,
		},
		{
			name:        "RepoError",
			key:         "site_name",
			role:        domain.RoleAdmin,
			requestBody: validBody,
			mockSetup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO instance_config").
					WillReturnError(domain.NewDomainError("DB_ERROR", "database error"))
			},
			expectedStatus: http.StatusInternalServerError,
			expectedSuccess: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h, db, mock := setupTest(t)
			defer db.Close()

			tc.mockSetup(mock)

			var body []byte
			if s, ok := tc.requestBody.(string); ok {
				body = []byte(s)
			} else {
				body, _ = json.Marshal(tc.requestBody)
			}

			req, _ := http.NewRequest("PUT", "/api/v1/admin/instance/config/"+tc.key, bytes.NewBuffer(body))

			ctx := context.WithValue(req.Context(), middleware.UserRoleKey, string(tc.role))
			ctx = context.WithValue(ctx, middleware.UserIDKey, "user-id")

			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("key", tc.key)
			ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)

			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			h.UpdateInstanceConfig(rr, req)

			assert.Equal(t, tc.expectedStatus, rr.Code)

			if tc.expectedSuccess {
				var resp JSONResponse
				err := json.Unmarshal(rr.Body.Bytes(), &resp)
				assert.NoError(t, err)
				assert.True(t, resp.Success)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestListInstanceConfigs_Success(t *testing.T) {
	h, db, mock := setupTest(t)
	defer db.Close()

	req, _ := http.NewRequest("GET", "/api/v1/admin/instance/config", nil)
	ctx := context.WithValue(req.Context(), middleware.UserRoleKey, string(domain.RoleAdmin))
	ctx = context.WithValue(ctx, middleware.UserIDKey, "admin-id")
	req = req.WithContext(ctx)

	now := time.Now()
	rows := sqlmock.NewRows([]string{"key", "value", "description", "is_public", "created_at", "updated_at"}).
		AddRow("k1", []byte(`"v1"`), "d1", true, now, now).
		AddRow("k2", []byte(`"v2"`), "d2", false, now, now)

	// Use regex matching for whitespace
	mock.ExpectQuery(`SELECT[\s\S]*FROM instance_config[\s\S]*ORDER BY key`).
		WillReturnRows(rows)

	rr := httptest.NewRecorder()
	h.ListInstanceConfigs(rr, req)

	if rr.Code != http.StatusOK {
		t.Logf("Response body: %s", rr.Body.String())
	}
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Data    []domain.InstanceConfig `json:"data"`
		Success bool                    `json:"success"`
	}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Len(t, resp.Data, 2)
	assert.Equal(t, "k1", resp.Data[0].Key)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListInstanceConfigs_Forbidden(t *testing.T) {
	h, db, mock := setupTest(t)
	defer db.Close()

	req, _ := http.NewRequest("GET", "/api/v1/admin/instance/config", nil)
	// User role (not admin)
	ctx := context.WithValue(req.Context(), middleware.UserRoleKey, string(domain.RoleUser))
	ctx = context.WithValue(ctx, middleware.UserIDKey, "user-id")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ListInstanceConfigs(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetInstanceConfig(t *testing.T) {
	type testCase struct {
		name           string
		key            string
		role           domain.UserRole
		mockSetup      func(mock sqlmock.Sqlmock)
		expectedStatus int
	}

	now := time.Now()

	testCases := []testCase{
		{
			name: "Success",
			key:  "site_name",
			role: domain.RoleAdmin,
			mockSetup: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"key", "value", "description", "is_public", "created_at", "updated_at"}).
					AddRow("site_name", []byte(`"My Site"`), "desc", true, now, now)
				mock.ExpectQuery(`SELECT[\s\S]*FROM instance_config[\s\S]*WHERE key = \$1`).
					WithArgs("site_name").
					WillReturnRows(rows)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "NotFound",
			key:  "unknown",
			role: domain.RoleAdmin,
			mockSetup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`SELECT[\s\S]*FROM instance_config[\s\S]*WHERE key = \$1`).
					WithArgs("unknown").
					WillReturnError(sql.ErrNoRows)
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name: "Forbidden",
			key:  "site_name",
			role: domain.RoleUser,
			mockSetup: func(mock sqlmock.Sqlmock) {},
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h, db, mock := setupTest(t)
			defer db.Close()

			tc.mockSetup(mock)

			req, _ := http.NewRequest("GET", "/api/v1/admin/instance/config/"+tc.key, nil)

			ctx := context.WithValue(req.Context(), middleware.UserRoleKey, string(tc.role))
			ctx = context.WithValue(ctx, middleware.UserIDKey, "user-id")

			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("key", tc.key)
			ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)

			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			h.GetInstanceConfig(rr, req)

			assert.Equal(t, tc.expectedStatus, rr.Code)

			if tc.expectedStatus == http.StatusOK {
				var resp struct {
					Data    domain.InstanceConfig `json:"data"`
					Success bool                  `json:"success"`
				}
				err := json.Unmarshal(rr.Body.Bytes(), &resp)
				assert.NoError(t, err)
				assert.True(t, resp.Success)
				assert.Equal(t, tc.key, resp.Data.Key)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
