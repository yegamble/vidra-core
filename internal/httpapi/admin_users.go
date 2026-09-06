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
	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// adminUserView is the admin projection of an account. It deliberately omits the
// password hash and never carries any secret. StorageQuotaBytes is the per-user
// override (null = the instance default applies; 0 = unlimited);
// StorageUsedBytes is the account's current usage. DeletedAt is non-null for a
// hard-deleted (tombstoned) account — the row stays listed, anonymised, so the
// console can label it and withhold the actions that cannot apply to it.
// IsOwner marks THE instance owner (0131): there is no owner role, so this flag
// is the only thing that tells a console which admin installed the instance and
// which guarded actions it must not offer against that row.
type adminUserView struct {
	ID                string     `json:"id"`
	Username          string     `json:"username"`
	Email             string     `json:"email"`
	Role              string     `json:"role"`
	IsActive          bool       `json:"is_active"`
	EmailVerified     bool       `json:"email_verified"`
	BypassQuarantine  bool       `json:"bypass_quarantine"`
	DisplayName       string     `json:"display_name"`
	StorageQuotaBytes *int64     `json:"storage_quota_bytes"`
	StorageUsedBytes  int64      `json:"storage_used_bytes"`
	CreatedAt         time.Time  `json:"created_at"`
	DeletedAt         *time.Time `json:"deleted_at"`
	IsOwner           bool       `json:"is_owner"`
}

func newAdminUserView(u sqlcgen.User, usedBytes int64) adminUserView {
	return adminUserView{
		ID:                u.ID.String(),
		Username:          u.Username,
		Email:             u.Email,
		Role:              u.Role,
		IsActive:          u.IsActive,
		EmailVerified:     u.EmailVerified,
		BypassQuarantine:  u.BypassQuarantine,
		DisplayName:       u.DisplayName,
		StorageQuotaBytes: u.StorageQuotaBytes,
		StorageUsedBytes:  usedBytes,
		CreatedAt:         u.CreatedAt,
		DeletedAt:         pgconv.TimeOrNil(u.DeletedAt),
		IsOwner:           u.IsOwner,
	}
}

// newAdminUserViewFromRow builds the view from a ListUsers row (which carries
// the usage aggregate inline, so the list costs one query).
func newAdminUserViewFromRow(r sqlcgen.ListUsersRow) adminUserView {
	return newAdminUserView(sqlcgen.User{
		ID: r.ID, Username: r.Username, Email: r.Email, Role: r.Role,
		IsActive: r.IsActive, EmailVerified: r.EmailVerified,
		BypassQuarantine: r.BypassQuarantine,
		DisplayName:      r.DisplayName, StorageQuotaBytes: r.StorageQuotaBytes,
		CreatedAt: r.CreatedAt, DeletedAt: r.DeletedAt, IsOwner: r.IsOwner,
	}, r.StorageUsedBytes)
}

// Page bounds for the admin user list. These are the user list's OWN limits:
// it previously borrowed the video feed's, which is why an instance with
// thousands of accounts could only ever show 100 of them — the feed's ceiling
// was never a statement about how many accounts an admin may page through.
const (
	defaultUserPageLimit = 50
	maxUserPageLimit     = 200
)

// adminUserListResponse is the paginated admin user list. Total is how many
// accounts match the same query, so a client can tell how many pages exist;
// without it a caller cannot distinguish "last page" from "there is more".
type adminUserListResponse struct {
	Users []adminUserView `json:"users"`
	pageMeta
}

