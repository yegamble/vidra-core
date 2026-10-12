package video

import (
	"context"
	"errors"
	"strings"

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
	// unknown, banned, deleted, or blocked either way. One error for all of
	// them, so the give route cannot tell them apart.
	ErrRecipientNotFound = errors.New("video: no such recipient")
	// ErrOwnershipSelf means the recipient already owns the video.
	ErrOwnershipSelf = errors.New("video: the recipient already owns the video")
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
}

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
// but cannot give it away (no actor class is exempt, as with moves). It returns
// the pending request and whether the recipient should be notified — false
// when they muted the giver: the request stands, the push does not.
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
		return sqlcgen.VideoOwnershipChange{}, false, ErrOwnershipSelf
	}
	oc, err := s.own.CreateVideoOwnershipChange(ctx, sqlcgen.CreateVideoOwnershipChangeParams{
		VideoID: videoID, InitiatorID: actorID, NextOwnerID: to.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.VideoOwnershipChange{}, false, ErrOwnershipPending
	}
	if err != nil {
		return sqlcgen.VideoOwnershipChange{}, false, err
	}
	return oc, !to.MutedInitiator, nil
}
