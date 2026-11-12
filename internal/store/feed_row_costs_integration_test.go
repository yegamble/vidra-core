//go:build integration

package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The search statements answer a flagless sensitive row from hide_sensitive
// directly and only ask vidra_sensitive_hidden() about flagged ones (the
// per-row call was most of the search count's time). This holds the shortcut
// to the function's own verdict, for every base policy and viewer.
func TestSearchSensitiveShortcutMatchesThePolicyFunction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	viewer, channelID, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	token := "guard" + uuid.NewString()[:8]
	var mine []uuid.UUID
	for _, v := range []struct {
		sensitive bool
		flags     []string
	}{{false, nil}, {true, nil}, {true, []string{"violent"}}, {true, []string{"violent", "sex"}}} {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx, `INSERT INTO videos (channel_id, title, privacy, state, is_sensitive, sensitive_flags)
			VALUES ($1, $2, 'public', 'published', $3, COALESCE($4::text[], '{}')) RETURNING id`,
			channelID, token+" clip", v.sensitive, v.flags).Scan(&id); err != nil {
			t.Fatalf("insert video: %v", err)
		}
		mine = append(mine, id)
	}

	for _, policies := range []string{`{}`, `{"violent":"hide"}`, `{"violent":"display"}`, `{"violent":"display","sex":"blur"}`} {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO user_content_prefs (user_id, sensitive_flag_policies) VALUES ($1, $2::jsonb)
			ON CONFLICT (user_id) DO UPDATE SET sensitive_flag_policies = EXCLUDED.sensitive_flag_policies`, viewer, policies); err != nil {
			t.Fatalf("save prefs: %v", err)
		}
		for _, hide := range []bool{false, true} {
			for _, authed := range []bool{false, true} {
				who := pgtype.UUID{Bytes: viewer, Valid: authed}
				rows, err := st.Pool.Query(ctx, `SELECT id FROM videos WHERE id = ANY($1)
					AND NOT (is_sensitive AND vidra_sensitive_hidden(is_sensitive, sensitive_flags, $2, $3))`, mine, hide, who)
				if err != nil {
					t.Fatalf("function verdict: %v", err)
				}
				var want []uuid.UUID
				for rows.Next() {
					var id uuid.UUID
					if err := rows.Scan(&id); err != nil {
						t.Fatalf("scan: %v", err)
					}
					want = append(want, id)
				}
				rows.Close()

				found, err := q.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{
					Query: token, ViewerID: who, HideSensitive: hide, Sort: "relevance", ResultLimit: 100,
				})
				if err != nil {
					t.Fatalf("SearchPublicVideos: %v", err)
				}
				var got []uuid.UUID
				for _, r := range found {
					got = append(got, r.ID)
				}
				n, err := q.CountSearchPublicVideos(ctx, sqlcgen.CountSearchPublicVideosParams{Query: token, ViewerID: who, HideSensitive: hide})
				if err != nil {
					t.Fatalf("CountSearchPublicVideos: %v", err)
				}
				sortIDs := func(ids []uuid.UUID) {
					slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
				}
				sortIDs(got)
				sortIDs(want)
				if !slices.Equal(got, want) || n != int64(len(want)) {
					t.Errorf("policies=%s hide=%v authed=%v: search = %v (count %d), function says %v", policies, hide, authed, got, n, want)
				}
			}
		}
	}
}

// Trending's age term is computed in float8 (numeric power() was most of the
// sort's time). This holds the order to the numeric definition it replaced,
// over ages from minutes to years and with tied scores.
func TestTrendingOrderMatchesTheNumericDefinition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	_, channelID, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	var mine []uuid.UUID
	ages := []string{"3 minutes", "50 minutes", "5 hours", "5 hours", "26 hours", "9 days", "9 days", "40 days", "400 days", "3 years"}
	for i, age := range ages {
		for _, views := range []int64{0, 7, 7, 1000 * int64(i+1)} {
			var id uuid.UUID
			if err := st.Pool.QueryRow(ctx, `INSERT INTO videos (channel_id, title, privacy, state, created_at)
				VALUES ($1, 'trend', 'public', 'published', date_trunc('second', now()) - $2::interval) RETURNING id`,
				channelID, age).Scan(&id); err != nil {
				t.Fatalf("insert video: %v", err)
			}
			if _, err := st.Pool.Exec(ctx, `INSERT INTO video_view_counts (video_id, views) VALUES ($1, $2)`, id, views); err != nil {
				t.Fatalf("insert views: %v", err)
			}
			mine = append(mine, id)
		}
	}

	// One transaction: both statements must read the same now().
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT v.id FROM videos v JOIN video_view_counts vc ON vc.video_id = v.id
		WHERE v.id = ANY($1)
		ORDER BY vc.views::float8 / power(EXTRACT(EPOCH FROM (now() - v.created_at)) / 3600.0 + 2.0, 1.5) DESC,
		         v.created_at DESC, v.id DESC`, mine)
	if err != nil {
		t.Fatalf("numeric order: %v", err)
	}
	var want []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		want = append(want, id)
	}
	rows.Close()

	feed, err := sqlcgen.New(tx).ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{Sort: "trending", ResultLimit: 1000000})
	if err != nil {
		t.Fatalf("ListPublicVideosSorted: %v", err)
	}
	var got []uuid.UUID
	for _, r := range feed {
		if slices.Contains(mine, r.ID) {
			got = append(got, r.ID)
		}
	}
	if len(want) != len(mine) || !slices.Equal(got, want) {
		t.Fatalf("trending order differs from the numeric definition\n got %v\nwant %v", got, want)
	}
}
