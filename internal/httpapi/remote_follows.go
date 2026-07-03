package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/federation"
)

// Outbound remote-channel follows (remote-content §3): the caller follows a
// remote channel by name@domain handle or actor URL; the backend resolves it
// (WebFinger, SSRF-guarded), records a pending follow, and queues a signed
// Follow as the caller's ACCOUNT actor. Unfollow is by follow row id — the row
// goes immediately (local intent wins) and an Undo{Follow} is queued.

// remoteFollowView is one outbound remote-channel follow.
type remoteFollowView struct {
	ID        string    `json:"id"`
	ActorURL  string    `json:"actor_url"`
	Handle    string    `json:"handle"` // name@domain
	Domain    string    `json:"domain"`
	State     string    `json:"state"` // pending | accepted
	CreatedAt time.Time `json:"created_at"`
}

func newRemoteFollowView(f federation.RemoteFollow) remoteFollowView {
	return remoteFollowView{
		ID:        f.ID.String(),
		ActorURL:  f.ActorURL,
		Handle:    f.Handle,
		Domain:    f.Domain,
		State:     f.State,
		CreatedAt: f.CreatedAt,
	}
}

// remoteFollowListResponse is the caller's paginated remote-follow list.
type remoteFollowListResponse struct {
	Follows []remoteFollowView `json:"follows"`
	Limit   int                `json:"limit"`
	Offset  int                `json:"offset"`
}

// maxFollowTargetLen bounds the submitted handle/actor URL.
const maxFollowTargetLen = 2000

// createRemoteFollowRequest is the POST /me/remote-follows body: exactly one
// way to name the target — a name@domain handle or a full actor URL.
type createRemoteFollowRequest struct {
	Handle   string `json:"handle"`
	ActorURL string `json:"actor_url"`
}

func (r createRemoteFollowRequest) Validate() []FieldError {
	handle := strings.TrimSpace(r.Handle)
	actorURL := strings.TrimSpace(r.ActorURL)
	switch {
	case handle == "" && actorURL == "":
		return []FieldError{{Field: "handle", Message: "either handle (name@domain) or actor_url is required"}}
	case handle != "" && actorURL != "":
		return []FieldError{{Field: "handle", Message: "provide either handle or actor_url, not both"}}
	case len(handle) > maxFollowTargetLen || len(actorURL) > maxFollowTargetLen:
		return []FieldError{{Field: "handle", Message: "is too long"}}
	}
	return nil
}

// target returns whichever of the two fields names the follow target.
func (r createRemoteFollowRequest) target() string {
	if t := strings.TrimSpace(r.Handle); t != "" {
		return t
	}
	return strings.TrimSpace(r.ActorURL)
}

// handleCreateRemoteFollow follows a remote channel as the caller. Behind
// requireAuth. Idempotent: re-following returns the existing row without
// sending a second Follow. Resolution failures (bad handle, unresolvable
// WebFinger/actor, a local target) are 422; when federation is disabled on
// this instance the endpoint is 503 (no outbound fetches are made).
func (s *Server) handleCreateRemoteFollow(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if !s.cfg.FederationEnabled {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "federation is disabled on this instance")
	}
	var in createRemoteFollowRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	follow, err := s.fedsvc.FollowRemoteChannel(c.Request().Context(), userID, in.target())
	if err != nil {
		switch {
		case errors.Is(err, federation.ErrBadFollowTarget):
			return &ValidationError{Fields: []FieldError{{Field: "handle", Message: "must be name@domain or an http(s) actor URL"}}}
		case errors.Is(err, federation.ErrLocalFollowTarget):
			return &ValidationError{Fields: []FieldError{{Field: "handle", Message: "is a local actor — follow local channels via POST /channels/{handle}/follow"}}}
		case errors.Is(err, federation.ErrRemoteUnresolvable):
			// The target is not echoed back (attacker-controlled input).
			return &ValidationError{Fields: []FieldError{{Field: "handle", Message: "could not be resolved to a remote channel"}}}
		case errors.Is(err, federation.ErrNotFound):
			return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
		}
		return err
	}
	return c.JSON(http.StatusCreated, newRemoteFollowView(follow))
}

// handleListRemoteFollows returns the caller's remote-channel follows, newest
// first, with the followed actor's identity and follow state. Behind
// requireAuth. Pagination via ?limit (1–100, default 20)/?offset.
func (s *Server) handleListRemoteFollows(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.fedsvc.ListRemoteFollows(c.Request().Context(), userID, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]remoteFollowView, 0, len(items))
	for _, it := range items {
		views = append(views, newRemoteFollowView(it))
	}
	return c.JSON(http.StatusOK, remoteFollowListResponse{Follows: views, Limit: limit, Offset: offset})
}

// handleDeleteRemoteFollow unfollows a remote channel by follow row id: the
// row is deleted immediately (local intent wins) and a signed Undo{Follow} is
// queued to the remote inbox. Behind requireAuth. Unknown — or another user's —
// id is 404.
func (s *Server) handleDeleteRemoteFollow(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "remote follow not found")
	}
	if err := s.fedsvc.UnfollowRemoteChannel(c.Request().Context(), userID, id); err != nil {
		if errors.Is(err, federation.ErrFollowNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "remote follow not found")
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
