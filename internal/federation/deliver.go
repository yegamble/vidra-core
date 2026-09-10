package federation

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/httpsig"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/retry"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/urlsafety"
)

// deliveryLeaseSeconds is how long a claimed row stops being due. Delivering an activity is one outbound HTTP POST, so
// the lease only has to outlast one attempt; it is generous rather than tight so
// a worker killed mid-request does not have its row re-claimed while the remote
// side is still processing the first attempt. A crashed worker's row becomes due
// again on its own once the lease elapses -- that is the whole recovery
// mechanism, and it needs no boot-time sweep and no assumption about who else is
// alive.
const deliveryLeaseSeconds = 300

// DeliveryCancelledByPolicy is the last_error stamped on an outbound delivery
// this instance CHOSE not to send, because its destination was on the admin
// blocklist when the row came due.
//
// It is exported because /admin/system has to be able to tell those rows from
// dead letters, and the difference is not cosmetic: a dead letter is a delivery
// that walked the whole retry ladder and never landed — a peer this instance
// wanted to reach and could not — while one of these never left on purpose and
// is waiting for an unblock to resume it (RedeliverAfterUnblock). The A29
// rehearsal-3 lab watched six of them counted as dead letters and reported to
// the operator as "peers did not accept what it sent", which is the exact
// opposite of what happened.
const DeliveryCancelledByPolicy = "cancelled: destination instance is blocked"

const (
	// maxDeliveryAttempts is how many times a delivery is retried before it is
	// dead-lettered (state 'failed').
	maxDeliveryAttempts = 6
	// deliveryBaseBackoff is the first retry delay; it doubles each attempt.
	deliveryBaseBackoff = 30 * time.Second
	// maxDeliveryBackoff caps the exponential backoff.
	maxDeliveryBackoff = 6 * time.Hour
	// maxLastErrorLen bounds the stored last_error string.
	maxLastErrorLen = 500
	// deliveryCancelledBlocked is the last_error a delivery carries when it was
	// never sent because its destination was on the admin blocklist. It is an
	// EXACT MATCH KEY, not just a message: RedeliverAfterUnblock finds the rows
	// to resume by it, and /admin/system counts them apart from real dead
	// letters by it, so every reader must use the same string and it must not
	// drift into something formatted per row.
	deliveryCancelledBlocked = DeliveryCancelledByPolicy
	// maxRedeliverAfterUnblock caps one unblock's resumption. A block window
	// can be arbitrarily long, and an admin's DELETE must not turn into an
	// unbounded scan-and-update; the remainder stays cancelled rather than the
	// request stalling, which the log line says.
	maxRedeliverAfterUnblock = 500
)

// DrainDeliveries claims up to limit due deliveries and attempts each: on success
// it is marked delivered; on failure it is rescheduled with exponential backoff,
// or dead-lettered after maxDeliveryAttempts. Returns the number delivered. Only
// the claim query error is returned (per-delivery failures are persisted, not
// surfaced). Intended to be called on a ticker by a single worker.
func (s *Service) DrainDeliveries(ctx context.Context, limit int) (int, error) {
	rows, err := s.repo.ClaimDueDeliveries(ctx, sqlcgen.ClaimDueDeliveriesParams{
		BatchSize:    int32(limit),
		LeaseSeconds: deliveryLeaseSeconds,
	})
	if err != nil {
		return 0, err
	}
	delivered := 0
	for _, row := range rows {
		// Instance blocklist (remote-content §8): deliveries to a blocked domain
		// are cancelled (dead-lettered) instead of sent — EXCEPT the ones whose
		// whole content is "stop" (A29-F5).
		//
		// A29 measured the failure this exception closes: an unfollow issued
		// while the destination was blocked deleted the local row (local intent
		// wins) while its Undo was cancelled, so the remote kept counting the
		// follower and kept delivering to an inbox that no longer wanted it — a
		// ghost follower with no reconciliation path. A block is a refusal to
		// RECEIVE, and cancelling the one message that reduces future contact
		// makes the block leakier, not tighter.
		if blocked, err := s.repo.IsInstanceBlocked(ctx, hostOf(row.InboxUrl)); err == nil && blocked && !severingActivity(row.Payload) {
			_ = s.repo.FailDelivery(ctx, sqlcgen.FailDeliveryParams{
				ID:        row.ID,
				LastError: deliveryCancelledBlocked,
			})
			continue
		}
		if err := s.attemptDelivery(ctx, row); err != nil {
			s.recordDeliveryFailure(ctx, row, err)
			continue
		}
		_ = s.repo.MarkDeliveryDelivered(ctx, row.ID)
		delivered++
	}
	return delivered, nil
}

