package moderation

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type reportRow struct {
	id             uuid.UUID
	reporterID     uuid.UUID
	targetType     string
	videoID        pgtype.UUID
	commentID      pgtype.UUID
	reportedUserID pgtype.UUID
	remoteVideoID  pgtype.UUID
	messageID      pgtype.UUID
	messageBody    string
	reason         string
	status         string
	note           string
	createdAt      time.Time
	resolvedAt     pgtype.Timestamptz
}

// fakeRepo is an in-memory moderation.Repository.
type fakeRepo struct {
	reports     []reportRow
	commentErr  error // returned by CreateCommentReport when set
	accountErr  error // returned by CreateAccountReport when set (e.g. a FK violation)
	blocked     map[uuid.UUID]bool
	blockReason map[uuid.UUID]string
	blockOrder  []uuid.UUID // block order (oldest first)
	blockErr    error       // returned by BlockVideo when set (e.g. a FK violation)
	// remote-video moderation (remote-content §8)
	remoteVideoErr   error // returned by the remote-video writes when set (e.g. a FK violation)
	remoteBlocked    map[uuid.UUID]string
	remoteBlockOrder []uuid.UUID
}

func (f *fakeRepo) CreateVideoReport(_ context.Context, a sqlcgen.CreateVideoReportParams) (uuid.UUID, error) {
	for _, r := range f.reports {
		if r.reporterID == a.ReporterID && r.videoID == a.VideoID {
			return uuid.Nil, pgx.ErrNoRows // already reported: ON CONFLICT DO NOTHING yields no row
		}
	}
	id := uuid.New()
	f.reports = append(f.reports, reportRow{
		id: id, reporterID: a.ReporterID, targetType: TargetVideo,
		videoID: a.VideoID, reason: a.Reason, status: StatusOpen, createdAt: time.Now(),
	})
	return id, nil
}

func (f *fakeRepo) CreateCommentReport(_ context.Context, a sqlcgen.CreateCommentReportParams) (uuid.UUID, error) {
	if f.commentErr != nil {
		return uuid.Nil, f.commentErr
	}
	id := uuid.New()
	f.reports = append(f.reports, reportRow{
		id: id, reporterID: a.ReporterID, targetType: TargetComment,
		commentID: a.CommentID, reason: a.Reason, status: StatusOpen, createdAt: time.Now(),
	})
	return id, nil
}

func (f *fakeRepo) CreateAccountReport(_ context.Context, a sqlcgen.CreateAccountReportParams) (uuid.UUID, error) {
	if f.accountErr != nil {
		return uuid.Nil, f.accountErr
	}
	for _, r := range f.reports {
		if r.reporterID == a.ReporterID && r.reportedUserID == a.ReportedUserID {
			return uuid.Nil, pgx.ErrNoRows // already reported: ON CONFLICT DO NOTHING yields no row
		}
	}
	id := uuid.New()
	f.reports = append(f.reports, reportRow{
		id: id, reporterID: a.ReporterID, targetType: TargetAccount,
		reportedUserID: a.ReportedUserID, reason: a.Reason, status: StatusOpen, createdAt: time.Now(),
	})
	return id, nil
}

func (f *fakeRepo) CreateMessageReport(_ context.Context, a sqlcgen.CreateMessageReportParams) (uuid.UUID, error) {
	for _, r := range f.reports {
		if r.reporterID == a.ReporterID && r.messageID == a.MessageID {
			return uuid.Nil, pgx.ErrNoRows // already reported: ON CONFLICT DO NOTHING yields no row
		}
	}
	id := uuid.New()
	f.reports = append(f.reports, reportRow{
		id: id, reporterID: a.ReporterID, targetType: TargetMessage,
		messageID: a.MessageID, messageBody: a.MessageBodySnapshot, reason: a.Reason,
		status: StatusOpen, createdAt: time.Now(),
	})
	return id, nil
}

