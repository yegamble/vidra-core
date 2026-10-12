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
// does not apply to, and one audit row — success or failure — carrying the
// safe row id only.
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
			s.audit(c, action, observability.ResultFailure, adminID.String(), id.String())
			if errors.Is(err, federation.ErrInstanceFollowNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "instance follow not found")
			}
			return err
		}
		s.audit(c, action, observability.ResultSuccess, adminID.String(), id.String())
		return c.NoContent(http.StatusNoContent)
	}
}

// handleListInstanceFollowing lists the instances ours follows.
func (s *Server) handleListInstanceFollowing(c echo.Context) error {
	return s.listInstanceFollows(c, s.fedsvc.ListInstanceFollowing)
}

// followInstanceRequest is the POST /admin/federation/instance-following body.
type followInstanceRequest struct {
	Host string `json:"host"`
}

// handleFollowInstance follows a remote instance by host, as our instance
// actor — PeerTube's POST /server/following. 201 with the follow (pending
// until the remote Accepts; an existing follow is returned as it is). The
// host is never echoed back in an error.
func (s *Server) handleFollowInstance(c echo.Context) error {
	adminID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if !s.cfg.FederationEnabled {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "federation is disabled on this instance")
	}
	var in followInstanceRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	f, err := s.fedsvc.FollowInstance(c.Request().Context(), in.Host)
	switch {
	case errors.Is(err, federation.ErrBadFollowTarget):
		return &ValidationError{Fields: []FieldError{{Field: "host", Message: "must be a host name, e.g. videos.example"}}}
	case errors.Is(err, federation.ErrLocalFollowTarget):
		return &ValidationError{Fields: []FieldError{{Field: "host", Message: "is this instance"}}}
	case errors.Is(err, federation.ErrRemoteUnresolvable):
		return &ValidationError{Fields: []FieldError{{Field: "host", Message: "has no instance actor this instance could reach"}}}
	case errors.Is(err, federation.ErrInstanceBlocked):
		return echo.NewHTTPError(http.StatusConflict, "that instance is blocked; unblock it first")
	case errors.Is(err, federation.ErrInstanceActorUnavailable):
		return echo.NewHTTPError(http.StatusConflict, "this instance does not serve its instance actor")
	case err != nil:
		return err
	}
	s.audit(c, observability.ActionFederationInstanceFollow, observability.ResultSuccess, adminID.String(), f.ID.String())
	return c.JSON(http.StatusCreated, instanceFollowView{ID: f.ID.String(), ActorURL: f.ActorURL, Host: f.Host,
		Handle: f.Handle, State: f.State, CreatedAt: f.CreatedAt})
}
