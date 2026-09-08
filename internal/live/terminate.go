package live

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Ending a broadcast on purpose — the moderator's control A26 measured as
// missing, and the owner's own, which was missing too.
//
// THE SEQUENCE, and why it is this order:
//
//  1. END THE SESSION AND STORE THE REASON, in one statement. This is what
//     stops the broadcast: /live/{id}/hls 404s the moment the state is no
//     longer 'live' (liveStreamForHLS), the stream drops off every listing, and
//     every playback token outstanding against it dies with it — a live token
//     grants nothing once the stream is not live, which is the property that
//     makes revocation free.
//  2. ROTATE THE STREAM KEY. Without this the publisher simply reconnects: the
//     RTMP boundary authenticates a KEY, not a session, and the key they are
//     holding is still valid. Rotation happens BEFORE the drop deliberately —
//     a race where the publisher reconnects between the drop and the rotation
//     would put them straight back on air.
//  3. DROP THE PUBLISHER. Only now, when there is nothing left to reconnect to.
//     This is the step that needs a control path to the ingest and the only one
//     that can be unavailable; when it is, the broadcast is still off the air
//     and the key is still dead, and the caller is told the socket survived.
//  4. LET THE ORDINARY STOP PATH PUBLISH THE REPLAY. Termination never calls
//     RunReplay. The recording is still OPEN while the publisher holds the
//     socket, so replaying it here would publish a truncated file; the drop in
//     step 3 closes the socket, which fires the ingest's on_publish_done, which
//     is the existing disconnect→replay path that A26 measured working. The
//     stop hook re-asserting 'ended' is idempotent and — by construction in
//     SetLiveStreamState — does not clear the reason.
//
// What is NOT done: the stream row is not deleted and the account is not
// touched. Termination ends a BROADCAST. Deleting the stream would take the
// creator's replay with it, and an account action is a separate A16 decision
// with its own audit action and its own appeal.

// TerminationReason is a moderator's reason code — a closed set, mirrored by the
// CHECK constraint in migration 0141.
//
// It is a code and not free text because the audit trail cannot carry prose
// (A16): a code is what an operator filters the trail on and what the UI turns
// into a sentence in the creator's language. The moderator's own words are
// stored on the row instead, exactly where a video block stores its reason.
type TerminationReason string

// The reason codes. Each one has to be a distinct ACTION a moderator would take,
// not a shade of the same one — a picker with twelve near-synonyms gets whatever
// is at the top of the list.
const (
	ReasonPolicyViolation  TerminationReason = "policy_violation"
	ReasonCopyright        TerminationReason = "copyright"
	ReasonSensitiveContent TerminationReason = "sensitive_content"
	ReasonSpam             TerminationReason = "spam"
	ReasonHarassment       TerminationReason = "harassment"
	ReasonLegalRequest     TerminationReason = "legal_request"
	ReasonTechnical        TerminationReason = "technical"
	ReasonOther            TerminationReason = "other"
)

// TerminationReasons is the allow-list in display order. Exported so the HTTP
// layer can validate against it and the OpenAPI enum can be checked against it,
// rather than three places each spelling the set out.
var TerminationReasons = []TerminationReason{
	ReasonPolicyViolation,
	ReasonCopyright,
	ReasonSensitiveContent,
	ReasonSpam,
	ReasonHarassment,
	ReasonLegalRequest,
	ReasonTechnical,
	ReasonOther,
}

// ValidTerminationReason reports whether code is in the allow-list.
func ValidTerminationReason(code string) bool {
	for _, r := range TerminationReasons {
		if string(r) == code {
			return true
		}
	}
	return false
}

// maxTerminationReasonLen bounds the moderator's free text. Generous enough for
// a real explanation, small enough that the column is not a place to paste a
// log.
const maxTerminationReasonLen = 1000

// ErrNotLive means the stream exists but is not currently broadcasting, so there
// is nothing to terminate. Distinct from ErrNotFound: a moderator who gets this
// learns the stream is real and already off, which is the outcome they wanted.
var ErrNotLive = errors.New("live: stream is not live")

// ErrInvalidTerminationReason means the reason code is not in the allow-list.
var ErrInvalidTerminationReason = errors.New("live: unknown termination reason")

// TerminateInput describes one termination.
type TerminateInput struct {
	// ActorID is the moderator. uuid.Nil for the owner's own end-stream and for
	// a system close, both of which store no actor.
	ActorID uuid.UUID
	// ReasonCode is required for a moderator termination and empty for an
	// owner's own end.
	ReasonCode string
	// Reason is the moderator's free text. Optional; trimmed and capped.
	Reason string
}

// TerminateResult reports what actually happened, because the parts can fail
// independently and a caller that is told only "ok" cannot tell a moderator
// whether the streamer is still connected.
type TerminateResult struct {
	// Stream is the row as it stood BEFORE the termination (its owner, channel
	// and title — the caller needs them to notify and to audit).
	Stream Stream
	// KeyRotated is false only when the rotation itself failed, which is the one
	// partial outcome that matters: the broadcast is off the air but the
	// credential that started it is still live.
	KeyRotated bool
	// PublisherDropped is true when the ingest confirmed the socket is gone
	// (including "there was no publisher"). False means the publisher may still
	// be connected and writing to the operator's disk.
	PublisherDropped bool
	// DropError is why the drop did not happen. Nil when it did, or when no
	// control surface is configured at all (see ControlConfigured).
	DropError error
	// ControlConfigured is false when this instance has no
	// LIVE_INGEST_CONTROL_URL, which is a deployment fact rather than a failure
	// and is reported to the moderator as such.
	ControlConfigured bool
}

