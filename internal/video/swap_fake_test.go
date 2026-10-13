package video

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// SwapVideoOriginalIfOwner mirrors the guarded single-statement swap: only while
// the video still belongs to OwnerID are its originals replaced by the new row.
func (f *fakeRepo) SwapVideoOriginalIfOwner(ctx context.Context, a sqlcgen.SwapVideoOriginalIfOwnerParams) (sqlcgen.VideoFile, error) {
	if v, ok := f.videos[a.VideoID]; !ok || v.OwnerID != a.OwnerID {
		return sqlcgen.VideoFile{}, pgx.ErrNoRows
	}
	_ = f.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{VideoID: a.VideoID, Kind: "original"})
	return f.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{VideoID: a.VideoID, Kind: "original", StorageKey: a.StorageKey,
		ContentType: a.ContentType, OriginalName: a.OriginalName, SizeBytes: a.SizeBytes, Sha256: a.Sha256})
}
