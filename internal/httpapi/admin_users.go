package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/admin"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// adminUserView is the admin projection of an account. It deliberately omits the
// password hash and never carries any secret. StorageQuotaBytes is the per-user
// override (null = the instance default applies; 0 = unlimited);
// StorageUsedBytes is the account's current usage.
type adminUserView struct {
	ID                string    `json:"id"`
	Username          string    `json:"username"`
	Email             string    `json:"email"`
	Role              string    `json:"role"`
	IsActive          bool      `json:"is_active"`
	EmailVerified     bool      `json:"email_verified"`
	DisplayName       string    `json:"display_name"`
	StorageQuotaBytes *int64    `json:"storage_quota_bytes"`
	StorageUsedBytes  int64     `json:"storage_used_bytes"`
	CreatedAt         time.Time `json:"created_at"`
}

func newAdminUserView(u sqlcgen.User, usedBytes int64) adminUserView {
	return adminUserView{
		ID:                u.ID.String(),
		Username:          u.Username,
		Email:             u.Email,
		Role:              u.Role,
		IsActive:          u.IsActive,
		EmailVerified:     u.EmailVerified,
		DisplayName:       u.DisplayName,
		StorageQuotaBytes: u.StorageQuotaBytes,
		StorageUsedBytes:  usedBytes,
		CreatedAt:         u.CreatedAt,
	}
}

// newAdminUserViewFromRow builds the view from a ListUsers row (which carries
// the usage aggregate inline, so the list costs one query).
func newAdminUserViewFromRow(r sqlcgen.ListUsersRow) adminUserView {
	return newAdminUserView(sqlcgen.User{
		ID: r.ID, Username: r.Username, Email: r.Email, Role: r.Role,
		IsActive: r.IsActive, EmailVerified: r.EmailVerified,
		DisplayName: r.DisplayName, StorageQuotaBytes: r.StorageQuotaBytes,
		CreatedAt: r.CreatedAt,
	}, r.StorageUsedBytes)
}

// adminUserListResponse is the paginated admin user list.
type adminUserListResponse struct {
	Users  []adminUserView `json:"users"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

// handleListUsers returns accounts, newest first, optionally filtered by ?q
// (username/email substring). Behind requireRole(admin). Pagination via ?limit
// (1–100, default 20) and ?offset.
func (s *Server) handleListUsers(c echo.Context) error {
	query := strings.TrimSpace(c.QueryParam("q"))
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	users, err := s.adminsvc.ListUsers(c.Request().Context(), query, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]adminUserView, 0, len(users))
	for _, u := range users {
		views = append(views, newAdminUserViewFromRow(u))
	}
	return c.JSON(http.StatusOK, adminUserListResponse{Users: views, Limit: limit, Offset: offset})
}

// updateUserRequest is the PATCH /admin/users/{id} body. Fields are optional;
// only those present are changed. storage_quota_bytes is tri-state: absent =
// unchanged, null = reset to the instance default, a non-negative integer =
// per-user override (0 = unlimited) — hence the RawMessage, which preserves
// the absent/null distinction JSON pointers cannot. email_verified lets an
// admin mark an address confirmed without the token round-trip (or revoke it).
type updateUserRequest struct {
	Role              *string         `json:"role"`
	IsActive          *bool           `json:"is_active"`
	EmailVerified     *bool           `json:"email_verified"`
	StorageQuotaBytes json.RawMessage `json:"storage_quota_bytes"`
}

// quotaField decodes the tri-state storage_quota_bytes field: set=false when
// absent; (set=true, value=nil) for null (reset to instance default);
// (set=true, value=&n) for a non-negative integer. A malformed or negative
// value yields a field error.
func (r updateUserRequest) quotaField() (set bool, value *int64, fe *FieldError) {
	raw := bytes.TrimSpace(r.StorageQuotaBytes)
	if len(raw) == 0 {
		return false, nil, nil
	}
	if string(raw) == "null" {
		return true, nil, nil
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil || n < 0 {
		return true, nil, &FieldError{Field: "storage_quota_bytes", Message: "must be a non-negative integer (bytes; 0 = unlimited) or null"}
	}
	return true, &n, nil
}

func (r updateUserRequest) Validate() []FieldError {
	quotaSet, _, quotaErr := r.quotaField()
	if quotaErr != nil {
		return []FieldError{*quotaErr}
	}
	if r.Role == nil && r.IsActive == nil && r.EmailVerified == nil && !quotaSet {
		return []FieldError{{Field: "role", Message: "at least one of role, is_active, email_verified, storage_quota_bytes is required"}}
	}
	if r.Role != nil && !admin.ValidRole(*r.Role) {
		return []FieldError{{Field: "role", Message: "must be one of user, moderator, admin"}}
	}
	return nil
}

// handleUpdateUser edits a user's role, active flag, and/or storage quota.
// Behind requireRole(admin). Self-demotion/self-deactivation is rejected; an
// unknown id is 404. Emits an audit event.
func (s *Server) handleUpdateUser(c echo.Context) error {
	callerID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	var in updateUserRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	quotaSet, quotaValue, _ := in.quotaField() // already validated

	updated, err := s.adminsvc.UpdateUser(c.Request().Context(), callerID, targetID, admin.UpdateUserInput{
		Role:              in.Role,
		IsActive:          in.IsActive,
		EmailVerified:     in.EmailVerified,
		SetStorageQuota:   quotaSet,
		StorageQuotaBytes: quotaValue,
	})
	if err != nil {
		switch {
		case errors.Is(err, admin.ErrNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		case errors.Is(err, admin.ErrSelfChange):
			s.audit(c, observability.ActionAdminUserUpdate, observability.ResultFailure, callerID.String(), "self_change")
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "cannot demote or deactivate yourself")
		}
		return err
	}
	used, err := s.adminsvc.StorageUsed(c.Request().Context(), targetID)
	if err != nil {
		return err
	}
	s.audit(c, observability.ActionAdminUserUpdate, observability.ResultSuccess, callerID.String(), adminChangeReason(targetID, in))
	return c.JSON(http.StatusOK, newAdminUserView(updated, used))
}

// adminChangeReason summarises an admin user edit for the audit log (no secrets).
func adminChangeReason(targetID uuid.UUID, in updateUserRequest) string {
	parts := []string{"target=" + targetID.String()}
	if in.Role != nil {
		parts = append(parts, "role="+*in.Role)
	}
	if in.IsActive != nil {
		parts = append(parts, "is_active="+strconv.FormatBool(*in.IsActive))
	}
	if in.EmailVerified != nil {
		parts = append(parts, "email_verified="+strconv.FormatBool(*in.EmailVerified))
	}
	if set, value, _ := in.quotaField(); set {
		if value == nil {
			parts = append(parts, "storage_quota_bytes=default")
		} else {
			parts = append(parts, "storage_quota_bytes="+strconv.FormatInt(*value, 10))
		}
	}
	return strings.Join(parts, " ")
}
