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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/httpsig"
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
		// are cancelled (dead-lettered) instead of sent.
		if blocked, err := s.repo.IsInstanceBlocked(ctx, hostOf(row.InboxUrl)); err == nil && blocked {
			_ = s.repo.FailDelivery(ctx, sqlcgen.FailDeliveryParams{
				ID:        row.ID,
				LastError: "cancelled: destination instance is blocked",
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
	d := deliveryBaseBackoff
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= maxDeliveryBackoff {
			return maxDeliveryBackoff
		}
	}
	return d
}

// enqueueChannelDelivery queues an activity payload to inboxURL, to be signed as
// the given local channel at send time.
func (s *Service) enqueueChannelDelivery(ctx context.Context, channelID uuid.UUID, channelHandle, inboxURL string, payload []byte) error {
	return s.repo.EnqueueDelivery(ctx, sqlcgen.EnqueueDeliveryParams{
		InboxUrl:             inboxURL,
		Payload:              payload,
		SigningChannelID:     pgtype.UUID{Bytes: channelID, Valid: true},
		SigningChannelHandle: channelHandle,
	})
}

// enqueueAccountDelivery queues an activity payload to inboxURL, to be signed as
// the given local user's ACCOUNT actor at send time (outbound remote-channel
// Follow/Undo — remote-content §3).
func (s *Service) enqueueAccountDelivery(ctx context.Context, userID uuid.UUID, username, inboxURL string, payload []byte) error {
	return s.repo.EnqueueDelivery(ctx, sqlcgen.EnqueueDeliveryParams{
		InboxUrl:        inboxURL,
		Payload:         payload,
		SigningUserID:   pgtype.UUID{Bytes: userID, Valid: true},
		SigningUsername: username,
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
