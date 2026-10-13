package ipfsmirror

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/mediaroute"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// hlsServed reports whether key (under prefix, a directory with a trailing "/")
// is a file the HLS routes serve. The mirror pins exactly this set: playlists, init
// and media segments, trick-play, the DASH manifest — and, for an imported
// PeerTube tree, the looser set its pass-through route serves. The definition is
// internal/mediaroute's, shared with internal/httpapi's handlers.
func hlsServed(prefix, masterKey, key string) bool {
	rel, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return false
	}
	return mediaroute.HLSServedRel(rel, masterKey, mediaroute.IsPeerTubeHLSMasterKey(prefix))
}

// hlsMasterKey is the row's promoted master key. "" with a nil error means there is
// none to read (a pre-provenance row, a lookups implementation without the reader,
// or no ready playlist) - hlsServed then falls back to the native master name. A
// lookup ERROR is returned: guessing the master name after a failed read would pin
// a tree under the wrong definition of "served".
func (s *Service) hlsMasterKey(ctx context.Context, row sqlcgen.ClaimDueIPFSPinsRow) (string, error) {
	reader, ok := s.lookups.(masterKeyReader)
	if !ok || !row.VideoID.Valid {
		return "", nil
	}
	master, found, err := reader.VideoHLSMasterKey(ctx, uuid.UUID(row.VideoID.Bytes))
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil
	}
	return master, nil
}
