package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/observability"
)

// domainParam reads the {domain} path segment, PERCENT-DECODED.
//
// The rehearsal measured the bug this closes: a fediverse domain in a lab (and
// on any instance behind a non-443 port) is a `host:port` literal, and a client
// that correctly percent-encodes the colon —
// `DELETE /admin/instances/blocked/127.0.0.1%3A28080` — got 422 invalid domain,
// while the raw colon worked. Echo hands back the RAW segment, so the encoded
// form reached the validator as the literal text "127.0.0.1%3A28080". Decoding
// here makes both spellings the same request, which is what a percent-encoding
// is for. An undecodable segment is passed through unchanged so the validator
// still refuses it with the message it already has.
func domainParam(c echo.Context) string {
	raw := c.Param("domain")
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}
	return decoded
}

// mutedInstanceView is one instance in the caller's instance-mute list.
type mutedInstanceView struct {
	Domain  string    `json:"domain"`
	MutedAt time.Time `json:"muted_at"`
}

// mutedInstanceListResponse is the paginated list of instances the caller has muted.
type mutedInstanceListResponse struct {
	Instances []mutedInstanceView `json:"instances"`
	pageMeta
}

// handleMuteInstance mutes a whole remote instance for the caller: remote
// content from that domain becomes hidden from their surfaces. Behind
// requireAuth. Idempotent; an invalid domain → 422.
func (s *Server) handleMuteInstance(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if err := s.instancemodsvc.MuteInstance(c.Request().Context(), userID, domainParam(c)); err != nil {
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if err := s.instancemodsvc.UnmuteInstance(c.Request().Context(), userID, domainParam(c)); err != nil {
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.instancemodsvc.ListMutedInstances(c.Request().Context(), userID, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]mutedInstanceView, 0, len(items))
	for _, it := range items {
		views = append(views, mutedInstanceView{Domain: it.Domain, MutedAt: it.MutedAt})
	}
	return c.JSON(http.StatusOK, mutedInstanceListResponse{Instances: views, pageMeta: page.meta(total)})
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
	pageMeta
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
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
//
// Lifting a block also RESUMES what the block cancelled: the outbound
// activities this instance refused to send while it stood (A29-F4). See
// federation.RedeliverAfterUnblock for which of them qualify and why the rest
// stay cancelled.
func (s *Server) handleUnblockInstance(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	domain := domainParam(c)
	blockedAt, wasBlocked, err := s.instancemodsvc.UnblockInstance(c.Request().Context(), domain)
	if err != nil {
		if errors.Is(err, instancemod.ErrInvalidDomain) {
			s.audit(c, observability.ActionInstanceUnblock, observability.ResultFailure, userID.String(), "invalid_domain")
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "invalid instance domain")
		}
		return err
	}
	if wasBlocked {
		s.redeliverAfterUnblock(c, strings.ToLower(strings.TrimSpace(domain)), blockedAt)
	}
	s.audit(c, observability.ActionInstanceUnblock, observability.ResultSuccess, userID.String(), "domain="+strings.ToLower(strings.TrimSpace(domain)))
	return c.NoContent(http.StatusNoContent)
}

// redeliverAfterUnblock resumes the cancelled outbound deliveries, DETACHED from
// the request.
//
// Detached for the same reason the edge-purge fan-out is: the work is a scan of
// the delivery queue and a bounded batch of updates, and an admin lifting a
// block must get their 204 at the speed of the DELETE that already committed.
// Nothing here can fail the unblock — the block IS lifted; a resumption that
// did not happen leaves rows exactly where they already were, which is the same
// place they were before this existed.
func (s *Server) redeliverAfterUnblock(c echo.Context, domain string, blockedAt time.Time) {
	if s.fedsvc == nil || !s.cfg.FederationEnabled {
		return
	}
	ctx := context.WithoutCancel(c.Request().Context())
	go func() {
		n, err := s.fedsvc.RedeliverAfterUnblock(ctx, domain, blockedAt)
		if err != nil {
			s.logger.WarnContext(ctx, "resuming deliveries after an instance unblock failed; the activities cancelled during the block stay cancelled",
				"domain", domain, "requeued", n)
			return
		}
		if n > 0 {
			s.logger.InfoContext(ctx, "resumed outbound deliveries cancelled while the instance was blocked",
				"domain", domain, "requeued", n)
		}
	}()
}

// handleListBlockedInstances returns the admin instance blocklist, newest block
// first. Behind requireRole(admin, moderator). Pagination via ?limit (1–100,
// default 20) and ?offset.
func (s *Server) handleListBlockedInstances(c echo.Context) error {
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.instancemodsvc.ListBlockedInstances(c.Request().Context(), page.Limit32(), page.Offset32())
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
	return c.JSON(http.StatusOK, blockedInstanceListResponse{Instances: views, pageMeta: page.meta(total)})
}