// attemptDelivery signs the queued payload as its signing actor (a local
// channel, or a local user's ACCOUNT actor for follow activities) and POSTs it.
func (s *Service) attemptDelivery(ctx context.Context, row sqlcgen.ClaimDueDeliveriesRow) error {
	var (
		signer httpsig.Signer
		err    error
	)
	switch {
	case row.SigningChannelID.Valid:
		signer, err = s.channelActorSigner(ctx, uuid.UUID(row.SigningChannelID.Bytes), row.SigningChannelHandle)
	case row.SigningUserID.Valid:
		signer, err = s.accountActorSigner(ctx, uuid.UUID(row.SigningUserID.Bytes), row.SigningUsername)
	default:
		return errors.New("federation: delivery has no signing actor")
	}
	if err != nil {
		return err
	}
	return s.deliverActivity(ctx, signer, row.InboxUrl, row.Payload)
}

// recordDeliveryFailure reschedules with backoff, or dead-letters after the cap.
func (s *Service) recordDeliveryFailure(ctx context.Context, row sqlcgen.ClaimDueDeliveriesRow, cause error) {
	attempts := int(row.Attempts) + 1
	msg := cause.Error()
	if len(msg) > maxLastErrorLen {
		msg = msg[:maxLastErrorLen]
	}
	if attempts >= maxDeliveryAttempts {
		_ = s.repo.FailDelivery(ctx, sqlcgen.FailDeliveryParams{ID: row.ID, LastError: msg})
		return
	}
	_ = s.repo.RescheduleDelivery(ctx, sqlcgen.RescheduleDeliveryParams{
		ID:            row.ID,
		NextAttemptAt: time.Now().UTC().Add(deliveryBackoff(attempts)),
		LastError:     msg,
	})
}

// deliveryBackoff is deliveryBaseBackoff * 2^(attempts-1), capped.
func deliveryBackoff(attempts int) time.Duration {
	return retry.Backoff(attempts, deliveryBaseBackoff, maxDeliveryBackoff)
}

// enqueueChannelDelivery queues an activity payload to inboxURL, to be signed as
// the given local channel at send time.
func (s *Service) enqueueChannelDelivery(ctx context.Context, channelID uuid.UUID, channelHandle, inboxURL string, payload []byte) error {
	ids := observability.CorrelationFromContext(ctx)
	return s.repo.EnqueueDelivery(ctx, sqlcgen.EnqueueDeliveryParams{
		InboxUrl:             inboxURL,
		Payload:              payload,
		SigningChannelID:     pgconv.UUID(channelID),
		SigningChannelHandle: channelHandle,
		RequestID:            ids.RequestID,
		CorrelationID:        ids.CorrelationID,
	})
}

// enqueueAccountDelivery queues an activity payload to inboxURL, to be signed as
// the given local user's ACCOUNT actor at send time (outbound remote-channel
// Follow/Undo — remote-content §3).
func (s *Service) enqueueAccountDelivery(ctx context.Context, userID uuid.UUID, username, inboxURL string, payload []byte) error {
	// The identity of the REQUEST that produced the fan-out rides along on the
	// queue row (migration 0139), so N deliveries from one act are recognisably
	// one act and a stuck inbox traces back to what queued for it (A29-F10).
	ids := observability.CorrelationFromContext(ctx)
	return s.repo.EnqueueDelivery(ctx, sqlcgen.EnqueueDeliveryParams{
		InboxUrl:        inboxURL,
		Payload:         payload,
		SigningUserID:   pgconv.UUID(userID),
		SigningUsername: username,
		RequestID:       ids.RequestID,
		CorrelationID:   ids.CorrelationID,
	})
}

