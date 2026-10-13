package ipfsmirror

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Tier A F5: tell the mirror when a pinned single-file object is deleted or
// REPLACED. Captions, the poster and the storyboard are pinned under a STABLE key,
// so neither a delete nor an in-place overwrite changes anything the mirror
// watches: a deleted caption stayed pinned and retrievable from the gateway, and a
// replaced poster/storyboard stayed pinned under its old bytes (and old CID).

// OnCaptionChanged is the video service's caption hook. A removed track is
// unpinned (the worker reference-checks the CID, so a CID another live row shares
// is kept); a stored-or-replaced track is force-re-armed so the worker adds the
// new bytes and swap-unpins the old CID. No-op when disabled; a video that is not
// mirror-eligible arms nothing (the privacy transition owns its unpinning).
func (s *Service) OnCaptionChanged(ctx context.Context, videoID uuid.UUID, objectKey string, deleted bool) error {
	if !s.enabled {
		return nil
	}
	if deleted {
		return s.repo.EnqueueIPFSUnpin(ctx, objectKey)
	}
	return s.repinStable(ctx, videoID, ClassCaption, objectKey)
}

// OnMediaReplaced is the video service's replacement hook (the one CDN
// invalidation also hangs off): kind is the video_files kind that was overwritten
// in place, "thumbnail" or "storyboard". The storyboard's WebVTT map is written
// beside its sprite, so it is re-armed with it. Re-arming an object that was never
// pinned simply pins it; the hook only fires for public+published videos.
func (s *Service) OnMediaReplaced(ctx context.Context, videoID uuid.UUID, kind string) error {
	if !s.enabled {
		return nil
	}
	var kinds []string
	switch kind {
	case "thumbnail":
		kinds = []string{"thumbnail"}
	case "storyboard":
		kinds = []string{"storyboard", "storyboard_vtt"}
	default:
		return nil
	}
	files, err := s.lookups.VideoFiles(ctx, videoID)
	if err != nil {
		return err
	}
	for _, f := range files {
		for _, k := range kinds {
			if f.Kind != k {
				continue
			}
			class, ok := videoFileMirrorClass(f.Kind)
			if !ok {
				return fmt.Errorf("ipfsmirror: no mirror class for video file kind %q", f.Kind)
			}
			if err := s.repinStable(ctx, videoID, class, f.StorageKey); err != nil {
				return err
			}
		}
	}
	return nil
}

// repinStable force-re-arms one stable-keyed object on the video's current swarm
// (RepinIPFSObject always returns the row to 'pending', preserving its previous
// CID so the worker can swap-unpin it after the new add). A video that routes to no
// swarm arms nothing.
func (s *Service) repinStable(ctx context.Context, videoID uuid.UUID, class MediaClass, objectKey string) error {
	net, ok, err := s.videoNetwork(ctx, videoID)
	if err != nil || !ok || net == NetworkNone {
		return err
	}
	return s.repo.RepinIPFSObject(ctx, sqlcgen.RepinIPFSObjectParams{
		ObjectKey:     objectKey,
		MediaClass:    string(class),
		VideoID:       pgUUID(videoID),
		TargetNetwork: net,
	})
}

// rearmReplacedImage closes the same-key avatar/banner gap (F5). The profileimage
// service calls the *ImagePin wrappers on every upload; when the new file has the
// same extension it has the SAME deterministic key, and routing an intent for a row
// that is already 'pinned' leaves it pinned — under the old bytes and old CID. If
// the key's row is pinned, force it back through the worker so the new bytes are
// added and the old CID swap-unpinned. A row that is not pinned (new, pending,
// or being unpinned because the owner is ineligible) needs nothing here.
func (s *Service) rearmReplacedImage(ctx context.Context, objectKey string, class MediaClass) error {
	if !s.enabled {
		return nil
	}
	row, err := s.repo.GetIPFSPinByObjectKey(ctx, objectKey)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.State != "pinned") {
		return nil
	}
	if err != nil {
		return err
	}
	return s.repo.RepinIPFSObject(ctx, sqlcgen.RepinIPFSObjectParams{
		ObjectKey:     objectKey,
		MediaClass:    string(class),
		VideoID:       row.VideoID,
		TargetNetwork: normalizeNetwork(row.Network),
	})
}
