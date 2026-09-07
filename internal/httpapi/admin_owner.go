package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// transferOwnershipRequest is the POST /api/v1/admin/owner/transfer body: which
// administrator becomes the owner, and the caller's current password.
//
// The password is the same confirmation DELETE /auth/me and POST
// /auth/me/deactivate ask for, and for the same reason: a stolen access token
// alone must not be able to perform the one action that permanently strips the
// caller of a capability. It is the only such action an ADMIN route carries,
// which is why this route asks and the rest of /admin does not.
type transferOwnershipRequest struct {
	UserID   string `json:"user_id"`
	Password string `json:"password"`
}

func (r transferOwnershipRequest) Validate() []FieldError {
	var fes []FieldError
	if r.UserID == "" {
		fes = append(fes, FieldError{Field: "user_id", Message: "is required"})
	}
	if r.Password == "" {
		fes = append(fes, FieldError{Field: "password", Message: "is required"})
	}
	return fes
}

// ownerTransferView is the 200 body: who holds the marker now, and who used to.
type ownerTransferView struct {
	NewOwnerID          string `json:"new_owner_id"`
	NewOwnerUsername    string `json:"new_owner_username"`
	FormerOwnerID       string `json:"former_owner_id"`
	FormerOwnerUsername string `json:"former_owner_username"`
}

// handleTransferOwnership moves the instance-owner marker (0131) to another
// administrator.
//
// Route shape: it lives under /admin because that is where the console's
// user-administration surface is and where requireRole(admin) is the router's
// coarse gate — every caller who could possibly be the owner is an admin, so the
// gate costs nothing and keeps this route out of reach of moderators and users
// without a second implementation of that check. The finer OWNER-only rule
// cannot live in requireRole at all: Vidra deliberately has no owner role (the
// owner holds `admin` like everyone else), so it is a service check, and a
// non-owner admin is refused 403 `owner_only` — an authorization answer, not a
// validation one, because nothing about the request is malformed.
//
// 200 with the new and former owner. 403 for a non-owner admin (`owner_only`) or
// a wrong password. 404 for an unknown account. 422 `owner_target_invalid` when
// the target is not an active, non-tombstoned administrator, or is the caller.
// 409 `owner_transfer_conflict` when another transfer committed first.
func (s *Server) handleTransferOwnership(c echo.Context) error {
	callerID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in transferOwnershipRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	targetID, err := uuid.Parse(in.UserID)
	if err != nil {
		// An unparseable id names no account; answer it as one, not as a hint
		// about which ids exist.
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	res, err := s.authsvc.TransferOwnership(c.Request().Context(), callerID, targetID, in.Password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrNotInstanceOwner):
			s.audit(c, observability.ActionOwnerTransfer, observability.ResultFailure, callerID.String(), "not_owner")
			return &OwnerOnlyError{}
		case errors.Is(err, auth.ErrInvalidPassword):
			s.audit(c, observability.ActionOwnerTransfer, observability.ResultFailure, callerID.String(), "invalid_password")
			return echo.NewHTTPError(http.StatusForbidden, "incorrect password")
		case errors.Is(err, auth.ErrAccountNotFound):
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		case errors.Is(err, auth.ErrOwnerTargetNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		case errors.Is(err, auth.ErrOwnerTargetIneligible):
			s.auditAdminUserRefusal(c, observability.ActionOwnerTransfer, callerID, targetID, "owner_target_invalid")
			return &OwnerTargetError{}
		case errors.Is(err, auth.ErrOwnerTransferConflict):
			s.auditAdminUserRefusal(c, observability.ActionOwnerTransfer, callerID, targetID, "owner_transfer_conflict")
			return &OwnerTransferConflictError{}
		}
		return err
	}
	// Structured, and inside the envelope's vocabulary: `is_owner` joins the
	// allowlist beside account_enabled and email_verified (a boolean state flag,
	// not content), the resource is the account that GAINED the marker, and
	// `count` records how many rows held it before — normally 1, and 0 on an
	// instance the 0131 backfill could not resolve, which is a fact an operator
	// reading the ledger wants. No prose: both parties are named by id.
	s.auditEvent(c, audit.Event{
		Action: observability.ActionOwnerTransfer, Result: observability.ResultSuccess,
		ActorID:      callerID.String(),
		Reason:       "from=" + res.FormerOwnerID.String() + " to=" + res.NewOwnerID.String(),
		ResourceType: auditResourceUser, ResourceID: res.NewOwnerID.String(),
		Changes: []audit.Change{{Field: "is_owner", Before: "false", After: "true"}},
		Metadata: []audit.MetadataField{
			{Key: "count", Value: strconv.FormatInt(res.PreviousOwnersCleared, 10)},
		},
	})
	return c.JSON(http.StatusOK, ownerTransferView{
		NewOwnerID:          res.NewOwnerID.String(),
		NewOwnerUsername:    res.NewOwnerUsername,
		FormerOwnerID:       res.FormerOwnerID.String(),
		FormerOwnerUsername: res.FormerOwnerUsername,
	})
}