// SendAcceptFollow signs an `Accept` of the given inbound Follow, as the local
// channel, and delivers it to the follower's inbox. This is the outbound
// counterpart to the inbox Follow handling: it tells the remote its follow was
// accepted. The durable delivery queue (Slice 5) is the intended caller — it is a
// primitive here, not yet wired into the request path.
func (s *Service) SendAcceptFollow(ctx context.Context, channelID uuid.UUID, channelHandle, followerActorURL, followActivityURL string) error {
	follower, err := s.resolveRemoteActor(ctx, followerActorURL)
	if err != nil {
		return err
	}
	if follower.InboxUrl == "" {
		return errors.New("federation: follower has no inbox")
	}
	signer, err := s.channelActorSigner(ctx, channelID, channelHandle)
	if err != nil {
		return err
	}
	payload, err := s.buildAcceptFollow(channelHandle, followerActorURL, followActivityURL)
	if err != nil {
		return err
	}
	return s.deliverActivity(ctx, signer, follower.InboxUrl, payload)
}

// buildAcceptFollow renders the Accept activity for a channel accepting a Follow.
func (s *Service) buildAcceptFollow(channelHandle, followerActorURL, followActivityURL string) ([]byte, error) {
	channelActor := s.baseURL + "/video-channels/" + channelHandle
	accept := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       channelActor + "/activities/accept/" + uuid.NewString(),
		"type":     "Accept",
		"actor":    channelActor,
		"object": map[string]any{
			"id":     followActivityURL,
			"type":   "Follow",
			"actor":  followerActorURL,
			"object": channelActor,
		},
	}
	return json.Marshal(accept)
}

// buildRejectFollow renders the Reject activity for a channel refusing a
// Follow (federation_allow_channel_followers off, or an admin rejection from
// the follower-approval queue — config-parity W12). Same embedded-Follow shape
// as the Accept, per the AP convention PeerTube/Mastodon follow.
func (s *Service) buildRejectFollow(channelHandle, followerActorURL, followActivityURL string) ([]byte, error) {
	channelActor := s.baseURL + "/video-channels/" + channelHandle
	reject := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       channelActor + "/activities/reject/" + uuid.NewString(),
		"type":     "Reject",
		"actor":    channelActor,
		"object": map[string]any{
			"id":     followActivityURL,
			"type":   "Follow",
			"actor":  followerActorURL,
			"object": channelActor,
		},
	}
	return json.Marshal(reject)
}

// channelActorSigner builds an HTTP-signature signer for a local channel, minting
// its keypair if needed and unlocking the private key.
func (s *Service) channelActorSigner(ctx context.Context, channelID uuid.UUID, channelHandle string) (httpsig.Signer, error) {
	if _, err := s.ensureChannelKey(ctx, channelID); err != nil {
		return httpsig.Signer{}, err
	}
	row, err := s.repo.GetChannelActorKey(ctx, channelID)
	if err != nil {
		return httpsig.Signer{}, err
	}
	priv, err := s.unlockPrivateKey(row.PrivateKeyPem)
	if err != nil {
		return httpsig.Signer{}, err
	}
	return httpsig.Signer{
		KeyID: s.baseURL + "/video-channels/" + channelHandle + "#main-key",
		Priv:  priv,
	}, nil
}

