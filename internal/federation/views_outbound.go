package federation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/ratelimit"
)

// remoteViewWindow is how long one local viewer's repeated views of a remote
// video send a single View — the local view-dedupe window (video.Service).
const remoteViewWindow = time.Hour

// ViewDeduper is first-occurrence-within-a-window (cache.Deduper, Redis SETNX).
type ViewDeduper interface {
	First(ctx context.Context, key string, window time.Duration) (bool, error)
}

// maxRemoteViewsPerOriginHour bounds the Views this instance sends to one
// origin (registrable domain) per hour, whatever the local audience: past it a
// view is dropped silently. It caps what a burst of local pings — or many
// IPs — can make this instance push at another server.
const maxRemoteViewsPerOriginHour = 50

// WithRemoteViewOriginCap wires the per-origin hourly cap; in production it must
// be shared across replicas (ratelimit.NewRedisCounter). Unset = uncapped.
func WithRemoteViewOriginCap(c ratelimit.Counter) Option {
	return func(s *Service) { s.remoteViewOriginCap = c }
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
	_, inbox, ok, err := s.resolveAuthoredCommentOrigin(ctx, remoteVideoID)
	if err != nil || !ok {
		return err
	}
	// The viewer's dedupe key is consumed only once the send is possible, and
	// the origin's budget only for a first view. A dedupe or cap error counts
	// as "do not send": unlike a local count, an over-send inflates another
	// instance's numbers.
	if first, err := s.remoteViewDeduper.First(ctx, "ap:remote-view:"+remoteVideoID.String()+":"+viewerKey, remoteViewWindow); err != nil || !first {
		return nil
	}
	if s.remoteViewOriginCap != nil {
		n, _, err := s.remoteViewOriginCap.Incr(ctx, "ap:remote-view:origin:"+capDomain(inbox), time.Hour)
		if err != nil || n > maxRemoteViewsPerOriginHour {
			return nil
		}
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
