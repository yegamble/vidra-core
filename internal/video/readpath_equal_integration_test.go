//go:build integration

package video_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// card is what a discovery card shows of one feed row; comparing it (not just
// the id) catches a statement that returns the right rows with the wrong join.
func card(id uuid.UUID, title, handle, author string, views int64, thumb, sensitive bool, duration *int32) string {
	d := int32(-1)
	if duration != nil {
		d = *duration
	}
	return fmt.Sprintf("%s|%s|%s|%s|%d|%v|%v|%d", id, title, handle, author, views, thumb, sensitive, d)
}

// TestListPublicMatchesTheGeneralStatement is the contract of the anonymous
// feed statements: for every sort, scope, filter, viewer and page, ListPublic
// returns exactly what ListPublicVideosSorted + CountPublicVideosSorted return
// for the same request — same rows, same order, same total. The fixture
// repeats every sort key, hides sensitive rows with and without flags, and
// has blocked videos, unlisted owners, private, internal and draft rows.
func TestListPublicMatchesTheGeneralStatement(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sfx := seedReadPath(t, st, readPathShape{Owners: 40, Videos: 600, Flags: true})
	var viewer uuid.UUID
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM users WHERE username = 'rpu' || $1 || '_1'`, sfx).Scan(&viewer); err != nil {
		t.Fatalf("pick viewer: %v", err)
	}

	// One snapshot for both sides: other packages write to this database while
	// the test runs, and trending reads now().
	tx, err := st.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlcgen.New(tx)
	svc := video.NewService(q, nil)

	filters := map[string]video.FeedFilter{
		"none":      {},
		"tag":       {Tag: "RPtag3"},
		"category":  {Category: "music"},
		"languages": {Languages: []string{"en"}},
	}
	// Every page shape for the request the anonymous statements serve (the
	// last two: the whole set in one page, and a page past the end); one page
	// for the shapes that must keep the general statement.
	allPages := [][2]int32{{20, 0}, {20, 20}, {7, 113}, {1000, 0}, {50, 100000}}
	for _, sort := range []string{"recent", "popular", "trending", "originally_published_at", "name"} {
		for _, scope := range []string{"local", "all"} {
			for name, base := range filters {
				for _, hide := range []bool{false, true} {
					for _, authed := range []bool{false, true} {
						pages := allPages[:1]
						if scope == "local" && name == "none" && !authed {
							pages = allPages
						}
						for _, pg := range pages {
							filter := base
							filter.HideSensitive = hide
							label := fmt.Sprintf("sort=%s scope=%s filter=%s hide=%v authed=%v page=%v", sort, scope, name, hide, authed, pg)

							items, total, err := svc.ListPublic(ctx, sort, scope, filter, viewer, authed, pg[0], pg[1])
							if err != nil {
								t.Fatalf("%s: ListPublic: %v", label, err)
							}
							arg := sqlcgen.ListPublicVideosSortedParams{
								Sort: video.NormalizeFeedSort(sort), IncludeRemote: scope == "all",
								ViewerID: pgtype.UUID{Bytes: viewer, Valid: authed}, HideSensitive: hide,
								Language: base.Languages, ResultLimit: pg[0], ResultOffset: pg[1],
							}
							if base.Tag != "" {
								tag := strings.ToLower(base.Tag)
								arg.Tag = &tag
							}
							if base.Category != "" {
								arg.Category = &base.Category
							}
							rows, err := q.ListPublicVideosSorted(ctx, arg)
							if err != nil {
								t.Fatalf("%s: ListPublicVideosSorted: %v", label, err)
							}
							wantTotal, err := q.CountPublicVideosSorted(ctx, sqlcgen.CountPublicVideosSortedParams{
								IncludeRemote: arg.IncludeRemote, ViewerID: arg.ViewerID, Tag: arg.Tag,
								Category: arg.Category, Language: arg.Language, HideSensitive: hide,
							})
							if err != nil {
								t.Fatalf("%s: CountPublicVideosSorted: %v", label, err)
							}

							got := make([]string, 0, len(items))
							for _, it := range items {
								got = append(got, card(it.Video.ID, it.Video.Title, it.ChannelHandle, it.AuthorDisplayName, it.Views, it.HasThumbnail, it.Video.IsSensitive, it.DurationSeconds))
							}
							want := make([]string, 0, len(rows))
							for _, r := range rows {
								want = append(want, card(r.ID, r.Title, r.ChannelHandle, r.AuthorDisplayName, r.Views, r.HasThumbnail, r.IsSensitive, r.DurationSeconds))
							}
							if total != wantTotal {
								t.Errorf("%s: total = %d, want %d", label, total, wantTotal)
							}
							if !slices.Equal(got, want) {
								t.Errorf("%s: rows differ\n got %v\nwant %v", label, got, want)
							}
							// An unread fixture would pass every comparison above.
							if name == "none" && pg == allPages[0] && len(want) != 20 {
								t.Fatalf("%s: the reference returned %d rows, want a full page", label, len(want))
							}
						}
					}
				}
			}
		}
	}
}