// accountActorSigner is channelActorSigner for a local user's ACCOUNT (Person)
// actor — outbound follows are attributed to the user, not a channel.
func (s *Service) accountActorSigner(ctx context.Context, userID uuid.UUID, username string) (httpsig.Signer, error) {
	if _, err := s.ensureAccountKey(ctx, userID); err != nil {
		return httpsig.Signer{}, err
	}
	row, err := s.repo.GetAccountActorKey(ctx, userID)
	if err != nil {
		return httpsig.Signer{}, err
	}
	priv, err := s.unlockPrivateKey(row.PrivateKeyPem)
	if err != nil {
		return httpsig.Signer{}, err
	}
	return httpsig.Signer{
		KeyID: s.baseURL + "/accounts/" + username + "#main-key",
		Priv:  priv,
	}, nil
}

// unlockPrivateKey returns the RSA private key from a stored actor key: it opens
// the secretbox envelope when the value is sealed (else treats it as raw dev PEM),
// then parses the PKCS#8 PEM.
func (s *Service) unlockPrivateKey(stored string) (*rsa.PrivateKey, error) {
	pemStr := stored
	if secretbox.IsSealed(stored) {
		if s.cipher == nil {
			return nil, errors.New("federation: key is sealed but no FEDERATION_KEY_KEK is configured")
		}
		raw, err := s.cipher.Open(stored)
		if err != nil {
			return nil, err
		}
		pemStr = string(raw)
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("federation: invalid private key PEM")
	}
	// PeerTube actor keys use PKCS#1; retain their identity after import.
	if block.Type == "RSA PRIVATE KEY" {
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("federation: parse private key: %w", err)
		}
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("federation: parse private key: %w", err)
	}
	rk, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("federation: private key is not RSA")
	}
	return rk, nil
}

// deliverActivity signs and POSTs an activity payload to a remote inbox through
// the SSRF guard.
func (s *Service) deliverActivity(ctx context.Context, signer httpsig.Signer, inboxURL string, payload []byte) error {
	guard := urlsafety.Guard{AllowPrivate: s.allowPrivateFetch}
	target, err := guard.ValidateURL(inboxURL)
	if err != nil {
		return fmt.Errorf("federation: unsafe inbox URL: %w", err)
	}
	client := s.fetchClient
	if client == nil {
		client = guard.NewClient(remoteFetchTimeout)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/activity+json")
	if err := signer.Sign(req, payload); err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("federation: deliver: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("federation: delivery returned status %d", resp.StatusCode)
	}
	return nil
}

// severingActivity reports whether a queued payload's whole purpose is to END a
// relationship — an Undo (of a Follow) or a Reject (of one). Those are the only
// activities a destination block must not cancel: they carry no content, they
// reduce future contact rather than creating it, and swallowing one strands the
// remote side in a relationship this instance has already left.
//
// It reads the envelope's `type` only; a malformed payload is treated as
// ordinary (not severing), so the conservative answer is the default.
func severingActivity(payload []byte) bool {
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return false
	}
	return env.Type == "Undo" || env.Type == "Reject"
}

