package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// registrationRequestView is the admin-queue projection of a pending/resolved
// signup. It never carries the password hash.
type registrationRequestView struct {
	ID               string     `json:"id"`
	Username         string     `json:"username"`
	Email            string     `json:"email"`
	Note             string     `json:"note,omitempty"`
	Status           string     `json:"status"`
	ModeratorNote    string     `json:"moderator_note,omitempty"`
	ReviewedAt       *time.Time `json:"reviewed_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	ReviewerUsername string     `json:"reviewer_username,omitempty"`
}

func newRegistrationRequestView(r auth.RegistrationRequest) registrationRequestView {
	return registrationRequestView{
		ID:               r.ID.String(),
		Username:         r.Username,
		Email:            r.Email,
		Note:             r.Note,
		Status:           r.Status,
		ModeratorNote:    r.ModeratorNote,
		ReviewedAt:       r.ReviewedAt,
		CreatedAt:        r.CreatedAt,
		ReviewerUsername: r.ReviewerUsername,
	}
}

// registrationRequestStatuses are the accepted ?status values; statusFilterAll
// is the "no filter" sentinel, which the service maps to a NULL predicate.
var registrationRequestStatuses = []string{"pending", "approved", "rejected", statusFilterAll}

// registrationRequestListResponse is the paginated approval queue.
type registrationRequestListResponse struct {
	Requests []registrationRequestView `json:"requests"`
	pageMeta
}

// handleListRegistrationRequests returns the registration approval queue with
// its total. Behind requireRole(admin). ?status is pending|approved|rejected|all
// (default all); pagination via ?limit (1–100, default 20) and ?offset.
//
// This used to be `c.QueryParam("status") == "pending"`, so ?status=approved —
// a perfectly reasonable request — silently returned the ENTIRE queue instead of
// the approved ones. An unrecognised status is now a 400.
func (s *Server) handleListRegistrationRequests(c echo.Context) error {
	status, err := parseEnumParam(c, "status", registrationRequestStatuses, statusFilterAll)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.authsvc.ListRegistrationRequests(c.Request().Context(), listFilterValue(status), page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]registrationRequestView, 0, len(items))
	for _, it := range items {
		views = append(views, newRegistrationRequestView(it))
	}
	return c.JSON(http.StatusOK, registrationRequestListResponse{Requests: views, pageMeta: page.meta(total)})
}

// handleApproveRegistration approves a pending request, creating the account.
// Behind requireRole(admin). Unknown/already-resolved id → 404; a username/email
// taken since the request was filed → 409. Emits an audit event.
func (s *Server) handleApproveRegistration(c echo.Context) error {
	adminID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "registration request not found")
	}
	user, err := s.authsvc.ApproveRegistration(c.Request().Context(), adminID, id)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrRegistrationRequestNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "registration request not found")
		case errors.Is(err, auth.ErrConflict):
			return echo.NewHTTPError(http.StatusConflict, "username or email already taken")
		}
		return err
	}
	s.audit(c, observability.ActionRegistrationApprove, observability.ResultSuccess, adminID.String(), user.ID.String())
	return c.NoContent(http.StatusNoContent)
}

// rejectRegistrationRequestBody is the optional body for rejecting a request.
type rejectRegistrationRequestBody struct {
	Note string `json:"note"`
}

func (r rejectRegistrationRequestBody) Validate() []FieldError {
	if len(r.Note) > maxRegistrationNoteLen {
		return []FieldError{{Field: "note", Message: "must be at most 2000 characters"}}
	}
	return nil
}

// handleRejectRegistration rejects a pending request with an optional moderator
// note. Behind requireRole(admin). Unknown/already-resolved id → 404. Emits an
// audit event.
func (s *Server) handleRejectRegistration(c echo.Context) error {
	adminID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "registration request not found")
	}
	var in rejectRegistrationRequestBody
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.authsvc.RejectRegistration(c.Request().Context(), adminID, id, strings.TrimSpace(in.Note)); err != nil {
		if errors.Is(err, auth.ErrRegistrationRequestNotFound) {
			s.audit(c, observability.ActionRegistrationReject, observability.ResultFailure, adminID.String(), "not_found")
			return echo.NewHTTPError(http.StatusNotFound, "registration request not found")
		}
		return err
	}
	s.audit(c, observability.ActionRegistrationReject, observability.ResultSuccess, adminID.String(), id.String())
	return c.NoContent(http.StatusNoContent)
}