// Terminate ends a live broadcast: it flips the state and records the reason,
// rotates the stream key, and disconnects the publisher. See the sequence note
// at the top of this file for why in that order.
//
// It is the SAME mechanism for a moderator and for the stream's owner — the
// owner's own end simply carries no actor and no reason. There is deliberately
// not a second, gentler path for the owner: a creator ending their own broadcast
// wants the publisher disconnected and the key rotated just as much (OBS
// reconnects on its own, and a stream that reappears after the creator ended it
// is a bug wearing a feature's clothes).
func (s *Service) Terminate(ctx context.Context, id uuid.UUID, in TerminateInput) (TerminateResult, error) {
	st, err := s.Get(ctx, id)
	if err != nil {
		return TerminateResult{}, err
	}
	if st.State != StateLive {
		return TerminateResult{}, ErrNotLive
	}
	code := strings.TrimSpace(in.ReasonCode)
	if code != "" && !ValidTerminationReason(code) {
		return TerminateResult{}, ErrInvalidTerminationReason
	}
	reason := strings.TrimSpace(in.Reason)
	if len(reason) > maxTerminationReasonLen {
		reason = reason[:maxTerminationReasonLen]
	}

	// A permanent stream goes back to 'offline' and a one-shot to 'ended' — the
	// same choice the duration watchdog and the stop hook make, so a terminated
	// stream is in a state the rest of the system already understands.
	next := StateEnded
	if st.Permanent {
		next = StateOffline
	}
	params := sqlcgen.TerminateLiveStreamParams{
		ID:                id,
		State:             next,
		TerminationReason: reason,
	}
	if in.ActorID != uuid.Nil {
		params.TerminatedBy = pgconv.UUID(in.ActorID)
	}
	if code != "" {
		params.TerminationReasonCode = &code
	}
	if err := s.repo.TerminateLiveStream(ctx, params); err != nil {
		// Nothing has changed yet — this is the one failure that leaves the
		// stream broadcasting, so it is the caller's error rather than a
		// partial result.
		return TerminateResult{}, err
	}
	res := TerminateResult{Stream: st, ControlConfigured: s.control != nil}

	// Rotate the key. A failure here is logged loudly and reported, but does not
	// abort: the broadcast is already off the air, and returning an error would
	// tell the moderator the termination failed when the part they care about
	// worked.
	if _, kerr := s.RegenerateKey(ctx, id); kerr != nil {
		s.logger.ErrorContext(ctx, "live terminate: stream key rotation failed — the publisher's credential is still valid",
			"stream_id", id.String(), "error", kerr)
	} else {
		res.KeyRotated = true
	}

	// Drop the publisher. Only meaningful with a control surface; without one
	// this is the documented degrade, not an error.
	if s.control != nil {
		derr := s.control.DropPublisher(ctx, id.String())
		switch {
		case derr == nil, errors.Is(derr, ErrIngestNoPublisher):
			res.PublisherDropped = true
		default:
			res.DropError = derr
			s.logger.WarnContext(ctx, "live terminate: could not disconnect the publisher; the RTMP socket may still be writing segments and a recording",
				"stream_id", id.String(), "error", derr)
		}
	} else {
		s.logger.InfoContext(ctx, "live terminate: no ingest control surface configured (LIVE_INGEST_CONTROL_URL); the stream is off the air and its key is rotated, but the publisher's socket is untouched",
			"stream_id", id.String())
	}

	s.auditTerminate(ctx, id, in.ActorID, code, res)
	return res, nil
}

// auditTerminate writes the structured envelope for one termination.
//
// The reason CODE goes in, the moderator's prose does not: the trail is a
// security record with 400-day retention, and A16 settled that it does not carry
// free text. resource_id carries the stream — A26 measured every live audit row
// leaving it EMPTY with the id buried in free-text `reason`, which is why the
// audit filter could not target a stream. This row can.
func (s *Service) auditTerminate(ctx context.Context, id, actorID uuid.UUID, code string, res TerminateResult) {
	if s.auditor == nil {
		return
	}
	ev := audit.Event{
		Action:     observability.ActionLiveTerminate,
		Result:     observability.ResultSuccess,
		ResourceID: id.String(),
	}
	if actorID != uuid.Nil {
		ev.Actor = audit.ActorSnapshot{Kind: "user", ID: actorID.String()}
	} else {
		ev.Actor = audit.ActorSnapshot{Kind: "system"}
	}
	// The reason field stays a stable, filterable token. When the publisher
	// survived the drop that is appended, because "the broadcast ended but the
	// streamer is still connected" is a materially different outcome and an
	// operator reading the trail a week later cannot reconstruct it otherwise.
	reason := code
	if reason == "" {
		reason = "owner_ended"
	}
	if !res.PublisherDropped {
		reason += " publisher_not_dropped"
	}
	if !res.KeyRotated {
		reason += " key_not_rotated"
	}
	ev.Reason = reason
	_ = s.auditor.Record(ctx, ev)
}
