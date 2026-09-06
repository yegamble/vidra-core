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
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// TestNewVideoFanOutOnRealPG is the proof behind the "new video from a channel
// you follow" fan-out (migration 0101). Every rule that decides WHO gets told
// lives in the NotifyFollowersOfNewVideo statement rather than in Go, so the
// service-level unit test can only prove pass-through — this is the test that
// actually holds the rules. It seeds one channel with a follower per rule and
// asserts the notified set exactly, because the failure that matters here is
// silent over-delivery: a muted, blocked, or opted-out user being told anyway
// is not a cosmetic bug, and a subset assertion would never catch it.
func TestNewVideoFanOutOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	// mkUser seeds a user and registers its cleanup; every other seeded row
	// hangs off a user or channel by ON DELETE CASCADE, so deleting the users at
	// the end takes the whole fixture with it.
	mkUser := func(name string) uuid.UUID {
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

	owner := mkUser("fanout-owner")
	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Fan-out') RETURNING id`,
		owner, "fanout-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	follow := func(user uuid.UUID, setting string) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO channel_follows (follower_id, channel_id, notification_setting) VALUES ($1, $2, $3)`,
			user, channelID, setting,
		); err != nil {
			t.Fatalf("seed follow: %v", err)
		}
	}

	// One follower per rule the statement enforces.
	plain := mkUser("fanout-plain")          // bell on, nothing in the way  → notified
	explicitOn := mkUser("fanout-prefon")    // bell on + pref explicitly ON → notified
	muted := mkUser("fanout-bellnone")       // bell muted                   → not notified
	prefOff := mkUser("fanout-prefoff")      // new_video turned off globally → not notified
	muter := mkUser("fanout-muter")          // muted the channel owner      → not notified
	blocker := mkUser("fanout-blocker")      // blocked the owner            → not notified
	blocked := mkUser("fanout-blocked")      // blocked BY the owner         → not notified
	nonFollower := mkUser("fanout-stranger") // does not follow at all       → not notified

	follow(plain, "all")
	follow(explicitOn, "all")
	follow(muted, "none")
	follow(prefOff, "all")
	follow(muter, "all")
	follow(blocker, "all")
	follow(blocked, "all")
	// The owner following their own channel must not self-notify.
	follow(owner, "all")

	for _, p := range []struct {
		user    uuid.UUID
		enabled bool
	}{{explicitOn, true}, {prefOff, false}} {
		if err := q.UpsertNotificationPref(ctx, sqlcgen.UpsertNotificationPrefParams{
			UserID: p.user, Type: "new_video", Enabled: p.enabled,
		}); err != nil {
			t.Fatalf("seed pref: %v", err)
		}
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`, muter, owner,
	); err != nil {
		t.Fatalf("seed mute: %v", err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2), ($3, $4)`,
		blocker, owner, owner, blocked,
	); err != nil {
		t.Fatalf("seed blocks: %v", err)
	}

	mkVideo := func(title, privacy, state string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, $2, $3, $4) RETURNING id`,
			channelID, title, privacy, state,
		).Scan(&id); err != nil {
			t.Fatalf("seed video %s: %v", title, err)
		}
		return id
	}
	// recipients reads back exactly who holds a new_video notification for a
	// video, so the assertions can compare whole sets.
	recipients := func(videoID uuid.UUID) map[uuid.UUID]bool {
		t.Helper()
		rows, err := st.Pool.Query(ctx,
			`SELECT user_id FROM notifications WHERE video_id = $1 AND type = 'new_video'`, videoID)
		if err != nil {
			t.Fatalf("read notifications: %v", err)
		}
		defer rows.Close()
		got := map[uuid.UUID]bool{}
		for rows.Next() {
			var u uuid.UUID
			if err := rows.Scan(&u); err != nil {
				t.Fatalf("scan recipient: %v", err)
			}
			got[u] = true
		}
		return got
	}

	published := mkVideo("Published public", "public", "published")
	notified, err := q.NotifyFollowersOfNewVideo(ctx, published)
	if err != nil {
		t.Fatalf("NotifyFollowersOfNewVideo: %v", err)
	}
	if notified != 2 {
		t.Fatalf("notified = %d, want 2 (the unobstructed followers)", notified)
	}
	got := recipients(published)
	want := map[uuid.UUID]bool{plain: true, explicitOn: true}
	if len(got) != len(want) {
		t.Fatalf("recipients = %d rows, want %d", len(got), len(want))
	}
	for _, excluded := range []struct {
		id  uuid.UUID
		why string
	}{
		{muted, "bell set to none"},
		{prefOff, "new_video preference off"},
		{muter, "muted the channel owner"},
		{blocker, "blocked the channel owner"},
		{blocked, "blocked by the channel owner"},
		{nonFollower, "does not follow the channel"},
		{owner, "is the channel owner (self-notify)"},
	} {
		if got[excluded.id] {
			t.Errorf("notified a follower who %s", excluded.why)
		}
	}
	for id, why := range map[uuid.UUID]string{
		plain:      "follows with the bell on",
		explicitOn: "follows with the bell on and new_video explicitly enabled",
	} {
		if !got[id] {
			t.Errorf("did NOT notify a follower who %s", why)
		}
	}

	// A hook that fires twice for the same video (publish-after-transcode
	// release, moderator approval, a retry) must not double-notify — the partial
	// unique index in 0101 makes that a database guarantee.
	again, err := q.NotifyFollowersOfNewVideo(ctx, published)
	if err != nil {
		t.Fatalf("second NotifyFollowersOfNewVideo: %v", err)
	}
	if again != 0 {
		t.Fatalf("re-running the fan-out inserted %d rows, want 0", again)
	}
	if n := len(recipients(published)); n != 2 {
		t.Fatalf("recipients after re-run = %d, want 2", n)
	}

	// Videos that must never reach a follower list at all.
	for _, tc := range []struct {
		name, privacy, state string
		block                bool
	}{
		{name: "unlisted", privacy: "unlisted", state: "published"},
		{name: "private", privacy: "private", state: "published"},
		{name: "password-protected", privacy: "password", state: "published"},
		{name: "still a draft", privacy: "public", state: "draft"},
		{name: "still transcoding", privacy: "public", state: "transcoding"},
		{name: "scheduled", privacy: "public", state: "scheduled"},
		{name: "quarantined", privacy: "public", state: "quarantined"},
		{name: "moderation-blocked", privacy: "public", state: "published", block: true},
	} {
		v := mkVideo(tc.name, tc.privacy, tc.state)
		if tc.block {
			if _, err := st.Pool.Exec(ctx,
				`INSERT INTO video_blocks (video_id, reason) VALUES ($1, 'test')`, v,
			); err != nil {
				t.Fatalf("seed video block: %v", err)
			}
		}
		n, err := q.NotifyFollowersOfNewVideo(ctx, v)
		if err != nil {
			t.Fatalf("fan-out for a %s video: %v", tc.name, err)
		}
		if n != 0 {
			t.Errorf("a %s video notified %d followers, want 0", tc.name, n)
		}
	}

	// The dedup index is scoped to type = 'new_video', so the types that
	// legitimately repeat per video are untouched by it. Two comment
	// notifications for the same (user, video) must still both land.
	for i := 0; i < 2; i++ {
		if _, err := q.CreateNotification(ctx, sqlcgen.CreateNotificationParams{
			UserID: plain, Type: "comment", VideoID: pgtype.UUID{Bytes: published, Valid: true},
		}); err != nil {
			t.Fatalf("comment notification %d blocked by the new_video dedup index: %v", i+1, err)
		}
	}
}

// TestNewReportStaffFanOutOnRealPG is the proof behind the "new abuse report"
// staff fan-out (migration 0103). Every rule that decides WHO gets told lives
// in the NotifyStaffOfNewReport statement rather than in Go, so the
// service-level unit test can only prove pass-through — this is the test that
// actually holds the rules. It seeds one staff member per rule and asserts the
// notified set exactly: silent over-delivery (an opted-out, deactivated, or
// deleted account being told, or a staff reporter told about their own filing)
// is the failure that matters, and a subset assertion would never catch it.
func TestNewReportStaffFanOutOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	// mkUser seeds a user with a role and registers its cleanup; the report and
	// notification rows hang off users by ON DELETE CASCADE.
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
		return id
	}

	// One staff member per rule the statement enforces.
	admin := mkUser("nrep-admin", "admin")              // active admin            → notified
	moderator := mkUser("nrep-mod", "moderator")        // active moderator        → notified
	plainUser := mkUser("nrep-user", "user")            // not staff               → not notified
	inactiveAdmin := mkUser("nrep-inactive", "admin")   // deactivated             → not notified
	deletedMod := mkUser("nrep-deleted", "moderator")   // account deleted         → not notified
	prefOffAdmin := mkUser("nrep-prefoff", "admin")     // new_report turned off   → not notified
	reporterMod := mkUser("nrep-reporter", "moderator") // filed the report        → not notified (self)

	if _, err := st.Pool.Exec(ctx, `UPDATE users SET is_active = FALSE WHERE id = $1`, inactiveAdmin); err != nil {
		t.Fatalf("deactivate admin: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, deletedMod); err != nil {
		t.Fatalf("soft-delete moderator: %v", err)
	}
	if err := q.UpsertNotificationPref(ctx, sqlcgen.UpsertNotificationPrefParams{
		UserID: prefOffAdmin, Type: "new_report", Enabled: false,
	}); err != nil {
		t.Fatalf("seed pref: %v", err)
	}

	// reporterMod (staff) reports plainUser's account.
	reportID, err := q.CreateAccountReport(ctx, sqlcgen.CreateAccountReportParams{
		ReporterID:     reporterMod,
		ReportedUserID: pgtype.UUID{Bytes: plainUser, Valid: true},
		Reason:         "seeded by TestNewReportStaffFanOutOnRealPG",
	})
	if err != nil {
		t.Fatalf("CreateAccountReport: %v", err)
	}

	notified, err := q.NotifyStaffOfNewReport(ctx, reportID)
	if err != nil {
		t.Fatalf("NotifyStaffOfNewReport: %v", err)
	}
	// The DB is shared with sibling integration tests, which may leave their
	// own staff users behind — so assert a floor here, prove exact membership
	// for the seeded fixture below, and prove the no-over-delivery rules with
	// the eligibility join (which is exact regardless of leftover rows).
	if notified < 2 {
		t.Fatalf("notified = %d, want at least the 2 unobstructed seeded staff members", notified)
	}

	recipients := func() map[uuid.UUID]bool {
		t.Helper()
		rows, err := st.Pool.Query(ctx,
			`SELECT user_id FROM notifications WHERE report_id = $1 AND type = 'new_report'`, reportID)
		if err != nil {
			t.Fatalf("read notifications: %v", err)
		}
		defer rows.Close()
		got := map[uuid.UUID]bool{}
		for rows.Next() {
			var u uuid.UUID
			if err := rows.Scan(&u); err != nil {
				t.Fatalf("scan recipient: %v", err)
			}
			got[u] = true
		}
		return got
	}

	got := recipients()
	for id, why := range map[uuid.UUID]string{
		admin:     "is an active admin",
		moderator: "is an active moderator",
	} {
		if !got[id] {
			t.Errorf("did NOT notify a staff member who %s", why)
		}
	}
	for _, excluded := range []struct {
		id  uuid.UUID
		why string
	}{
		{plainUser, "is not staff"},
		{inactiveAdmin, "is deactivated"},
		{deletedMod, "has a deleted account"},
		{prefOffAdmin, "turned the new_report type off"},
		{reporterMod, "filed the report themselves"},
	} {
		if got[excluded.id] {
			t.Errorf("notified a user who %s", excluded.why)
		}
	}
	// No over-delivery, exactly: every recipient row must join back to an
	// eligible user (active, non-deleted staff, not the reporter, no opt-out).
	// This holds regardless of what sibling tests left in the shared DB.
	var violators int
	if err := st.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM notifications n
		JOIN users u ON u.id = n.user_id
		WHERE n.report_id = $1 AND n.type = 'new_report'
		  AND (u.role NOT IN ('admin', 'moderator')
		       OR NOT u.is_active
		       OR u.deleted_at IS NOT NULL
		       OR u.id = $2
		       OR EXISTS (
		           SELECT 1 FROM notification_prefs np
		           WHERE np.user_id = u.id AND np.type = 'new_report' AND np.enabled = FALSE
		       ))`, reportID, reporterMod).Scan(&violators); err != nil {
		t.Fatalf("count ineligible recipients: %v", err)
	}
	if violators != 0 {
		t.Errorf("fan-out reached %d ineligible recipients, want 0", violators)
	}

	// The notification carries the reporter as actor and joins back to the
	// report's status/target for display.
	var actorID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`SELECT actor_id FROM notifications WHERE report_id = $1 AND user_id = $2`, reportID, admin,
	).Scan(&actorID); err != nil {
		t.Fatalf("read actor: %v", err)
	}
	if actorID != reporterMod {
		t.Errorf("actor = %s, want the reporter %s", actorID, reporterMod)
	}

	// A fan-out that fires twice for the same report must not double-notify —
	// the partial unique index in 0103 makes that a database guarantee.
	firstRoundRecipients := len(got)
	again, err := q.NotifyStaffOfNewReport(ctx, reportID)
	if err != nil {
		t.Fatalf("second NotifyStaffOfNewReport: %v", err)
	}
	if again != 0 {
		t.Fatalf("re-running the fan-out inserted %d rows, want 0", again)
	}
	if n := len(recipients()); n != firstRoundRecipients {
		t.Fatalf("recipients after re-run = %d, want %d (unchanged)", n, firstRoundRecipients)
	}

	// An idempotent repeat report yields no row (pgx.ErrNoRows), so the caller
	// never re-fires the fan-out for it.
	if _, err := q.CreateAccountReport(ctx, sqlcgen.CreateAccountReportParams{
		ReporterID:     reporterMod,
		ReportedUserID: pgtype.UUID{Bytes: plainUser, Valid: true},
		Reason:         "duplicate",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("duplicate CreateAccountReport err = %v, want pgx.ErrNoRows", err)
	}

	// The dedup index is scoped to type = 'new_report': the reporter's own
	// report_resolved for the same (user, report) must still land later.
	if _, err := q.CreateNotification(ctx, sqlcgen.CreateNotificationParams{
		UserID: admin, Type: "report_resolved", ReportID: pgtype.UUID{Bytes: reportID, Valid: true},
	}); err != nil {
		t.Fatalf("report_resolved notification blocked by the new_report dedup index: %v", err)
	}
}