// RedeliverAfterUnblock re-enqueues the outbound activities this instance
// CANCELLED while domain was blocked, so lifting a block resumes the half of the
// conversation this instance is still holding (A29-F4).
//
// WHAT A BLOCK ACTUALLY DID, AND WHICH HALF IS REPAIRABLE. A block is symmetric
// in effect and asymmetric in recoverability. Inbound activities from the
// blocked instance were answered 202 and dropped: the remote considers them
// delivered and will never resend, so they are gone for good and no amount of
// unblocking brings them back — A29 recorded that, and it stays true. But this
// instance's OWN outbound deliveries were cancelled with their payloads intact
// in federation_deliveries, marked failed with deliveryCancelledBlocked. Those
// rows are still here. Leaving them where they are means a remote follower
// silently misses every video published during the block, forever, with no
// reconciliation path — the same shape of permanent divergence A29-F5 closed for
// severing activities.
//
// WHAT IS RESUMED, AND WHY NOT EVERYTHING:
//
//   - Delete is always resumed. It can only reduce what the remote holds, and a
//     Delete that never arrived is the worst thing to drop: the remote keeps
//     serving a copy of something this instance has removed.
//   - Accept is always resumed. It carries no content and its absence strands
//     the remote in a pending follow this instance already granted.
//   - Create and Update are resumed ONLY IF the object still exists and is
//     still public. Their payload is a SNAPSHOT taken when the activity was
//     queued, and a video that went private, was unpublished, was deleted, or
//     whose channel has since opted out of ActivityPub must not be published to
//     a remote server by a message the block happened to delay. This is the
//     whole reason redelivery is not a bulk UPDATE.
//   - That object is a VIDEO or a COMMENT. The rehearsal-3 lab measured the
//     comment half missing: a Create{Note} cancelled inside a clean block
//     window was left cancelled on unblock, because the object id is
//     `…/comments/<uuid>` and the resolver only knew `…/videos/<uuid>`, so a
//     follower's mirrored thread stayed permanently short every comment written
//     while the block stood — the same permanent divergence one level down.
//   - Anything else is left cancelled. Undo and Reject were never cancelled in
//     the first place (severingActivity), and an unrecognised type is not
//     something to replay on a guess.
//
// The window is the BLOCK's: blockedAt is the moment the block began (the
// timestamp UnblockInstance returns as it deletes the row), so a delivery that
// failed for an unrelated reason, or was cancelled by an earlier block already
// lifted and dealt with, is not this unblock's to resume.
//
// It returns how many rows were re-enqueued. It is best-effort by construction:
// a row that cannot be requeued is left cancelled, which is exactly where it
// already was.
func (s *Service) RedeliverAfterUnblock(ctx context.Context, domain string, blockedAt time.Time) (int, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if s == nil || domain == "" {
		return 0, nil
	}
	rows, err := s.repo.ListCancelledDeliveriesForRedelivery(ctx, sqlcgen.ListCancelledDeliveriesForRedeliveryParams{
		CancelReason: deliveryCancelledBlocked,
		Since:        blockedAt,
		// A prefilter only, so the cap below is spent on THIS domain's rows
		// rather than on other still-blocked domains'. hostOf below is still
		// what decides.
		HostLike:    domain,
		ResultLimit: maxRedeliverAfterUnblock,
	})
	if err != nil {
		return 0, err
	}
	requeued := 0
	for _, row := range rows {
		// The destination is matched with the SAME function that decided to
		// cancel it (hostOf, used by DrainDeliveries against the blocklist), so
		// the two cannot disagree about what host an inbox URL belongs to.
		if hostOf(row.InboxUrl) != domain {
			continue
		}
		ok, err := s.redeliverable(ctx, row.Payload)
		if err != nil {
			return requeued, err
		}
		if !ok {
			continue
		}
		if _, err := s.repo.RequeueCancelledDelivery(ctx, row.ID); err != nil {
			return requeued, err
		}
		requeued++
	}
	return requeued, nil
}

