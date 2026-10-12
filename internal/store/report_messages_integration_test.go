//go:build integration

// Integration test for report message threads and their notification fan-out
// (migration 0163). Every who-is-told rule lives in SQL, so it is proved here
// on real PostgreSQL. The DB is shared with sibling tests that leave staff
// behind, so membership is asserted only over this test's own seeded users.
package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestReportMessageFanOutOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	var seeded []uuid.UUID
	mkUser := func(name, role string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash, role) VALUES ($1, $2, 'x', $3) RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test", role,
		).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		seeded = append(seeded, id)
		return id
	}
	reporter := mkUser("rm-reporter", "user")
	admin := mkUser("rm-admin", "admin")
	mod := mkUser("rm-mod", "moderator")
	plain := mkUser("rm-user", "user")
	inactive := mkUser("rm-inactive", "admin")
	deleted := mkUser("rm-deleted", "moderator")
	prefOff := mkUser("rm-prefoff", "moderator")
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET is_active = FALSE WHERE id = $1`, inactive); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, deleted); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertNotificationPref(ctx, sqlcgen.UpsertNotificationPrefParams{UserID: prefOff, Type: "report_message", Enabled: false}); err != nil {
		t.Fatal(err)
	}

	reportID, err := q.CreateAccountReport(ctx, sqlcgen.CreateAccountReportParams{
		ReporterID: reporter, ReportedUserID: pgtype.UUID{Bytes: plain, Valid: true},
		Reason: "seeded by TestReportMessageFanOutOnRealPG",
	})
	if err != nil {
		t.Fatalf("CreateAccountReport: %v", err)
	}
	if got, err := q.GetReportReporterID(ctx, reportID); err != nil || uuid.UUID(got.Bytes) != reporter {
		t.Fatalf("GetReportReporterID = %v, %v; want the reporter", got, err)
	}

	post := func(author uuid.UUID, byModerator bool, body string) uuid.UUID {
		t.Helper()
		row, err := q.CreateReportMessage(ctx, sqlcgen.CreateReportMessageParams{
			ReportID: reportID, AuthorID: author, IsModerator: byModerator, Body: body})
		if err != nil {
			t.Fatalf("CreateReportMessage: %v", err)
		}
		return row.ID
	}
	// told returns which seeded users hold a report_message notification for
	// msg, with the actor each one carries.
	told := func(msg uuid.UUID) map[uuid.UUID]pgtype.UUID {
		t.Helper()
		rows, err := st.Pool.Query(ctx,
			`SELECT user_id, actor_id FROM notifications
			  WHERE type = 'report_message' AND report_message_id = $1 AND report_id = $2 AND user_id = ANY($3)`,
			msg, reportID, seeded)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := map[uuid.UUID]pgtype.UUID{}
		for rows.Next() {
			var id uuid.UUID
			var actor pgtype.UUID
			if err := rows.Scan(&id, &actor); err != nil {
				t.Fatal(err)
			}
			got[id] = actor
		}
		return got
	}

	// The reporter writes: the active, opted-in staff are told, with the
	// reporter as the actor; nobody else, and never the author.
	fromReporter := post(reporter, false, "here is more context")
	if _, err := q.NotifyReportMessage(ctx, fromReporter); err != nil {
		t.Fatalf("NotifyReportMessage: %v", err)
	}
	got := told(fromReporter)
	if len(got) != 2 || uuid.UUID(got[admin].Bytes) != reporter || uuid.UUID(got[mod].Bytes) != reporter {
		t.Errorf("reporter message told %v, want exactly admin + moderator with the reporter as actor", got)
	}
	if n, err := q.NotifyReportMessage(ctx, fromReporter); err != nil || n != 0 {
		t.Errorf("repeat fan-out inserted %d, %v; want 0 (idempotent per message)", n, err)
	}

	// A moderator writes: only the reporter is told, and no actor names the
	// moderator; the other staff are not told about a staff message.
	fromMod := post(mod, true, "which video?")
	if _, err := q.NotifyReportMessage(ctx, fromMod); err != nil {
		t.Fatalf("NotifyReportMessage: %v", err)
	}
	got = told(fromMod)
	if actor, ok := got[reporter]; len(got) != 1 || !ok || actor.Valid {
		t.Errorf("moderator message told %v, want only the reporter, with no actor", got)
	}

	// A reporter who turned report_message off hears nothing.
	if err := q.UpsertNotificationPref(ctx, sqlcgen.UpsertNotificationPrefParams{UserID: reporter, Type: "report_message", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	quiet := post(admin, true, "anything else?")
	if n, err := q.NotifyReportMessage(ctx, quiet); err != nil || len(told(quiet)) != 0 {
		t.Errorf("opted-out reporter fan-out = %d, %v, told %v; want nobody", n, err, told(quiet))
	}

	// The thread reads back oldest first with each author's username; the
	// count agrees with the list.
	msgs, err := q.ListReportMessages(ctx, sqlcgen.ListReportMessagesParams{ReportID: reportID, ResultLimit: 10})
	if err != nil || len(msgs) != 3 {
		t.Fatalf("ListReportMessages = %d, %v; want 3", len(msgs), err)
	}
	if msgs[0].ID != fromReporter || msgs[0].IsModerator || msgs[1].ID != fromMod || !msgs[1].IsModerator ||
		!strings.HasPrefix(msgs[1].AuthorUsername, "rm-mod-") {
		t.Errorf("thread = %+v, want reporter then moderator, oldest first, with usernames", msgs)
	}
	if n, err := q.CountReportMessages(ctx, reportID); err != nil || n != 3 {
		t.Errorf("CountReportMessages = %d, %v; want 3", n, err)
	}

	// The body is bounded in the database, not just the handler.
	_, err = q.CreateReportMessage(ctx, sqlcgen.CreateReportMessageParams{
		ReportID: reportID, AuthorID: reporter, Body: strings.Repeat("x", 3001)})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("3001-char body err = %v, want a CHECK violation", err)
	}

	// A departed moderator's message stays in the thread, authorless.
	if _, err := st.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, mod); err != nil {
		t.Fatal(err)
	}
	if msgs, err := q.ListReportMessages(ctx, sqlcgen.ListReportMessagesParams{ReportID: reportID, ResultLimit: 10}); err != nil ||
		len(msgs) != 3 || msgs[1].AuthorID.Valid || msgs[1].AuthorUsername != "" {
		t.Errorf("after the author's deletion thread = %+v, %v; want the message kept with no author", msgs, err)
	}
}

// A remote Flag report has no local reporter: a moderator's message on it (the
// handler refuses to write one; this proves the fan-out would not misdeliver
// even if a row existed) tells nobody.
func TestReportMessageFanOutRemoteReportOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	_, channelID := seedOwnerChannel(ctx, t, st, "rmflag")
	videoID := seedPublishedVideo(ctx, t, st, channelID, "flagged")
	staff, _ := seedOwnerChannel(ctx, t, st, "rmflagstaff")
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1`, staff); err != nil {
		t.Fatal(err)
	}
	reportID, err := q.CreateRemoteFlagReport(ctx, sqlcgen.CreateRemoteFlagReportParams{
		TargetType: "video", VideoID: pgtype.UUID{Bytes: videoID, Valid: true}, Reason: "spam",
		ActorUrl: "https://pt.example/accounts/peertube", Domain: "pt.example",
		FlagActivityID: "https://pt.example/admin/abuses/" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateRemoteFlagReport: %v", err)
	}
	if got, err := q.GetReportReporterID(ctx, reportID); err != nil || got.Valid {
		t.Fatalf("GetReportReporterID(remote) = %v, %v; want NULL", got, err)
	}
	row, err := q.CreateReportMessage(ctx, sqlcgen.CreateReportMessageParams{
		ReportID: reportID, AuthorID: staff, IsModerator: true, Body: "hello?"})
	if err != nil {
		t.Fatalf("CreateReportMessage: %v", err)
	}
	if n, err := q.NotifyReportMessage(ctx, row.ID); err != nil || n != 0 {
		t.Errorf("remote-report fan-out = %d, %v; want 0 (no local reporter)", n, err)
	}
}

