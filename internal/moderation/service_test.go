package moderation

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type reportRow struct {
	id         uuid.UUID
	reporterID uuid.UUID
	targetType string
	videoID    pgtype.UUID
	commentID  pgtype.UUID
	reason     string
	status     string
	note       string
	createdAt  time.Time
	resolvedAt pgtype.Timestamptz
}

// fakeRepo is an in-memory moderation.Repository.
type fakeRepo struct {
	reports     []reportRow
	commentErr  error // returned by CreateCommentReport when set
	blocked     map[uuid.UUID]bool
	blockReason map[uuid.UUID]string
	blockOrder  []uuid.UUID // block order (oldest first)
	blockErr    error       // returned by BlockVideo when set (e.g. a FK violation)
}

func (f *fakeRepo) CreateVideoReport(_ context.Context, a sqlcgen.CreateVideoReportParams) (int64, error) {
	for _, r := range f.reports {
		if r.reporterID == a.ReporterID && r.videoID == a.VideoID {
			return 0, nil // already reported
		}
	}
	f.reports = append(f.reports, reportRow{
		id: uuid.New(), reporterID: a.ReporterID, targetType: TargetVideo,
		videoID: a.VideoID, reason: a.Reason, status: StatusOpen, createdAt: time.Now(),
	})
	return 1, nil
}

func (f *fakeRepo) CreateCommentReport(_ context.Context, a sqlcgen.CreateCommentReportParams) (int64, error) {
	if f.commentErr != nil {
		return 0, f.commentErr
	}
	f.reports = append(f.reports, reportRow{
		id: uuid.New(), reporterID: a.ReporterID, targetType: TargetComment,
		commentID: a.CommentID, reason: a.Reason, status: StatusOpen, createdAt: time.Now(),
	})
	return 1, nil
}

func (f *fakeRepo) ListReports(_ context.Context, a sqlcgen.ListReportsParams) ([]sqlcgen.ListReportsRow, error) {
	var rows []sqlcgen.ListReportsRow
	for i := len(f.reports) - 1; i >= 0; i-- { // newest first
		r := f.reports[i]
		if a.OpenOnly && r.status != StatusOpen {
			continue
		}
		rows = append(rows, sqlcgen.ListReportsRow{
			ID: r.id, TargetType: r.targetType, VideoID: r.videoID, CommentID: r.commentID,
			Reason: r.reason, Status: r.status, ModeratorNote: r.note,
			ResolvedAt: r.resolvedAt, CreatedAt: r.createdAt, ReporterUsername: "reporter",
		})
	}
	return rows, nil
}