func (f *fakeRepo) ListReports(_ context.Context, a sqlcgen.ListReportsParams) ([]sqlcgen.ListReportsRow, error) {
	var rows []sqlcgen.ListReportsRow
	for i := len(f.reports) - 1; i >= 0; i-- { // newest first
		r := f.reports[i]
		if a.Status != nil {
			if *a.Status == StatusResolved {
				if r.status == StatusOpen {
					continue
				}
			} else if r.status != *a.Status {
				continue
			}
		}
		row := sqlcgen.ListReportsRow{
			ID: r.id, TargetType: r.targetType, VideoID: r.videoID, CommentID: r.commentID,
			ReportedUserID: r.reportedUserID, MessageID: r.messageID, MessageBodySnapshot: r.messageBody,
			Reason: r.reason, Status: r.status,
			ModeratorNote: r.note, ResolvedAt: r.resolvedAt, CreatedAt: r.createdAt,
			ReporterUsername: "reporter",
		}
		if r.reportedUserID.Valid {
			name := "target"
			row.ReportedUsername = &name
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (f *fakeRepo) ResolveReport(_ context.Context, a sqlcgen.ResolveReportParams) (uuid.UUID, error) {
	for i := range f.reports {
		if f.reports[i].id == a.ID {
			f.reports[i].status = a.Status
			f.reports[i].note = a.ModeratorNote
			f.reports[i].resolvedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return f.reports[i].reporterID, nil
		}
	}
	return uuid.Nil, pgx.ErrNoRows
}

func (f *fakeRepo) DeleteReport(_ context.Context, id uuid.UUID) (int64, error) {
	for i := range f.reports {
		if f.reports[i].id == id {
			f.reports = append(f.reports[:i], f.reports[i+1:]...)
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

func (f *fakeRepo) CreateRemoteVideoReport(_ context.Context, a sqlcgen.CreateRemoteVideoReportParams) (uuid.UUID, error) {
	if f.remoteVideoErr != nil {
		return uuid.Nil, f.remoteVideoErr
	}
	for _, r := range f.reports {
		if r.reporterID == a.ReporterID && r.remoteVideoID == a.RemoteVideoID {
			return uuid.Nil, pgx.ErrNoRows // already reported: ON CONFLICT DO NOTHING yields no row
		}
	}
	id := uuid.New()
	f.reports = append(f.reports, reportRow{
		id: id, reporterID: a.ReporterID, targetType: TargetRemoteVideo,
		remoteVideoID: a.RemoteVideoID, reason: a.Reason, status: StatusOpen, createdAt: time.Now(),
	})
	return id, nil
}

func (f *fakeRepo) BlockRemoteVideo(_ context.Context, a sqlcgen.BlockRemoteVideoParams) (int64, error) {
	if f.remoteVideoErr != nil {
		return 0, f.remoteVideoErr
	}
	if f.remoteBlocked == nil {
		f.remoteBlocked = map[uuid.UUID]string{}
	}
	if _, ok := f.remoteBlocked[a.RemoteVideoID]; !ok {
		f.remoteBlockOrder = append(f.remoteBlockOrder, a.RemoteVideoID)
	}
	f.remoteBlocked[a.RemoteVideoID] = a.Reason
	return 1, nil
}

func (f *fakeRepo) UnblockRemoteVideo(_ context.Context, id uuid.UUID) (int64, error) {
	if _, ok := f.remoteBlocked[id]; !ok {
		return 0, nil
	}
	delete(f.remoteBlocked, id)
	for i, v := range f.remoteBlockOrder {
		if v == id {
			f.remoteBlockOrder = append(f.remoteBlockOrder[:i], f.remoteBlockOrder[i+1:]...)
			break
		}
	}
	return 1, nil
}

func (f *fakeRepo) ListBlockedRemoteVideos(_ context.Context, a sqlcgen.ListBlockedRemoteVideosParams) ([]sqlcgen.ListBlockedRemoteVideosRow, error) {
	var rows []sqlcgen.ListBlockedRemoteVideosRow
	for i := len(f.remoteBlockOrder) - 1; i >= 0; i-- { // newest block first
		id := f.remoteBlockOrder[i]
		rows = append(rows, sqlcgen.ListBlockedRemoteVideosRow{RemoteVideoID: id, Reason: f.remoteBlocked[id]})
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

	firstID, err := svc.ReportVideo(ctx, reporter, vid, "spam")
	if err != nil {
		t.Fatalf("ReportVideo: %v", err)
	}
	if firstID == uuid.Nil {
		t.Fatal("ReportVideo returned uuid.Nil for a new report, want its id")
	}
	dupID, err := svc.ReportVideo(ctx, reporter, vid, "spam again") // idempotent
	if err != nil {
		t.Fatalf("ReportVideo dup: %v", err)
	}
	if dupID != uuid.Nil {
		t.Errorf("ReportVideo dup id = %s, want uuid.Nil (no staff re-notify)", dupID)
	}
	if _, err := svc.ReportComment(ctx, reporter, cid, "abuse"); err != nil {
		t.Fatalf("ReportComment: %v", err)
	}

	items, _, err := svc.List(ctx, "", 20, 0)
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
	if _, err := svc.ReportComment(context.Background(), uuid.New(), uuid.New(), "x"); err != ErrInvalidTarget {
		t.Errorf("err = %v, want ErrInvalidTarget", err)
	}
}

func TestReportAccount(t *testing.T) {
	ctx := context.Background()
	reporter, target := uuid.New(), uuid.New()

	svc := NewService(&fakeRepo{})
	if _, err := svc.ReportAccount(ctx, reporter, target, "harassment"); err != nil {
		t.Fatalf("ReportAccount: %v", err)
	}
	// Idempotent: a second report of the same account is a no-op (no error).
	if _, err := svc.ReportAccount(ctx, reporter, target, "harassment again"); err != nil {
		t.Fatalf("ReportAccount dup: %v", err)
	}
	items, _, err := svc.List(ctx, "", 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("account reports = %d, want 1 (dedup'd)", len(items))
	}
	if items[0].TargetType != TargetAccount || items[0].ReportedUserID != target.String() || items[0].ReportedUsername != "target" {
		t.Errorf("item = %+v, want account report for %s (target)", items[0], target)
	}

	// Reporting yourself is rejected before any insert.
	if _, err := svc.ReportAccount(ctx, reporter, reporter, "me"); err != ErrCannotReportSelf {
		t.Errorf("self-report err = %v, want ErrCannotReportSelf", err)
	}

	// An unknown target account (FK violation) → ErrInvalidTarget.
	fkSvc := NewService(&fakeRepo{accountErr: &pgconn.PgError{Code: "23503"}})
	if _, err := fkSvc.ReportAccount(ctx, uuid.New(), uuid.New(), "x"); err != ErrInvalidTarget {
		t.Errorf("unknown target err = %v, want ErrInvalidTarget", err)
	}
}

func TestReportMessage(t *testing.T) {
	ctx := context.Background()
	reporter, msgID := uuid.New(), uuid.New()
	svc := NewService(&fakeRepo{})

	if _, err := svc.ReportMessage(ctx, reporter, msgID, "the reported text", "abuse"); err != nil {
		t.Fatalf("ReportMessage: %v", err)
	}
	// Idempotent per (reporter, message).
	if _, err := svc.ReportMessage(ctx, reporter, msgID, "the reported text", "abuse again"); err != nil {
		t.Fatalf("ReportMessage dup: %v", err)
	}
	items, _, err := svc.List(ctx, "", 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("message reports = %d, want 1 (dedup'd)", len(items))
	}
	if items[0].TargetType != TargetMessage || items[0].MessageID != msgID.String() || items[0].MessageBody != "the reported text" {
		t.Errorf("item = %+v, want message report with body snapshot", items[0])
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
	lifted, err := svc.UnblockVideo(ctx, vid)
	if err != nil {
		t.Fatalf("UnblockVideo: %v", err)
	}
	if !lifted {
		t.Error("UnblockVideo reported no block lifted after blocking one — the creator's notice would never fire")
	}
	if blocked, _ := svc.IsBlocked(ctx, vid); blocked {
		t.Error("video should not be blocked after UnblockVideo")
	}
	// Unblocking an already-unblocked video is a no-op (no error) — and it
	// reports so, which is what keeps a second notice out of the creator's inbox.
	again, err := svc.UnblockVideo(ctx, vid)
	if err != nil {
		t.Errorf("idempotent UnblockVideo: %v", err)
	}
	if again {
		t.Error("a repeated UnblockVideo claimed it lifted a block; a second 'your video is back' would follow")
	}
}

func TestListBlocked(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()
	mod := uuid.New()
	v1, v2 := uuid.New(), uuid.New()

	if items, _, _ := svc.ListBlocked(ctx, 20, 0); len(items) != 0 {
		t.Fatalf("blocked list before any block = %d, want 0", len(items))
	}
	if err := svc.BlockVideo(ctx, mod, v1, "spam"); err != nil {
		t.Fatalf("block v1: %v", err)
	}
	if err := svc.BlockVideo(ctx, mod, v2, "abuse"); err != nil {
		t.Fatalf("block v2: %v", err)
	}
	items, _, err := svc.ListBlocked(ctx, 20, 0)
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
	if _, err := svc.UnblockVideo(ctx, v1); err != nil {
		t.Fatalf("unblock v1: %v", err)
	}
	items, _, _ = svc.ListBlocked(ctx, 20, 0)
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
	_, _ = svc.ReportVideo(ctx, reporter, vid, "spam")

	items, _, _ := svc.List(ctx, StatusOpen, 20, 0)
	id := items[0].ID

	got, err := svc.Resolve(ctx, mod, id, StatusAccepted, "actioned")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// The reporter comes back so the caller can notify them.
	if got != reporter {
		t.Errorf("Resolve reporter = %s, want %s", got, reporter)
	}
	// It's no longer in the open queue.
	if open, _, _ := svc.List(ctx, StatusOpen, 20, 0); len(open) != 0 {
		t.Errorf("open after resolve = %d, want 0", len(open))
	}
	// Unknown id → ErrNotFound.
	if _, err := svc.Resolve(ctx, mod, uuid.New(), StatusRejected, ""); err != ErrNotFound {
		t.Errorf("resolve unknown = %v, want ErrNotFound", err)
	}
}

// TestDeleteReport proves the admin purge: the row is gone from the queue, and
// re-deleting (or deleting an unknown id) is an idempotent no-op.
func TestDeleteReport(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()
	reporter, video := uuid.New(), uuid.New()

	if _, err := svc.ReportVideo(ctx, reporter, video, "spam"); err != nil {
		t.Fatalf("ReportVideo: %v", err)
	}
	items, _, _ := svc.List(ctx, "", 20, 0)
	if len(items) != 1 {
		t.Fatalf("reports = %d, want 1", len(items))
	}
	id := items[0].ID

	if err := svc.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if items, _, _ := svc.List(ctx, "", 20, 0); len(items) != 0 {
		t.Fatalf("reports after delete = %d, want 0", len(items))
	}
	// Idempotent: unknown/already-deleted ids are a no-op.
	if err := svc.Delete(ctx, id); err != nil {
		t.Errorf("re-delete = %v, want nil", err)
	}
	if err := svc.Delete(ctx, uuid.New()); err != nil {
		t.Errorf("delete unknown = %v, want nil", err)
	}
}

func (f *fakeRepo) CountReports(ctx context.Context, status *string) (int64, error) {
	rows, err := f.ListReports(ctx, sqlcgen.ListReportsParams{Status: status, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *fakeRepo) CountBlockedVideos(ctx context.Context) (int64, error) {
	rows, err := f.ListBlockedVideos(ctx, sqlcgen.ListBlockedVideosParams{ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *fakeRepo) CountBlockedRemoteVideos(ctx context.Context) (int64, error) {
	rows, err := f.ListBlockedRemoteVideos(ctx, sqlcgen.ListBlockedRemoteVideosParams{ResultLimit: 1 << 30})
	return int64(len(rows)), err
}
