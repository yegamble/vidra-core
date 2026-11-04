package blobverify

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/vidra/vidra-core/internal/hlsmanifest"
	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/storage"
)

// Finding contains canonical storage identifiers, never a rejected raw URI.
type Finding struct {
	VideoID    string `json:"video_id,omitempty"`
	Root       string `json:"root,omitempty"`
	Parent     string `json:"parent,omitempty"`
	Key        string `json:"key,omitempty"`
	Reason     string `json:"reason"`
	Unresolved bool   `json:"unresolved_subtree"`
	Line       int    `json:"line,omitempty"`
}

type hlsTreeResult struct {
	usage    hlsUsage
	complete bool
	media    int // observed distinct non-playlist MEDIA roles, not a decoder proof
}

type hlsTreeWalker struct {
	blobs   storage.Backend
	limits  hlsLimits
	probe   func(string) outcome
	finding func(Finding) error
}

func (w hlsTreeWalker) walk(ctx context.Context, pl mediagc.PlaylistRef) (result hlsTreeResult, err error) {
	b := hlsBudget{limits: w.limits}
	defer func() { result.usage = b.usage }()
	result.complete = true
	root := ""
	var sinkErr error
	record := func(parent, key, reason string, unresolved bool, line int) error {
		result.complete = false
		if w.finding != nil {
			sinkErr = w.finding(Finding{VideoID: pl.VideoID.String(), Root: root, Parent: parent, Key: key, Reason: reason, Unresolved: unresolved, Line: line})
		}
		return sinkErr
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if _, err := resolveHLSReference(pl, pl.MasterKey, path.Base(pl.MasterKey)); err != nil {
		return result, record("", "", "invalid_root", true, 0)
	}
	root = pl.MasterKey
	active, media := map[string]bool{}, map[string]bool{}
	var visit func(string, string, hlsmanifest.ReferenceKind, int) error
	visit = func(parent, key string, kind hlsmanifest.ReferenceKind, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		playlist := key == root || strings.HasSuffix(key, ".m3u8")
		fresh, err := b.admit(key, depth)
		if err != nil {
			if e := record(parent, key, "limit_"+b.usage.exhausted, true, 0); e != nil {
				return e
			}
			return err
		}
		if kind == hlsmanifest.PlaylistReference && !playlist {
			return record(parent, key, "invalid_playlist_reference", true, 0)
		}
		// Membership deduplication must retain a later MEDIA role on a file
		// first encountered as MAP (PeerTube's shared byte-range MP4 layout).
		if kind == hlsmanifest.MediaReference && !playlist && !media[key] {
			media[key] = true
			result.media++
		}
		if playlist && active[key] {
			return record(parent, key, "cycle", true, 0)
		}
		if !fresh {
			return nil
		}
		switch w.probe(key).cls {
		case classMissing:
			return record(parent, key, "missing", playlist, 0)
		case classKnownMissing:
			return record(parent, key, "known_missing", playlist, 0)
		case classError:
			return record(parent, key, "unreadable", playlist, 0)
		case classMismatch:
			if err := record(parent, key, "hash_mismatch", playlist, 0); err != nil {
				return err
			}
		}
		if !playlist {
			return nil
		}
		active[key] = true
		defer delete(active, key)
		rc, err := w.blobs.Open(ctx, key)
		if err != nil {
			reason := "playlist_read_error"
			if errors.Is(err, storage.ErrNotFound) {
				reason = "missing"
			}
			return record(parent, key, reason, true, 0)
		}
		body, readErr := b.readPlaylist(rc)
		_ = rc.Close()
		if readErr != nil {
			reason := "playlist_read_error"
			if errors.Is(readErr, errHLSLimit) {
				reason = "limit_" + b.usage.exhausted
			}
			if err := record(parent, key, reason, true, 0); err != nil {
				return err
			}
			if errors.Is(readErr, errHLSLimit) {
				return readErr
			}
			return nil
		}
		var callbackErr error
		err = hlsmanifest.VisitReferences(body, func(ref hlsmanifest.Reference) error {
			child, err := resolveHLSReference(pl, key, ref.URI)
			if err != nil {
				callbackErr = record(key, "", "invalid_reference", true, ref.Line)
			} else {
				callbackErr = visit(key, child, ref.Kind, depth+1)
			}
			return callbackErr
		})
		if callbackErr != nil {
			return callbackErr
		}
		if err != nil {
			return record(parent, key, "invalid_manifest", true, 0)
		}
		return nil
	}
	err = visit("", root, hlsmanifest.PlaylistReference, 0)
	if sinkErr != nil {
		return result, sinkErr
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil && !errors.Is(err, errHLSLimit) {
		return result, err
	}
	// A missing playlist's descendants remain unknown; only a completely read
	// graph can prove that it contains no actual media references.
	if result.complete && result.media == 0 {
		return result, record("", root, "no_media", false, 0)
	}
	return result, nil
}
