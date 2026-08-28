package httpapi

// Admin federation follower-approval queue (config-parity W12), modeled on the
// registration-requests queue: while federation_follower_approval is on, new
// inbound ActivityPub channel Follows wait PENDING here; approving delivers
// the Accept (plus the auto follow-back when that gate is on), rejecting
// delivers a Reject. This is a vidra deviation from PeerTube recorded in the
// parity ledger: approval applies to CHANNEL followers, because vidra has no
// instance-level AP actor. The queue governs the ActivityPub inbox only — the
// ATProto integration is outbound cross-posting and has no inbound path.

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/observability"
)

// federationFollowerRequestView is the admin-queue projection of one pending
// inbound channel Follow.
type federationFollowerRequestView struct {
	ID            string `json:"id"`
	ChannelID     string `json:"channel_id"`
	ChannelHandle string `json:"channel_handle"`
	ActorURL      string `json:"actor_url"`
	// Handle is the follower's cached fediverse identity
	// (preferred_username@domain); omitted when the actor cache row is gone.
	Handle    string    `json:"handle,omitempty"`
	Domain    string    `json:"domain,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func newFederationFollowerRequestView(r federation.FollowerRequest) federationFollowerRequestView {
	return federationFollowerRequestView{
		ID:            r.ID.String(),
		ChannelID:     r.ChannelID.String(),
		ChannelHandle: r.ChannelHandle,
		ActorURL:      r.ActorURL,
		Handle:        r.Handle,
		Domain:        r.Domain,
		CreatedAt:     r.CreatedAt,
	}
}

// federationFollowerRequestListResponse is the paginated approval queue.
type federationFollowerRequestListResponse struct {
	Requests []federationFollowerRequestView `json:"requests"`
	pageMeta
}

// handleListFederationFollowerRequests returns the pending follower-approval
// queue, newest first. Behind requireRole(admin). Pagination via ?limit
// (1–100, default 20) and ?offset.
func (s *Server) handleListFederationFollowerRequests(c echo.Context) error {
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.fedsvc.ListFollowerRequests(c.Request().Context(), page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]federationFollowerRequestView, 0, len(items))
	for _, it := range items {
		views = append(views, newFederationFollowerRequestView(it))
	}
	return c.JSON(http.StatusOK, federationFollowerRequestListResponse{Requests: views, pageMeta: page.meta(total)})
}

// handleApproveFederationFollowerRequest approves a pending follower request:
// the follow flips to accepted and the Accept activity is delivered to the
// follower (with the auto follow-back when federation_auto_follow_back is on).
// Behind requireRole(admin). Unknown/already-resolved id → 404. Emits an audit
// event (safe row id only).
func (s *Server) handleApproveFederationFollowerRequest(c echo.Context) error {
	adminID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "follower request not found")
	}
	if err := s.fedsvc.ApproveFollowerRequest(c.Request().Context(), id); err != nil {
		if errors.Is(err, federation.ErrFollowerRequestNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "follower request not found")
		}
		return err
	}
	s.audit(c, observability.ActionFederationFollowerApprove, observability.ResultSuccess, adminID.String(), id.String())
	return c.NoContent(http.StatusNoContent)
}

// handleRejectFederationFollowerRequest rejects a pending follower request:
// the pending follow is removed and a Reject activity is delivered to the
// follower. Behind requireRole(admin). Unknown/already-resolved id → 404.
// Emits an audit event (safe row id only).
func (s *Server) handleRejectFederationFollowerRequest(c echo.Context) error {
	adminID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "follower request not found")
	}
	if err := s.fedsvc.RejectFollowerRequest(c.Request().Context(), id); err != nil {
		if errors.Is(err, federation.ErrFollowerRequestNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "follower request not found")
		}
		return err
	}
	s.audit(c, observability.ActionFederationFollowerReject, observability.ResultSuccess, adminID.String(), id.String())
	return c.NoContent(http.StatusNoContent)
}