func (f *fakeRepo) ResolveReport(_ context.Context, a sqlcgen.ResolveReportParams) (int64, error) {
	for i := range f.reports {
		if f.reports[i].id == a.ID {
			f.reports[i].status = a.Status
			f.reports[i].note = a.ModeratorNote
			f.reports[i].resolvedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeRepo) BlockVideo(_ context.Context, a sqlcgen.BlockVideoParams) (int64, error) {
	if f.blockErr != nil {
		return 0, f.blockErr
	}
	if f.blocked == nil {
		f.blocked = map[uuid.UUID]bool{}
		f.blockReason = map[uuid.UUID]string{}
	}
	if !f.blocked[a.VideoID] {
		f.blockOrder = append(f.blockOrder, a.VideoID)
	}
	f.blocked[a.VideoID] = true
	f.blockReason[a.VideoID] = a.Reason
	return 1, nil
}

func (f *fakeRepo) UnblockVideo(_ context.Context, videoID uuid.UUID) (int64, error) {
	if f.blocked[videoID] {
		delete(f.blocked, videoID)
		delete(f.blockReason, videoID)
		for i, id := range f.blockOrder {
			if id == videoID {
				f.blockOrder = append(f.blockOrder[:i], f.blockOrder[i+1:]...)
				break
			}
		}
		return 1, nil
	}
	return 0, nil
}

func (f *fakeRepo) IsVideoBlocked(_ context.Context, videoID uuid.UUID) (bool, error) {
	return f.blocked[videoID], nil
}

func (f *fakeRepo) ListBlockedVideos(_ context.Context, a sqlcgen.ListBlockedVideosParams) ([]sqlcgen.ListBlockedVideosRow, error) {
	var rows []sqlcgen.ListBlockedVideosRow
	for i := len(f.blockOrder) - 1; i >= 0; i-- { // newest block first
		vid := f.blockOrder[i]
		rows = append(rows, sqlcgen.ListBlockedVideosRow{VideoID: vid, Reason: f.blockReason[vid]})
	}
	off := min(int(a.ResultOffset), len(rows))
	rows = rows[off:]
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(rows) {
		rows = rows[:a.ResultLimit]
	}
	return rows, nil
}

func TestReportListAndDedup(t *testing.T) {
	svc := NewService(&fakeRepo{})
	ctx := context.Background()
	reporter, vid, cid := uuid.New(), uuid.New(), uuid.New()

	if err := svc.ReportVideo(ctx, reporter, vid, "spam"); err != nil {
		t.Fatalf("ReportVideo: %v", err)
	}
	if err := svc.ReportVideo(ctx, reporter, vid, "spam again"); err != nil { // idempotent
		t.Fatalf("ReportVideo dup: %v", err)
	}
	if err := svc.ReportComment(ctx, reporter, cid, "abuse"); err != nil {
		t.Fatalf("ReportComment: %v", err)
	}

	items, err := svc.List(ctx, false, 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("reports = %d, want 2 (video dedup'd)", len(items))
	}
	// Newest first: the comment report came last.
	if items[0].TargetType != TargetComment || items[1].TargetType != TargetVideo {
		t.Errorf("order = [%s, %s], want [comment, video]", items[0].TargetType, items[1].TargetType)
	}
}

func TestReportCommentInvalidTarget(t *testing.T) {
	svc := NewService(&fakeRepo{commentErr: &pgconn.PgError{Code: "23503"}})
	if err := svc.ReportComment(context.Background(), uuid.New(), uuid.New(), "x"); err != ErrInvalidTarget {
		t.Errorf("err = %v, want ErrInvalidTarget", err)
	}
}

func TestBlockUnblockVideo(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()
	mod, vid := uuid.New(), uuid.New()

	if blocked, _ := svc.IsBlocked(ctx, vid); blocked {
		t.Fatal("video should not be blocked initially")
	}
	if err := svc.BlockVideo(ctx, mod, vid, "spam"); err != nil {
		t.Fatalf("BlockVideo: %v", err)
	}
	if blocked, _ := svc.IsBlocked(ctx, vid); !blocked {
		t.Error("video should be blocked after BlockVideo")
	}
	// Re-blocking is idempotent.
	if err := svc.BlockVideo(ctx, mod, vid, "still spam"); err != nil {
		t.Fatalf("re-BlockVideo: %v", err)
	}
	if err := svc.UnblockVideo(ctx, vid); err != nil {
		t.Fatalf("UnblockVideo: %v", err)
	}
	if blocked, _ := svc.IsBlocked(ctx, vid); blocked {
		t.Error("video should not be blocked after UnblockVideo")
	}
	// Unblocking an already-unblocked video is a no-op (no error).
	if err := svc.UnblockVideo(ctx, vid); err != nil {
		t.Errorf("idempotent UnblockVideo: %v", err)
	}
}

func TestListBlocked(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()
	mod := uuid.New()
	v1, v2 := uuid.New(), uuid.New()

	if items, _ := svc.ListBlocked(ctx, 20, 0); len(items) != 0 {
		t.Fatalf("blocked list before any block = %d, want 0", len(items))
	}
	if err := svc.BlockVideo(ctx, mod, v1, "spam"); err != nil {
		t.Fatalf("block v1: %v", err)
	}
	if err := svc.BlockVideo(ctx, mod, v2, "abuse"); err != nil {
		t.Fatalf("block v2: %v", err)
	}
	items, err := svc.ListBlocked(ctx, 20, 0)
	if err != nil {
		t.Fatalf("ListBlocked: %v", err)
	}
	// Newest block first: v2 then v1, each carrying its reason.
	if len(items) != 2 {
		t.Fatalf("blocked list = %d, want 2", len(items))
	}
	if items[0].VideoID != v2 || items[0].Reason != "abuse" {
		t.Errorf("items[0] = {%s,%q}, want {v2,abuse}", items[0].VideoID, items[0].Reason)
	}
	if items[1].VideoID != v1 || items[1].Reason != "spam" {
		t.Errorf("items[1] = {%s,%q}, want {v1,spam}", items[1].VideoID, items[1].Reason)
	}
	// Unblocking removes it from the list.
	if err := svc.UnblockVideo(ctx, v1); err != nil {
		t.Fatalf("unblock v1: %v", err)
	}
	items, _ = svc.ListBlocked(ctx, 20, 0)
	if len(items) != 1 || items[0].VideoID != v2 {
		t.Errorf("blocked list after unblock = %+v, want [v2]", items)
	}
}

func TestBlockVideoNotFound(t *testing.T) {
	svc := NewService(&fakeRepo{blockErr: &pgconn.PgError{Code: "23503"}})
	if err := svc.BlockVideo(context.Background(), uuid.New(), uuid.New(), "x"); err != ErrVideoNotFound {
		t.Errorf("err = %v, want ErrVideoNotFound", err)
	}
}

func TestResolveAndNotFound(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()
	reporter, vid, mod := uuid.New(), uuid.New(), uuid.New()
	_ = svc.ReportVideo(ctx, reporter, vid, "spam")

	items, _ := svc.List(ctx, true, 20, 0)
	id := items[0].ID

	if err := svc.Resolve(ctx, mod, id, StatusAccepted, "actioned"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// It's no longer in the open queue.
	if open, _ := svc.List(ctx, true, 20, 0); len(open) != 0 {
		t.Errorf("open after resolve = %d, want 0", len(open))
	}
	// Unknown id → ErrNotFound.
	if err := svc.Resolve(ctx, mod, uuid.New(), StatusRejected, ""); err != ErrNotFound {
		t.Errorf("resolve unknown = %v, want ErrNotFound", err)
	}
}
