package moderation

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// MyReport is one of a reporter's own reports, as "My reports" shows it. It
// deliberately has no moderator note: that is staff-internal. Empty strings
// mean the field does not apply (or the target is gone or no longer public).
type MyReport struct {
	ID                                 uuid.UUID
	TargetType, Status, Reason         string
	CreatedAt, LastActivityAt          time.Time
	ResolvedAt                         *time.Time
	MessageCount                       int64
	VideoID, VideoTitle                string
	ReportedUsername, RemoteVideoTitle string
}

// ListMine returns reporterID's own reports, most recently active first, with
// how many there are. The caller clamps limit/offset.
func (s *Service) ListMine(ctx context.Context, reporterID uuid.UUID, limit, offset int32) ([]MyReport, int64, error) {
	items, err := s.mine(ctx, reporterID, pgtype.UUID{}, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountMyReports(ctx, reporterID)
	return items, total, err
}

// GetMine returns one of reporterID's own reports; anyone else's is ErrNotFound.
func (s *Service) GetMine(ctx context.Context, reporterID, reportID uuid.UUID) (MyReport, error) {
	items, err := s.mine(ctx, reporterID, pgconv.UUID(reportID), 1, 0)
	if err != nil {
		return MyReport{}, err
	}
	if len(items) == 0 {
		return MyReport{}, ErrNotFound
	}
	return items[0], nil
}

func (s *Service) mine(ctx context.Context, reporterID uuid.UUID, reportID pgtype.UUID, limit, offset int32) ([]MyReport, error) {
	rows, err := s.repo.ListMyReports(ctx, sqlcgen.ListMyReportsParams{
		ReporterID: reporterID, ReportID: reportID, ResultLimit: limit, ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]MyReport, 0, len(rows))
	for _, r := range rows {
		items = append(items, MyReport{
			ID: r.ID, TargetType: r.TargetType, Status: r.Status, Reason: r.Reason,
			CreatedAt: r.CreatedAt, LastActivityAt: r.LastActivityAt, ResolvedAt: pgconv.TimeOrNil(r.ResolvedAt),
			MessageCount: r.MessageCount, VideoID: uuidString(r.VideoID), VideoTitle: r.VideoTitle,
			ReportedUsername: pgconv.Deref(r.ReportedUsername), RemoteVideoTitle: pgconv.Deref(r.RemoteVideoTitle),
		})
	}
	return items, nil
}
