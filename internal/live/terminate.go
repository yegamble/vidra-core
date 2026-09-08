package live

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/admin"
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

// auditResourceLiveStream is the audit trail's resource type for a live stream,
// in the snake_case singular convention the rest of the trail uses ("video",
// "user", "watched_word_match"). The envelope validates it against
// ^[a-z][a-z0-9_.-]{0,63}$ and requires it whenever a resource id is present.
const auditResourceLiveStream = "live_stream"

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

// TerminationSource says WHO ended the broadcast, and it is one field rather
// than three because the three things it decides must never disagree: which
// audit action is written, what kind of actor writes it, and whether the row
// records a person's decision at all.
//
// The zero value is SourceModerator — a takedown, the strongest reading — so a
// caller that forgets to say cannot silently under-record a moderation action.
type TerminationSource string

const (
	// SourceModerator is a staff takedown: content.live.terminate, the actor's
	// user id AND role on the trail, and terminated_by + the reason code and
	// text on the row, which is what the creator is shown.
	SourceModerator TerminationSource = "moderator"
	// SourceOwner is the creator's (or a channel content manager's) own End
	// stream: content.live.ended, the actor on the trail, and a bare
	// terminated_at on the row. A26 measured this path writing NO audit row of
	// any kind — the one deliberate end of a broadcast that left no trace.
	SourceOwner TerminationSource = "owner"
	// SourceSystem is the live_max_duration_secs watchdog:
	// content.live.force_close with a system actor, and NOTHING on the row.
	// Storing terminated_at for it would make the creator's page say "You ended
	// this stream" — the owner's own copy — about a cut they did not make.
	SourceSystem TerminationSource = "system"
)

