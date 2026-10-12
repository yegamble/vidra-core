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
	// ErrOwnershipAnswered means the request was already accepted or refused.
	ErrOwnershipAnswered = errors.New("video: ownership change already answered")
	// ErrOwnershipStale means the giver no longer owns the video.
	ErrOwnershipStale = errors.New("video: the giver no longer owns the video")
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
	ListIncomingVideoOwnershipChanges(ctx context.Context, arg sqlcgen.ListIncomingVideoOwnershipChangesParams) ([]sqlcgen.ListIncomingVideoOwnershipChangesRow, error)
	CountIncomingVideoOwnershipChanges(ctx context.Context, nextOwnerID uuid.UUID) (int64, error)
	GetVideoOwnershipChange(ctx context.Context, id uuid.UUID) (sqlcgen.VideoOwnershipChange, error)
	AcceptVideoOwnershipChange(ctx context.Context, arg sqlcgen.AcceptVideoOwnershipChangeParams) (sqlcgen.AcceptVideoOwnershipChangeRow, error)
	RefuseVideoOwnershipChange(ctx context.Context, arg sqlcgen.RefuseVideoOwnershipChangeParams) (int64, error)
	SumVideoStorageUsage(ctx context.Context, videoID uuid.UUID) (int64, error)
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

// ListIncomingOwnershipChanges pages the requests addressed to userID, newest
// first, with the total for the page envelope. Titles are the snapshot taken
// when each was offered; a pending request that can no longer be accepted reads
// 'expired'.
func (s *Service) ListIncomingOwnershipChanges(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]sqlcgen.ListIncomingVideoOwnershipChangesRow, int64, error) {
	if s.own == nil {
		return nil, 0, ErrOwnershipUnavailable
	}
	rows, err := s.own.ListIncomingVideoOwnershipChanges(ctx, sqlcgen.ListIncomingVideoOwnershipChangesParams{
		NextOwnerID: userID, ExpiredBefore: time.Now().Add(-OwnershipOfferWindow),
		ResultLimit: limit, ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.own.CountIncomingVideoOwnershipChanges(ctx, userID)
	return rows, total, err
}

// pendingChangeFor loads a pending request addressed to actorID. A request
// for someone else is ErrOwnershipNotFound, exactly like a missing one.
func (s *Service) pendingChangeFor(ctx context.Context, actorID, changeID uuid.UUID) (sqlcgen.VideoOwnershipChange, error) {
	if s.own == nil {
		return sqlcgen.VideoOwnershipChange{}, ErrOwnershipUnavailable
	}
	oc, err := s.own.GetVideoOwnershipChange(ctx, changeID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && oc.NextOwnerID != actorID) {
		return sqlcgen.VideoOwnershipChange{}, ErrOwnershipNotFound
	}
	if err != nil {
		return sqlcgen.VideoOwnershipChange{}, err
	}
	if oc.Status != "pending" {
		return sqlcgen.VideoOwnershipChange{}, ErrOwnershipAnswered
	}
	return oc, nil
}

// AcceptOwnershipChange answers a request addressed to actorID by moving the
// video into channelID, which must be one of actorID's channels. fits is the
// caller's storage-quota gate for the video's stored bytes (the one uploads
// use); nil checks nothing. The checks before the write give each refusal its
// own error; the write itself (AcceptVideoOwnershipChange) re-checks all of
// them in the statement that flips the request and moves the video, so a
// change in between — the giver losing the video, a concurrent answer — moves
// nothing (ErrOwnershipStale). After the move the same side effects fire as
// for a same-owner move (fireUpdateHooks). Returns the moved video's id and the
// channel it left.
func (s *Service) AcceptOwnershipChange(ctx context.Context, actorID, changeID, channelID uuid.UUID, fits func(context.Context, int64) error) (sqlcgen.AcceptVideoOwnershipChangeRow, error) {
	var none sqlcgen.AcceptVideoOwnershipChangeRow
	oc, err := s.pendingChangeFor(ctx, actorID, changeID)
	if err != nil {
		return none, err
	}
	v, err := s.GetByID(ctx, oc.VideoID)
	if err != nil || v.OwnerID != oc.InitiatorID {
		return none, ErrOwnershipStale
	}
	if !settledForTransfer(v.State) {
		return none, ErrNotSettled
	}
	if target, terr := s.repo.GetChannelByID(ctx, channelID); terr != nil || target.OwnerID != actorID {
		return none, ErrMoveRefused
	}
	if fits != nil {
		bytes, err := s.own.SumVideoStorageUsage(ctx, v.ID)
		if err != nil {
			return none, err
		}
		if err := fits(ctx, bytes); err != nil {
			return none, err
		}
	}
	moved, err := s.own.AcceptVideoOwnershipChange(ctx, sqlcgen.AcceptVideoOwnershipChangeParams{
		ID: changeID, NextOwnerID: actorID, ChannelID: channelID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return none, ErrOwnershipStale
	}
	if err != nil {
		return none, err
	}
	// The statement's own from_channel_id: the owner may have moved the video
	// between their channels since v was read, and the retract must address the
	// channel it actually left.
	v.ChannelID = moved.FromChannelID
	s.fireUpdateHooks(ctx, v, true)
	return moved, nil
}

// RefuseOwnershipChange answers a request addressed to actorID with a no. The
// video is untouched and its owner may offer it again.
func (s *Service) RefuseOwnershipChange(ctx context.Context, actorID, changeID uuid.UUID) error {
	if _, err := s.pendingChangeFor(ctx, actorID, changeID); err != nil {
		return err
	}
	n, err := s.own.RefuseVideoOwnershipChange(ctx, sqlcgen.RefuseVideoOwnershipChangeParams{ID: changeID, NextOwnerID: actorID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrOwnershipAnswered // a concurrent answer won
	}
	return nil
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