// redeliverable decides whether one cancelled payload may be sent now. See
// RedeliverAfterUnblock for the reasoning; the conservative answer (do not
// resend) is the default for every shape this does not recognise.
func (s *Service) redeliverable(ctx context.Context, payload []byte) (bool, error) {
	var env struct {
		Type   string          `json:"type"`
		Object json.RawMessage `json:"object"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return false, nil
	}
	switch env.Type {
	case "Delete", "Accept":
		return true, nil
	case "Create", "Update":
	default:
		return false, nil
	}
	if videoID, ok := s.localVideoIDFromObject(env.Object); ok {
		return s.videoStillPublishable(ctx, videoID)
	}
	if commentID, ok := s.localCommentIDFromObject(env.Object); ok {
		return s.commentStillPublishable(ctx, commentID)
	}
	// A Create/Update whose object this instance cannot resolve to one of its
	// own videos or comments cannot be checked, and an unverifiable snapshot is
	// not worth publishing late.
	return false, nil
}

// videoStillPublishable is the video arm of redeliverable: the snapshot may go
// out only if the thing it describes is still the thing a stranger may see.
func (s *Service) videoStillPublishable(ctx context.Context, videoID uuid.UUID) (bool, error) {
	v, ch, found, err := s.loadVideoAndChannel(ctx, videoID)
	if err != nil || !found {
		return false, err
	}
	return ch.ActivitypubEnabled && v.Privacy == "public" && v.State == "published", nil
}

// commentStillPublishable is the comment arm, and it is deliberately the video
// predicate with the comment's own existence clause in front of it.
//
// THE PREDICATE, clause by clause, each of which means "leave it cancelled":
//
//   - the comment is GONE. A comment deleted by its author or removed by a
//     moderator is a hard DELETE here, so the row simply is not found. Sending
//     a Create for it would plant, on every follower instance, a comment this
//     instance no longer holds and can no longer retract — the Delete that
//     would have retracted it was cancelled by the same block and is not
//     resumable, because its object no longer resolves.
//   - the comment is TOMBSTONED (`deleted_at`). An account deletion empties the
//     body and stamps the row rather than removing it, so that reply threads
//     survive; every reader here sees "[deleted]". Broadcasting the original
//     body afterwards would un-delete it on someone else's server.
//   - the comment is REMOTE-AUTHORED. `federateComment` never fans those out —
//     this instance does not re-broadcast content signed by another server —
//     and a resume path that did would be a re-broadcast by the back door.
//   - the VIDEO is no longer public+published on an AP-enabled channel. Same
//     clause, same reasoning and the same helper as the video arm: a comment is
//     only ever federated as part of a public video's conversation, so a video
//     that went private takes its whole thread out of the fediverse with it.
//
// What it deliberately does NOT consult is a per-VIEWER block or mute. Those are
// read filters evaluated against one reader, and a fan-out to a remote inbox has
// no reader to evaluate them for; the receiving instance applies its own
// readers' filters to what it stores, which is the same division of labour every
// other outbound activity here follows.
func (s *Service) commentStillPublishable(ctx context.Context, commentID uuid.UUID) (bool, error) {
	c, err := s.repo.GetComment(ctx, commentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if c.DeletedAt.Valid || !c.UserID.Valid {
		return false, nil
	}
	return s.videoStillPublishable(ctx, c.VideoID)
}

// localCommentIDFromObject extracts THIS instance's comment id from an
// activity's object, on exactly the same terms localVideoIDFromObject applies to
// videos: the id must be this instance's own <baseURL>/comments/<uuid>, because
// a remote id that happens to end in a uuid we also hold is not ours to
// re-publish.
func (s *Service) localCommentIDFromObject(raw json.RawMessage) (uuid.UUID, bool) {
	return s.localIDFromObject(raw, "/comments/")
}

// localVideoIDFromObject extracts THIS instance's video id from an activity's
// object, which is either an embedded AS object with an "id" (Create/Update) or
// a bare id string (Delete). The id must be this instance's own
// <baseURL>/videos/<uuid>: an object belonging to anywhere else is not ours to
// re-publish, and matching on the suffix alone would accept a remote id that
// happened to end in a uuid we also have.
func (s *Service) localVideoIDFromObject(raw json.RawMessage) (uuid.UUID, bool) {
	return s.localIDFromObject(raw, "/videos/")
}

// localIDFromObject is the shared parser both resolvers use: an activity's
// object is either an embedded AS object with an "id" (Create/Update) or a bare
// id string (Delete), and the id must sit under this instance's own baseURL +
// path.
func (s *Service) localIDFromObject(raw json.RawMessage, path string) (uuid.UUID, bool) {
	if len(raw) == 0 {
		return uuid.Nil, false
	}
	var id string
	if err := json.Unmarshal(raw, &id); err != nil {
		var obj struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return uuid.Nil, false
		}
		id = obj.ID
	}
	prefix := s.baseURL + path
	if s.baseURL == "" || !strings.HasPrefix(id, prefix) {
		return uuid.Nil, false
	}
	parsed, err := uuid.Parse(strings.TrimPrefix(id, prefix))
	if err != nil {
		return uuid.Nil, false
	}
	return parsed, true
}
