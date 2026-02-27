package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/usecase"
)

// AdminUserHandlers handles admin user management HTTP requests.
type AdminUserHandlers struct {
	userRepo usecase.UserRepository
}

// NewAdminUserHandlers creates a new AdminUserHandlers.
func NewAdminUserHandlers(userRepo usecase.UserRepository) *AdminUserHandlers {
	return &AdminUserHandlers{userRepo: userRepo}
}

// ListUsers handles GET /api/v1/admin/users
// Supports ?limit=20&offset=0&search=term query params.
// Search filters in-memory; Total reflects filtered count for correct pagination.
func (h *AdminUserHandlers) ListUsers(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))

	if search != "" {
		// Fetch users and filter in-memory so Total is based on filtered count.
		// Cap at 10000 to avoid unbounded memory usage.
		all, err := h.userRepo.List(r.Context(), 10000, 0)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list users"))
			return
		}

		var filtered []*domain.User
		for _, u := range all {
			if strings.Contains(strings.ToLower(u.Username), search) ||
				strings.Contains(strings.ToLower(u.Email), search) {
				filtered = append(filtered, u)
			}
		}

		total := int64(len(filtered))

		// Apply pagination on the filtered results
		if offset >= len(filtered) {
			shared.WriteJSONWithMeta(w, http.StatusOK, []*domain.User{}, &shared.Meta{
				Total:  total,
				Limit:  limit,
				Offset: offset,
			})
			return
		}
		end := offset + limit
		if end > len(filtered) {
			end = len(filtered)
		}

		shared.WriteJSONWithMeta(w, http.StatusOK, filtered[offset:end], &shared.Meta{
			Total:  total,
			Limit:  limit,
			Offset: offset,
		})
		return
	}

	// No search: use paginated List + Count for efficiency.
	users, err := h.userRepo.List(r.Context(), limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list users"))
		return
	}
	count, err := h.userRepo.Count(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to count users"))
		return
	}

	shared.WriteJSONWithMeta(w, http.StatusOK, users, &shared.Meta{
		Total:  count,
		Limit:  limit,
		Offset: offset,
	})
}

// updateUserRequest is the request body for UpdateUser.
type updateUserRequest struct {
	Role     *string `json:"role"`
	IsActive *bool   `json:"is_active"`
}

// validRoles is the set of valid role values.
var validRoles = map[string]domain.UserRole{
	"user":      domain.RoleUser,
	"admin":     domain.RoleAdmin,
	"moderator": domain.RoleMod,
}

// UpdateUser handles PUT /api/v1/admin/users/{id}
// Supports role changes and ban/unban (is_active flag).
func (h *AdminUserHandlers) UpdateUser(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "id")
	if targetID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_USER_ID", "User ID is required"))
		return
	}

	callerID, _ := r.Context().Value(middleware.UserIDKey).(string)

	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	// Validate role if provided.
	var newRole *domain.UserRole
	if req.Role != nil {
		role, ok := validRoles[strings.ToLower(*req.Role)]
		if !ok {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_ROLE", "Role must be one of: user, moderator, admin"))
			return
		}
		// Prevent admin from changing their own role.
		if callerID == targetID {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("SELF_DEMOTION", "Admins cannot change their own role"))
			return
		}
		newRole = &role
	}

	user, err := h.userRepo.GetByID(r.Context(), targetID)
	if err != nil {
		if err == domain.ErrUserNotFound {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("USER_NOT_FOUND", "User not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to fetch user"))
		return
	}

	if newRole != nil {
		// Last-admin protection: don't allow demoting the last admin.
		if user.Role == domain.RoleAdmin && *newRole != domain.RoleAdmin {
			all, listErr := h.userRepo.List(r.Context(), 10000, 0)
			if listErr != nil {
				shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to verify admin count"))
				return
			}
			adminCount := 0
			for _, u := range all {
				if u.Role == domain.RoleAdmin && u.IsActive {
					adminCount++
				}
			}
			if adminCount <= 1 {
				shared.WriteError(w, http.StatusConflict, domain.NewDomainError("LAST_ADMIN_PROTECTION", "Cannot demote the last admin"))
				return
			}
		}
		user.Role = *newRole
	}

	if req.IsActive != nil {
		user.IsActive = *req.IsActive
	}

	if err := h.userRepo.Update(r.Context(), user); err != nil {
		if err == domain.ErrUserNotFound {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("USER_NOT_FOUND", "User not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update user"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, user)
}
