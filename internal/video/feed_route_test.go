package video

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// feedRouteRepo records which feed statements ListPublic reaches.
type feedRouteRepo struct {
	*fakeRepo
	calls []string
	hide  []bool
}

func (r *feedRouteRepo) ListPublicVideosRecent(ctx context.Context, a sqlcgen.ListPublicVideosRecentParams) ([]sqlcgen.ListPublicVideosRecentRow, error) {
	r.calls, r.hide = append(r.calls, "recent"), append(r.hide, a.HideSensitive)
	return r.fakeRepo.ListPublicVideosRecent(ctx, a)
}

func (r *feedRouteRepo) ListPublicVideosRanked(ctx context.Context, a sqlcgen.ListPublicVideosRankedParams) ([]sqlcgen.ListPublicVideosRankedRow, error) {
	r.calls, r.hide = append(r.calls, "ranked:"+a.Sort), append(r.hide, a.HideSensitive)
	return r.fakeRepo.ListPublicVideosRanked(ctx, a)
}

func (r *feedRouteRepo) CountPublicVideosLocal(ctx context.Context, hide bool) (int64, error) {
	r.calls, r.hide = append(r.calls, "count_local"), append(r.hide, hide)
	return r.fakeRepo.CountPublicVideosLocal(ctx, hide)
}

func (r *feedRouteRepo) ListPublicVideosSorted(ctx context.Context, a sqlcgen.ListPublicVideosSortedParams) ([]sqlcgen.ListPublicVideosSortedRow, error) {
	r.calls = append(r.calls, "sorted:"+a.Sort)
	return r.fakeRepo.ListPublicVideosSorted(ctx, a)
}

func (r *feedRouteRepo) CountPublicVideosSorted(ctx context.Context, a sqlcgen.CountPublicVideosSortedParams) (int64, error) {
	r.calls = append(r.calls, "count_sorted")
	return r.fakeRepo.CountPublicVideosSorted(ctx, a)
}

// The anonymous, local, unfiltered feed has statements of its own; anything a
// viewer, a scope or a filter can change must keep the general statement, or a
// tag filter (say) would silently be dropped.
func TestListPublicPicksTheStatementForTheRequestShape(t *testing.T) {
	viewer := uuid.New()
	cases := []struct {
		name   string
		sort   string
		scope  string
		filter FeedFilter
		authed bool
		want   []string
	}{
		{"anonymous recent", "recent", "local", FeedFilter{}, false, []string{"recent", "count_local"}},
		{"anonymous unknown sort is recent", "hot", "", FeedFilter{}, false, []string{"recent", "count_local"}},
		{"anonymous trending", "trending", "local", FeedFilter{}, false, []string{"ranked:trending", "count_local"}},
		{"anonymous popular", "popular", "local", FeedFilter{}, false, []string{"ranked:popular", "count_local"}},
		{"anonymous name", "name", "local", FeedFilter{}, false, []string{"ranked:name", "count_local"}},
		{"anonymous original date", "originally_published_at", "local", FeedFilter{}, false, []string{"ranked:originally_published_at", "count_local"}},
		{"anonymous hide-sensitive stays", "recent", "local", FeedFilter{HideSensitive: true}, false, []string{"recent", "count_local"}},
		{"signed in", "recent", "local", FeedFilter{}, true, []string{"sorted:recent", "count_sorted"}},
		{"scope all", "trending", "all", FeedFilter{}, false, []string{"sorted:trending", "count_sorted"}},
		{"tag", "recent", "local", FeedFilter{Tag: "Go"}, false, []string{"sorted:recent", "count_sorted"}},
		{"category", "popular", "local", FeedFilter{Category: "music"}, false, []string{"sorted:popular", "count_sorted"}},
		{"languages", "name", "local", FeedFilter{Languages: []string{"en"}}, false, []string{"sorted:name", "count_sorted"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &feedRouteRepo{fakeRepo: newFakeRepo(uuid.New())}
			svc := NewService(repo, nil)
			if _, _, err := svc.ListPublic(context.Background(), tc.sort, tc.scope, tc.filter, viewer, tc.authed, 20, 0); err != nil {
				t.Fatalf("ListPublic: %v", err)
			}
			if !slices.Equal(repo.calls, tc.want) {
				t.Errorf("statements = %v, want %v", repo.calls, tc.want)
			}
			for _, h := range repo.hide {
				if h != tc.filter.HideSensitive {
					t.Errorf("hide_sensitive reached the statement as %v, want %v", h, tc.filter.HideSensitive)
				}
			}
		})
	}
}
