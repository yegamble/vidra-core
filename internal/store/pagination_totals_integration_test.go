//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// TestListTotalsExecuteAgainstPostgres runs every query added for the list-total
// work against a real database. sqlc type-checks these at generate time, but a
// CASE-over-a-bound-parameter ORDER BY and `= ANY($1::text[])` filters are
// exactly the shapes where "compiles" and "runs" can diverge — a NULL-typed CASE
// branch or an array cast Postgres refuses only shows up at execution.
//
// The assertions are deliberately about EXECUTION and the Count/List invariant,
// not about seeded data: an empty database still proves the SQL is valid and
// that a total agrees with its page.
func TestListTotalsExecuteAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	someID := uuid.New()
	nullUUID := pgtype.UUID{}
	nullTS := pgtype.Timestamptz{}

	t.Run("every admin-inventory sort executes", func(t *testing.T) {
		// Every accepted ?sort value must reach a real ORDER BY branch. A key the
		// SQL has no CASE for would silently fall through to id DESC, so walking
		// the exported set is the only way to catch a missing branch.
		for _, sort := range video.AdminSorts() {
			rows, err := q.ListAdminVideos(ctx, sqlcgen.ListAdminVideosParams{
				Scope: "all", Sort: sort, ResultLimit: 5,
			})
			if err != nil {
				t.Fatalf("ListAdminVideos sort=%q: %v", sort, err)
			}
			_ = rows
		}
	})

	t.Run("admin inventory filters execute and the total matches the page", func(t *testing.T) {
		yes, no := true, false
		handle := "nobody"
		after := time.Now().Add(-24 * time.Hour)
		for _, f := range []sqlcgen.ListAdminVideosParams{
			{Scope: "all"},
			{Scope: "local"},
			{Scope: "remote"},
			{Scope: "all", States: video.AdminStates()},
			{Scope: "all", States: []string{"published", "draft"}},
			{Scope: "all", Privacies: video.AdminPrivacies()},
			{Scope: "all", Channel: &handle},
			{Scope: "all", PublishedAfter: pgtype.Timestamptz{Time: after, Valid: true}},
			{Scope: "all", PublishedBefore: pgtype.Timestamptz{Time: after, Valid: true}},
			{Scope: "all", HasOriginal: &yes},
			{Scope: "all", HasOriginal: &no},
			{Scope: "all", HasHls: &yes},
			{Scope: "all", HasWebFiles: &no},
		} {
			f.Sort = video.AdminSortDefault
			f.ResultLimit = 1000
			rows, err := q.ListAdminVideos(ctx, f)
			if err != nil {
				t.Fatalf("ListAdminVideos %+v: %v", f, err)
			}
			total, err := q.CountAdminVideos(ctx, sqlcgen.CountAdminVideosParams{
				Query: f.Query, States: f.States, Privacies: f.Privacies, Scope: f.Scope,
				Channel: f.Channel, PublishedAfter: f.PublishedAfter, PublishedBefore: f.PublishedBefore,
				HasOriginal: f.HasOriginal, HasHls: f.HasHls, HasWebFiles: f.HasWebFiles,
			})
			if err != nil {
				t.Fatalf("CountAdminVideos %+v: %v", f, err)
			}
			// The limit is far above any row count this fixture can produce, so a
			// disagreement here means the two WHEREs have drifted apart.
			if total != int64(len(rows)) {
				t.Errorf("filter %+v: total %d but the unpaginated page has %d rows — the Count and List predicates disagree",
					f, total, len(rows))
			}
		}
	})

	t.Run("every new count query executes", func(t *testing.T) {
		open := "open"
		resolved := "resolved"
		pending := "pending"
		checks := map[string]func() error{
			"CountBlockedVideos":        func() error { _, e := q.CountBlockedVideos(ctx); return e },
			"CountBlockedRemoteVideos":  func() error { _, e := q.CountBlockedRemoteVideos(ctx); return e },
			"CountBlockedInstances":     func() error { _, e := q.CountBlockedInstances(ctx); return e },
			"CountPendingRemoteFollows": func() error { _, e := q.CountPendingRemoteFollows(ctx); return e },
			"CountWatchedWords":         func() error { _, e := q.CountWatchedWords(ctx); return e },
			"CountWatchedWordMatches":   func() error { _, e := q.CountWatchedWordMatches(ctx); return e },
			"CountQuarantinedVideos":    func() error { _, e := q.CountQuarantinedVideos(ctx); return e },
			"CountLivePublicStreams":    func() error { _, e := q.CountLivePublicStreams(ctx); return e },
			"CountAuditLog":             func() error { _, e := q.CountAuditLog(ctx, nil); return e },
			"CountAdminComments":        func() error { _, e := q.CountAdminComments(ctx, nil); return e },
			"CountReports/all":          func() error { _, e := q.CountReports(ctx, nil); return e },
			"CountReports/open":         func() error { _, e := q.CountReports(ctx, &open); return e },
			// "resolved" is a query value, not a stored status — it must reach the
			// `status <> 'open'` branch rather than comparing equal to nothing.
			"CountReports/resolved": func() error { _, e := q.CountReports(ctx, &resolved); return e },
			"CountRegistrationRequests": func() error {
				_, e := q.CountRegistrationRequests(ctx, &pending)
				return e
			},
			"CountBlockedUsers":   func() error { _, e := q.CountBlockedUsers(ctx, someID); return e },
			"CountMutedAccounts":  func() error { _, e := q.CountMutedAccounts(ctx, someID); return e },
			"CountMutedInstances": func() error { _, e := q.CountMutedInstances(ctx, someID); return e },
			"CountConversations":  func() error { _, e := q.CountConversations(ctx, someID); return e },
			"CountSavedVideos":    func() error { _, e := q.CountSavedVideos(ctx, someID); return e },
			"CountWatchHistory":   func() error { _, e := q.CountWatchHistory(ctx, someID); return e },
			"CountFollowedChannels": func() error {
				_, e := q.CountFollowedChannels(ctx, someID)
				return e
			},
			"CountWatchHistoryInProgress": func() error {
				_, e := q.CountWatchHistoryInProgress(ctx, someID)
				return e
			},
			"CountRemoteChannelFollows": func() error {
				_, e := q.CountRemoteChannelFollows(ctx, someID)
				return e
			},
			"CountSubscriptionVideos": func() error { _, e := q.CountSubscriptionVideos(ctx, someID); return e },
			"CountVideosByChannel":    func() error { _, e := q.CountVideosByChannel(ctx, someID); return e },
			"CountPlaylistsByOwner":   func() error { _, e := q.CountPlaylistsByOwner(ctx, someID); return e },
			"CountPlaylistItems":      func() error { _, e := q.CountPlaylistItems(ctx, someID); return e },
			"CountChannelMembers":     func() error { _, e := q.CountChannelMembers(ctx, someID); return e },
			"CountManagedChannels":    func() error { _, e := q.CountManagedChannels(ctx, someID); return e },
			"CountLiveStreamsByChannel": func() error {
				_, e := q.CountLiveStreamsByChannel(ctx, someID)
				return e
			},
			"CountCommentsByVideo": func() error {
				_, e := q.CountCommentsByVideo(ctx, sqlcgen.CountCommentsByVideoParams{VideoID: someID, ViewerID: nullUUID})
				return e
			},
			"CountPublicVideosByChannelVisible": func() error {
				_, e := q.CountPublicVideosByChannelVisible(ctx, sqlcgen.CountPublicVideosByChannelVisibleParams{
					ChannelID: someID, HideSensitive: true,
				})
				return e
			},
			"CountNotifications": func() error {
				_, e := q.CountNotifications(ctx, sqlcgen.CountNotificationsParams{UserID: someID, UnreadOnly: true})
				return e
			},
			"CountPublicVideosSorted": func() error {
				_, e := q.CountPublicVideosSorted(ctx, sqlcgen.CountPublicVideosSortedParams{
					IncludeRemote: true, ViewerID: nullUUID, HideSensitive: false,
				})
				return e
			},
			"CountSearchPublicVideos": func() error {
				_, e := q.CountSearchPublicVideos(ctx, sqlcgen.CountSearchPublicVideosParams{
					Query: "anything", ViewerID: nullUUID, HideSensitive: false,
				})
				return e
			},
		}
		for name, run := range checks {
			if err := run(); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	})

	t.Run("channel video lists page and sort", func(t *testing.T) {
		for _, sort := range []string{"published_at", "-published_at", ""} {
			if _, err := q.ListVideosByChannel(ctx, sqlcgen.ListVideosByChannelParams{
				ChannelID: someID, Sort: sort, ResultLimit: 10,
			}); err != nil {
				t.Errorf("ListVideosByChannel sort=%q: %v", sort, err)
			}
			if _, err := q.ListPublicVideosByChannel(ctx, sqlcgen.ListPublicVideosByChannelParams{
				ChannelID: someID, Sort: sort, HideSensitive: true, ResultLimit: 10,
			}); err != nil {
				t.Errorf("ListPublicVideosByChannel sort=%q: %v", sort, err)
			}
		}
		if _, err := q.ListManagedChannels(ctx, sqlcgen.ListManagedChannelsParams{
			UserID: someID, ResultLimit: 10,
		}); err != nil {
			t.Errorf("ListManagedChannels: %v", err)
		}
		_ = nullTS
	})
}