// TestListMyReportsOnRealPG proves the "My reports" read: only the caller's
// own reports, most recent activity first (a thread message or a resolution
// moves a report up), a message count, the report_id narrowing, a video title
// only while the video is still public, and a count that agrees with the list.
func TestListMyReportsOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	reporter, _ := seedOwnerChannel(ctx, t, st, "mine")
	other, _ := seedOwnerChannel(ctx, t, st, "notmine")
	_, channelID := seedOwnerChannel(ctx, t, st, "minechan")
	public := seedPublishedVideo(ctx, t, st, channelID, "still public")
	hidden := seedPublishedVideo(ctx, t, st, channelID, "made private later")
	file := func(by, video uuid.UUID) uuid.UUID {
		t.Helper()
		id, err := q.CreateVideoReport(ctx, sqlcgen.CreateVideoReportParams{
			ReporterID: by, VideoID: pgtype.UUID{Bytes: video, Valid: true}, Reason: "seeded by TestListMyReportsOnRealPG"})
		if err != nil {
			t.Fatalf("CreateVideoReport: %v", err)
		}
		return id
	}
	first := file(reporter, public)
	second := file(reporter, hidden)
	_ = file(other, public)
	if _, err := st.Pool.Exec(ctx, `UPDATE reports SET created_at = now() - interval '1 hour' WHERE id = $1`, first); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE videos SET privacy = 'private' WHERE id = $1`, hidden); err != nil {
		t.Fatal(err)
	}
	list := func() []sqlcgen.ListMyReportsRow {
		t.Helper()
		rows, err := q.ListMyReports(ctx, sqlcgen.ListMyReportsParams{ReporterID: reporter, ResultLimit: 10})
		if err != nil {
			t.Fatalf("ListMyReports: %v", err)
		}
		return rows
	}

	rows := list()
	if len(rows) != 2 || rows[0].ID != second || rows[1].ID != first {
		t.Fatalf("ListMyReports = %+v, want [second, first] and none of the other reporter's", rows)
	}
	if rows[0].VideoTitle != "" || rows[1].VideoTitle != "still public" {
		t.Errorf("titles = %q / %q, want the private video's hidden", rows[0].VideoTitle, rows[1].VideoTitle)
	}
	if n, err := q.CountMyReports(ctx, reporter); err != nil || n != 2 {
		t.Errorf("CountMyReports = %d, %v; want 2", n, err)
	}

	// A thread message on the older report makes it the most recently active.
	if _, err := q.CreateReportMessage(ctx, sqlcgen.CreateReportMessageParams{
		ReportID: first, AuthorID: reporter, Body: "more context"}); err != nil {
		t.Fatal(err)
	}
	rows = list()
	if rows[0].ID != first || rows[0].MessageCount != 1 || rows[1].MessageCount != 0 {
		t.Errorf("after a message: %+v, want first on top with message_count 1", rows)
	}
	if !rows[0].LastActivityAt.After(rows[0].CreatedAt) {
		t.Errorf("last_activity_at %v not after created_at %v", rows[0].LastActivityAt, rows[0].CreatedAt)
	}

	one, err := q.ListMyReports(ctx, sqlcgen.ListMyReportsParams{ReporterID: reporter,
		ReportID: pgtype.UUID{Bytes: second, Valid: true}, ResultLimit: 1})
	if err != nil || len(one) != 1 || one[0].ID != second {
		t.Errorf("ListMyReports(report_id) = %+v, %v; want just that report", one, err)
	}
	none, err := q.ListMyReports(ctx, sqlcgen.ListMyReportsParams{ReporterID: other,
		ReportID: pgtype.UUID{Bytes: second, Valid: true}, ResultLimit: 1})
	if err != nil || len(none) != 0 {
		t.Errorf("another reporter's ListMyReports(report_id) = %+v, %v; want nothing", none, err)
	}
}
