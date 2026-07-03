package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/observability"
)

// mutedInstanceView is one instance in the caller's instance-mute list.
type mutedInstanceView struct {
	Domain  string    `json:"domain"`
	MutedAt time.Time `json:"muted_at"`
}

// mutedInstanceListResponse is the paginated list of instances the caller has muted.
type mutedInstanceListResponse struct {
	Instances []mutedInstanceView `json:"instances"`
	Limit     int                 `json:"limit"`
	Offset    int                 `json:"offset"`
}

// handleMuteInstance mutes a whole remote instance for the caller: remote
// content from that domain becomes hidden from their surfaces. Behind
// requireAuth. Idempotent; an invalid domain → 422.
func (s *Server) handleMuteInstance(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if err := s.instancemodsvc.MuteInstance(c.Request().Context(), userID, c.Param("domain")); err != nil {
		if errors.Is(err, instancemod.ErrInvalidDomain) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "invalid instance domain")
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleUnmuteInstance lifts the caller's mute of a remote instance. Behind
// requireAuth. Idempotent; an invalid domain → 422.
func (s *Server) handleUnmuteInstance(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if err := s.instancemodsvc.UnmuteInstance(c.Request().Context(), userID, c.Param("domain")); err != nil {
		if errors.Is(err, instancemod.ErrInvalidDomain) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "invalid instance domain")
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleListMutedInstances returns the instances the caller has muted, newest
// first. Behind requireAuth. Pagination via ?limit (1–100, default 20)/?offset.
func (s *Server) handleListMutedInstances(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.instancemodsvc.ListMutedInstances(c.Request().Context(), userID, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]mutedInstanceView, 0, len(items))
	for _, it := range items {
		views = append(views, mutedInstanceView{Domain: it.Domain, MutedAt: it.MutedAt})
	}
	return c.JSON(http.StatusOK, mutedInstanceListResponse{Instances: views, Limit: limit, Offset: offset})
}

// blockedInstanceView is one entry of the admin instance blocklist.
type blockedInstanceView struct {
	Domain    string    `json:"domain"`
	Reason    string    `json:"reason"`
	BlockedBy string    `json:"blocked_by,omitempty"`
	BlockedAt time.Time `json:"blocked_at"`
}

// blockedInstanceListResponse is the paginated admin instance blocklist.
type blockedInstanceListResponse struct {
	Instances []blockedInstanceView `json:"instances"`
	Limit     int                   `json:"limit"`
	Offset    int                   `json:"offset"`
}

// blockInstanceRequest is the POST /admin/instances/blocked body. The reason is
// recorded for the blocklist + audit trail (it may be empty).
type blockInstanceRequest struct {
	Domain string `json:"domain"`
	Reason string `json:"reason"`
}

func (r blockInstanceRequest) Validate() []FieldError {
	var errs []FieldError
	if strings.TrimSpace(r.Domain) == "" {
		errs = append(errs, FieldError{Field: "domain", Message: "is required"})
	}
	if len(r.Reason) > maxReportReasonLen {
		errs = append(errs, FieldError{Field: "reason", Message: "must be at most 2000 characters"})
	}
	return errs
}

// handleBlockInstance adds a remote instance to the admin blocklist: inbound
// activities from it are dropped, its existing remote content is hidden from
// all surfaces, and outbound deliveries to it are cancelled. Behind
// requireRole(admin, moderator). Idempotent; an invalid domain → 422. Emits an
// audit event.
func (s *Server) handleBlockInstance(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in blockInstanceRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.instancemodsvc.BlockInstance(c.Request().Context(), userID, in.Domain, strings.TrimSpace(in.Reason)); err != nil {
		if errors.Is(err, instancemod.ErrInvalidDomain) {
			s.audit(c, observability.ActionInstanceBlock, observability.ResultFailure, userID.String(), "invalid_domain")
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "invalid instance domain")
		}
		return err
	}
	s.audit(c, observability.ActionInstanceBlock, observability.ResultSuccess, userID.String(), "domain="+strings.ToLower(strings.TrimSpace(in.Domain)))
	return c.NoContent(http.StatusNoContent)
}

// handleUnblockInstance lifts an instance block. Behind requireRole(admin,
// moderator). Idempotent; an invalid domain → 422. Emits an audit event.
func (s *Server) handleUnblockInstance(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	domain := c.Param("domain")
	if err := s.instancemodsvc.UnblockInstance(c.Request().Context(), domain); err != nil {
		if errors.Is(err, instancemod.ErrInvalidDomain) {
			s.audit(c, observability.ActionInstanceUnblock, observability.ResultFailure, userID.String(), "invalid_domain")
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "invalid instance domain")
		}
		return err
	}
	s.audit(c, observability.ActionInstanceUnblock, observability.ResultSuccess, userID.String(), "domain="+strings.ToLower(strings.TrimSpace(domain)))
	return c.NoContent(http.StatusNoContent)
}

// handleListBlockedInstances returns the admin instance blocklist, newest block
// first. Behind requireRole(admin, moderator). Pagination via ?limit (1–100,
// default 20) and ?offset.
func (s *Server) handleListBlockedInstances(c echo.Context) error {
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.instancemodsvc.ListBlockedInstances(c.Request().Context(), int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]blockedInstanceView, 0, len(items))
	for _, it := range items {
		v := blockedInstanceView{Domain: it.Domain, Reason: it.Reason, BlockedAt: it.BlockedAt}
		if it.BlockedBy != nil {
			v.BlockedBy = it.BlockedBy.String()
		}
		views = append(views, v)
	}
	return c.JSON(http.StatusOK, blockedInstanceListResponse{Instances: views, Limit: limit, Offset: offset})
}
