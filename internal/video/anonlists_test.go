package video

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func cachedFeedService(t *testing.T) (*Service, *feedRouteRepo, uuid.UUID) {
	t.Helper()
	owner := uuid.New()
	repo := &feedRouteRepo{fakeRepo: newFakeRepo(owner)}
	// An hour: only an invalidation can explain a fresh answer in these tests.
	return NewService(repo, nil, WithAnonymousCache(time.Hour)), repo, owner
}

func TestAnonymousListIsReadOnceAndSignedInListsNever(t *testing.T) {
	svc, repo, _ := cachedFeedService(t)
	ctx := context.Background()
	v := publishDraft(t, svc, ctx, uuid.New(), CreateInput{Title: "a", Privacy: "public"})
	repo.calls = nil

	for i := 0; i < 3; i++ {
		items, total, err := svc.ListPublic(ctx, "recent", "local", FeedFilter{}, uuid.Nil, false, 20, 0)
		if err != nil || total != 1 || len(items) != 1 || items[0].Video.ID != v.ID {
			t.Fatalf("call %d: %v, total %d, %v", i, feedIDs(items), total, err)
		}
		items[0] = FeedItem{} // a caller scribbling on its page must not reach the next caller's
	}
	if want := []string{"recent", "count_local"}; !slices.Equal(repo.calls, want) {
		t.Fatalf("anonymous statements = %v, want one read %v", repo.calls, want)
	}

	repo.calls = nil
	viewer := uuid.New()
	for i := 0; i < 2; i++ {
		if _, _, err := svc.ListPublic(ctx, "recent", "local", FeedFilter{}, viewer, true, 20, 0); err != nil {
			t.Fatalf("signed-in list: %v", err)
		}
	}
	if want := []string{"sorted:recent", "count_sorted", "sorted:recent", "count_sorted"}; !slices.Equal(repo.calls, want) {
		t.Fatalf("signed-in statements = %v, want a read per call %v", repo.calls, want)
	}
}

// Everything the request can vary is part of the key: a different sort, page or
// hide policy is a different answer, never another request's cached one.
func TestAnonymousListKeyIsTheWholeRequest(t *testing.T) {
	svc, repo, _ := cachedFeedService(t)
	ctx := context.Background()
	ch := uuid.New()
	plain := publishDraft(t, svc, ctx, ch, CreateInput{Title: "plain", Privacy: "public"})
	publishDraft(t, svc, ctx, ch, CreateInput{Title: "flagged", Privacy: "public", IsSensitive: true})
	repo.calls = nil

	shown, total, _ := svc.ListPublic(ctx, "recent", "local", FeedFilter{}, uuid.Nil, false, 20, 0)
	if len(shown) != 2 || total != 2 {
		t.Fatalf("policy off: %v, total %d", feedIDs(shown), total)
	}
	hidden, total, _ := svc.ListPublic(ctx, "recent", "local", FeedFilter{HideSensitive: true}, uuid.Nil, false, 20, 0)
	if len(hidden) != 1 || hidden[0].Video.ID != plain.ID || total != 1 {
		t.Fatalf("policy hide: %v, total %d — the policy-off page was served", feedIDs(hidden), total)
	}
	if page2, _, _ := svc.ListPublic(ctx, "recent", "local", FeedFilter{}, uuid.Nil, false, 20, 20); len(page2) != 0 {
		t.Fatalf("offset 20 = %v, want an empty page of its own", feedIDs(page2))
	}
	_, _, _ = svc.ListPublic(ctx, "name", "local", FeedFilter{}, uuid.Nil, false, 20, 0)
	_, _, _ = svc.ListPublic(ctx, "recent", "local", FeedFilter{Tag: "go"}, uuid.Nil, false, 20, 0)
	_, _, _ = svc.ListPublic(ctx, "recent", "all", FeedFilter{}, uuid.Nil, false, 20, 0)
	if lists := len(repo.calls) / 2; lists != 6 {
		t.Fatalf("six different requests ran %d reads: %v", lists, repo.calls)
	}
}

// A takedown must leave the anonymous lists with the write, not with the TTL.
func TestVideoWritesInvalidateAnonymousLists(t *testing.T) {
	svc, _, owner := cachedFeedService(t)
	ctx := context.Background()
	ch := uuid.New()
	list := func() []uuid.UUID {
		t.Helper()
		items, total, err := svc.ListPublic(ctx, "recent", "local", FeedFilter{}, uuid.Nil, false, 20, 0)
		if err != nil || int(total) != len(items) {
			t.Fatalf("list: %d items, total %d, %v", len(items), total, err)
		}
		ids := feedIDs(items)
		slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
		return ids
	}
	sorted := func(ids ...uuid.UUID) []uuid.UUID {
		slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
		return ids
	}

	a := publishDraft(t, svc, ctx, ch, CreateInput{Title: "a", Privacy: "public"})
	if got := list(); !slices.Equal(got, sorted(a.ID)) {
		t.Fatalf("after first publish: %v", got)
	}
	b := publishDraft(t, svc, ctx, ch, CreateInput{Title: "b", Privacy: "public"})
	c := publishDraft(t, svc, ctx, ch, CreateInput{Title: "c", Privacy: "public"})
	if got := list(); !slices.Equal(got, sorted(a.ID, b.ID, c.ID)) {
		t.Fatalf("a publish did not reach the list: %v", got)
	}

	private := "private"
	if _, err := svc.Update(ctx, owner, a.ID, UpdateInput{Privacy: &private}); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if got := list(); !slices.Equal(got, sorted(b.ID, c.ID)) {
		t.Fatalf("a video made private is still listed: %v", got)
	}
	if err := svc.Delete(ctx, owner, b.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := list(); !slices.Equal(got, sorted(c.ID)) {
		t.Fatalf("a deleted video is still listed: %v", got)
	}
	if _, err := svc.RejectQuarantined(ctx, c.ID, uuid.New(), "no"); err == nil {
		if got := list(); len(got) != 0 {
			t.Fatalf("a rejected video is still listed: %v", got)
		}
	}
}

func TestAnonymousSearchCountIsReadOnce(t *testing.T) {
	svc, _, _ := cachedFeedService(t)
	ctx := context.Background()
	ch := uuid.New()
	publishDraft(t, svc, ctx, ch, CreateInput{Title: "gopher one", Privacy: "public"})
	count := func(authed bool) int64 {
		t.Helper()
		n, err := svc.CountSearchPublic(ctx, "gopher", SearchFilter{}, uuid.New(), authed)
		if err != nil {
			t.Fatalf("CountSearchPublic: %v", err)
		}
		return n
	}
	if count(false) != 1 {
		t.Fatal("first count")
	}
	// Behind the service's back: no write went through it, so nothing invalidated.
	second := publishDraft(t, NewService(svc.repo.(invalidatingRepo).Repository, nil), ctx, ch, CreateInput{Title: "gopher two", Privacy: "public"})
	if n := count(false); n != 1 {
		t.Fatalf("anonymous count = %d, want the cached 1", n)
	}
	if n := count(true); n != 2 {
		t.Fatalf("signed-in count = %d, want a fresh 2", n)
	}
	svc.InvalidateAnonymous()
	if n := count(false); n != 2 {
		t.Fatalf("after InvalidateAnonymous = %d, want 2 (video %s)", n, second.ID)
	}
}
