package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/live"
)

// Moderator termination of a live broadcast — the control A26 measured as
// missing outright ("a moderator watching an instance-damaging broadcast can
// change a global setting, delete the account, or nothing").
//
// It is an /admin route rather than a widened owner route on purpose. The owner
// routes are scoped by ownership and answer 404 to everyone else, which is
// exactly right for them: a moderator is not a co-owner, their action needs a
// reason and an audit row, and its outcome is different (the reason is stored
// and shown to the creator). Two routes, one mechanism — see internal/live's
// Terminate, which both call.

// terminateLiveRequest is the POST /admin/live/{id}/terminate body.
type terminateLiveRequest struct {
	// ReasonCode is required and must be one of live.TerminationReasons. It is
	// the only part that reaches the audit trail.
	ReasonCode string `json:"reason_code"`
	// Reason is the moderator's own words, shown to the creator alongside the
	// code's sentence. Optional.
	Reason string `json:"reason"`
}

func (r terminateLiveRequest) Validate() []FieldError {
	var errs []FieldError
	code := strings.TrimSpace(r.ReasonCode)
	switch {
	case code == "":
		errs = append(errs, FieldError{Field: "reason_code", Message: "is required"})
	case !live.ValidTerminationReason(code):
		// The message names the set rather than saying "invalid": a moderator
		// typing this into a console needs to know what IS accepted, and the
		// list is short enough to state.
		errs = append(errs, FieldError{
			Field:   "reason_code",
			Message: "must be one of " + strings.Join(terminationReasonStrings(), ", "),
		})
	}
	if len(r.Reason) > 1000 {
		errs = append(errs, FieldError{Field: "reason", Message: "must be at most 1000 characters"})
	}
	return errs
}

// terminationReasonStrings renders the allow-list for an error message and for
// the OpenAPI enum check, from the one place the set is defined.
func terminationReasonStrings() []string {
	out := make([]string, 0, len(live.TerminationReasons))
	for _, r := range live.TerminationReasons {
		out = append(out, string(r))
	}
	return out
}

// terminateLiveResponse tells the moderator what actually happened.
//
// It is not a bare 204, and that is the point. The three parts of a termination
// fail independently: the broadcast always ends, but the key rotation can fail
// and the publisher's socket can survive (no control surface configured, or the
// ingest not answering). A moderator who is told only "ok" while the streamer is
// still connected and writing to the operator's disk has been misinformed at the
// exact moment it matters most.
type terminateLiveResponse struct {
	// State is what the stream is now — "ended", or "offline" for a permanent
	// stream, which is reusable and therefore not ended.
	State string `json:"state"`
	// PublisherDisconnected is false when the RTMP socket may still be open.
	PublisherDisconnected bool `json:"publisher_disconnected"`
	// StreamKeyRotated is false only if the rotation itself failed — the one
	// outcome in which the publisher could reconnect.
	StreamKeyRotated bool `json:"stream_key_rotated"`
	// Detail explains a partial outcome in a sentence a moderator can act on.
	// Absent when everything landed.
	Detail string `json:"detail,omitempty"`
}

// handleTerminateLiveStream ends a live broadcast as a moderation action.
// Behind requireAuth + requireRole(admin, moderator).
func (s *Server) handleTerminateLiveStream(c echo.Context) error {
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "live stream not found")
	if err != nil {
		return err
	}
	var in terminateLiveRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if s.livesvc == nil {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	res, err := s.livesvc.Terminate(c.Request().Context(), id, live.TerminateInput{
		Source:     live.SourceModerator,
		ActorID:    userID,
		ActorRole:  role,
		ReasonCode: strings.TrimSpace(in.ReasonCode),
		Reason:     strings.TrimSpace(in.Reason),
	})
	if err != nil {
		return liveTerminateError(err)
	}
	return c.JSON(http.StatusOK, s.terminateView(res))
}

