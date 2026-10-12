package moderation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeMessage mirrors a report_messages row (0163).
type fakeMessage struct {
	id, reportID, authorID uuid.UUID
	byModerator            bool
	body                   string
	createdAt              time.Time
}

func (f *fakeRepo) GetReportReporterID(_ context.Context, id uuid.UUID) (pgtype.UUID, error) {
	for _, r := range f.reports {
		if r.id == id {
			// A remote Flag report stores no reporter_id (0153).
			return pgtype.UUID{Bytes: r.reporterID, Valid: r.remoteActor == ""}, nil
		}
	}
	return pgtype.UUID{}, pgx.ErrNoRows
}

func (f *fakeRepo) CreateReportMessage(_ context.Context, a sqlcgen.CreateReportMessageParams) (sqlcgen.CreateReportMessageRow, error) {
	m := fakeMessage{id: uuid.New(), reportID: a.ReportID, authorID: a.AuthorID, byModerator: a.IsModerator,
		body: a.Body, createdAt: time.Now()}
	f.messages = append(f.messages, m)
	return sqlcgen.CreateReportMessageRow{ID: m.id, CreatedAt: m.createdAt}, nil
}

func (f *fakeRepo) ListReportMessages(_ context.Context, a sqlcgen.ListReportMessagesParams) ([]sqlcgen.ListReportMessagesRow, error) {
	var rows []sqlcgen.ListReportMessagesRow
	for _, m := range f.messages { // insertion order = oldest first
		if m.reportID == a.ReportID {
			rows = append(rows, sqlcgen.ListReportMessagesRow{ID: m.id, AuthorID: pgtype.UUID{Bytes: m.authorID, Valid: true},
				IsModerator: m.byModerator, Body: m.body, CreatedAt: m.createdAt})
		}
	}
	rows = rows[min(int(a.ResultOffset), len(rows)):]
	return rows[:min(int(a.ResultLimit), len(rows))], nil
}

func (f *fakeRepo) CountReportMessages(ctx context.Context, reportID uuid.UUID) (int64, error) {
	rows, err := f.ListReportMessages(ctx, sqlcgen.ListReportMessagesParams{ReportID: reportID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

// The thread's access rule: the report's local reporter writes as the
// reporter, staff who are not the reporter write as moderators, and anyone
// else — like an unknown report — is ErrNotFound, never a distinct "forbidden"
// that would confirm the report exists.
func TestReportThreadAccess(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()
	reporter, staff, stranger := uuid.New(), uuid.New(), uuid.New()
	reportID, err := svc.ReportVideo(ctx, reporter, uuid.New(), "spam")
	if err != nil {
		t.Fatal(err)
	}

	mine, err := svc.PostMessage(ctx, reportID, reporter, false, "more context")
	if err != nil || mine.ByModerator {
		t.Fatalf("reporter PostMessage = %+v, %v; want a reporter-side message", mine, err)
	}
	theirs, err := svc.PostMessage(ctx, reportID, staff, true, "which part?")
	if err != nil || !theirs.ByModerator {
		t.Fatalf("staff PostMessage = %+v, %v; want a moderator-side message", theirs, err)
	}
	// A staff member who filed the report writes as its reporter.
	if m, err := svc.PostMessage(ctx, reportID, reporter, true, "me again"); err != nil || m.ByModerator {
		t.Errorf("staff reporter PostMessage = %+v, %v; want the reporter side", m, err)
	}

	thread, err := svc.Messages(ctx, reportID, reporter, false, 50, 0)
	if err != nil || thread.Total != 3 || len(thread.Messages) != 3 || !thread.CanReply {
		t.Fatalf("reporter Messages = %+v, %v; want 3 messages and CanReply", thread, err)
	}
	if thread.Messages[0].Body != "more context" || !thread.Messages[1].ByModerator {
		t.Errorf("thread order = %+v, want oldest first", thread.Messages)
	}

	for _, tc := range []struct {
		name   string
		report uuid.UUID
		caller uuid.UUID
		staff  bool
	}{
		{"stranger", reportID, stranger, false},
		{"unknown report (reporter)", uuid.New(), reporter, false},
		{"unknown report (staff)", uuid.New(), staff, true},
	} {
		if _, err := svc.Messages(ctx, tc.report, tc.caller, tc.staff, 50, 0); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s Messages err = %v, want ErrNotFound", tc.name, err)
		}
		if _, err := svc.PostMessage(ctx, tc.report, tc.caller, tc.staff, "x"); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s PostMessage err = %v, want ErrNotFound", tc.name, err)
		}
	}
	if len(repo.messages) != 3 {
		t.Errorf("stored %d messages, want 3 (refused writes stored nothing)", len(repo.messages))
	}
}

// A remote Flag report has no local reporter: staff may read its thread, but
// nobody can write to it, and the thread says so.
func TestRemoteReportThreadIsReadOnly(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()
	reportID, err := svc.ReportFromRemote(ctx, RemoteReport{FlagActivityID: "https://pt.example/abuses/1",
		ActorURL: "https://pt.example/accounts/peertube", Domain: "pt.example",
		TargetType: TargetVideo, TargetID: uuid.New(), Reason: "spam"})
	if err != nil {
		t.Fatal(err)
	}
	staff := uuid.New()
	thread, err := svc.Messages(ctx, reportID, staff, true, 50, 0)
	if err != nil || thread.CanReply || thread.Total != 0 {
		t.Errorf("staff Messages(remote) = %+v, %v; want an empty, read-only thread", thread, err)
	}
	if _, err := svc.PostMessage(ctx, reportID, staff, true, "hello?"); !errors.Is(err, ErrNoLocalReporter) {
		t.Errorf("PostMessage(remote) err = %v, want ErrNoLocalReporter", err)
	}
	if _, err := svc.Messages(ctx, reportID, uuid.New(), false, 50, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("non-staff Messages(remote) err = %v, want ErrNotFound", err)
	}
	if len(repo.messages) != 0 {
		t.Errorf("stored %d messages on a remote report, want 0", len(repo.messages))
	}
}
