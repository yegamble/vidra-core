package federation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrActorMismatch means a signed activity's `actor` is not the request signer —
// i.e. someone tried to act on another actor's behalf.
var ErrActorMismatch = errors.New("federation: activity actor does not match the signer")

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
	default:
		// Undo / Create / Announce / Delete land in later slices; accept & ignore.
		return nil
	}
}

// handleFollow records a remote actor following one of our local channels.
func (s *Service) handleFollow(ctx context.Context, act inboxActivity, signerActorURL string) error {
	// The signer must be the Follow's actor — no following on another's behalf.
	if act.Actor != signerActorURL {
		return ErrActorMismatch
	}
	handle, ok := s.localChannelHandle(objectID(act.Object))
	if !ok {
		return nil // not a Follow of one of our channels (e.g. an account) → ignore for now
	}
	ch, err := s.repo.GetChannelByHandle(ctx, handle)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // unknown local channel → ignore
		}
		return err
	}
	return s.repo.InsertRemoteFollow(ctx, sqlcgen.InsertRemoteFollowParams{
		ChannelID:         ch.ID,
		RemoteActorUrl:    signerActorURL,
		FollowActivityUrl: act.ID,
	})
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