// handleEndOwnLiveStream is the CREATOR's own "end stream", which did not exist
// before this: an owner could edit or delete a stream but had no way to end a
// broadcast in progress, so the only ways off the air were to stop the encoder
// (leaving the row live until the stop hook or the watchdog caught up) or to
// delete the stream and lose its replay with it.
//
// Same mechanism as the moderator's, minus the reason and minus the audit
// action's actor semantics: the row records terminated_at and nothing else, so
// the creator's own end can never be mistaken for a takedown by the UI that
// renders it. Behind requireAuth; owner or a channel content manager only, and
// 404 (not 403) to anyone else, matching every other owner-scoped live route.
func (s *Server) handleEndOwnLiveStream(c echo.Context) error {
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "live stream not found")
	if err != nil {
		return err
	}
	notFound := echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	if s.livesvc == nil {
		return notFound
	}
	ctx := c.Request().Context()
	stream, err := s.livesvc.Get(ctx, id)
	if err != nil || (stream.OwnerID != userID && !s.canManageChannelContent(ctx, userID, stream.ChannelID)) {
		return notFound
	}
	// The actor is carried even though the ROW stores none: A26 measured this
	// route landing no audit row of any kind, so the one deliberate end of a
	// broadcast that a person performs left no trace at all. live.SourceOwner is
	// what keeps it off the row and out of the moderation action.
	res, err := s.livesvc.Terminate(ctx, id, live.TerminateInput{
		Source:    live.SourceOwner,
		ActorID:   userID,
		ActorRole: role,
	})
	if err != nil {
		return liveTerminateError(err)
	}
	return c.JSON(http.StatusOK, s.terminateView(res))
}

// terminateView renders the outcome, including the sentence for a partial one.
func (s *Server) terminateView(res live.TerminateResult) terminateLiveResponse {
	state := live.StateEnded
	if res.Stream.Permanent {
		state = live.StateOffline
	}
	out := terminateLiveResponse{
		State:                 state,
		PublisherDisconnected: res.PublisherDropped,
		StreamKeyRotated:      res.KeyRotated,
	}
	switch {
	case !res.KeyRotated:
		// Worst case first: the stream is off the air but the credential that
		// started it still works, so the publisher can simply reconnect.
		out.Detail = "The broadcast was ended, but rotating the stream key FAILED — the publisher's existing key still works and they can start broadcasting again. Rotate it from the stream's settings."
	case !res.ControlConfigured:
		out.Detail = "The broadcast was ended and the stream key rotated, but this instance has no ingest control surface (LIVE_INGEST_CONTROL_URL is unset), so the publisher's connection was left open. They cannot start a new broadcast, but they are still uploading to this server until they stop."
	case errors.Is(res.DropError, live.ErrIngestNoPublisher):
		// The honest reading of a drop that returned 0. Before this it was
		// reported as a confirmed disconnect, which is what a hook-only phantom
		// session — flipped live by a valid hook with no RTMP publish — always
		// looks like, and what a drop that reaches the wrong nginx worker looked
		// like for the whole of A26.
		out.Detail = "The broadcast was ended and the stream key rotated. The media server reported that NO publisher was connected under this stream, so nothing was disconnected — either they had already stopped, or this session was never actually publishing."
	case !res.PublisherDropped:
		out.Detail = "The broadcast was ended and the stream key rotated, but the media server did not confirm disconnecting the publisher. They cannot start a new broadcast; check the ingest if their connection persists."
	}
	return out
}

// liveTerminateError maps the service's sentinels onto the API's envelope.
func liveTerminateError(err error) error {
	switch {
	case errors.Is(err, live.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	case errors.Is(err, live.ErrNotLive):
		// 409 rather than 404: the stream is real and the caller's goal — it is
		// not broadcasting — already holds. A 404 would send a moderator hunting
		// for a stream that is sitting right there.
		return echo.NewHTTPError(http.StatusConflict, "this stream is not currently live")
	case errors.Is(err, live.ErrInvalidTerminationReason):
		return &ValidationError{Fields: []FieldError{{
			Field:   "reason_code",
			Message: "must be one of " + strings.Join(terminationReasonStrings(), ", "),
		}}}
	default:
		return err
	}
}
