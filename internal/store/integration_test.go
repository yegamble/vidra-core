//go:build integration

// Integration tests require a live PostgreSQL and Redis reachable via the
// DATABASE_URL and REDIS_URL environment variables, with migrations already
// applied. Run with:
//
//	docker compose --profile core up -d postgres redis migrate
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	REDIS_URL=redis://localhost:6379/0 \
//	go test -tags=integration ./internal/store/...
package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func dsn(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

// TestFreshDatabaseHasFoundationTables proves migrations applied against the
// target database created the users and sessions tables.
func TestFreshDatabaseHasFoundationTables(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	for _, table := range []string{"users", "sessions", "channels", "channel_follows", "videos"} {
		var exists bool
		err := st.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
			table,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("query table %q: %v", table, err)
		}
		if !exists {
			t.Errorf("expected table %q to exist after migration", table)
		}
	}
}

// TestRequiredExtensionsInstalled proves the 0001 extensions migration applied.
func TestRequiredExtensionsInstalled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	for _, ext := range []string{"pg_trgm", "uuid-ossp"} {
		var exists bool
		err := st.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = $1)`,
			ext,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("query extension %q: %v", ext, err)
		}
		if !exists {
			t.Errorf("expected extension %q to be installed", ext)
		}
	}
}

// TestReorderPlaylistItemsPersists proves the ReorderPlaylistItems query rewrites
// item positions atomically against a real PostgreSQL: the unnest(...) WITH
// ORDINALITY UPDATE assigns each video its 1-based index, and the reordered
// items read back in the new order. Seeds the full FK chain and cleans up via
// the users→… ON DELETE CASCADE.
func TestReorderPlaylistItemsPersists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	var userID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"reorder-"+suffix, "reorder-"+suffix+"@example.test",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// ON DELETE CASCADE from users tears down channels/videos/playlists/items.
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) }()

	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Reorder') RETURNING id`,
		userID, "reorder_"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	vids := make([]uuid.UUID, 3)
	for i := range vids {
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, $2, 'public', 'published') RETURNING id`,
			channelID, "v",
		).Scan(&vids[i]); err != nil {
			t.Fatalf("seed video %d: %v", i, err)
		}
	}

	var playlistID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO playlists (owner_id, title) VALUES ($1, 'Mix') RETURNING id`, userID,
	).Scan(&playlistID); err != nil {
		t.Fatalf("seed playlist: %v", err)
	}
	for i, v := range vids {
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO playlist_items (playlist_id, video_id, position) VALUES ($1, $2, $3)`,
			playlistID, v, i+1,
		); err != nil {
			t.Fatalf("seed item %d: %v", i, err)
		}
	}

	// Reverse the order via the query under test.
	want := []uuid.UUID{vids[2], vids[1], vids[0]}
	if err := q.ReorderPlaylistItems(ctx, sqlcgen.ReorderPlaylistItemsParams{
		PlaylistID: playlistID,
		VideoIds:   want,
	}); err != nil {
		t.Fatalf("ReorderPlaylistItems: %v", err)
	}

	got, err := q.ListPlaylistItemVideoIDs(ctx, playlistID)
	if err != nil {
		t.Fatalf("ListPlaylistItemVideoIDs: %v", err)
	}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("order after reorder = %v, want %v", got, want)
	}
}

// TestUpdateCommentPersists proves the UpdateComment query edits the body and
// advances updated_at past created_at against a real PostgreSQL. Seeds the FK
// chain and cleans up via the users→… ON DELETE CASCADE.
func TestUpdateCommentPersists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	var userID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"cedit-"+suffix, "cedit-"+suffix+"@example.test",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) }()

	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'C') RETURNING id`,
		userID, "cedit_"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	var videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'v', 'public', 'published') RETURNING id`,
		channelID,
	).Scan(&videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	var commentID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO comments (video_id, user_id, body) VALUES ($1, $2, 'original') RETURNING id`,
		videoID, userID,
	).Scan(&commentID); err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	updated, err := q.UpdateComment(ctx, sqlcgen.UpdateCommentParams{ID: commentID, Body: "revised"})
	if err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	if updated.Body != "revised" {
		t.Errorf("body = %q, want revised", updated.Body)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) {
		t.Errorf("updated_at %v should be after created_at %v", updated.UpdatedAt, updated.CreatedAt)
	}

	got, err := q.GetComment(ctx, commentID)
	if err != nil || got.Body != "revised" {
		t.Fatalf("GetComment after edit = (%q, %v), want revised", got.Body, err)
	}
}

