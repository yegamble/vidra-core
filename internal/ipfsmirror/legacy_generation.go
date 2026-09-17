package ipfsmirror

import (
	"context"
	"path"
	"slices"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type legacyGenerationWriter interface {
	RecordLegacyIPFSGeneration(context.Context, sqlcgen.RecordLegacyIPFSGenerationParams) (int64, error)
}

// Capture before AddDirectory: current metadata after a copy cannot prove what
// was copied. The managed runner has its own admission-bound generation receipt.
func (s *Service) legacyCopyGeneration(ctx context.Context, nc netClient, row sqlcgen.ClaimDueIPFSPinsRow, prefix string, keys []string) string {
	if _, ok := s.repo.(legacyGenerationWriter); !ok || nc.network != "public" || !row.VideoID.Valid {
		return ""
	}
	reader, ok := s.lookups.(masterKeyReader)
	if !ok {
		return ""
	}
	master, found, err := reader.VideoHLSMasterKey(ctx, uuid.UUID(row.VideoID.Bytes))
	if err != nil || !found || master == "" || path.Base(master) == media.VP9WebMFilename || path.Dir(master)+"/" != prefix || !slices.Contains(keys, master) {
		return ""
	}
	return master
}