// SystemReasonMaxDuration is the audit reason code for the duration watchdog's
// force-close.
//
// It is deliberately NOT in TerminationReasons: that list is a moderator's
// picker and every code in it is stored on the row under migration 0141's CHECK
// constraint. This one reaches the audit trail and nothing else, so adding it to
// the picker would offer a moderator a reason no human ever chooses and adding
// it to the row would need a migration to say something the creator is not being
// told anyway.
const SystemReasonMaxDuration = "max_duration"

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
	// Source says who is ending the broadcast; see TerminationSource. The zero
	// value is a moderator takedown.
	Source TerminationSource
	// ActorID is the acting user — the moderator for a takedown, the creator or
	// their channel manager for an owner end. uuid.Nil for a system close, and
	// for a takedown by something that is not a person.
	//
	// It ALWAYS reaches the audit trail. Whether it is stored on the row as
	// terminated_by is decided by Source, because a creator ending their own
	// broadcast must never render as a takedown.
	ActorID uuid.UUID
	// ActorRole is the acting user's role at the time ("user", "moderator",
	// "admin"). A26 measured content.live.terminate landing with actor_kind=user
	// and an EMPTY actor_role, so the trail could not answer "was this staff?"
	// without a second query against a users table that may since have changed.
	ActorRole string
	// ReasonCode is required for a moderator termination, empty for an owner's
	// own end, and SystemReasonMaxDuration for the watchdog.
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
	// PublisherDropped is true ONLY when the ingest reported closing at least
	// one publisher connection. It used to be true for "there was no publisher"
	// as well, on the reading that an absent publisher is the outcome a drop
	// wanted — but A26 showed those are the same 200 on this module and that the
	// difference is the count, so a phantom session with no publisher at all was
	// reported to the moderator as a confirmed disconnect. False now means the
	// publisher may still be connected and writing to the operator's disk, or
	// there was never one; DropError says which.
	PublisherDropped bool
	// DroppedCount is how many publisher connections the ingest says it closed.
	// It rides the audit row, because "the drop returned 0" is the difference
	// between a termination that reached the publisher and one that did not, and
	// nothing else in the trail can distinguish them a week later.
	DroppedCount int
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
	if in.Source == "" {
		in.Source = SourceModerator
	}
	code := strings.TrimSpace(in.ReasonCode)
	// The allow-list is the MODERATOR's picker and the row's CHECK constraint. A
	// system close carries a code no human picks and stores it nowhere, so it is
	// not validated against a list it is deliberately absent from.
	if in.Source != SourceSystem && code != "" && !ValidTerminationReason(code) {
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
	if err := s.writeTerminatedState(ctx, id, next, in, code, reason); err != nil {
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
	//
	// The watchdog rotates too, and that is not incidental: without it OBS
	// reconnects on the key it is holding within seconds, the stream goes back on
	// air, and the next sweep cuts it again — an over-limit publisher would
	// bounce every 30 seconds instead of stopping. The cost is that a creator cut
	// by the limit must copy a fresh key from the Studio before going live again.
	if _, kerr := s.RegenerateKey(ctx, id); kerr != nil {
		s.logger.ErrorContext(ctx, "live terminate: stream key rotation failed — the publisher's credential is still valid",
			"stream_id", id.String(), "error", kerr)
	} else {
		res.KeyRotated = true
	}

	// Drop the publisher. Only meaningful with a control surface; without one
	// this is the documented degrade, not an error.
	if s.control != nil {
		n, derr := s.control.DropPublisher(ctx, id.String())
		res.DroppedCount = n
		switch {
		case derr == nil && n > 0:
			res.PublisherDropped = true
		case errors.Is(derr, ErrIngestNoPublisher):
			// Not an error and not a success: the ingest answered and said it
			// closed nothing. Either the publisher had already gone — the state
			// this was asking for — or the session was a hook-only phantom that
			// was never publishing. Reporting it as a disconnect is what A26
			// caught the old code doing while a publisher streamed on.
			res.DropError = derr
			s.logger.InfoContext(ctx, "live terminate: the ingest dropped no publisher; nobody was connected under this stream id",
				"stream_id", id.String())
		default:
			if derr == nil {
				derr = ErrIngestNoPublisher
			}
			res.DropError = derr
			s.logger.WarnContext(ctx, "live terminate: could not disconnect the publisher; the RTMP socket may still be writing segments and a recording",
				"stream_id", id.String(), "error", derr)
		}
	} else {
		s.logger.InfoContext(ctx, "live terminate: no ingest control surface configured (LIVE_INGEST_CONTROL_URL); the stream is off the air and its key is rotated, but the publisher's socket is untouched",
			"stream_id", id.String())
	}

	// The viewer set belongs to the broadcast that just ended, so it is dropped
	// here rather than by each caller: a stream that goes live again would
	// otherwise inherit up to a window of phantom viewers, and the watchdog —
	// which had no such cleanup at all — gets it for free.
	s.viewers.Reset(ctx, id)

	s.auditTerminate(ctx, id, in, code, res)
	return res, nil
}

// writeTerminatedState performs step 1 for the source that asked.
//
// A person's decision is recorded on the row (one statement, so a crash can
// never leave an ended stream with no reason); the watchdog's is not. That split
// is the whole reason this is a function: the row's termination columns are what
// the CREATOR is shown, and "You ended this stream" over a cut the instance made
// on a duration limit would be a lie told in the creator's own voice. What the
// watchdog leaves behind is exactly what it left behind before — an ended row —
// plus, now, an audit row that names the stream.
func (s *Service) writeTerminatedState(ctx context.Context, id uuid.UUID, next string, in TerminateInput, code, reason string) error {
	if in.Source == SourceSystem {
		return s.repo.SetLiveStreamState(ctx, sqlcgen.SetLiveStreamStateParams{ID: id, State: next})
	}
	params := sqlcgen.TerminateLiveStreamParams{
		ID:                id,
		State:             next,
		TerminationReason: reason,
	}
	// terminated_by names a MODERATOR only. An owner's own end stores a bare
	// timestamp, which is what keeps TerminatedByModerator() false and the
	// creator's page reading "You ended this stream".
	if in.Source == SourceModerator && in.ActorID != uuid.Nil {
		params.TerminatedBy = pgconv.UUID(in.ActorID)
	}
	if in.Source == SourceModerator && code != "" {
		params.TerminationReasonCode = &code
	}
	return s.repo.TerminateLiveStream(ctx, params)
}

// auditTerminate writes the structured envelope for one termination.
//
// The reason CODE goes in, the moderator's prose does not: the trail is a
// security record with 400-day retention, and A16 settled that it does not carry
// free text. resource_id carries the stream — A26 measured every live audit row
// leaving it EMPTY with the id buried in free-text `reason`, which is why the
// audit filter could not target a stream. This row can, and so, now, can the
// watchdog's force_close and the owner's own end, which wrote no row at all.
func (s *Service) auditTerminate(ctx context.Context, id uuid.UUID, in TerminateInput, code string, res TerminateResult) {
	if s.auditor == nil {
		return
	}
	ev := audit.Event{
		Action: in.Source.auditAction(),
		Result: observability.ResultSuccess,
		// ResourceType is not decoration: internal/audit's envelope REFUSES an
		// event that carries a resource id with no type, and Record's error is
		// discarded here on purpose (a moderation action must never fail because
		// the trail did). Without it this row was rejected before it reached the
		// table and vanished with no log line — the A26 rehearsal terminated a
		// real broadcast and found `content.live.terminate` absent from
		// audit_log while every other part of the termination had landed.
		ResourceType: auditResourceLiveStream,
		ResourceID:   id.String(),
	}
	if in.Source != SourceSystem && in.ActorID != uuid.Nil {
		// Role as well as id. The envelope allows a role only on a user actor,
		// and only the stable set ("user", "moderator", "admin"); anything else
		// would reject the whole event, so an unknown role is dropped rather
		// than being allowed to take the row down with it.
		ev.Actor = audit.ActorSnapshot{Kind: "user", ID: in.ActorID.String(), Role: auditActorRole(in.ActorRole)}
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
	switch {
	case res.ControlConfigured && errors.Is(res.DropError, ErrIngestNoPublisher):
		// Distinct from a failed drop: the ingest answered and there was nobody
		// under that name. A hook-only phantom session reads exactly like this.
		reason += " no_publisher"
	case !res.PublisherDropped:
		reason += " publisher_not_dropped"
	}
	if !res.KeyRotated {
		reason += " key_not_rotated"
	}
	ev.Reason = reason
	if res.ControlConfigured {
		// `count` is one of internal/audit's allow-listed metadata keys, so the
		// number the drop actually returned survives into the row instead of
		// being flattened into the boolean above.
		ev.Metadata = []audit.MetadataField{{Key: "count", Value: strconv.Itoa(res.DroppedCount)}}
	}
	_ = s.auditor.Record(ctx, ev)
}

// auditAction maps who ended the broadcast onto the trail's action. Three
// sources, three actions: a takedown, a creator's own end and a machine's cut
// are not the same event and an operator filtering the trail must not have to
// infer which one they are looking at from the actor kind.
func (src TerminationSource) auditAction() string {
	switch src {
	case SourceOwner:
		return observability.ActionLiveEnded
	case SourceSystem:
		return observability.ActionLiveForceClose
	default:
		return observability.ActionLiveTerminate
	}
}

// auditActorRole keeps only the roles internal/audit's envelope accepts. An
// unrecognised role is dropped, not passed through: the envelope rejects the
// whole event on an invalid role, and losing the role is much better than losing
// the row.
func auditActorRole(role string) string {
	switch role {
	case admin.RoleUser, admin.RoleModerator, admin.RoleAdmin:
		return role
	default:
		return ""
	}
}
