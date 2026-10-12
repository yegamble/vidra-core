package video

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Video ownership changes (T21, migration 0165; PeerTube's "give ownership"):
// the owner offers a video to another local user, who accepts it into one of
// their own channels or refuses. A video's owner is derived from its channel,
// so the accept IS a channel move — the cross-owner counterpart of the
// same-owner move in UpdateForActor.

var (
	// ErrOwnershipUnavailable means no ownership repository is wired.
	ErrOwnershipUnavailable = errors.New("video: ownership changes are not available")
	// ErrRecipientNotFound means the named account cannot receive a video:
	// unknown, banned, deleted, blocked either way, or the giver themselves.
	// One error for all of them, so the give route cannot tell them apart.
	ErrRecipientNotFound = errors.New("video: no such recipient")
	// ErrOwnershipCooldown means the recipient refused an offer from this giver
	// within OwnershipRefusalCooldown.
	ErrOwnershipCooldown = errors.New("video: the recipient recently refused an offer")
	// ErrOwnershipNotFound means no such request exists for the caller: none
	// addressed to them (accept/refuse), or no pending one they made (cancel).
	ErrOwnershipNotFound = errors.New("video: ownership change not found")
	// ErrOwnershipPending means the video already has a pending request.
	ErrOwnershipPending = errors.New("video: an ownership change is already pending")
	// ErrNotSettled means the video's upload or processing has not finished.
	ErrNotSettled = errors.New("video: still uploading or processing")
)

// OwnershipRepository is the data access for ownership change requests.
// *sqlcgen.Queries satisfies it.
type OwnershipRepository interface {
	ResolveOwnershipRecipient(ctx context.Context, arg sqlcgen.ResolveOwnershipRecipientParams) (sqlcgen.ResolveOwnershipRecipientRow, error)
	CreateVideoOwnershipChange(ctx context.Context, arg sqlcgen.CreateVideoOwnershipChangeParams) (sqlcgen.VideoOwnershipChange, error)
	ExpireStaleVideoOwnershipChanges(ctx context.Context, arg sqlcgen.ExpireStaleVideoOwnershipChangesParams) (int64, error)
	HasPendingVideoOwnershipChange(ctx context.Context, videoID uuid.UUID) (bool, error)
	HasRecentOwnershipRefusal(ctx context.Context, arg sqlcgen.HasRecentOwnershipRefusalParams) (bool, error)
	CancelVideoOwnershipChange(ctx context.Context, arg sqlcgen.CancelVideoOwnershipChangeParams) (int64, error)
}

const (
	// OwnershipOfferWindow is how long a request stays answerable. An older
	// pending request is expired: it cannot be accepted, and it no longer
	// blocks a new offer of the same video.
	OwnershipOfferWindow = 30 * 24 * time.Hour
	// OwnershipRefusalCooldown is how long after a refusal the same giver may
	// not offer the same recipient another video, so a "no" is not answered by
	// a fresh push notification straight away.
	OwnershipRefusalCooldown = 24 * time.Hour
)

// WithOwnershipRepository wires ownership change requests.
func WithOwnershipRepository(r OwnershipRepository) Option {
	return func(s *Service) { s.own = r }
}

// settledForTransfer reports whether a video's upload has finished. A draft
// may still be receiving its file (an upload session owned by the GIVER) and a
// processing video is being finalized, so the bytes the recipient's quota is
// checked against are not known yet.
func settledForTransfer(state string) bool {
	return state != "draft" && state != "processing"
}

// GiveOwnership offers videoID to the local account named username. Only the
// video's owner may: an editor collaborator or staff member manages the video
// but cannot give it away (no actor class is exempt, as with moves; PeerTube
// also lets staff give). It returns the pending request and whether the
// recipient should be notified — false when they muted the giver: the request
// stands, the push does not.
//
// Every check that does not depend on the recipient runs BEFORE the username
// is resolved, and every ineligible recipient answers ErrRecipientNotFound,
// so no answer reveals more about an account than "can receive videos".
func (s *Service) GiveOwnership(ctx context.Context, actorID, videoID uuid.UUID, username string) (sqlcgen.VideoOwnershipChange, bool, error) {
	if s.own == nil {
		return sqlcgen.VideoOwnershipChange{}, false, ErrOwnershipUnavailable
	}
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return sqlcgen.VideoOwnershipChange{}, false, err
	}
	if v.OwnerID != actorID {
		return sqlcgen.VideoOwnershipChange{}, false, ErrForbidden
	}
	if !settledForTransfer(v.State) {
		return sqlcgen.VideoOwnershipChange{}, false, ErrNotSettled
	}
	// A pending request the giver can no longer honour (they lost the video,
	// or it outlived the offer window) must not block the current owner.
	if _, err := s.own.ExpireStaleVideoOwnershipChanges(ctx, sqlcgen.ExpireStaleVideoOwnershipChangesParams{
		VideoID: videoID, ExpiredBefore: time.Now().Add(-OwnershipOfferWindow),
	}); err != nil {
		return sqlcgen.VideoOwnershipChange{}, false, err
	}
	if pending, err := s.own.HasPendingVideoOwnershipChange(ctx, videoID); err != nil {
		return sqlcgen.VideoOwnershipChange{}, false, err
	} else if pending {
		return sqlcgen.VideoOwnershipChange{}, false, ErrOwnershipPending
	}
	to, err := s.own.ResolveOwnershipRecipient(ctx, sqlcgen.ResolveOwnershipRecipientParams{
		InitiatorID: actorID, Username: strings.TrimSpace(username),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.VideoOwnershipChange{}, false, ErrRecipientNotFound
	}
	if err != nil {
		return sqlcgen.VideoOwnershipChange{}, false, err
	}
	if to.ID == actorID {
		return sqlcgen.VideoOwnershipChange{}, false, ErrRecipientNotFound
	}
	if cooling, err := s.own.HasRecentOwnershipRefusal(ctx, sqlcgen.HasRecentOwnershipRefusalParams{
		InitiatorID: actorID, NextOwnerID: to.ID, RefusedAfter: time.Now().Add(-OwnershipRefusalCooldown),
	}); err != nil {
		return sqlcgen.VideoOwnershipChange{}, false, err
	} else if cooling {
		return sqlcgen.VideoOwnershipChange{}, false, ErrOwnershipCooldown
	}
	oc, err := s.own.CreateVideoOwnershipChange(ctx, sqlcgen.CreateVideoOwnershipChangeParams{
		VideoID: videoID, InitiatorID: actorID, NextOwnerID: to.ID, VideoTitle: v.Title,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.VideoOwnershipChange{}, false, ErrOwnershipPending
	}
	if err != nil {
		return sqlcgen.VideoOwnershipChange{}, false, err
	}
	return oc, !to.MutedInitiator, nil
}

// CancelOwnershipChange withdraws the caller's pending offer of videoID.
// ErrOwnershipNotFound when there is none — whether the video is unknown, not
// theirs, or has no pending offer from them.
func (s *Service) CancelOwnershipChange(ctx context.Context, actorID, videoID uuid.UUID) error {
	if s.own == nil {
		return ErrOwnershipUnavailable
	}
	n, err := s.own.CancelVideoOwnershipChange(ctx, sqlcgen.CancelVideoOwnershipChangeParams{
		VideoID: videoID, InitiatorID: actorID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrOwnershipNotFound
	}
	return nil
}
