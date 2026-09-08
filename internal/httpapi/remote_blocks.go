package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/federation"
)

// Per-remote-account blocks (A29-F7): the viewer surface.
//
// It is a SIBLING of /me/blocks rather than an extension of it, and the shape
// forced that. A local block is addressed by a user uuid in the path; a remote
// actor is addressed by a URL, which cannot be a path segment, or by a
// @user@domain handle, which is not a uuid. Widening /me/blocks/{id} to accept
// three identity shapes in one path parameter would make the route's meaning
// depend on parsing its own argument.

// remoteBlockRequest is the identity a viewer supplies: an actor URL, or a
// fediverse handle (@user@domain or user@domain).
type remoteBlockRequest struct {
	Actor string `json:"actor"`
}

// remoteBlockView is one entry in the caller's remote block list. Handle is
// empty when this instance has never cached the actor — the block still lists,
// because a block the viewer cannot see is a block they cannot lift.
type remoteBlockView struct {
	ActorURL  string `json:"actor_url"`
	Handle    string `json:"handle"`
	Domain    string `json:"domain"`
	BlockedAt string `json:"blocked_at"`
}

type remoteBlockListResponse struct {
	Actors []remoteBlockView `json:"actors"`
	pageMeta
}

// handleBlockRemoteActor blocks one remote actor for the caller. Behind
// requireAuth. Idempotent; a local identity → 422; an unresolvable handle → 422.
func (s *Server) handleBlockRemoteActor(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in remoteBlockRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	actorURL, err := s.resolveRemoteBlockTarget(c, strings.TrimSpace(in.Actor))
	if err != nil {
		return err
	}
	if err := s.fedsvc.BlockRemoteActor(c.Request().Context(), userID, actorURL); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleUnblockRemoteActor lifts one. Behind requireAuth. Idempotent.
//
// The actor is a QUERY parameter here because a DELETE carries no body in this
// API and an actor URL cannot be a path segment. It is matched verbatim against
// the stored URL: unblocking must work on exactly what the list showed, so no
// handle resolution runs on this path (a WebFinger that has since started
// failing must never strand a viewer with a block they cannot lift).
func (s *Server) handleUnblockRemoteActor(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	actor := strings.TrimSpace(c.QueryParam("actor"))
	if actor == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, `"actor" is required`)
	}
	if err := s.fedsvc.UnblockRemoteActor(c.Request().Context(), userID, actor); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleListRemoteBlocks returns the remote actors the caller has blocked,
// newest block first. Behind requireAuth.
func (s *Server) handleListRemoteBlocks(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.fedsvc.ListRemoteActorBlocks(c.Request().Context(), userID, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]remoteBlockView, 0, len(items))
	for _, it := range items {
		views = append(views, remoteBlockView{
			ActorURL:  it.ActorURL,
			Handle:    it.Handle,
			Domain:    it.Domain,
			BlockedAt: it.BlockedAt,
		})
	}
	return c.JSON(http.StatusOK, remoteBlockListResponse{Actors: views, pageMeta: page.meta(total)})
}

// resolveRemoteBlockTarget maps the supplied identity to a canonical actor URL,
// turning the service's typed refusals into the HTTP answers this API uses.
func (s *Server) resolveRemoteBlockTarget(c echo.Context, identity string) (string, error) {
	if identity == "" {
		return "", echo.NewHTTPError(http.StatusUnprocessableEntity, `"actor" is required`)
	}
	actorURL, err := s.fedsvc.ResolveRemoteActorIdentity(c.Request().Context(), identity)
	switch {
	case errors.Is(err, federation.ErrRemoteActorRequired):
		return "", echo.NewHTTPError(http.StatusUnprocessableEntity,
			"actor must be a fediverse handle (@user@domain) or an actor URL")
	case errors.Is(err, federation.ErrLocalFollowTarget):
		return "", echo.NewHTTPError(http.StatusUnprocessableEntity,
			"that account is local to this instance; block it from the account itself")
	case errors.Is(err, federation.ErrRemoteUnresolvable):
		return "", echo.NewHTTPError(http.StatusUnprocessableEntity, "that remote account could not be resolved")
	case err != nil:
		return "", err
	}
	return actorURL, nil
}
