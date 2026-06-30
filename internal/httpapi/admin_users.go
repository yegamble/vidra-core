package httpapi

import (
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
// password hash and never carries any secret.
type adminUserView struct {
	ID            string    `json:"id"`
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	IsActive      bool      `json:"is_active"`
	EmailVerified bool      `json:"email_verified"`
	DisplayName   string    `json:"display_name"`
	CreatedAt     time.Time `json:"created_at"`
}

func newAdminUserView(u sqlcgen.User) adminUserView {
	return adminUserView{
		ID:            u.ID.String(),
		Username:      u.Username,
		Email:         u.Email,
		Role:          u.Role,
		IsActive:      u.IsActive,
		EmailVerified: u.EmailVerified,
		DisplayName:   u.DisplayName,
		CreatedAt:     u.CreatedAt,
	}
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
		views = append(views, newAdminUserView(u))
	}
	return c.JSON(http.StatusOK, adminUserListResponse{Users: views, Limit: limit, Offset: offset})
}

// updateUserRequest is the PATCH /admin/users/{id} body. Fields are optional;
// only those present are changed.
type updateUserRequest struct {
	Role     *string `json:"role"`
	IsActive *bool   `json:"is_active"`
}

func (r updateUserRequest) Validate() []FieldError {
	if r.Role == nil && r.IsActive == nil {
		return []FieldError{{Field: "role", Message: "at least one of role, is_active is required"}}
	}
	if r.Role != nil && !admin.ValidRole(*r.Role) {
		return []FieldError{{Field: "role", Message: "must be one of user, moderator, admin"}}
	}
	return nil
}

// handleUpdateUser edits a user's role and/or active flag. Behind
// requireRole(admin). Self-demotion/self-deactivation is rejected; an unknown id
// is 404. Emits an audit event.
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

	updated, err := s.adminsvc.UpdateUser(c.Request().Context(), callerID, targetID, in.Role, in.IsActive)
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
	s.audit(c, observability.ActionAdminUserUpdate, observability.ResultSuccess, callerID.String(), adminChangeReason(targetID, in))
	return c.JSON(http.StatusOK, newAdminUserView(updated))
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
	return strings.Join(parts, " ")
}
