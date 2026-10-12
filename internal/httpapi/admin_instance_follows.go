package httpapi

// Admin instance follows (migration 0166): the instances following ours, and
// the admin's decisions on them — PeerTube's /server/followers. Admin only, like
// the channel-follower approval queue; rate limited and audited like every
// other admin write.

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/observability"
)

// instanceFollowView is the admin projection of one instance follow, in either
// direction.
type instanceFollowView struct {
	ID        string    `json:"id"`
	ActorURL  string    `json:"actor_url"`
	Host      string    `json:"host"`
	Handle    string    `json:"handle"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

type instanceFollowListResponse struct {
	Follows []instanceFollowView `json:"follows"`
	pageMeta
}

// instanceFollowStates are the ?state= filters a listing accepts.
var instanceFollowStates = map[string]bool{"": true, "pending": true, "accepted": true, "rejected": true}

// listInstanceFollows serves a page of instance follows from list.
func (s *Server) listInstanceFollows(c echo.Context, list func(context.Context, string, int32, int32) ([]federation.InstanceFollow, int64, error)) error {
	state := c.QueryParam("state")
	if !instanceFollowStates[state] {
		return &ValidationError{Fields: []FieldError{{Field: "state", Message: "must be pending, accepted or rejected"}}}
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := list(c.Request().Context(), state, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]instanceFollowView, 0, len(items))
	for _, f := range items {
		views = append(views, instanceFollowView{ID: f.ID.String(), ActorURL: f.ActorURL, Host: f.Host,
			Handle: f.Handle, State: f.State, CreatedAt: f.CreatedAt})
	}
	return c.JSON(http.StatusOK, instanceFollowListResponse{Follows: views, pageMeta: page.meta(total)})
}

// handleListInstanceFollowers lists the instances following ours.
func (s *Server) handleListInstanceFollowers(c echo.Context) error {
	return s.listInstanceFollows(c, s.fedsvc.ListInstanceFollowers)
}

// instanceFollowDecision is the handler for one admin decision on an instance
// follow by id: 204 on success, 404 for an unknown id or a state the decision
// does not apply to, and one audit row carrying the safe row id only.
func (s *Server) instanceFollowDecision(do func(context.Context, uuid.UUID) error, action string) echo.HandlerFunc {
	return func(c echo.Context) error {
		adminID, _, err := mustPrincipal(c)
		if err != nil {
			return err
		}
		id, err := pathUUID(c, "id", "instance follow not found")
		if err != nil {
			return err
		}
		if err := do(c.Request().Context(), id); err != nil {
			if errors.Is(err, federation.ErrInstanceFollowNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "instance follow not found")
			}
			return err
		}
		s.audit(c, action, observability.ResultSuccess, adminID.String(), id.String())
		return c.NoContent(http.StatusNoContent)
	}
}
