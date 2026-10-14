package federation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// remoteViewWindow is how long one local viewer's repeated views of a remote
// video send a single View — the local view-dedupe window (video.Service).
const remoteViewWindow = time.Hour

// ViewDeduper is first-occurrence-within-a-window (cache.Deduper, Redis SETNX).
type ViewDeduper interface {
	First(ctx context.Context, key string, window time.Duration) (bool, error)
}

// WithRemoteViewDeduper wires the per-viewer dedupe RecordRemoteVideoView needs.
// Unset = views of remote videos are not federated (every ping would be a
// delivery otherwise).
func WithRemoteViewDeduper(d ViewDeduper) Option {
	return func(s *Service) { s.remoteViewDeduper = d }
}

// RecordRemoteVideoView federates a local view of a remote video to its origin,
// which owns the count (PeerTube's sendView after addLocalView): a View signed
// by this instance's actor — never the viewer's account, so who watched what
// does not leave the instance — sent once per viewer per window. viewerKey is
// the hashed viewer key the local view seam uses. An unknown video, or one
// whose origin is blocked here (excluded by GetRemoteVideoByID), is
// ErrNotFound; everything else that prevents a send is a silent no-op.
func (s *Service) RecordRemoteVideoView(ctx context.Context, remoteVideoID uuid.UUID, viewerKey string) error {
	rv, err := s.repo.GetRemoteVideoByID(ctx, remoteVideoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if s.remoteViewDeduper == nil {
		return nil
	}
	actor, ok := s.InstanceActorAvailable(ctx)
	if !ok {
		return nil
	}
	// A dedupe error is treated as "already sent": unlike a local count, an
	// over-send here inflates another instance's numbers.
	if first, err := s.remoteViewDeduper.First(ctx, "ap:remote-view:"+remoteVideoID.String()+":"+viewerKey, remoteViewWindow); err != nil || !first {
		return nil
	}
	_, inbox, ok, err := s.resolveAuthoredCommentOrigin(ctx, remoteVideoID)
	if err != nil || !ok {
		return err
	}
	// PeerTube's buildViewActivity without viewersCount: no expires, no result.
	payload, err := json.Marshal(map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       actor + "/views/videos/" + remoteVideoID.String() + "/" + uuid.NewString(),
		"type":     "View",
		"actor":    actor,
		"object":   rv.ObjectUrl,
		"to":       []string{publicAudience},
	})
	if err != nil {
		return err
	}
	return s.enqueueInstanceDelivery(ctx, inbox, payload)
}
