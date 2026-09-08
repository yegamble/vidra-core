package httpapi

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
)

// auditResourceRemoteActor is the resource type an instance-wide per-actor block
// names. The actor URL IS the identity here — there is no local row to point at.
const auditResourceRemoteActor = "remote_actor"

// remoteActorAuditEvent builds the trail row for an instance-wide per-actor
// block or unblock.
//
// The identity goes in REASON, not in resource_id, and that is a correction the
// A29 rehearsal 3 lab forced. resource_id is validated as "a bounded opaque
// identifier, not a URL or payload" — an actor URL fails that check, Record
// returned an error, and the persist is best-effort by design, so both admin
// actions answered 204 while writing NOTHING to the durable trail. Reason
// carries no such restriction and already holds machine-written identity on the
// sibling control: moderation.instance.block writes "domain=<host>". This is the
// same shape one level down, so the two moderation actions an admin chooses
// between read the same way in the log.
//
// A moderator's own note is still deliberately absent: it is prose, it lives on
// blocked_remote_actors, and audit_log does not carry prose.
func remoteActorAuditEvent(action, actorID, actorURL string) audit.Event {
	return audit.Event{
		Action: action, Result: observability.ResultSuccess,
		ActorID:      actorID,
		ResourceType: auditResourceRemoteActor,
		Reason:       "actor=" + actorURL,
	}
}

// Instance-wide per-remote-ACCOUNT blocks: the admin surface (A29 parity).
//
// It sits beside /admin/instances/blocked rather than inside it because the two
// answer different questions. An instance block is a judgement about a SERVER —
// nothing from it is worth carrying — and it takes every creator on that server
// with it. This is a judgement about one PERSON, and the rehearsal's own words
// for the alternative were "a sledgehammer": one troll on a busy server costing
// every reader here every creator on it.
//
// It reuses the viewer surface's identity resolution, so an admin pastes the
// same `@name@domain` or actor URL a viewer would, and it resolves UP to the
// account the same way — one block, covering that account's channels, including
// the ones it has not created yet.

// adminRemoteBlockRequest is the identity plus the moderator's note.
type adminRemoteBlockRequest struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason,omitempty"`
}

// maxRemoteBlockReasonLen bounds the moderator note.
const maxRemoteBlockReasonLen = 500

func (r adminRemoteBlockRequest) Validate() []FieldError {
	var fes []FieldError
	if strings.TrimSpace(r.Actor) == "" {
		fes = append(fes, FieldError{Field: "actor", Message: "is required"})
	}
	if len(r.Reason) > maxRemoteBlockReasonLen {
		fes = append(fes, FieldError{Field: "reason", Message: "must be at most 500 characters"})
	}
	return fes
}

// blockedRemoteActorView is one instance-wide block. Handle is empty when this
// instance has never cached the actor — the block still lists, because a block
// nobody can see is a block nobody can lift.
type blockedRemoteActorView struct {
	ActorURL  string `json:"actor_url"`
	Handle    string `json:"handle"`
	Domain    string `json:"domain"`
	Reason    string `json:"reason"`
	BlockedAt string `json:"blocked_at"`
}

type blockedRemoteActorListResponse struct {
	Actors []blockedRemoteActorView `json:"actors"`
	pageMeta
}

// handleAdminBlockRemoteActor blocks one remote account for everyone. Behind
// requireRole(admin, moderator). Idempotent; a local identity → 422; an
// unresolvable handle → 422.
func (s *Server) handleAdminBlockRemoteActor(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in adminRemoteBlockRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	actorURL, err := s.resolveRemoteBlockTarget(c, strings.TrimSpace(in.Actor))
	if err != nil {
		s.audit(c, observability.ActionRemoteActorBlock, observability.ResultFailure, userID.String(), "unresolvable")
		return err
	}
	if err := s.fedsvc.BlockRemoteActorInstanceWide(c.Request().Context(), actorURL, userID, strings.TrimSpace(in.Reason)); err != nil {
		return err
	}
	s.auditEvent(c, remoteActorAuditEvent(observability.ActionRemoteActorBlock, userID.String(), actorURL))
	return c.NoContent(http.StatusNoContent)
}

// handleAdminUnblockRemoteActor lifts one. Idempotent.
//
// The actor is a QUERY parameter for the same reason it is on the viewer route:
// a DELETE carries no body in this API and an actor URL cannot be a path
// segment. It is matched VERBATIM against the stored URL — no handle resolution
// runs here, so a WebFinger that has since started failing can never strand an
// admin with a block they cannot lift.
func (s *Server) handleAdminUnblockRemoteActor(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	actor := strings.TrimSpace(c.QueryParam("actor"))
	if actor == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, `"actor" is required`)
	}
	if _, err := s.fedsvc.UnblockRemoteActorInstanceWide(c.Request().Context(), actor); err != nil {
		return err
	}
	s.auditEvent(c, remoteActorAuditEvent(observability.ActionRemoteActorUnblock, userID.String(), actor))
	return c.NoContent(http.StatusNoContent)
}

// handleAdminListBlockedRemoteActors lists the instance-wide blocks, newest
// first.
func (s *Server) handleAdminListBlockedRemoteActors(c echo.Context) error {
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.fedsvc.ListBlockedRemoteActors(c.Request().Context(), page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]blockedRemoteActorView, 0, len(items))
	for _, it := range items {
		views = append(views, blockedRemoteActorView{
			ActorURL:  it.ActorURL,
			Handle:    it.Handle,
			Domain:    it.Domain,
			Reason:    it.Reason,
			BlockedAt: it.BlockedAt,
		})
	}
	return c.JSON(http.StatusOK, blockedRemoteActorListResponse{Actors: views, pageMeta: page.meta(total)})
}
