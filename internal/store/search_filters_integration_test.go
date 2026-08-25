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

// The search sorts/filters and the two entity-search queries, executed against a
// real PostgreSQL. sqlc type-checks these at generate time, but three of the
// shapes here are exactly where "compiles" and "runs" diverge:
//
//   - an ORDER BY built from CASE over a bound parameter — a branch that never
//     matches is valid SQL that silently returns the fallback order;
//   - `= ANY($1::text[])` and cardinality() over a nullable array parameter;
//   - greatest(similarity(...), similarity(...)), which needs pg_trgm loaded and
//     the argument types to line up.
//
// The account-visibility assertions are the important ones: those run against
// the real WHERE clause, not the in-memory fake the HTTP tests use.

// TestSearchQueriesExecuteAgainstPostgres proves every new sort value and filter
// combination runs, and that each Count agrees with its unpaginated List. An
// empty result still proves the SQL is valid and the two WHERE clauses match.
func TestSearchQueriesExecuteAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	nullUUID := pgtype.UUID{}
	lo, hi := int32(60), int32(600)
	when := pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true}

	t.Run("every search sort executes", func(t *testing.T) {
		// Walking the exported set is the only way to catch a sort key the SQL
		// has no CASE branch for: it would fall through to created_at DESC and
		// look like a working sort.
		for _, sort := range video.SearchSorts() {
			if _, err := q.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{
				Query: "zzz", ViewerID: nullUUID, Sort: sort, ResultLimit: 5,
			}); err != nil {
				t.Errorf("SearchPublicVideos sort=%q: %v", sort, err)
			}
		}
	})

	t.Run("every search filter executes and the total matches the page", func(t *testing.T) {
		for _, f := range []sqlcgen.SearchPublicVideosParams{
			{},
			{DurationMin: &lo},
			{DurationMax: &hi},
			{DurationMin: &lo, DurationMax: &hi},
			{PublishedAfter: when},
			{PublishedBefore: when},
			{PublishedAfter: when, PublishedBefore: when},
			{TagsAllOf: []string{"go"}},
			{TagsAllOf: []string{"go", "redis"}},
			{TagsOneOf: []string{"go", "redis"}},
			{TagsAllOf: []string{"go"}, TagsOneOf: []string{"redis"}},
			{TagsAllOf: []string{"go"}, DurationMin: &lo, PublishedAfter: when},
		} {
			f.Query, f.ViewerID, f.Sort, f.ResultLimit = "zzz", nullUUID, video.SearchSortDefault, 1000
			rows, err := q.SearchPublicVideos(ctx, f)
			if err != nil {
				t.Fatalf("SearchPublicVideos %+v: %v", f, err)
			}
			total, err := q.CountSearchPublicVideos(ctx, sqlcgen.CountSearchPublicVideosParams{
				Query: f.Query, ViewerID: f.ViewerID, Tag: f.Tag, Category: f.Category,
				Language: f.Language, TagsAllOf: f.TagsAllOf, TagsOneOf: f.TagsOneOf,
				HideSensitive: f.HideSensitive, DurationMin: f.DurationMin, DurationMax: f.DurationMax,
				PublishedAfter: f.PublishedAfter, PublishedBefore: f.PublishedBefore,
			})
			if err != nil {
				t.Fatalf("CountSearchPublicVideos %+v: %v", f, err)
			}
			if total != int64(len(rows)) {
				t.Errorf("filter %+v: total %d != unpaginated page %d", f, total, len(rows))
			}
		}
	})

	t.Run("entity searches execute and their totals match", func(t *testing.T) {
		for _, viewer := range []pgtype.UUID{nullUUID, {Bytes: uuid.New(), Valid: true}} {
			chRows, err := q.SearchPublicChannels(ctx, sqlcgen.SearchPublicChannelsParams{
				Query: "zzz", ViewerID: viewer, ResultLimit: 1000,
			})
			if err != nil {
				t.Fatalf("SearchPublicChannels: %v", err)
			}
			chTotal, err := q.CountSearchPublicChannels(ctx, sqlcgen.CountSearchPublicChannelsParams{
				Query: "zzz", ViewerID: viewer,
			})
			if err != nil {
				t.Fatalf("CountSearchPublicChannels: %v", err)
			}
			if chTotal != int64(len(chRows)) {
				t.Errorf("channel total %d != page %d", chTotal, len(chRows))
			}
			acRows, err := q.SearchPublicAccounts(ctx, sqlcgen.SearchPublicAccountsParams{
				Query: "zzz", ViewerID: viewer, ResultLimit: 1000,
			})
			if err != nil {
				t.Fatalf("SearchPublicAccounts: %v", err)
			}
			acTotal, err := q.CountSearchPublicAccounts(ctx, sqlcgen.CountSearchPublicAccountsParams{
				Query: "zzz", ViewerID: viewer,
			})
			if err != nil {
				t.Fatalf("CountSearchPublicAccounts: %v", err)
			}
			if acTotal != int64(len(acRows)) {
				t.Errorf("account total %d != page %d", acTotal, len(acRows))
			}
		}
	})
}

