//go:build integration

// Integration test for report predefined reasons and the video timestamp range
// (migration 0170): the CHECK, the array round trip, and the reason filter on
// both the queue list and its count.
package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestReportReasonsOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	owner, channelID := seedOwnerChannel(ctx, t, st, "reasons")
	video := pgtype.UUID{Bytes: seedPublishedVideo(ctx, t, st, channelID, "reasoned video"), Valid: true}
	reporter, _ := seedOwnerChannel(ctx, t, st, "reasonrep")
	i32 := func(v int32) *int32 { return &v }

	id, err := q.CreateVideoReport(ctx, sqlcgen.CreateVideoReportParams{
		ReporterID: reporter, VideoID: video, Reason: "bad",
		PredefinedReasons: []string{"privacy", "captions"}, VideoStartSeconds: i32(3), VideoEndSeconds: i32(8),
	})
	if err != nil || id == uuid.Nil {
		t.Fatalf("CreateVideoReport = %s, %v", id, err)
	}
	// A nil reasons slice is stored as '{}', not NULL.
	if _, err := q.CreateVideoReport(ctx, sqlcgen.CreateVideoReportParams{ReporterID: owner, VideoID: video, Reason: "plain"}); err != nil {
		t.Fatalf("report without reasons: %v", err)
	}

	hit, miss := "captions", "rights"
	rows, err := q.ListReports(ctx, sqlcgen.ListReportsParams{PredefinedReason: &hit, ResultLimit: 50})
	if err != nil || len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("ListReports(captions) = %d rows, %v", len(rows), err)
	}
	if r := rows[0]; len(r.PredefinedReasons) != 2 || r.VideoStartSeconds == nil || *r.VideoStartSeconds != 3 || *r.VideoEndSeconds != 8 {
		t.Errorf("row = %+v", r)
	}
	if n, _ := q.CountReports(ctx, sqlcgen.CountReportsParams{PredefinedReason: &hit}); n != 1 {
		t.Errorf("CountReports(captions) = %d, want 1 (must match the list)", n)
	}
	if n, _ := q.CountReports(ctx, sqlcgen.CountReportsParams{PredefinedReason: &miss}); n != 0 {
		t.Errorf("CountReports(rights) = %d, want 0", n)
	}

	// The schema holds the range invariants even if a writer skips the service.
	var pgErr *pgconn.PgError
	for name, stmt := range map[string]string{
		"end <= start":      `INSERT INTO reports (reporter_id, target_type, video_id, reason, video_start_seconds, video_end_seconds) VALUES ($1, 'video', $2, 'x', 5, 5)`,
		"negative start":    `INSERT INTO reports (reporter_id, target_type, video_id, reason, video_start_seconds) VALUES ($1, 'video', $2, 'x', -1)`,
		"end without start": `INSERT INTO reports (reporter_id, target_type, video_id, reason, video_end_seconds) VALUES ($1, 'video', $2, 'x', 5)`,
	} {
		_, err := st.Pool.Exec(ctx, stmt, uuid.New(), video.Bytes)
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Errorf("%s: err = %v, want a check violation", name, err)
		}
	}
}
