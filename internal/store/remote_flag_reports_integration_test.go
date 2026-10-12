//go:build integration

// Integration test for reports filed by another instance through an inbound
// ActivityPub Flag (migration 0153). Same harness contract as
// integration_test.go (live PostgreSQL via DATABASE_URL, migrations applied).
package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestRemoteFlagReportsOnRealPG proves the remote-report rules where they live —
// in the schema and the SQL: a report with no local reporter is storable only
// with a remote reporter, it is idempotent per (Flag, target) and per nothing
// else, the target FK still holds, the queue lists and counts it, and the staff
// fan-out reaches staff for it (the old `u.id <> r.reporter_id` would have
// notified nobody for a NULL reporter).
func TestRemoteFlagReportsOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	owner, channelID := seedOwnerChannel(ctx, t, st, "flag")
	videoID := seedPublishedVideo(ctx, t, st, channelID, "flagged video")
	staff, _ := seedOwnerChannel(ctx, t, st, "flagstaff")
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET role = 'moderator' WHERE id = $1`, staff); err != nil {
		t.Fatalf("promote staff: %v", err)
	}

	const actor = "https://peertube.example/accounts/peertube"
	flagID := "https://peertube.example/admin/abuses/" + uuid.NewString()
	fileFrom := func(domain, flag, target string, video, user pgtype.UUID) (uuid.UUID, error) {
		return q.CreateRemoteFlagReport(ctx, sqlcgen.CreateRemoteFlagReportParams{
			TargetType: target, VideoID: video, ReportedUserID: user,
			Reason:   "[spamOrMisleading] seeded by TestRemoteFlagReportsOnRealPG",
			ActorUrl: "https://" + domain + "/accounts/peertube", Domain: domain, FlagActivityID: flag,
		})
	}
	file := func(flag, target string, video, user pgtype.UUID) (uuid.UUID, error) {
		return fileFrom("peertube.example", flag, target, video, user)
	}
	vid := pgtype.UUID{Bytes: videoID, Valid: true}
	acct := pgtype.UUID{Bytes: owner, Valid: true}

	videoReport, err := file(flagID, "video", vid, pgtype.UUID{})
	if err != nil || videoReport == uuid.Nil {
		t.Fatalf("CreateRemoteFlagReport(video) = %s, %v", videoReport, err)
	}
	// The same Flag redelivered: no row.
	if _, err := file(flagID, "video", vid, pgtype.UUID{}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("redelivered Flag err = %v, want pgx.ErrNoRows", err)
	}
	// The same Flag naming a second target is a new report.
	if id, err := file(flagID, "account", pgtype.UUID{}, acct); err != nil || id == uuid.Nil {
		t.Errorf("second target of the same Flag = %s, %v; want a new report", id, err)
	}
	// At most one OPEN remote report per (reporting domain, target): a second
	// Flag from the same domain on the same open target is a no-op, while
	// another domain's Flag on it is a new report.
	if _, err := file(flagID+"-2", "video", vid, pgtype.UUID{}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("second Flag from the same domain on an open target err = %v, want pgx.ErrNoRows", err)
	}
	if id, err := fileFrom("mastodon.example", flagID+"-m", "video", vid, pgtype.UUID{}); err != nil || id == uuid.Nil {
		t.Errorf("another domain's Flag on the same video = %s, %v; want a new report", id, err)
	}
	// A local report on the same video coexists with the remote ones.
	if _, err := q.CreateVideoReport(ctx, sqlcgen.CreateVideoReportParams{ReporterID: staff, VideoID: vid, Reason: "local"}); err != nil {
		t.Errorf("local report alongside remote ones: %v", err)
	}
	// The target FK holds for remote reports too.
	var pgErr *pgconn.PgError
	if _, err := file(flagID, "video", pgtype.UUID{Bytes: uuid.New(), Valid: true}, pgtype.UUID{}); !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Errorf("unknown video err = %v, want a foreign-key violation", err)
	}
	// A report with no reporter of either kind is refused by the CHECK.
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO reports (target_type, video_id, reason) VALUES ('video', $1, 'orphan')`, videoID,
	); !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("reporterless insert err = %v, want a check violation", err)
	}

	// The queue lists the remote report with its remote reporter, and the
	// count agrees with the list.
	open := "open"
	rows, err := q.ListReports(ctx, sqlcgen.ListReportsParams{Status: &open, ResultLimit: 200})
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r.ID != videoReport {
			continue
		}
		found = true
		if r.ReporterUsername != "" || r.RemoteReporterActorUrl == nil || *r.RemoteReporterActorUrl != actor ||
			r.RemoteReporterDomain == nil || *r.RemoteReporterDomain != "peertube.example" {
			t.Errorf("queue row reporter = %q / %v / %v, want '' + the remote actor and domain",
				r.ReporterUsername, r.RemoteReporterActorUrl, r.RemoteReporterDomain)
		}
		if r.VideoTitle == nil || *r.VideoTitle != "flagged video" {
			t.Errorf("queue row lost its target context: %v", r.VideoTitle)
		}
	}
	if !found {
		t.Fatal("the remote report is missing from the moderation queue")
	}
	// One REPEATABLE READ snapshot for both counts, so sibling packages writing
	// reports into the shared database cannot make them disagree.
	tx, err := st.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var direct int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM reports WHERE status = 'open'`).Scan(&direct); err != nil {
		t.Fatalf("count reports: %v", err)
	}
	if n, err := q.WithTx(tx).CountReports(ctx, &open); err != nil || n != direct {
		t.Errorf("CountReports = %d, %v; want %d (every open report, remote ones included)", n, err, direct)
	}
	_ = tx.Rollback(ctx)

	// The staff fan-out reaches staff for a report with no local reporter, and
	// carries no actor.
	if n, err := q.NotifyStaffOfNewReport(ctx, videoReport); err != nil || n < 1 {
		t.Fatalf("NotifyStaffOfNewReport = %d, %v; want the seeded moderator notified", n, err)
	}
	var actorID pgtype.UUID
	if err := st.Pool.QueryRow(ctx,
		`SELECT actor_id FROM notifications WHERE report_id = $1 AND user_id = $2 AND type = 'new_report'`,
		videoReport, staff).Scan(&actorID); err != nil {
		t.Fatalf("the moderator was not notified of the remote report: %v", err)
	}
	if actorID.Valid {
		t.Errorf("remote report notification actor = %v, want NULL", actorID)
	}

	// Resolving it yields no local reporter to notify.
	reporter, err := q.ResolveReport(ctx, sqlcgen.ResolveReportParams{
		ID: videoReport, Status: "accepted", ResolvedBy: pgtype.UUID{Bytes: staff, Valid: true},
	})
	if err != nil || reporter.Valid {
		t.Errorf("ResolveReport(remote) = %v, %v; want a NULL reporter", reporter, err)
	}
	// Once that report is no longer open, the same domain can report the
	// target again.
	if id, err := file(flagID+"-3", "video", vid, pgtype.UUID{}); err != nil || id == uuid.Nil {
		t.Errorf("same domain after the open report was resolved = %s, %v; want a new report", id, err)
	}
}
