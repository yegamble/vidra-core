package federation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/publicsuffix"

	"github.com/vidra/vidra-core/internal/ratelimit"
)

// maxRemoteViewsPerDomainHour caps the views one remote domain can add across
// this instance per hour. A remote View cannot be verified (the sender only
// says someone watched), so a domain minting actors gets a bounded effect, and
// the remedy for a hostile domain is the instance block.
const maxRemoteViewsPerDomainHour = 100

// RemoteViewRecorder counts one view of a local video under viewerKey through
// the local view-counting seam (video.Service.RecordViewGated), which dedupes a
// viewer per window, consults gate only for a first view, and keeps the per-day
// rollup in step.
type RemoteViewRecorder func(ctx context.Context, videoID uuid.UUID, viewerKey string, gate func(context.Context) (bool, error)) error

// WithViewCounting wires inbound ActivityPub View into the local view count.
// domainCap backs the per-domain hourly cap; it must be shared across API
// replicas in production (ratelimit.NewRedisCounter). Unset = Views ignored.
func WithViewCounting(record RemoteViewRecorder, domainCap ratelimit.Counter) Option {
	return func(s *Service) { s.recordView, s.viewDomainCap = record, domainCap }
}

// handleView counts a remote View of one of OUR videos (PeerTube's
// processViewActivity): PeerTube sends one per view it counted, signed by its
// instance actor. The count is always ours — a remote `result` counter is
// never read. A View with `expires` is PeerTube's live-viewer heartbeat, which
// PeerTube itself keeps out of the view count; it is accepted and ignored.
//
// The viewer is the signing actor: one view per (actor, video) per dedupe
// window, through the same seam and window as a local viewer. As with ratings,
// every refusal except the spoof and the admin block is accept-and-ignore, so a
// private video or a creator's block cannot be probed.
func (s *Service) handleView(ctx context.Context, act inboxActivity, signerActorURL string) error {
	if act.Actor != signerActorURL {
		return ErrActorMismatch
	}
	// PeerTube builds the view id from the actor URL and refuses one on
	// another host; so do we.
	if len(act.ID) > maxRateIDLen || !sameHost(act.ID, signerActorURL) {
		return ErrBadResource
	}
	if blocked, err := s.repo.IsRemoteActorBlockedInstanceWide(ctx, signerActorURL); err != nil {
		return err
	} else if blocked {
		return ErrSenderBlocked
	}
	if s.recordView == nil || act.Expires != "" {
		return nil
	}
	videoID, ok := s.localVideoID(objectID(act.Object))
	if !ok {
		return nil
	}
	if visible, err := s.videoIsPubliclyVisible(ctx, videoID); err != nil || !visible {
		return err
	}
	if blocked, err := s.videoOwnerBlocksRemoteActor(ctx, videoID, signerActorURL); err != nil || blocked {
		return err
	}
	// The domain budget is spent only on a first view (the seam's gate runs
	// after its dedupe), keyed on the registrable domain so fresh subdomains
	// share one budget.
	gate := func(ctx context.Context) (bool, error) {
		if s.viewDomainCap == nil {
			return true, nil
		}
		n, _, err := s.viewDomainCap.Incr(ctx, "ap:view:domain:"+capDomain(signerActorURL), time.Hour)
		return err == nil && n <= maxRemoteViewsPerDomainHour, err
	}
	// Hashed like the local viewer key, so actor URLs never become Redis keys.
	sum := sha256.Sum256([]byte("ap:" + signerActorURL))
	return s.recordView(ctx, videoID, hex.EncodeToString(sum[:]), gate)
}

// capDomain is the key for a per-sender-domain budget: the registrable domain
// (eTLD+1) of the URL's normalised host, so sub1.evil.example and
// sub2.evil.example share one budget. An IP literal, or a host the public
// suffix list cannot reduce, keys on the normalised host itself.
func capDomain(rawURL string) string {
	host := hostOf(rawURL)
	if n, ok := NormalizeFollowHost(host); ok {
		host = n
	}
	if net.ParseIP(host) != nil {
		return host
	}
	if d, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return d
	}
	return host
}
