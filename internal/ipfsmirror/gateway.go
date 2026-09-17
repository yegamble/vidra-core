package ipfsmirror

import (
	"context"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type gatewayRootReader interface {
	ListPublicIPFSRootPins(context.Context, string) ([]sqlcgen.MediaIpfsPin, error)
}

// PublicGatewayRootAllowed checks live visibility, independently of asynchronous
// unpin/GC. Never consult gateway health here: its probe uses this same gate.
// Publication may be paused while retained public pins remain readable.
func (s *Service) PublicGatewayRootAllowed(ctx context.Context, cid string) (bool, error) {
	reader, ok := s.repo.(gatewayRootReader)
	if !ok || ipfs.ValidateCID(cid) != nil {
		return false, nil
	}
	rows, err := reader.ListPublicIPFSRootPins(ctx, cid)
	if err != nil {
		return false, err
	}
	// Bound request fan-out; truncation is a denial, never partial authorization.
	if len(rows) > 128 {
		return false, nil
	}
	for _, row := range rows {
		if row.Cid != cid || row.Network != NetworkPublic || row.State != "pinned" {
			continue
		}
		allowed, err := s.gatewayRowEligible(ctx, row)
		if err != nil {
			return false, err
		}
		if allowed {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) gatewayRowEligible(ctx context.Context, row sqlcgen.MediaIpfsPin) (bool, error) {
	subject := Subject{Class: MediaClass(row.MediaClass)}
	switch {
	case isVideoDerived(subject.Class):
		if !row.VideoID.Valid {
			return false, nil
		}
		id := uuid.UUID(row.VideoID.Bytes)
		if reader, ok := s.lookups.(interface {
			VideoMirrorProtected(context.Context, uuid.UUID) (bool, error)
		}); ok {
			protected, err := reader.VideoMirrorProtected(ctx, id)
			if err != nil || protected {
				return false, err
			}
		}
		privacy, state, owner, found, err := s.lookups.VideoVisibility(ctx, id)
		if err != nil || !found {
			return false, err
		}
		active, unlisted, found, err := s.lookups.UserFlags(ctx, owner)
		if err != nil || !found || !active {
			return false, err
		}
		blocked, err := s.lookups.VideoBlocked(ctx, id)
		if err != nil {
			return false, err
		}
		subject.VideoPrivacy, subject.VideoState, subject.OwnerUnlisted, subject.VideoBlocked = privacy, state, unlisted, blocked
		if Route(subject) != NetworkPublic {
			return false, nil
		}
		if subject.Class == ClassHLS {
			reader, ok := s.lookups.(masterKeyReader)
			if !ok {
				return false, nil
			}
			master, found, err := reader.VideoHLSMasterKey(ctx, id)
			return found && row.CommittedGeneration == master && row.ObjectKey == media.HLSKeyPrefix(id)+"/" && row.CarRoot == row.Cid, err
		}
		refs, err := s.videoMirrorRefs(ctx, id)
		if err != nil {
			return false, err
		}
		return gatewayCurrentRef(refs, row), nil
	case isIdentityImage(subject.Class):
		if !row.OwnerUserID.Valid {
			return false, nil
		}
		id := uuid.UUID(row.OwnerUserID.Bytes)
		active, unlisted, found, err := s.lookups.UserFlags(ctx, id)
		if err != nil || !found {
			return false, err
		}
		subject.OwnerActive, subject.OwnerUnlisted = active, unlisted
		refs, err := s.lookups.OwnerImageRefs(ctx, id)
		if err != nil {
			return false, err
		}
		if !gatewayCurrentRef(refs, row) {
			return false, nil
		}
	case subject.Class == ClassPlaylistCover:
		// Playlist cover rows predate provenance columns; derive the identity only
		// from their exact deterministic key and verify it against the live cover.
		if !strings.HasPrefix(row.ObjectKey, "playlist-thumbnails/") {
			return false, nil
		}
		base := path.Base(row.ObjectKey)
		id, err := uuid.Parse(strings.TrimSuffix(base, path.Ext(base)))
		if err != nil {
			return false, nil
		}
		visibility, key, found, err := s.lookups.PlaylistCover(ctx, id)
		if err != nil || !found || key != row.ObjectKey {
			return false, err
		}
		subject.PlaylistVisibility = visibility
	default:
		return false, nil
	}
	return Route(subject) == NetworkPublic, nil
}

func gatewayCurrentRef(refs []ImageRef, row sqlcgen.MediaIpfsPin) bool {
	for _, ref := range refs {
		if ref.ObjectKey == row.ObjectKey && string(ref.Class) == row.MediaClass {
			return true
		}
	}
	return false
}