// TestCommentReplyRecipientOnRealPG holds the rules behind the comment_reply
// notification. Every decision about WHO hears that they were replied to lives
// in the CommentReplyRecipient statement rather than in Go, so the service unit
// test can only prove the service reacts faithfully to each answer — this is the
// test that holds the rules themselves.
//
// The failure that matters is over-delivery: a muted or blocked account
// reaching the notification surface it was excluded from, or a tombstoned /
// deactivated account being dragged back into one. Each case therefore asserts
// the EXACT answer (a specific recipient, or pgx.ErrNoRows), never a subset.
func TestCommentReplyRecipientOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	mkUser := func(name string) uuid.UUID {
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

	owner := mkUser("reply-owner")
	author := mkUser("reply-author")   // writes the parent comment
	replier := mkUser("reply-replier") // answers it
	var channelID, videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Replies') RETURNING id`,
		owner, "reply-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'Reply thread', 'public', 'published') RETURNING id`,
		channelID,
	).Scan(&videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	comment := func(user uuid.UUID, body string, parent *uuid.UUID) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO comments (video_id, user_id, body, parent_id) VALUES ($1, $2, $3, $4) RETURNING id`,
			videoID, user, body, parent,
		).Scan(&id); err != nil {
			t.Fatalf("seed comment %q: %v", body, err)
		}
		return id
	}
	// recipientOf is the whole assertion surface: the resolved recipient, or
	// uuid.Nil when the statement deliberately answers "nobody".
	recipientOf := func(commentID uuid.UUID) uuid.UUID {
		t.Helper()
		row, err := q.CommentReplyRecipient(ctx, commentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil
		}
		if err != nil {
			t.Fatalf("CommentReplyRecipient: %v", err)
		}
		if row.VideoID != videoID {
			t.Errorf("video_id = %s, want the thread's video %s", row.VideoID, videoID)
		}
		if !row.RecipientID.Valid {
			return uuid.Nil
		}
		return uuid.UUID(row.RecipientID.Bytes)
	}

	parent := comment(author, "parent", nil)
	reply := comment(replier, "reply", &parent)

	// The base case: the parent's author is the recipient.
	if got := recipientOf(reply); got != author {
		t.Fatalf("recipient = %s, want the parent's author %s", got, author)
	}
	// A top-level comment answers nobody — the normal "no row" case, not an error.
	if got := recipientOf(parent); got != uuid.Nil {
		t.Errorf("a top-level comment resolved recipient %s, want nobody", got)
	}
	// Replying to yourself notifies nobody.
	if got := recipientOf(comment(author, "self reply", &parent)); got != uuid.Nil {
		t.Errorf("a self-reply resolved recipient %s, want nobody", got)
	}

	// Each exclusion, applied and then lifted, so the test proves the clause is
	// doing the work rather than the fixture being broken.
	for _, tc := range []struct {
		why     string
		apply   string
		args    []any
		unapply string
		unargs  []any
	}{
		{
			why:     "the parent's author muted the replier",
			apply:   `INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`,
			args:    []any{author, replier},
			unapply: `DELETE FROM muted_accounts WHERE muter_id = $1 AND muted_id = $2`,
			unargs:  []any{author, replier},
		},
		{
			why:     "the parent's author blocked the replier",
			apply:   `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`,
			args:    []any{author, replier},
			unapply: `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`,
			unargs:  []any{author, replier},
		},
		{
			why:     "the replier blocked the parent's author",
			apply:   `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`,
			args:    []any{replier, author},
			unapply: `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`,
			unargs:  []any{replier, author},
		},
		{
			why:     "the parent is a tombstone (its author's account was deleted)",
			apply:   `UPDATE comments SET deleted_at = now(), body = '' WHERE id = $1`,
			args:    []any{parent},
			unapply: `UPDATE comments SET deleted_at = NULL, body = 'parent' WHERE id = $1`,
			unargs:  []any{parent},
		},
		{
			why:     "the parent's author is deactivated",
			apply:   `UPDATE users SET is_active = FALSE WHERE id = $1`,
			args:    []any{author},
			unapply: `UPDATE users SET is_active = TRUE WHERE id = $1`,
			unargs:  []any{author},
		},
		{
			why:     "the parent's author is a deleted account",
			apply:   `UPDATE users SET deleted_at = now() WHERE id = $1`,
			args:    []any{author},
			unapply: `UPDATE users SET deleted_at = NULL WHERE id = $1`,
			unargs:  []any{author},
		},
	} {
		if _, err := st.Pool.Exec(ctx, tc.apply, tc.args...); err != nil {
			t.Fatalf("apply %q: %v", tc.why, err)
		}
		if got := recipientOf(reply); got != uuid.Nil {
			t.Errorf("resolved recipient %s even though %s", got, tc.why)
		}
		if _, err := st.Pool.Exec(ctx, tc.unapply, tc.unargs...); err != nil {
			t.Fatalf("unapply %q: %v", tc.why, err)
		}
		if got := recipientOf(reply); got != author {
			t.Fatalf("lifting %q left the recipient at %s, want %s — the fixture, not the clause, was doing the work", tc.why, got, author)
		}
	}

	// A federated parent has no local user_id (0053) and therefore no inbox to
	// deliver into: replying to one notifies nobody rather than erroring.
	actorURL := "https://remote.example/users/ghost-" + suffix
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO remote_actors (actor_url, actor_type, preferred_username, domain, public_key_pem)
		 VALUES ($1, 'Person', 'ghost', 'remote.example', 'x')`, actorURL,
	); err != nil {
		t.Fatalf("seed remote actor: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url = $1`, actorURL)
	})
	var remoteParent uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO comments (video_id, body, remote_actor_url, remote_author_name, remote_object_url)
		 VALUES ($1, 'from afar', $2, 'ghost', $3) RETURNING id`,
		videoID, actorURL, actorURL+"/note",
	).Scan(&remoteParent); err != nil {
		t.Fatalf("seed remote parent comment: %v", err)
	}
	if got := recipientOf(comment(replier, "answering a remote comment", &remoteParent)); got != uuid.Nil {
		t.Errorf("a reply to a FEDERATED comment resolved local recipient %s, want nobody", got)
	}
}

// TestCommentVideoOwnerRecipientOnRealPG holds the rules behind the video-owner
// 'comment' notification. Until A12/SOC-02 this path consulted no mute and no
// block at all: an account the owner had muted — or either side of a block —
// reached the owner's inbox simply by commenting on their video, with the
// comment id attached, which is the muted content arriving through another
// door. Every decision about WHO hears it now lives in the
// CommentVideoOwnerRecipient statement, so this is the test that holds the
// rules; the service unit test can only prove the service reacts faithfully.
//
// The failure that matters is over-delivery, so each case asserts the EXACT
// answer (a specific recipient, or pgx.ErrNoRows) and then LIFTS the condition
// and re-asserts — a clause deleted from the statement makes this test fail,
// where a fixture-driven test would keep passing.
func TestCommentVideoOwnerRecipientOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	mkUser := func(name string) uuid.UUID {
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

	owner := mkUser("owner-notif")         // owns the channel and so the video
	commenter := mkUser("commenter-notif") // comments on it
	var channelID, videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Owner notifs') RETURNING id`,
		owner, "ownernotif-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'Owner notifs', 'public', 'published') RETURNING id`,
		channelID,
	).Scan(&videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	mkComment := func(user uuid.UUID, body string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO comments (video_id, user_id, body) VALUES ($1, $2, $3) RETURNING id`,
			videoID, user, body,
		).Scan(&id); err != nil {
			t.Fatalf("seed comment %q: %v", body, err)
		}
		return id
	}
	recipientOf := func(commentID uuid.UUID) uuid.UUID {
		t.Helper()
		row, err := q.CommentVideoOwnerRecipient(ctx, commentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil
		}
		if err != nil {
			t.Fatalf("CommentVideoOwnerRecipient: %v", err)
		}
		if row.VideoID != videoID {
			t.Errorf("video_id = %s, want the commented video %s", row.VideoID, videoID)
		}
		return row.RecipientID
	}

	c := mkComment(commenter, "nice clip")

	// The base case: the video's owner is the recipient.
	if got := recipientOf(c); got != owner {
		t.Fatalf("recipient = %s, want the video's owner %s", got, owner)
	}
	// Commenting on your own video tells nobody.
	if got := recipientOf(mkComment(owner, "thanks all")); got != uuid.Nil {
		t.Errorf("the owner's own comment resolved recipient %s, want nobody", got)
	}
	// An unknown comment id is the "nobody" answer, not an error.
	if got := recipientOf(uuid.New()); got != uuid.Nil {
		t.Errorf("an unknown comment resolved recipient %s, want nobody", got)
	}

	for _, tc := range []struct {
		why     string
		apply   string
		args    []any
		unapply string
		unargs  []any
	}{
		{
			why:     "the owner muted the commenter",
			apply:   `INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`,
			args:    []any{owner, commenter},
			unapply: `DELETE FROM muted_accounts WHERE muter_id = $1 AND muted_id = $2`,
			unargs:  []any{owner, commenter},
		},
		{
			why:     "the owner blocked the commenter",
			apply:   `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`,
			args:    []any{owner, commenter},
			unapply: `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`,
			unargs:  []any{owner, commenter},
		},
		{
			why:     "the commenter blocked the owner",
			apply:   `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`,
			args:    []any{commenter, owner},
			unapply: `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`,
			unargs:  []any{commenter, owner},
		},
		{
			why:     "the comment is a tombstone (its author's account was deleted)",
			apply:   `UPDATE comments SET deleted_at = now(), body = '' WHERE id = $1`,
			args:    []any{c},
			unapply: `UPDATE comments SET deleted_at = NULL, body = 'nice clip' WHERE id = $1`,
			unargs:  []any{c},
		},
		{
			why:     "the owner is deactivated",
			apply:   `UPDATE users SET is_active = FALSE WHERE id = $1`,
			args:    []any{owner},
			unapply: `UPDATE users SET is_active = TRUE WHERE id = $1`,
			unargs:  []any{owner},
		},
		{
			why:     "the owner is a deleted account",
			apply:   `UPDATE users SET deleted_at = now() WHERE id = $1`,
			args:    []any{owner},
			unapply: `UPDATE users SET deleted_at = NULL WHERE id = $1`,
			unargs:  []any{owner},
		},
	} {
		if _, err := st.Pool.Exec(ctx, tc.apply, tc.args...); err != nil {
			t.Fatalf("apply %q: %v", tc.why, err)
		}
		if got := recipientOf(c); got != uuid.Nil {
			t.Errorf("resolved recipient %s even though %s", got, tc.why)
		}
		if _, err := st.Pool.Exec(ctx, tc.unapply, tc.unargs...); err != nil {
			t.Fatalf("unapply %q: %v", tc.why, err)
		}
		if got := recipientOf(c); got != owner {
			t.Fatalf("lifting %q left the recipient at %s, want %s — the fixture, not the clause, was doing the work", tc.why, got, owner)
		}
	}

	// A FEDERATED comment has no local user_id (0053). The inbound federation
	// path does not raise this notification at all, and the statement must not
	// resolve one either — an owner cannot mute or block a remote actor by
	// account id, so a row here would be unmutable by construction.
	actorURL := "https://remote.example/users/commenter-" + suffix
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO remote_actors (actor_url, actor_type, preferred_username, domain, public_key_pem)
		 VALUES ($1, 'Person', 'ghost', 'remote.example', 'x')`, actorURL,
	); err != nil {
		t.Fatalf("seed remote actor: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url = $1`, actorURL)
	})
	var remoteComment uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO comments (video_id, body, remote_actor_url, remote_author_name, remote_object_url)
		 VALUES ($1, 'from afar', $2, 'ghost', $3) RETURNING id`,
		videoID, actorURL, actorURL+"/note",
	).Scan(&remoteComment); err != nil {
		t.Fatalf("seed remote comment: %v", err)
	}
	if got := recipientOf(remoteComment); got != uuid.Nil {
		t.Errorf("a FEDERATED comment resolved local recipient %s, want nobody", got)
	}
}

// TestFollowNotificationRecipientOnRealPG holds the rules behind the 'follow'
// notification. Until this slice that path consulted no mute and no block at
// all: an account the channel owner had muted — or either side of a block —
// reached the owner's inbox simply by following, and repeatably, because the
// handler raises the notification whenever the follow row is genuinely new, so
// unfollow-then-follow produces another. Every other notification path already
// excluded them. Every decision about WHO hears it now lives in the
// FollowNotificationRecipient statement, so this is the test that holds the
// rules; the service unit test can only prove the service reacts faithfully.
//
// The failure that matters is over-delivery, so each case asserts the EXACT
// answer (a specific recipient, or pgx.ErrNoRows) and then LIFTS the condition
// and re-asserts — a clause deleted from the statement makes this test fail,
// where a fixture-driven test would keep passing.
func TestFollowNotificationRecipientOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	mkUser := func(name string) uuid.UUID {
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

	owner := mkUser("owner-follow")
	follower := mkUser("follower-follow")
	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Follow notifs') RETURNING id`,
		owner, "ownerfollow-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	recipientOf := func(actor uuid.UUID) uuid.UUID {
		t.Helper()
		got, err := q.FollowNotificationRecipient(ctx, sqlcgen.FollowNotificationRecipientParams{
			ChannelID: channelID, FollowerID: actor,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil
		}
		if err != nil {
			t.Fatalf("FollowNotificationRecipient: %v", err)
		}
		return got
	}

	// The base case: the channel's owner is the recipient.
	if got := recipientOf(follower); got != owner {
		t.Fatalf("recipient = %s, want the channel's owner %s", got, owner)
	}
	// Following your own channel tells nobody.
	if got := recipientOf(owner); got != uuid.Nil {
		t.Errorf("a self-follow resolved recipient %s, want nobody", got)
	}
	// An unknown channel id is the "nobody" answer, not an error.
	if _, err := q.FollowNotificationRecipient(ctx, sqlcgen.FollowNotificationRecipientParams{
		ChannelID: uuid.New(), FollowerID: follower,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown channel err = %v, want pgx.ErrNoRows", err)
	}

	for _, tc := range []struct {
		why     string
		apply   string
		args    []any
		unapply string
		unargs  []any
	}{
		{
			why:     "the owner muted the follower",
			apply:   `INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`,
			args:    []any{owner, follower},
			unapply: `DELETE FROM muted_accounts WHERE muter_id = $1 AND muted_id = $2`,
			unargs:  []any{owner, follower},
		},
		{
			why:     "the owner blocked the follower",
			apply:   `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`,
			args:    []any{owner, follower},
			unapply: `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`,
			unargs:  []any{owner, follower},
		},
		{
			why:     "the follower blocked the owner",
			apply:   `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`,
			args:    []any{follower, owner},
			unapply: `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`,
			unargs:  []any{follower, owner},
		},
		{
			why:     "the owner is deactivated",
			apply:   `UPDATE users SET is_active = FALSE WHERE id = $1`,
			args:    []any{owner},
			unapply: `UPDATE users SET is_active = TRUE WHERE id = $1`,
			unargs:  []any{owner},
		},
		{
			why:     "the owner is a deleted account",
			apply:   `UPDATE users SET deleted_at = now() WHERE id = $1`,
			args:    []any{owner},
			unapply: `UPDATE users SET deleted_at = NULL WHERE id = $1`,
			unargs:  []any{owner},
		},
	} {
		if _, err := st.Pool.Exec(ctx, tc.apply, tc.args...); err != nil {
			t.Fatalf("apply %q: %v", tc.why, err)
		}
		if got := recipientOf(follower); got != uuid.Nil {
			t.Errorf("resolved recipient %s even though %s", got, tc.why)
		}
		if _, err := st.Pool.Exec(ctx, tc.unapply, tc.unargs...); err != nil {
			t.Fatalf("unapply %q: %v", tc.why, err)
		}
		if got := recipientOf(follower); got != owner {
			t.Fatalf("lifting %q left the recipient at %s, want %s — the fixture, not the clause, was doing the work", tc.why, got, owner)
		}
	}
}
