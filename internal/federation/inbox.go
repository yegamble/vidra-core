package federation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrActorMismatch means a signed activity's `actor` is not the request signer —
// i.e. someone tried to act on another actor's behalf.
var ErrActorMismatch = errors.New("federation: activity actor does not match the signer")

// ErrSenderBlocked means the activity was refused because its origin instance is
// on the admin blocklist (A29-F4).
//
// It is a REPORTING signal, not a wire one: the caller still answers 202,
// because telling a blocked instance that it is blocked turns the blocklist into
// a probe target and invites evasion. What changes is on this side — the handler
// writes one audit row so the refusal is visible to the admin who asked for it,
// instead of being indistinguishable from acceptance.
var ErrSenderBlocked = errors.New("federation: sender instance is blocked")

// inboxActivity is the envelope we parse from an inbound ActivityPub activity.
type inboxActivity struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Actor  string          `json:"actor"`
	Object json.RawMessage `json:"object"`
}

// HandleInbox dispatches a signature-verified inbound activity. signerActorURL is
// the actor URL from the verified HTTP signature (fragment stripped); body is the
// raw request body (already signature- and digest-checked by the caller). It is
// idempotent (deduped by activity id) and returns nil for accepted-and-ignored
// activity types. A `Follow` of a local channel records an (auto-accepted) remote
// follow; sending the Accept back to the remote is the delivery slice.
func (s *Service) HandleInbox(ctx context.Context, signerActorURL string, body []byte) error {
	var act inboxActivity
	if err := json.Unmarshal(body, &act); err != nil {
		return ErrBadResource
	}
	if act.ID == "" || act.Type == "" {
		return ErrBadResource
	}
	// Instance blocklist (remote-content §8): activities from actors on a
	// blocked domain are dropped after signature verification — accepted (202)
	// but never dispatched, and not marked processed (an unblock lets a
	// redelivery through).
	if blocked, err := s.signerDomainBlocked(ctx, signerActorURL); err != nil {
		return err
	} else if blocked {
		return ErrSenderBlocked
	}
	// Idempotency: process each activity id at most once. Dispatch first, then mark,
	// so a failed dispatch is retried by the remote rather than silently dropped.
	if seen, err := s.repo.IsActivityProcessed(ctx, act.ID); err != nil {
		return err
	} else if seen {
		return nil
	}
	if err := s.dispatchActivity(ctx, act, signerActorURL); err != nil {
		return err
	}
	return s.repo.MarkActivityProcessed(ctx, act.ID)
}

func (s *Service) dispatchActivity(ctx context.Context, act inboxActivity, signerActorURL string) error {
	switch act.Type {
	case "Follow":
		return s.handleFollow(ctx, act, signerActorURL)
	case "Undo":
		return s.handleUndo(ctx, act, signerActorURL)
	case "Create":
		if objectType(act.Object) == "Note" {
			// federation_accept_remote_comments (config-parity W12): when the
			// gate is off, inbound remote comments are dropped — accepted and
			// marked processed, never stored. Runs AFTER the blocked-domain
			// check (HandleInbox). Not retroactive: existing comments stay.
			if !s.acceptRemoteCommentsEnabled() {
				s.logDroppedRemoteComment(act.Type, act.ID, signerActorURL)
				return nil
			}
			return s.handleCreateNote(ctx, act, signerActorURL)
		}
		return s.handleCreateVideo(ctx, act, signerActorURL)
	case "Update":
		if objectType(act.Object) == "Note" {
			// The comment gate also covers edits (an Update{Note} is comment
			// ingestion); Delete{Note} stays ungated — removals are always
			// honoured.
			if !s.acceptRemoteCommentsEnabled() {
				s.logDroppedRemoteComment(act.Type, act.ID, signerActorURL)
				return nil
			}
			return s.handleUpdateNote(ctx, act, signerActorURL)
		}
		// Update{Video} upserts under the same authority + follow-edge gate as
		// Create (remote-content §7).
		return s.handleCreateVideo(ctx, act, signerActorURL)
	case "Delete":
		return s.handleDelete(ctx, act, signerActorURL)
	case "Announce":
		return s.handleAnnounce(ctx, act, signerActorURL)
	case "Accept":
		return s.handleAccept(ctx, act, signerActorURL)
	case "Reject":
		return s.handleReject(ctx, act, signerActorURL)
	default:
		return nil // unhandled activity types are accepted-and-ignored
	}
}

// handleUndo reverses a previously-federated activity. Only Undo{Follow} (a remote
// actor un-following a local channel) is handled today; other Undo objects are
// accepted and ignored.
func (s *Service) handleUndo(ctx context.Context, act inboxActivity, signerActorURL string) error {
	var inner struct {
		Type   string          `json:"type"`
		Actor  string          `json:"actor"`
		Object json.RawMessage `json:"object"`
	}
	if err := json.Unmarshal(act.Object, &inner); err != nil || inner.Type != "Follow" {
		return nil // not an Undo{Follow} → ignore
	}
	// The undoer must be the request signer and (if named) the Follow's actor.
	if act.Actor != signerActorURL || (inner.Actor != "" && inner.Actor != signerActorURL) {
		return ErrActorMismatch
	}
	handle, ok := s.localChannelHandle(objectID(inner.Object))
	if !ok {
		return nil // not one of our channels → nothing to undo
	}
	// resolveLocalChannel, not GetChannelByHandle: after migration 0142 renames
	// a channel out of a namespace collision, the FROZEN handle is the address
	// every peer that already federated with it holds, and it is the one this
	// instance still advertises as the actor id. An inbound activity aimed at
	// that id must find the channel.
	ch, err := s.resolveLocalChannel(ctx, handle)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	return s.repo.DeleteRemoteFollow(ctx, sqlcgen.DeleteRemoteFollowParams{
		ChannelID:      ch.ID,
		RemoteActorUrl: signerActorURL,
	})
}

