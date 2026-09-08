package federation

// Per-remote-ACCOUNT blocks (A29-F7).
//
// A29 measured the gap precisely: /me/blocks/{id} and /me/mutes/accounts/{id}
// both take a LOCAL user uuid, and muted_accounts.muted_id is a users FK, so the
// only control a viewer had against a remote PERSON was blocking their entire
// instance. That is a sledgehammer — one troll on a busy server costs the viewer
// every creator on it — and it is also the wrong shape: an instance block is an
// ADMIN action affecting everyone, while "I do not want to hear from this
// person" is one viewer's own decision.
//
// The block is keyed on the ACTOR URL, which is the identity the fediverse
// itself uses. Two consequences that are the point rather than side effects: a
// viewer can block an actor this instance has never cached (an actor is NAMED in
// an activity before it is ever resolved), and evicting the actor cache cannot
// silently lift a block.
//
// WHAT IT DOES, in three places:
//
//   - the viewer's feeds and reads exclude that actor's remote videos (the
//     NOT EXISTS clause added to every remote-video feed query);
//   - an inbound Create{Note} from that actor onto content the BLOCKER owns is
//     dropped rather than stored;
//   - an inbound Follow of a channel the BLOCKER owns is refused.
//
// WHAT IT DELIBERATELY DOES NOT DO: it does not hide the blocker from the
// blocked actor, and it does not stop delivery. Federation has no mechanism that
// could make either true — a remote server decides what it shows its own users —
// and pretending otherwise would be the worst kind of safety feature: one that
// reads as protection and is not.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/urlsafety"
)

// ErrRemoteActorRequired means the supplied identity was neither an actor URL
// nor a @user@domain handle.
var ErrRemoteActorRequired = errors.New("federation: a remote actor URL or @user@domain handle is required")

// RemoteActorBlock is one entry in a viewer's remote block list.
type RemoteActorBlock struct {
	ActorURL string
	// Handle is preferredUsername@domain when the actor is cached, else "".
	Handle    string
	Domain    string
	BlockedAt string
}

// ResolveRemoteActorIdentity turns the two identities a person can paste — a
// fediverse handle (@user@domain or user@domain) or an actor URL — into the
// canonical actor URL a block is keyed on.
//
// A HANDLE IS RESOLVED THROUGH WEBFINGER, not string-built, because the actor
// URL for user@domain is whatever that server says it is; guessing a shape would
// key the block on a URL that never appears in any activity. A URL is validated
// and SSRF-guarded but NOT dereferenced: a block must work against an actor that
// is offline, gone, or refusing us — which is exactly the actor a viewer is most
// likely to be blocking.
func (s *Service) ResolveRemoteActorIdentity(ctx context.Context, identity string) (string, error) {
	identity = strings.TrimSpace(identity)
	switch ClassifySearchQuery(identity) {
	case SearchQueryHandle:
		name, domain, _ := strings.Cut(strings.TrimPrefix(identity, "@"), "@")
		if strings.EqualFold(domain, s.domain()) {
			return "", ErrLocalFollowTarget
		}
		actorURL, err := s.webFingerRemote(ctx, name, domain)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrRemoteUnresolvable, err)
		}
		return actorURL, nil
	case SearchQueryURI:
		if strings.EqualFold(hostOf(identity), s.domain()) {
			return "", ErrLocalFollowTarget
		}
		guard := urlsafety.Guard{AllowPrivate: s.allowPrivateFetch}
		if _, err := guard.ValidateURL(identity); err != nil {
			return "", fmt.Errorf("%w: %w", ErrRemoteUnresolvable, err)
		}
		return identity, nil
	}
	return "", ErrRemoteActorRequired
}

// BlockRemoteActor records a viewer's block of one remote actor. Idempotent.
func (s *Service) BlockRemoteActor(ctx context.Context, blockerID uuid.UUID, actorURL string) error {
	return s.repo.BlockRemoteActor(ctx, sqlcgen.BlockRemoteActorParams{
		BlockerID: blockerID, RemoteActorUrl: actorURL,
	})
}

// UnblockRemoteActor lifts one. Idempotent — unblocking what was never blocked
// still succeeds, like every other block surface here.
func (s *Service) UnblockRemoteActor(ctx context.Context, blockerID uuid.UUID, actorURL string) error {
	_, err := s.repo.UnblockRemoteActor(ctx, sqlcgen.UnblockRemoteActorParams{
		BlockerID: blockerID, RemoteActorUrl: actorURL,
	})
	return err
}

// ListRemoteActorBlocks returns a viewer's remote blocks, newest first, with the
// total. A block whose actor this instance has never cached still lists, showing
// the URL rather than a handle — the alternative is a block the viewer cannot
// see or lift.
func (s *Service) ListRemoteActorBlocks(ctx context.Context, blockerID uuid.UUID, limit, offset int32) ([]RemoteActorBlock, int64, error) {
	rows, err := s.repo.ListRemoteActorBlocks(ctx, sqlcgen.ListRemoteActorBlocksParams{
		BlockerID: blockerID, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountRemoteActorBlocks(ctx, blockerID)
	if err != nil {
		return nil, 0, err
	}
	out := make([]RemoteActorBlock, 0, len(rows))
	for _, r := range rows {
		b := RemoteActorBlock{
			ActorURL:  r.RemoteActorUrl,
			Domain:    r.Domain,
			BlockedAt: r.CreatedAt.UTC().Format(rfc3339),
		}
		if r.PreferredUsername != "" && r.Domain != "" {
			b.Handle = r.PreferredUsername + "@" + r.Domain
		}
		out = append(out, b)
	}
	return out, total, nil
}

// rfc3339 is the wire time format every federation surface uses.
const rfc3339 = "2006-01-02T15:04:05Z07:00"

// remoteActorBlockedBy reports whether a specific local user blocks a remote
// actor.
//
// It is deliberately two reads on the inbound path rather than one: the cheap
// "does ANYONE block this actor?" question is asked first (an indexed existence
// check that is false for essentially every activity), and only then does the
// per-owner question run. Blocks are rare; inbound activities are not.
func (s *Service) remoteActorBlockedBy(ctx context.Context, ownerID uuid.UUID, actorURL string) (bool, error) {
	anyone, err := s.repo.IsRemoteActorBlockedByAnyone(ctx, actorURL)
	if err != nil || !anyone {
		return false, err
	}
	return s.repo.IsRemoteActorBlockedBy(ctx, sqlcgen.IsRemoteActorBlockedByParams{
		BlockerID: ownerID, RemoteActorUrl: actorURL,
	})
}

// videoOwnerBlocksRemoteActor reports whether the OWNER of a local video blocks
// the given remote actor — the gate on storing an inbound Note against it.
//
// The owner is the right party because a comment on a video is content the
// video's owner hosts and answers for. A viewer's own block already removes the
// commenter's videos from their feed; this is the half that stops a blocked
// person appearing under the blocker's own work.
func (s *Service) videoOwnerBlocksRemoteActor(ctx context.Context, videoID uuid.UUID, actorURL string) (bool, error) {
	v, err := s.repo.GetVideoByID(ctx, videoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	ch, err := s.repo.GetChannelByID(ctx, v.ChannelID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return s.remoteActorBlockedBy(ctx, ch.OwnerID, actorURL)
}
