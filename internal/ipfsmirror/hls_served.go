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

// hlsMasterKey is the row's promoted master key, or "" when it cannot be read (a
// pre-provenance row, or a lookups implementation without the reader) — in which
// case hlsServed falls back to the native master name.
func (s *Service) hlsMasterKey(ctx context.Context, row sqlcgen.ClaimDueIPFSPinsRow) string {
	reader, ok := s.lookups.(masterKeyReader)
	if !ok || !row.VideoID.Valid {
		return ""
	}
	master, found, err := reader.VideoHLSMasterKey(ctx, uuid.UUID(row.VideoID.Bytes))
	if err != nil || !found {
		return ""
	}
	return master
}