// handleFollow records a remote actor following one of our local channels,
// subject to the W12 policy gates: followers disallowed → Reject; approval
// required → the follow waits PENDING in the admin queue (no Accept yet);
// otherwise the shipped auto-accept, optionally followed by an auto
// follow-back.
func (s *Service) handleFollow(ctx context.Context, act inboxActivity, signerActorURL string) error {
	// The signer must be the Follow's actor — no following on another's behalf.
	if act.Actor != signerActorURL {
		return ErrActorMismatch
	}
	handle, ok := s.localChannelHandle(objectID(act.Object))
	if !ok {
		return nil // not a Follow of one of our channels (e.g. an account) → ignore for now
	}
	// Same as handleUndo: a Follow addressed at the frozen actor id of a
	// renamed channel is the ordinary case for every peer that followed before
	// the rename, and the A29 rehearsal 3 lab watched one answered 202 and
	// dropped because this lookup could not see the alias.
	ch, err := s.resolveLocalChannel(ctx, handle)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return nil // unknown local channel → ignore
		}
		return err
	}
	// Everything this instance SIGNS for the channel — the Accept, a Reject, an
	// auto follow-back — is signed as the actor id it publishes, whichever
	// spelling the peer used to reach us.
	handle = s.channelActorHandle(ctx, ch)
	// activitypub_enabled off (migration 0096): the channel does not federate.
	// Drop the inbound Follow — record nothing and send nothing (an AP-disabled
	// channel emits no outbound activity, so no Reject either).
	if !ch.ActivitypubEnabled {
		return nil
	}
	// The channel OWNER's per-remote-account block (A29-F7). Dropped rather
	// than Rejected, on the same reasoning as the instance blocklist: a Reject
	// would tell the blocked actor exactly what happened, and a follow that is
	// never recorded delivers nothing to them either way.
	if blocked, err := s.remoteActorBlockedBy(ctx, ch.OwnerID, signerActorURL); err != nil {
		return err
	} else if blocked {
		return nil
	}
	// federation_allow_channel_followers off: answer with a Reject and record
	// nothing. Existing followers are untouched (the gate is not retroactive).
	if !s.channelFollowersAllowed() {
		return s.enqueueRejectFollow(ctx, ch.ID, handle, signerActorURL, act.ID)
	}
	// federation_follower_approval on: the follow waits in the admin queue.
	// The Accept is delivered on approval; a Reject on rejection. An existing
	// row (accepted or already pending) stays as-is.
	if s.followerApprovalRequired() {
		return s.repo.InsertRemoteFollowPending(ctx, sqlcgen.InsertRemoteFollowPendingParams{
			ChannelID:         ch.ID,
			RemoteActorUrl:    signerActorURL,
			FollowActivityUrl: act.ID,
		})
	}
	if err := s.repo.InsertRemoteFollow(ctx, sqlcgen.InsertRemoteFollowParams{
		ChannelID:         ch.ID,
		RemoteActorUrl:    signerActorURL,
		FollowActivityUrl: act.ID,
	}); err != nil {
		return err
	}
	if err := s.enqueueAcceptFollow(ctx, ch.ID, handle, signerActorURL, act.ID); err != nil {
		return err
	}
	return s.maybeAutoFollowBack(ctx, ch.ID, handle, signerActorURL)
}

// enqueueAcceptFollow queues an Accept back to the follower's inbox (a durable,
// signed delivery). The follower was just fetched+cached during signature
// verification, so this reads the cache (no network in the request path); if it
// is somehow absent or lacks an inbox, the follow is still recorded and the Accept
// is simply not queued.
func (s *Service) enqueueAcceptFollow(ctx context.Context, channelID uuid.UUID, channelHandle, followerActorURL, followActivityURL string) error {
	follower, err := s.repo.GetRemoteActor(ctx, followerActorURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if follower.InboxUrl == "" {
		return nil
	}
	payload, err := s.buildAcceptFollow(channelHandle, followerActorURL, followActivityURL)
	if err != nil {
		return err
	}
	return s.enqueueChannelDelivery(ctx, channelID, channelHandle, follower.InboxUrl, payload)
}

// localChannelHandle returns the channel handle when actorURL is one of our local
// channel actor URLs (baseURL + /video-channels/<handle>), else ("", false).
func (s *Service) localChannelHandle(actorURL string) (string, bool) {
	prefix := s.baseURL + "/video-channels/"
	if !strings.HasPrefix(actorURL, prefix) {
		return "", false
	}
	handle := strings.TrimPrefix(actorURL, prefix)
	if handle == "" || strings.ContainsAny(handle, "/#?") {
		return "", false
	}
	return handle, true
}

// objectType extracts an activity object's type ("" for bare string objects).
func objectType(raw json.RawMessage) string {
	var obj struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	return obj.Type
}

// objectID extracts an activity object's id whether it is a bare string URL or an
// object with an "id" field.
func objectID(raw json.RawMessage) string {
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str
	}
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.ID
	}
	return ""
}