// handleListUsers returns accounts, newest first, optionally filtered by ?q
// (username/email substring). Behind requireRole(admin). Pagination via ?limit
// (1–200, default 50) and ?offset.
func (s *Server) handleListUsers(c echo.Context) error {
	ctx := c.Request().Context()
	query := strings.TrimSpace(c.QueryParam("q"))
	page := parsePage(c, defaultUserPageLimit, maxUserPageLimit)
	users, err := s.adminsvc.ListUsers(ctx, query, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	// Counted with the SAME query as the page, so a filtered page reports the
	// size of its own result set rather than the instance total.
	total, err := s.adminsvc.CountUsersMatching(ctx, query)
	if err != nil {
		return err
	}
	views := make([]adminUserView, 0, len(users))
	for _, u := range users {
		views = append(views, newAdminUserViewFromRow(u))
	}
	return c.JSON(http.StatusOK, adminUserListResponse{Users: views, pageMeta: page.meta(total)})
}

// updateUserRequest is the PATCH /admin/users/{id} body. Fields are optional;
// only those present are changed. storage_quota_bytes is tri-state: absent =
// unchanged, null = reset to the instance default, a non-negative integer =
// per-user override (0 = unlimited) — hence the RawMessage, which preserves
// the absent/null distinction JSON pointers cannot. email_verified lets an
// admin mark an address confirmed without the token round-trip (or revoke it).
// bypass_quarantine exempts a trusted account from the QUARANTINE_NEW_UPLOADS
// gate (§11): their uploads publish directly.
type updateUserRequest struct {
	Role              *string         `json:"role"`
	IsActive          *bool           `json:"is_active"`
	EmailVerified     *bool           `json:"email_verified"`
	BypassQuarantine  *bool           `json:"bypass_quarantine"`
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
	if r.Role == nil && r.IsActive == nil && r.EmailVerified == nil && r.BypassQuarantine == nil && !quotaSet {
		return []FieldError{{Field: "role", Message: "at least one of role, is_active, email_verified, bypass_quarantine, storage_quota_bytes is required"}}
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
	callerID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	targetID, err := pathUUID(c, "id", "user not found")
	if err != nil {
		return err
	}
	var in updateUserRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	quotaSet, quotaValue, _ := in.quotaField() // already validated

	res, err := s.adminsvc.UpdateUserDetailed(c.Request().Context(), callerID, targetID, admin.UpdateUserInput{
		Role:              in.Role,
		IsActive:          in.IsActive,
		EmailVerified:     in.EmailVerified,
		BypassQuarantine:  in.BypassQuarantine,
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
		case errors.Is(err, admin.ErrDeletedAccount):
			s.auditAdminUserRefusal(c, observability.ActionAdminUserUpdate, callerID, targetID, "deleted_account")
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "this account was deleted; deletion is permanent and cannot be reversed")
		case errors.Is(err, admin.ErrOwnerProtected):
			s.auditAdminUserRefusal(c, observability.ActionAdminUserUpdate, callerID, targetID, "owner_protected")
			return &OwnerProtectedError{}
		case errors.Is(err, admin.ErrLastAdmin):
			s.auditAdminUserRefusal(c, observability.ActionAdminUserUpdate, callerID, targetID, "last_admin")
			return &LastAdminError{}
		}
		return err
	}
	used, err := s.adminsvc.StorageUsed(c.Request().Context(), targetID)
	if err != nil {
		return err
	}
	// The ledger carries BOTH: the structured field list a consumer can read
	// without parsing, and the human line the admin audit view already renders.
	s.auditEvent(c, audit.Event{
		Action: observability.ActionAdminUserUpdate, Result: observability.ResultSuccess,
		ActorID: callerID.String(), Reason: adminChangeReason(targetID, in),
		ResourceType: auditResourceUser, ResourceID: targetID.String(),
		Changes: adminUserChanges(res, in),
	})
	return c.JSON(http.StatusOK, newAdminUserView(res.After, used))
}

// auditResourceUser is the audit envelope's resource type for an account.
const auditResourceUser = "user"

// auditAdminUserRefusal records a refused admin write as a FAILURE naming the
// target and the guard that fired. reason_code is an allowlisted metadata key,
// so the machine-readable cause survives where prose would be rejected.
func (s *Server) auditAdminUserRefusal(c echo.Context, action string, callerID, targetID uuid.UUID, code string) {
	s.auditEvent(c, audit.Event{
		Action: action, Result: observability.ResultFailure, ActorID: callerID.String(),
		Reason:       code + " target=" + targetID.String(),
		ResourceType: auditResourceUser, ResourceID: targetID.String(),
		Metadata: []audit.MetadataField{{Key: "reason_code", Value: code}},
	})
}

// adminUserChanges renders an admin edit as the envelope's structured changes
// array — A16 slice 1 finding (5), which was that a consumer had to parse prose
// to learn what an admin altered. Only fields the request actually carried are
// listed, every one of them is in audit's allowlist of safe state/config fields,
// and no user prose or address is ever included.
func adminUserChanges(res admin.UpdateResult, in updateUserRequest) []audit.Change {
	var out []audit.Change
	if in.Role != nil {
		out = append(out, audit.Change{Field: "role", Before: res.Before.Role, After: res.After.Role})
	}
	if in.IsActive != nil {
		out = append(out, audit.Change{Field: "account_enabled",
			Before: strconv.FormatBool(res.Before.IsActive), After: strconv.FormatBool(res.After.IsActive)})
	}
	if in.EmailVerified != nil {
		out = append(out, audit.Change{Field: "email_verified",
			Before: strconv.FormatBool(res.Before.EmailVerified), After: strconv.FormatBool(res.After.EmailVerified)})
	}
	if in.BypassQuarantine != nil {
		out = append(out, audit.Change{Field: "bypass_quarantine",
			Before: strconv.FormatBool(res.Before.BypassQuarantine), After: strconv.FormatBool(res.After.BypassQuarantine)})
	}
	if set, _, _ := in.quotaField(); set {
		out = append(out, audit.Change{Field: "storage_quota_bytes",
			Before: quotaAuditValue(res.Before.StorageQuotaBytes), After: quotaAuditValue(res.After.StorageQuotaBytes)})
	}
	return out
}

// quotaAuditValue renders the tri-state quota for the ledger. NULL — "the
// instance default applies" — is spelled out rather than left empty, because an
// empty change value means "absent" and would read as no quota information at
// all.
func quotaAuditValue(v *int64) string {
	if v == nil {
		return "default"
	}
	return strconv.FormatInt(*v, 10)
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
	if in.BypassQuarantine != nil {
		parts = append(parts, "bypass_quarantine="+strconv.FormatBool(*in.BypassQuarantine))
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