// TestSearchSortAndFilterBehaviourAgainstPostgres seeds real rows and asserts
// the ORDER BY actually orders and each filter actually narrows.
func TestSearchSortAndFilterBehaviourAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	_, channelID := seedOwnerChannel(ctx, t, st, "srchsort")
	// A unique token in every title keeps a shared database from polluting the
	// assertions: the search matches on it and nothing else can.
	token := "zq" + uuid.NewString()[:8]

	for _, sd := range []struct {
		title    string
		ageHours int
		duration *int32
		views    int64
		tags     []string
	}{
		// The suffixes are deliberately the same LENGTH. similarity() is a
		// trigram ratio, so a longer title dilutes the score: with names like
		// "short" and "unmeasured" the relevance order is decided by title
		// length, and the ordering assertions below would be measuring that
		// instead of the ORDER BY. Equal-length suffixes tie the similarity
		// across all four rows, which is what makes the relevance branch's
		// created_at tiebreak observable at all.
		{title: token + " aa", ageHours: 72, duration: i32(60), views: 5, tags: []string{"go", "redis"}},
		{title: token + " bb", ageHours: 48, duration: i32(400), views: 500, tags: []string{"go"}},
		{title: token + " cc", ageHours: 24, duration: i32(1200), views: 50, tags: []string{"redis"}},
		// No video_metadata row at all: an UNKNOWN duration.
		{title: token + " dd", ageHours: 12, duration: nil, views: 1},
	} {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state, created_at)
			 VALUES ($1, $2, 'public', 'published', now() - make_interval(hours => $3)) RETURNING id`,
			channelID, sd.title, sd.ageHours,
		).Scan(&id); err != nil {
			t.Fatalf("seed %q: %v", sd.title, err)
		}
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO video_view_counts (video_id, views) VALUES ($1, $2)`, id, sd.views); err != nil {
			t.Fatalf("seed views: %v", err)
		}
		if sd.duration != nil {
			if _, err := st.Pool.Exec(ctx,
				`INSERT INTO video_metadata (video_id, duration_seconds) VALUES ($1, $2)`, id, *sd.duration); err != nil {
				t.Fatalf("seed metadata: %v", err)
			}
		}
		for _, tag := range sd.tags {
			if _, err := st.Pool.Exec(ctx,
				`INSERT INTO video_tags (video_id, tag) VALUES ($1, $2)`, id, tag); err != nil {
				t.Fatalf("seed tag: %v", err)
			}
		}
	}

	titles := func(t *testing.T, p sqlcgen.SearchPublicVideosParams) []string {
		t.Helper()
		p.Query, p.ResultLimit = token, 50
		if p.Sort == "" {
			p.Sort = video.SearchSortDefault
		}
		rows, err := q.SearchPublicVideos(ctx, p)
		if err != nil {
			t.Fatalf("SearchPublicVideos %+v: %v", p, err)
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
	names := func(suffixes ...string) []string {
		out := make([]string, 0, len(suffixes))
		for _, s := range suffixes {
			out = append(out, token+" "+s)
		}
		return out
	}

	t.Run("sorts order", func(t *testing.T) {
		// With the similarity tied (see the seed comment), relevance falls
		// through to its created_at tiebreak — which is exactly the fixed
		// newest-first ordering this endpoint had before it took a sort at all.
		newestFirst := names("dd", "cc", "bb", "aa")
		for _, tc := range []struct {
			sort string
			want []string
		}{
			{video.SearchSortDefault, newestFirst},
			{video.SearchSortNewest, newestFirst},
			{video.SearchSortOldest, names("aa", "bb", "cc", "dd")},
			// views: aa=5, bb=500, cc=50, dd=1.
			{video.SearchSortMostViewed, names("bb", "cc", "aa", "dd")},
			{video.SearchSortLeastViewed, names("dd", "aa", "cc", "bb")},
		} {
			if got := titles(t, sqlcgen.SearchPublicVideosParams{Sort: tc.sort}); !same(got, tc.want) {
				t.Errorf("sort=%q ordered %v, want %v", tc.sort, got, tc.want)
			}
		}
	})

	// The filter subtests pin an explicit newest-first sort so what they assert
	// is the FILTER, never the ranking.
	newest := video.SearchSortNewest

	t.Run("duration bounds narrow, and unknown duration never matches", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			min, max *int32
			want     []string
		}{
			{name: "max 240", max: i32(240), want: names("aa")},
			{name: "240..600", min: i32(240), max: i32(600), want: names("bb")},
			{name: "min 600", min: i32(600), want: names("cc")},
			// The whole seeded range, plus the point of this subtest: the video
			// with NO duration row is still excluded, because `NULL >= 0` is
			// NULL rather than true.
			{name: "min 0", min: i32(0), want: names("cc", "bb", "aa")},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := titles(t, sqlcgen.SearchPublicVideosParams{
					Sort: newest, DurationMin: tc.min, DurationMax: tc.max,
				})
				if !same(got, tc.want) {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			})
		}
		// Unfiltered, the unmeasured video IS a normal result.
		if got := titles(t, sqlcgen.SearchPublicVideosParams{Sort: newest}); len(got) != 4 {
			t.Errorf("unfiltered = %v, want all four", got)
		}
	})

	t.Run("publish window narrows", func(t *testing.T) {
		cutoff := pgtype.Timestamptz{Time: time.Now().Add(-36 * time.Hour), Valid: true}
		if got, want := titles(t, sqlcgen.SearchPublicVideosParams{Sort: newest, PublishedAfter: cutoff}),
			names("dd", "cc"); !same(got, want) {
			t.Errorf("published_after = %v, want %v", got, want)
		}
		if got, want := titles(t, sqlcgen.SearchPublicVideosParams{Sort: newest, PublishedBefore: cutoff}),
			names("bb", "aa"); !same(got, want) {
			t.Errorf("published_before = %v, want %v", got, want)
		}
	})

	t.Run("tag sets narrow", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			all, oneOf []string
			want       []string
		}{
			{name: "all_of both", all: []string{"go", "redis"}, want: names("aa")},
			{name: "all_of one", all: []string{"go"}, want: names("bb", "aa")},
			{name: "one_of either", oneOf: []string{"go", "redis"}, want: names("cc", "bb", "aa")},
			{name: "one_of single", oneOf: []string{"redis"}, want: names("cc", "aa")},
			// A repeated tag must not make all_of unsatisfiable. This case
			// FAILED against real Postgres before the query compared the
			// DISTINCT count of the requested tags instead of cardinality():
			// count(DISTINCT tag)=1 could never equal cardinality({go,go})=2,
			// so the filter matched nothing at all.
			{name: "all_of repeated", all: []string{"go", "go"}, want: names("bb", "aa")},
			{name: "all_of unknown", all: []string{"nosuchtag"}, want: nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := titles(t, sqlcgen.SearchPublicVideosParams{
					Sort: newest, TagsAllOf: tc.all, TagsOneOf: tc.oneOf,
				})
				if !same(got, tc.want) {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			})
		}
	})
}