// TestSumUserStorageUsageAggregates proves the quota usage aggregate counts
// every video_files row (original + rendition + thumbnail) across the videos
// owned via a user's channels — and nobody else's — against a real PostgreSQL.
// Also exercises AdminUpdateUser's tri-state storage_quota_bytes (set, keep on
// unrelated edits, reset to NULL). Seeds the FK chain and cleans up via the
// users→… ON DELETE CASCADE.
func TestSumUserStorageUsageAggregates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	seedUser := func(name string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test",
		).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	seedVideo := func(owner uuid.UUID, handle string) uuid.UUID {
		t.Helper()
		var channelID uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Q') RETURNING id`,
			owner, handle+"_"+suffix,
		).Scan(&channelID); err != nil {
			t.Fatalf("seed channel %s: %v", handle, err)
		}
		var videoID uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'v', 'public', 'published') RETURNING id`,
			channelID,
		).Scan(&videoID); err != nil {
			t.Fatalf("seed video for %s: %v", handle, err)
		}
		return videoID
	}
	seedFile := func(videoID uuid.UUID, kind string, size int64) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO video_files (video_id, kind, storage_key, size_bytes) VALUES ($1, $2, $3, $4)`,
			videoID, kind, "quota-test/"+uuid.NewString(), size,
		); err != nil {
			t.Fatalf("seed %s file: %v", kind, err)
		}
	}

	ada := seedUser("qada")
	bob := seedUser("qbob")
	adaV1 := seedVideo(ada, "qada1")
	adaV2 := seedVideo(ada, "qada2")
	bobV := seedVideo(bob, "qbob1")

	// Every kind counts: original + thumbnail on one video, a rendition on the
	// other. Bob's file must not leak into ada's sum.
	seedFile(adaV1, "original", 100)
	seedFile(adaV1, "thumbnail", 20)
	seedFile(adaV2, "rendition", 50)
	seedFile(bobV, "original", 999)

	if used, err := q.SumUserStorageUsage(ctx, ada); err != nil || used != 170 {
		t.Errorf("ada usage = (%d, %v), want 170", used, err)
	}
	if used, err := q.SumUserStorageUsage(ctx, bob); err != nil || used != 999 {
		t.Errorf("bob usage = (%d, %v), want 999", used, err)
	}
	// A user with no files sums to 0 (COALESCE, not NULL/error).
	empty := seedUser("qempty")
	if used, err := q.SumUserStorageUsage(ctx, empty); err != nil || used != 0 {
		t.Errorf("empty usage = (%d, %v), want 0", used, err)
	}

	// ListUsers carries the same aggregate per row.
	rows, err := q.ListUsers(ctx, sqlcgen.ListUsersParams{Query: "qada-" + suffix, ResultLimit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListUsers(qada) = (%d rows, %v), want 1", len(rows), err)
	}
	if rows[0].StorageUsedBytes != 170 || rows[0].StorageQuotaBytes != nil {
		t.Errorf("list row = used %d quota %v, want 170/nil", rows[0].StorageUsedBytes, rows[0].StorageQuotaBytes)
	}

	// Tri-state quota update: set → returned; unrelated edit keeps it; NULL resets.
	quota := int64(4096)
	u, err := q.AdminUpdateUser(ctx, sqlcgen.AdminUpdateUserParams{ID: ada, SetStorageQuota: true, StorageQuotaBytes: &quota})
	if err != nil || u.StorageQuotaBytes == nil || *u.StorageQuotaBytes != 4096 {
		t.Fatalf("set quota = (%v, %v), want 4096", u.StorageQuotaBytes, err)
	}
	role := "moderator"
	u, err = q.AdminUpdateUser(ctx, sqlcgen.AdminUpdateUserParams{ID: ada, Role: &role})
	if err != nil || u.StorageQuotaBytes == nil || *u.StorageQuotaBytes != 4096 {
		t.Fatalf("quota after unrelated edit = (%v, %v), want kept 4096", u.StorageQuotaBytes, err)
	}
	u, err = q.AdminUpdateUser(ctx, sqlcgen.AdminUpdateUserParams{ID: ada, SetStorageQuota: true})
	if err != nil || u.StorageQuotaBytes != nil {
		t.Fatalf("reset quota = (%v, %v), want NULL", u.StorageQuotaBytes, err)
	}
}

