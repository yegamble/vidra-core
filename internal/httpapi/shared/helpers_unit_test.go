package shared

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

func TestGetBoolParam(t *testing.T) {
	tests := []struct {
		name         string
		queryValue   string
		defaultValue bool
		expected     bool
	}{
		{
			name:         "true value",
			queryValue:   "true",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "false value",
			queryValue:   "false",
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "empty value returns default",
			queryValue:   "",
			defaultValue: true,
			expected:     true,
		},
		{
			name:         "invalid value returns default",
			queryValue:   "invalid",
			defaultValue: false,
			expected:     false,
		},
		{
			name:         "1 as true",
			queryValue:   "1",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "0 as false",
			queryValue:   "0",
			defaultValue: true,
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/?key="+tt.queryValue, nil)
			result := GetBoolParam(req, "key", tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetIntParam(t *testing.T) {
	tests := []struct {
		name         string
		queryValue   string
		defaultValue int
		expected     int
	}{
		{
			name:         "valid integer",
			queryValue:   "42",
			defaultValue: 10,
			expected:     42,
		},
		{
			name:         "negative integer",
			queryValue:   "-5",
			defaultValue: 0,
			expected:     -5,
		},
		{
			name:         "empty value returns default",
			queryValue:   "",
			defaultValue: 100,
			expected:     100,
		},
		{
			name:         "invalid value returns default",
			queryValue:   "abc",
			defaultValue: 50,
			expected:     50,
		},
		{
			name:         "zero value",
			queryValue:   "0",
			defaultValue: 10,
			expected:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/?key="+tt.queryValue, nil)
			result := GetIntParam(req, "key", tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParsePagination(t *testing.T) {
	tests := []struct {
		name             string
		queryParams      string
		defaultPageSize  int
		expectedPage     int
		expectedLimit    int
		expectedOffset   int
		expectedPageSize int
	}{
		{
			name:             "modern params - page and pageSize",
			queryParams:      "?page=2&pageSize=20",
			defaultPageSize:  10,
			expectedPage:     2,
			expectedLimit:    20,
			expectedOffset:   20,
			expectedPageSize: 20,
		},
		{
			name:             "legacy params - limit and offset",
			queryParams:      "?limit=15&offset=30",
			defaultPageSize:  10,
			expectedPage:     3,
			expectedLimit:    15,
			expectedOffset:   30,
			expectedPageSize: 15,
		},
		{
			name:             "no params - use defaults",
			queryParams:      "",
			defaultPageSize:  25,
			expectedPage:     1,
			expectedLimit:    25,
			expectedOffset:   0,
			expectedPageSize: 25,
		},
		{
			name:             "page only - use default pageSize",
			queryParams:      "?page=3",
			defaultPageSize:  10,
			expectedPage:     3,
			expectedLimit:    10,
			expectedOffset:   20,
			expectedPageSize: 10,
		},
		{
			name:             "pageSize exceeds max (100)",
			queryParams:      "?page=1&pageSize=150",
			defaultPageSize:  10,
			expectedPage:     1,
			expectedLimit:    10,
			expectedOffset:   0,
			expectedPageSize: 10,
		},
		{
			name:             "negative offset normalized to 0",
			queryParams:      "?offset=-10",
			defaultPageSize:  10,
			expectedPage:     1,
			expectedLimit:    10,
			expectedOffset:   0,
			expectedPageSize: 10,
		},
		{
			name:             "offset-based calculation",
			queryParams:      "?offset=50&limit=25",
			defaultPageSize:  10,
			expectedPage:     3,
			expectedLimit:    25,
			expectedOffset:   50,
			expectedPageSize: 25,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/"+tt.queryParams, nil)
			page, limit, offset, pageSize := ParsePagination(req, tt.defaultPageSize)

			assert.Equal(t, tt.expectedPage, page, "page mismatch")
			assert.Equal(t, tt.expectedLimit, limit, "limit mismatch")
			assert.Equal(t, tt.expectedOffset, offset, "offset mismatch")
			assert.Equal(t, tt.expectedPageSize, pageSize, "pageSize mismatch")
		})
	}
}

func TestRequireUUIDParam(t *testing.T) {
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	invalidUUID := "not-a-uuid"

	tests := []struct {
		name           string
		paramValue     string
		expectedID     string
		expectedOK     bool
		expectedStatus int
	}{
		{
			name:           "valid UUID",
			paramValue:     validUUID,
			expectedID:     validUUID,
			expectedOK:     true,
			expectedStatus: 0,
		},
		{
			name:           "invalid UUID format",
			paramValue:     invalidUUID,
			expectedID:     "",
			expectedOK:     false,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "empty UUID",
			paramValue:     "",
			expectedID:     "",
			expectedOK:     false,
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/resource/"+tt.paramValue, nil)

			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tt.paramValue)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			id, ok := RequireUUIDParam(rec, req, "id", "MISSING_ID", "INVALID_ID", "ID is required", "Invalid ID format")

			assert.Equal(t, tt.expectedID, id)
			assert.Equal(t, tt.expectedOK, ok)

			if !tt.expectedOK {
				assert.Equal(t, tt.expectedStatus, rec.Code)
			}
		})
	}
}

func TestIsAdmin(t *testing.T) {
	tests := []struct {
		name     string
		user     *domain.User
		expected bool
	}{
		{
			name:     "nil user",
			user:     nil,
			expected: false,
		},
		{
			name: "admin user",
			user: &domain.User{
				Role: domain.RoleAdmin,
			},
			expected: true,
		},
		{
			name: "regular user",
			user: &domain.User{
				Role: domain.RoleUser,
			},
			expected: false,
		},
		{
			name: "moderator user",
			user: &domain.User{
				Role: domain.RoleMod,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAdmin(tt.user)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsModerator(t *testing.T) {
	tests := []struct {
		name     string
		user     *domain.User
		expected bool
	}{
		{
			name:     "nil user",
			user:     nil,
			expected: false,
		},
		{
			name: "moderator user",
			user: &domain.User{
				Role: domain.RoleMod,
			},
			expected: true,
		},
		{
			name: "admin user",
			user: &domain.User{
				Role: domain.RoleAdmin,
			},
			expected: false,
		},
		{
			name: "regular user",
			user: &domain.User{
				Role: domain.RoleUser,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsModerator(tt.user)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsAdminOrModerator(t *testing.T) {
	tests := []struct {
		name     string
		user     *domain.User
		expected bool
	}{
		{
			name:     "nil user",
			user:     nil,
			expected: false,
		},
		{
			name: "admin user",
			user: &domain.User{
				Role: domain.RoleAdmin,
			},
			expected: true,
		},
		{
			name: "moderator user",
			user: &domain.User{
				Role: domain.RoleMod,
			},
			expected: true,
		},
		{
			name: "regular user",
			user: &domain.User{
				Role: domain.RoleUser,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAdminOrModerator(tt.user)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRequireAdminRole(t *testing.T) {
	tests := []struct {
		name      string
		user      *domain.User
		expectErr bool
	}{
		{
			name: "admin user - no error",
			user: &domain.User{
				Role: domain.RoleAdmin,
			},
			expectErr: false,
		},
		{
			name: "regular user - error",
			user: &domain.User{
				Role: domain.RoleUser,
			},
			expectErr: true,
		},
		{
			name: "moderator user - error",
			user: &domain.User{
				Role: domain.RoleMod,
			},
			expectErr: true,
		},
		{
			name:      "nil user - error",
			user:      nil,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RequireAdminRole(tt.user)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRequireModeratorRole(t *testing.T) {
	tests := []struct {
		name      string
		user      *domain.User
		expectErr bool
	}{
		{
			name: "admin user - no error",
			user: &domain.User{
				Role: domain.RoleAdmin,
			},
			expectErr: false,
		},
		{
			name: "moderator user - no error",
			user: &domain.User{
				Role: domain.RoleMod,
			},
			expectErr: false,
		},
		{
			name: "regular user - error",
			user: &domain.User{
				Role: domain.RoleUser,
			},
			expectErr: true,
		},
		{
			name:      "nil user - error",
			user:      nil,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RequireModeratorRole(tt.user)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetUserRoleFromContext(t *testing.T) {
	tests := []struct {
		name         string
		contextValue interface{}
		expected     domain.UserRole
	}{
		{
			name:         "admin role in context",
			contextValue: string(domain.RoleAdmin),
			expected:     domain.RoleAdmin,
		},
		{
			name:         "moderator role in context",
			contextValue: string(domain.RoleMod),
			expected:     domain.RoleMod,
		},
		{
			name:         "user role in context",
			contextValue: string(domain.RoleUser),
			expected:     domain.RoleUser,
		},
		{
			name:         "no role in context - default to user",
			contextValue: nil,
			expected:     domain.RoleUser,
		},
		{
			name:         "invalid type in context - default to user",
			contextValue: 123,
			expected:     domain.RoleUser,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.contextValue != nil {
				ctx := context.WithValue(req.Context(), middleware.UserRoleKey, tt.contextValue)
				req = req.WithContext(ctx)
			}

			role := GetUserRoleFromContext(req)
			assert.Equal(t, tt.expected, role)
		})
	}
}

func TestIsAdminFromContext(t *testing.T) {
	tests := []struct {
		name     string
		role     domain.UserRole
		expected bool
	}{
		{
			name:     "admin role",
			role:     domain.RoleAdmin,
			expected: true,
		},
		{
			name:     "moderator role",
			role:     domain.RoleMod,
			expected: false,
		},
		{
			name:     "user role",
			role:     domain.RoleUser,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			ctx := context.WithValue(req.Context(), middleware.UserRoleKey, string(tt.role))
			req = req.WithContext(ctx)

			result := IsAdminFromContext(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsModeratorFromContext(t *testing.T) {
	tests := []struct {
		name     string
		role     domain.UserRole
		expected bool
	}{
		{
			name:     "admin role",
			role:     domain.RoleAdmin,
			expected: true,
		},
		{
			name:     "moderator role",
			role:     domain.RoleMod,
			expected: true,
		},
		{
			name:     "user role",
			role:     domain.RoleUser,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			ctx := context.WithValue(req.Context(), middleware.UserRoleKey, string(tt.role))
			req = req.WithContext(ctx)

			result := IsModeratorFromContext(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}