// TestEntitySearchVisibilityAgainstPostgres is the account-visibility proof
// against the REAL query rather than the HTTP layer's in-memory fake.
//
// It asserts both directions of the rule: a public account is returned, and an
// account made non-public by EACH of the three mechanisms is not — including the
// negative case that matters most, an account that never opted its profile in.
func TestEntitySearchVisibilityAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	// A token unique to this run, embedded in every seeded username, handle and
	// display name, so the assertions cannot see anything else in the database.
	token := "zq" + uuid.NewString()[:8]
	type seed struct {
		name          string
		active        bool
		profilePublic bool
		unlisted      bool
	}
	users := map[string]uuid.UUID{}
	for _, s := range []seed{
		{name: "pub", active: true, profilePublic: true},
		{name: "private", active: true, profilePublic: false},
		{name: "gone", active: false, profilePublic: true},
		{name: "hidden", active: true, profilePublic: true, unlisted: true},
	} {
		username := token + "_" + s.name
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash, display_name, is_active, profile_public, unlisted)
			 VALUES ($1, $2, 'x', $3, $4, $5, $6) RETURNING id`,
			username, username+"@example.test", "Name "+username, s.active, s.profilePublic, s.unlisted,
		).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", s.name, err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		users[s.name] = id
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, $3)`,
			id, username, "Channel "+username,
		); err != nil {
			t.Fatalf("seed channel for %s: %v", s.name, err)
		}
	}

	accountNames := func(t *testing.T, viewer pgtype.UUID) []string {
		t.Helper()
		rows, err := q.SearchPublicAccounts(ctx, sqlcgen.SearchPublicAccountsParams{
			Query: token, ViewerID: viewer, ResultLimit: 50,
		})
		if err != nil {
			t.Fatalf("SearchPublicAccounts: %v", err)
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Username)
		}
		return out
	}
	channelHandles := func(t *testing.T, viewer pgtype.UUID) []string {
		t.Helper()
		rows, err := q.SearchPublicChannels(ctx, sqlcgen.SearchPublicChannelsParams{
			Query: token, ViewerID: viewer, ResultLimit: 50,
		})
		if err != nil {
			t.Fatalf("SearchPublicChannels: %v", err)
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Handle)
		}
		return out
	}
	has := func(list []string, want string) bool {
		for _, v := range list {
			if v == want {
				return true
			}
		}
		return false
	}

	anon := pgtype.UUID{}
	got := accountNames(t, anon)
	if len(got) != 1 || got[0] != token+"_pub" {
		t.Fatalf("account search = %v, want only %s_pub — every other seed is non-public", got, token)
	}
	for _, name := range []string{"private", "gone", "hidden"} {
		if has(got, token+"_"+name) {
			t.Errorf("a %s account leaked into the account search: %v", name, got)
		}
	}
	// The profile lookup this rule comes from must agree on the two it refuses.
	for _, name := range []string{"private", "gone"} {
		if _, err := q.GetPublicUserProfileByUsername(ctx, token+"_"+name); err == nil {
			t.Errorf("GetPublicUserProfileByUsername(%s) resolved; the two surfaces disagree", name)
		}
	}
	// …and on the one it does still serve by direct lookup.
	if _, err := q.GetPublicUserProfileByUsername(ctx, token+"_hidden"); err != nil {
		t.Errorf("an unlisted account must keep its direct profile URL: %v", err)
	}

	// Channels follow the OWNER: active and not unlisted. profile_public is not
	// consulted, so the private account's channel IS discoverable.
	chGot := channelHandles(t, anon)
	for _, want := range []string{token + "_pub", token + "_private"} {
		if !has(chGot, want) {
			t.Errorf("channel %s missing from channel search: %v", want, chGot)
		}
	}
	for _, notWant := range []string{token + "_gone", token + "_hidden"} {
		if has(chGot, notWant) {
			t.Errorf("channel %s of a deactivated/unlisted owner leaked: %v", notWant, chGot)
		}
	}

	// Per-viewer blocks and mutes remove rows from both searches.
	viewerID := users["gone"] // any existing account can act as the viewer
	viewer := pgtype.UUID{Bytes: viewerID, Valid: true}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, viewerID, users["pub"]); err != nil {
		t.Fatalf("seed block: %v", err)
	}
	if has(accountNames(t, viewer), token+"_pub") {
		t.Error("a blocked account is still returned to the blocker")
	}
	if has(channelHandles(t, viewer), token+"_pub") {
		t.Error("a blocked account's channel is still returned to the blocker")
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`, viewerID, users["private"]); err != nil {
		t.Fatalf("seed mute: %v", err)
	}
	if has(channelHandles(t, viewer), token+"_private") {
		t.Error("a muted account's channel is still returned to the muter")
	}
	// Anonymous callers are unaffected: the predicates are per-viewer.
	if !has(accountNames(t, anon), token+"_pub") {
		t.Error("a per-viewer block leaked into the anonymous view")
	}
}