// TestReportResolutionNotificationPersists proves the 0042 report context on
// notifications against a real PostgreSQL: ResolveReport returns the reporter,
// a report_resolved notification can reference the report (FK), and
// ListNotifications resolves the joined report status/target type. Seeds the FK
// chain and cleans up via the users→… ON DELETE CASCADE.
func TestReportResolutionNotificationPersists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	seedUser := func(prefix string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			prefix+"-"+suffix, prefix+"-"+suffix+"@example.test",
		).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", prefix, err)
		}
		return id
	}
	reporter := seedUser("nrep")
	reported := seedUser("ntgt")
	moderator := seedUser("nmod")
	defer func() {
		for _, id := range []uuid.UUID{reporter, reported, moderator} {
			_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
		}
	}()

	// File an account report and resolve it: the reporter comes back.
	var reportID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO reports (reporter_id, target_type, reported_user_id, reason)
		 VALUES ($1, 'account', $2, 'impersonation') RETURNING id`,
		reporter, reported,
	).Scan(&reportID); err != nil {
		t.Fatalf("seed report: %v", err)
	}
	gotReporter, err := q.ResolveReport(ctx, sqlcgen.ResolveReportParams{
		ID:            reportID,
		Status:        "accepted",
		ModeratorNote: "handled",
		ResolvedBy:    pgtype.UUID{Bytes: moderator, Valid: true},
	})
	if err != nil {
		t.Fatalf("ResolveReport: %v", err)
	}
	if gotReporter != reporter {
		t.Fatalf("ResolveReport reporter = %s, want %s", gotReporter, reporter)
	}

	// The notification stores the report FK (no actor: the moderator's identity
	// must not be recorded) and the list view joins its status/target back.
	if _, err := q.CreateNotification(ctx, sqlcgen.CreateNotificationParams{
		UserID:   reporter,
		Type:     "report_resolved",
		ReportID: pgtype.UUID{Bytes: reportID, Valid: true},
	}); err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	rows, err := q.ListNotifications(ctx, sqlcgen.ListNotificationsParams{
		UserID: reporter, ResultLimit: 10,
	})
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("notifications = %d, want 1", len(rows))
	}
	n := rows[0]
	if n.Type != "report_resolved" || !n.ReportID.Valid || uuid.UUID(n.ReportID.Bytes) != reportID {
		t.Errorf("row = {type:%s report:%v}, want {report_resolved %s}", n.Type, n.ReportID, reportID)
	}
	if n.ReportStatus == nil || *n.ReportStatus != "accepted" ||
		n.ReportTargetType == nil || *n.ReportTargetType != "account" {
		t.Errorf("report context = (%v, %v), want (accepted, account)", n.ReportStatus, n.ReportTargetType)
	}
	if n.ActorUsername != nil {
		t.Errorf("actor resolved on report_resolved notification = %q, want none", *n.ActorUsername)
	}

	// Deleting the report (the admin hard-delete query) cascades the
	// notification away (0042 FK); re-deleting is an idempotent no-op.
	if deleted, err := q.DeleteReport(ctx, reportID); err != nil || deleted != 1 {
		t.Fatalf("DeleteReport = %d/%v, want 1 row deleted", deleted, err)
	}
	if deleted, err := q.DeleteReport(ctx, reportID); err != nil || deleted != 0 {
		t.Fatalf("re-DeleteReport = %d/%v, want 0 rows (idempotent)", deleted, err)
	}
	rows, err = q.ListNotifications(ctx, sqlcgen.ListNotificationsParams{UserID: reporter, ResultLimit: 10})
	if err != nil {
		t.Fatalf("ListNotifications after delete: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("notifications after report delete = %d, want 0 (ON DELETE CASCADE)", len(rows))
	}
}

// TestNotificationPrefsPersist proves the 0043 notification_prefs queries
// against a real PostgreSQL: no row defaults to enabled, upsert flips and
// re-flips one (user, type), the list returns only stored rows, and deleting
// the user cascades the pref rows away.
func TestNotificationPrefsPersist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	var user uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"nprefs-"+suffix, "nprefs-"+suffix+"@example.test",
	).Scan(&user); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user) }()

	// No stored row → default enabled.
	if on, err := q.IsNotificationTypeEnabled(ctx, sqlcgen.IsNotificationTypeEnabledParams{
		UserID: user, Type: "follow",
	}); err != nil || !on {
		t.Fatalf("IsNotificationTypeEnabled default = %v/%v, want true/nil", on, err)
	}
	if rows, err := q.ListNotificationPrefs(ctx, user); err != nil || len(rows) != 0 {
		t.Fatalf("ListNotificationPrefs empty = %v/%v, want 0 rows", rows, err)
	}

	// Disable → false; upsert again to true → true (conflict path).
	if err := q.UpsertNotificationPref(ctx, sqlcgen.UpsertNotificationPrefParams{
		UserID: user, Type: "follow", Enabled: false,
	}); err != nil {
		t.Fatalf("UpsertNotificationPref insert: %v", err)
	}
	if on, _ := q.IsNotificationTypeEnabled(ctx, sqlcgen.IsNotificationTypeEnabledParams{
		UserID: user, Type: "follow",
	}); on {
		t.Fatal("follow still enabled after disable")
	}
	// Other types are untouched.
	if on, _ := q.IsNotificationTypeEnabled(ctx, sqlcgen.IsNotificationTypeEnabledParams{
		UserID: user, Type: "comment",
	}); !on {
		t.Fatal("comment flipped by an unrelated upsert")
	}
	if err := q.UpsertNotificationPref(ctx, sqlcgen.UpsertNotificationPrefParams{
		UserID: user, Type: "follow", Enabled: true,
	}); err != nil {
		t.Fatalf("UpsertNotificationPref update: %v", err)
	}
	if on, _ := q.IsNotificationTypeEnabled(ctx, sqlcgen.IsNotificationTypeEnabledParams{
		UserID: user, Type: "follow",
	}); !on {
		t.Fatal("follow not re-enabled by the conflict-path upsert")
	}
	rows, err := q.ListNotificationPrefs(ctx, user)
	if err != nil || len(rows) != 1 || rows[0].Type != "follow" || !rows[0].Enabled {
		t.Fatalf("ListNotificationPrefs = %+v/%v, want one enabled follow row", rows, err)
	}

	// Deleting the user cascades the pref rows.
	if _, err := st.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, user); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var count int
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM notification_prefs WHERE user_id = $1`, user,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("pref rows after user delete = %d/%v, want 0", count, err)
	}
}
