package ipfsmirror

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/media"
)

type masterKeyReader interface {
	VideoHLSMasterKey(context.Context, uuid.UUID) (string, bool, error)
}

// PublicPlaybackHLS returns only a committed copy of the current ready generation.
// Imported masters keep their original filename, which need not be master.m3u8.
func (s *Service) PublicPlaybackHLS(ctx context.Context, id uuid.UUID, masterKey string) (string, bool, error) {
	reader, ok := s.lookups.(masterKeyReader)
	if !ok || s.gatewayURL == "" || masterKey == "" {
		return "", false, nil
	}
	current, found, err := reader.VideoHLSMasterKey(ctx, id)
	if err != nil || !found || current != masterKey {
		return "", false, err
	}
	row, err := s.repo.GetIPFSPinByObjectKey(ctx, media.HLSKeyPrefix(id)+"/")
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if row.State != "pinned" || row.Network != "public" || row.MediaClass != string(ClassHLS) || row.Cid == "" || row.CarRoot != row.Cid || row.CommittedGeneration != masterKey || ipfs.ValidateCID(row.Cid) != nil {
		return "", false, nil
	}
	allowed, err := s.gatewayRowEligible(ctx, row)
	if err != nil || !allowed {
		return "", false, err
	}
	return strings.TrimRight(s.gatewayURL, "/") + "/ipfs/" + row.Cid + "/" + path.Base(masterKey), true, nil
}