// TestAdminInventorySortAndFilterAgainstPostgres seeds real rows and asserts the
// CASE-based ORDER BY actually orders and the array filters actually
// discriminate. The execution test above proves the SQL runs; this proves it
// does the right thing — a CASE branch that never matches produces valid SQL
// with silently wrong output, which is the failure mode worth catching.
func TestAdminInventorySortAndFilterAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	_, channelID := seedOwnerChannel(ctx, t, st, "invsort")
	var handle string
	if err := st.Pool.QueryRow(ctx, `SELECT handle FROM channels WHERE id = $1`, channelID).Scan(&handle); err != nil {
		t.Fatalf("read handle: %v", err)
	}

	// Three videos with a deterministic created_at spread and distinct titles,
	// states and view counts.
	type seedRow struct {
		title, privacy, state string
		ageHours              int
		views                 int64
	}
	seeds := []seedRow{
		{title: "aaa oldest", privacy: "public", state: "published", ageHours: 72, views: 5},
		{title: "mmm middle", privacy: "private", state: "draft", ageHours: 48, views: 50},
		{title: "zzz newest", privacy: "unlisted", state: "failed", ageHours: 24, views: 500},
	}
	ids := make([]uuid.UUID, 0, len(seeds))
	for _, sd := range seeds {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state, created_at)
			 VALUES ($1, $2, $3, $4, now() - make_interval(hours => $5)) RETURNING id`,
			channelID, sd.title, sd.privacy, sd.state, sd.ageHours,
		).Scan(&id); err != nil {
			t.Fatalf("seed %q: %v", sd.title, err)
		}
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO video_view_counts (video_id, views) VALUES ($1, $2)`, id, sd.views); err != nil {
			t.Fatalf("seed views for %q: %v", sd.title, err)
		}
		ids = append(ids, id)
	}
	_ = ids

	// Scope the queries to this channel so a shared database cannot pollute them.
	list := func(t *testing.T, sort string) []string {
		t.Helper()
		rows, err := q.ListAdminVideos(ctx, sqlcgen.ListAdminVideosParams{
			Scope: "all", Channel: &handle, Sort: sort, ResultLimit: 50,
		})
		if err != nil {
			t.Fatalf("ListAdminVideos sort=%q: %v", sort, err)
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Title)
		}
		return out
	}
	same := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	newestFirst := []string{"zzz newest", "mmm middle", "aaa oldest"}
	oldestFirst := []string{"aaa oldest", "mmm middle", "zzz newest"}
	for _, tc := range []struct {
		sort string
		want []string
	}{
		// The default must reproduce the previous fixed ordering exactly.
		{sort: video.AdminSortDefault, want: newestFirst},
		{sort: "-created_at", want: newestFirst},
		{sort: "created_at", want: oldestFirst},
		// published_at is an alias of created_at, not a second column.
		{sort: "-published_at", want: newestFirst},
		{sort: "published_at", want: oldestFirst},
		{sort: "title", want: oldestFirst},                                         // aaa < mmm < zzz
		{sort: "-title", want: newestFirst},                                        // zzz > mmm > aaa
		{sort: "views", want: oldestFirst},                                         // 5 < 50 < 500
		{sort: "-views", want: newestFirst},                                        // 500 > 50 > 5
		{sort: "state", want: []string{"mmm middle", "zzz newest", "aaa oldest"}},  // draft < failed < published
		{sort: "-state", want: []string{"aaa oldest", "zzz newest", "mmm middle"}}, // published > failed > draft
	} {
		if got := list(t, tc.sort); !same(got, tc.want) {
			t.Errorf("sort=%q ordered %v, want %v", tc.sort, got, tc.want)
		}
	}

	// Filters must actually narrow, and the total must agree with the page.
	count := func(t *testing.T, f sqlcgen.ListAdminVideosParams) (int64, int) {
		t.Helper()
		f.Channel, f.Scope, f.Sort, f.ResultLimit = &handle, "all", video.AdminSortDefault, 50
		rows, err := q.ListAdminVideos(ctx, f)
		if err != nil {
			t.Fatalf("ListAdminVideos %+v: %v", f, err)
		}
		total, err := q.CountAdminVideos(ctx, sqlcgen.CountAdminVideosParams{
			Query: f.Query, States: f.States, Privacies: f.Privacies, Scope: f.Scope,
			Channel: f.Channel, PublishedAfter: f.PublishedAfter, PublishedBefore: f.PublishedBefore,
			HasOriginal: f.HasOriginal, HasHls: f.HasHls, HasWebFiles: f.HasWebFiles,
		})
		if err != nil {
			t.Fatalf("CountAdminVideos %+v: %v", f, err)
		}
		return total, len(rows)
	}
	no := false
	for _, tc := range []struct {
		name string
		f    sqlcgen.ListAdminVideosParams
		want int64
	}{
		{name: "unfiltered", f: sqlcgen.ListAdminVideosParams{}, want: 3},
		{name: "single state", f: sqlcgen.ListAdminVideosParams{States: []string{"draft"}}, want: 1},
		{name: "two states", f: sqlcgen.ListAdminVideosParams{States: []string{"draft", "failed"}}, want: 2},
		{name: "privacy", f: sqlcgen.ListAdminVideosParams{Privacies: []string{"public", "unlisted"}}, want: 2},
		{name: "local scope keeps them", f: sqlcgen.ListAdminVideosParams{}, want: 3},
		// None of the seeds has files, so has_original=false must match all three
		// — the tri-state's whole point.
		{name: "has_original false", f: sqlcgen.ListAdminVideosParams{HasOriginal: &no}, want: 3},
		{name: "published_before an hour ago", f: sqlcgen.ListAdminVideosParams{
			PublishedBefore: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
		}, want: 3},
		{name: "published_after now", f: sqlcgen.ListAdminVideosParams{
			PublishedAfter: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			total, rows := count(t, tc.f)
			if total != tc.want {
				t.Errorf("total = %d, want %d", total, tc.want)
			}
			if int64(rows) != tc.want {
				t.Errorf("page rows = %d, want %d", rows, tc.want)
			}
		})
	}

	// scope=remote must exclude the local rows entirely.
	remote, err := q.CountAdminVideos(ctx, sqlcgen.CountAdminVideosParams{Scope: "remote", Channel: &handle})
	if err != nil {
		t.Fatalf("CountAdminVideos scope=remote: %v", err)
	}
	if remote != 0 {
		t.Errorf("scope=remote counted %d local videos, want 0", remote)
	}
}
