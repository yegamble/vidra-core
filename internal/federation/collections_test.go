package federation

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestChannelCollectionCounts(t *testing.T) {
	cid := uuid.New()
	repo := fakeRepo{
		channels:        map[string]sqlcgen.Channel{"films": {ID: cid, Handle: "films"}},
		localFollowers:  map[uuid.UUID]int64{cid: 3},
		remoteFollowerN: map[uuid.UUID]int64{cid: 2},
		channelVideoN:   map[uuid.UUID]int64{cid: 7},
	}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()

	followers, err := svc.ChannelCollection(ctx, "films", "followers")
	if err != nil {
		t.Fatalf("followers: %v", err)
	}
	if followers.TotalItems != 5 { // 3 local + 2 remote
		t.Errorf("followers totalItems = %d, want 5", followers.TotalItems)
	}
	if followers.Type != "OrderedCollection" || followers.ID != "https://videos.example/video-channels/films/followers" {
		t.Errorf("collection = %+v", followers)
	}

	outbox, err := svc.ChannelCollection(ctx, "films", "outbox")
	if err != nil {
		t.Fatalf("outbox: %v", err)
	}
	if outbox.TotalItems != 7 {
		t.Errorf("outbox totalItems = %d, want 7", outbox.TotalItems)
	}
	if outbox.First != "https://videos.example/video-channels/films/outbox?page=1" {
		t.Errorf("outbox first = %q, want a page-1 link", outbox.First)
	}

	following, err := svc.ChannelCollection(ctx, "films", "following")
	if err != nil {
		t.Fatalf("following: %v", err)
	}
	if following.TotalItems != 0 {
		t.Errorf("following totalItems = %d, want 0", following.TotalItems)
	}

	if _, err := svc.ChannelCollection(ctx, "nope", "followers"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown channel: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.ChannelCollection(ctx, "films", "bogus"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown kind: err = %v, want ErrNotFound", err)
	}
}

func TestChannelOutboxPage(t *testing.T) {
	cid := uuid.New()
	full := make([]sqlcgen.ListChannelOutboxVideosRow, outboxPageSize)
	for i := range full {
		full[i] = sqlcgen.ListChannelOutboxVideosRow{ID: uuid.New(), Title: "v", Description: ""}
	}
	repo := fakeRepo{
		channels:     map[string]sqlcgen.Channel{"films": {ID: cid, Handle: "films"}},
		outboxVideos: map[uuid.UUID][]sqlcgen.ListChannelOutboxVideosRow{cid: full},
	}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()

	pg, err := svc.ChannelOutboxPage(ctx, "films", 1)
	if err != nil {
		t.Fatalf("outbox page: %v", err)
	}
	if pg.Type != "OrderedCollectionPage" || pg.PartOf != "https://videos.example/video-channels/films/outbox" {
		t.Errorf("page = %+v", pg)
	}
	if len(pg.OrderedItems) != outboxPageSize {
		t.Fatalf("items = %d, want %d", len(pg.OrderedItems), outboxPageSize)
	}
	if pg.Next != "https://videos.example/video-channels/films/outbox?page=2" {
		t.Errorf("a full page should link next, got %q", pg.Next)
	}
	if pg.OrderedItems[0]["type"] != "Create" {
		t.Errorf("item type = %v, want Create", pg.OrderedItems[0]["type"])
	}

	// A partial (last) page has no next.
	repo2 := fakeRepo{
		channels:     map[string]sqlcgen.Channel{"films": {ID: cid, Handle: "films"}},
		outboxVideos: map[uuid.UUID][]sqlcgen.ListChannelOutboxVideosRow{cid: full[:3]},
	}
	pg2, err := NewService(repo2, WithBaseURL("https://videos.example")).ChannelOutboxPage(ctx, "films", 1)
	if err != nil {
		t.Fatalf("partial page: %v", err)
	}
	if len(pg2.OrderedItems) != 3 || pg2.Next != "" {
		t.Errorf("partial page items=%d next=%q, want 3 / no next", len(pg2.OrderedItems), pg2.Next)
	}

	if _, err := svc.ChannelOutboxPage(ctx, "nope", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown channel: err = %v, want ErrNotFound", err)
	}
}

func TestAccountCollectionEmpty(t *testing.T) {
	repo := fakeRepo{
		usersByName: map[string]sqlcgen.GetUserActorByUsernameRow{"ada": {Username: "ada"}},
	}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()

	c, err := svc.AccountCollection(ctx, "ada", "outbox")
	if err != nil {
		t.Fatalf("account outbox: %v", err)
	}
	if c.TotalItems != 0 || c.ID != "https://videos.example/accounts/ada/outbox" {
		t.Errorf("collection = %+v", c)
	}
	if _, err := svc.AccountCollection(ctx, "ghost", "followers"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown account: err = %v, want ErrNotFound", err)
	}
}
