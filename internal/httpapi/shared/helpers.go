package shared

import (
	"errors"
	"net/http"
	"strconv"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// GetBoolParam extracts a boolean query parameter with a default value
func GetBoolParam(r *http.Request, key string, defaultValue bool) bool {
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultValue
	}

	b, err := strconv.ParseBool(val)
	if err != nil {
		return defaultValue
	}

	return b
}

// GetIntParam extracts an integer query parameter with a default value
func GetIntParam(r *http.Request, key string, defaultValue int) int {
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultValue
	}

	n, err := strconv.Atoi(val)
	if err != nil {
		return defaultValue
	}

	return n
}

// ParsePagination extracts pagination parameters from the request with backward compatibility
// Supports both page/pageSize (modern) and limit/offset (legacy) parameters
// Returns: page, limit, offset, pageSize
func ParsePagination(r *http.Request, defaultPageSize int) (page, limit, offset, pageSize int) {
	// Parse all parameters
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ = strconv.Atoi(r.URL.Query().Get("pageSize"))
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))

	// Set pageSize with fallbacks
	if pageSize <= 0 || pageSize > 100 {
		if limit > 0 {
			pageSize = limit
		} else {
			pageSize = defaultPageSize
		}
	}

	// Calculate page from offset if not provided
	if page <= 0 {
		if offset < 0 {
			offset = 0
		}
		page = (offset / pageSize) + 1
		if page <= 0 {
			page = 1
		}
	}

	// Compute final limit/offset from page
	limit = pageSize
	offset = (page - 1) * pageSize

	return page, limit, offset, pageSize
}

// RequireUUIDParam extracts a path parameter and validates it as a UUID.
// On failure it writes an error response and returns ok=false.
func RequireUUIDParam(w http.ResponseWriter, r *http.Request, param, missingCode, invalidCode, missingMsg, invalidMsg string) (string, bool) {
	id := chi.URLParam(r, param)
	if id == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError(missingCode, missingMsg))
		return "", false
	}
	if _, err := uuid.Parse(id); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError(invalidCode, invalidMsg))
		return "", false
	}
	return id, true
}

// IsAdmin checks if a user has admin role
func IsAdmin(user *domain.User) bool {
	if user == nil {
		return false
	}
	return user.Role == domain.RoleAdmin
}

// IsModerator checks if a user has moderator role
func IsModerator(user *domain.User) bool {
	if user == nil {
		return false
	}
	return user.Role == domain.RoleMod
}

// IsAdminOrModerator checks if a user has admin or moderator role
func IsAdminOrModerator(user *domain.User) bool {
	return IsAdmin(user) || IsModerator(user)
}

// RequireAdminRole checks if user is admin, returns error if not
func RequireAdminRole(user *domain.User) error {
	if !IsAdmin(user) {
		return errors.New("admin role required")
	}
	return nil
}

// RequireModeratorRole checks if user is moderator or admin, returns error if not
func RequireModeratorRole(user *domain.User) error {
	if !IsAdminOrModerator(user) {
		return errors.New("moderator or admin role required")
	}
	return nil
}

// GetUserRoleFromContext retrieves the user role from request context
func GetUserRoleFromContext(r *http.Request) domain.UserRole {
	roleValue := r.Context().Value(middleware.UserRoleKey)
	if roleValue == nil {
		return domain.RoleUser // default to user role
	}

	// The role is stored as a string in the context
	roleStr, ok := roleValue.(string)
	if !ok {
		return domain.RoleUser
	}

	return domain.UserRole(roleStr)
}

// IsAdminFromContext checks if the request has admin role in context
func IsAdminFromContext(r *http.Request) bool {
	role := GetUserRoleFromContext(r)
	return role == domain.RoleAdmin
}

// IsModeratorFromContext checks if the request has moderator or admin role in context
func IsModeratorFromContext(r *http.Request) bool {
	role := GetUserRoleFromContext(r)
	return role == domain.RoleMod || role == domain.RoleAdmin
}
