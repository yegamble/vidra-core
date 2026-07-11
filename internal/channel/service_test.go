package channel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory channel.Repository keyed by lowercased handle.
type fakeRepo struct {
	byHandle   map[string]sqlcgen.Channel
	follows    map[string]bool      // "followerID|channelID"
	followedAt map[string]time.Time // when each follow was created
	followSeq  []string             // follow keys in follow order
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byHandle:   map[string]sqlcgen.Channel{},
		follows:    map[string]bool{},
		followedAt: map[string]time.Time{},
	}
}

func followKey(follower, channel uuid.UUID) string { return follower.String() + "|" + channel.String() }

func (f *fakeRepo) FollowChannel(_ context.Context, a sqlcgen.FollowChannelParams) (int64, error) {
	key := followKey(a.FollowerID, a.ChannelID)
	if f.follows[key] {
		return 0, nil // already following
	}
	f.follows[key] = true
	f.followedAt[key] = time.Now()
	f.followSeq = append(f.followSeq, key)
	return 1, nil
}

func (f *fakeRepo) UnfollowChannel(_ context.Context, a sqlcgen.UnfollowChannelParams) error {
	key := followKey(a.FollowerID, a.ChannelID)
	delete(f.follows, key)
	delete(f.followedAt, key)
	for i, k := range f.followSeq {
		if k == key {
			f.followSeq = append(f.followSeq[:i], f.followSeq[i+1:]...)
			break
		}
	}
	return nil
}

func (f *fakeRepo) ListFollowedChannels(ctx context.Context, a sqlcgen.ListFollowedChannelsParams) ([]sqlcgen.ListFollowedChannelsRow, error) {
	prefix := a.FollowerID.String() + "|"
	var out []sqlcgen.ListFollowedChannelsRow
	skipped, taken := int32(0), int32(0)
	for i := len(f.followSeq) - 1; i >= 0; i-- { // reverse = most recently followed first
		key := f.followSeq[i]
		if !f.follows[key] || !strings.HasPrefix(key, prefix) {
			continue
		}
		chID := strings.TrimPrefix(key, prefix)
		var ch sqlcgen.Channel
		found := false
		for _, c := range f.byHandle {
			if c.ID.String() == chID {
				ch, found = c, true
				break
			}
		}
		if !found {
			continue
		}
		if skipped < a.Offset {
			skipped++
			continue
		}
		if a.Limit > 0 && taken >= a.Limit {
			break
		}
		count, _ := f.CountChannelFollowers(ctx, ch.ID)
		out = append(out, sqlcgen.ListFollowedChannelsRow{
			ID: ch.ID, OwnerID: ch.OwnerID, Handle: ch.Handle,
			DisplayName: ch.DisplayName, Description: ch.Description,
			CreatedAt: ch.CreatedAt, UpdatedAt: ch.UpdatedAt,
			FollowerCount: count, FollowedAt: f.followedAt[key],
		})
		taken++
	}
	return out, nil
}

func (f *fakeRepo) CountChannelFollowers(_ context.Context, channelID uuid.UUID) (int64, error) {
	var n int64
	for k := range f.follows {
		if k[len(k)-len(channelID.String()):] == channelID.String() {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) CreateChannel(_ context.Context, a sqlcgen.CreateChannelParams) (sqlcgen.Channel, error) {
	key := strings.ToLower(a.Handle)
	if _, ok := f.byHandle[key]; ok {
		return sqlcgen.Channel{}, &pgconn.PgError{Code: "23505"}
	}
	ch := sqlcgen.Channel{
		ID: uuid.New(), OwnerID: a.OwnerID, Handle: a.Handle,
		DisplayName: a.DisplayName, Description: a.Description,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.byHandle[key] = ch
	return ch, nil
}

func (f *fakeRepo) GetChannelByHandle(_ context.Context, lowerHandle string) (sqlcgen.Channel, error) {
	ch, ok := f.byHandle[strings.ToLower(lowerHandle)]
	if !ok {
		return sqlcgen.Channel{}, errors.New("not found")
	}
	return ch, nil
}

func (f *fakeRepo) ListChannelsByOwner(_ context.Context, ownerID uuid.UUID) ([]sqlcgen.Channel, error) {
	var out []sqlcgen.Channel
	for _, ch := range f.byHandle {
		if ch.OwnerID == ownerID {
			out = append(out, ch)
		}
	}
	return out, nil
}

func (f *fakeRepo) UpdateChannel(_ context.Context, a sqlcgen.UpdateChannelParams) (sqlcgen.Channel, error) {
	for k, ch := range f.byHandle {
		if ch.ID == a.ID {
			if a.DisplayName != nil {
				ch.DisplayName = *a.DisplayName
			}
			if a.Description != nil {
				ch.Description = *a.Description
			}
			ch.UpdatedAt = time.Now()
			f.byHandle[k] = ch
			return ch, nil
		}
	}
	return sqlcgen.Channel{}, errors.New("not found")
}

func (f *fakeRepo) DeleteChannel(_ context.Context, id uuid.UUID) error {
	for k, ch := range f.byHandle {
		if ch.ID == id {
			delete(f.byHandle, k)
			return nil
		}
	}
	return nil
}

func TestCreateChannel(t *testing.T) {
	svc := NewService(newFakeRepo())
	owner := uuid.New()
	ch, err := svc.Create(context.Background(), owner, CreateInput{Handle: "ada_makes", DisplayName: "Ada Makes"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ch.OwnerID != owner || ch.Handle != "ada_makes" {
		t.Errorf("unexpected channel: %+v", ch)
	}
}

func TestCreateChannelDuplicateIsConflict(t *testing.T) {
	svc := NewService(newFakeRepo())
	owner := uuid.New()
	if _, err := svc.Create(context.Background(), owner, CreateInput{Handle: "ada", DisplayName: "Ada"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// Different owner, same handle (case-insensitive) → conflict.
	_, err := svc.Create(context.Background(), uuid.New(), CreateInput{Handle: "ADA", DisplayName: "Ada2"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestGetByHandle(t *testing.T) {
	svc := NewService(newFakeRepo())
	owner := uuid.New()
	_, _ = svc.Create(context.Background(), owner, CreateInput{Handle: "ada", DisplayName: "Ada"})

	got, err := svc.GetByHandle(context.Background(), "ADA")
	if err != nil {
		t.Fatalf("GetByHandle: %v", err)
	}
	if got.Handle != "ada" {
		t.Errorf("handle = %q, want ada", got.Handle)
	}

	if _, err := svc.GetByHandle(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func strptr(s string) *string { return &s }

func TestUpdateChannel(t *testing.T) {
	svc := NewService(newFakeRepo())
	owner := uuid.New()
	_, _ = svc.Create(context.Background(), owner, CreateInput{Handle: "ada", DisplayName: "Ada", Description: "old"})

	// Partial update: only description changes.
	ch, err := svc.Update(context.Background(), owner, "ada", UpdateInput{Description: strptr("new")})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if ch.Description != "new" || ch.DisplayName != "Ada" {
		t.Errorf("unexpected update: %+v", ch)
	}
}

func TestUpdateChannelNonOwnerForbidden(t *testing.T) {
	svc := NewService(newFakeRepo())
	owner := uuid.New()
	_, _ = svc.Create(context.Background(), owner, CreateInput{Handle: "ada", DisplayName: "Ada"})

	_, err := svc.Update(context.Background(), uuid.New(), "ada", UpdateInput{DisplayName: strptr("Hax")})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestUpdateChannelNotFound(t *testing.T) {
	svc := NewService(newFakeRepo())
	if _, err := svc.Update(context.Background(), uuid.New(), "ghost", UpdateInput{DisplayName: strptr("x")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteChannel(t *testing.T) {
	svc := NewService(newFakeRepo())
	owner := uuid.New()
	_, _ = svc.Create(context.Background(), owner, CreateInput{Handle: "ada", DisplayName: "Ada"})

	if err := svc.Delete(context.Background(), owner, "ada"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.GetByHandle(context.Background(), "ada"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v, want ErrNotFound", err)
	}
}

func TestDeleteChannelNonOwnerForbidden(t *testing.T) {
	svc := NewService(newFakeRepo())
	owner := uuid.New()
	_, _ = svc.Create(context.Background(), owner, CreateInput{Handle: "ada", DisplayName: "Ada"})

	if err := svc.Delete(context.Background(), uuid.New(), "ada"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestFollowUnfollowAndCount(t *testing.T) {
	svc := NewService(newFakeRepo())
	ctx := context.Background()
	owner := uuid.New()
	_, _ = svc.Create(ctx, owner, CreateInput{Handle: "ada", DisplayName: "Ada"})
	ch, _ := svc.GetByHandle(ctx, "ada")

	f1, f2 := uuid.New(), uuid.New()
	if _, created, err := svc.Follow(ctx, f1, "ada"); err != nil || !created {
		t.Fatalf("follow f1: created=%v err=%v, want created=true", created, err)
	}
	// Following twice is idempotent and reports created=false.
	if _, created, err := svc.Follow(ctx, f1, "ada"); err != nil || created {
		t.Fatalf("follow f1 again: created=%v err=%v, want created=false", created, err)
	}
	if _, created, err := svc.Follow(ctx, f2, "ada"); err != nil || !created {
		t.Fatalf("follow f2: created=%v err=%v, want created=true", created, err)
	}
	if n, _ := svc.FollowerCount(ctx, ch.ID); n != 2 {
		t.Errorf("follower count = %d, want 2", n)
	}

	if err := svc.Unfollow(ctx, f1, "ada"); err != nil {
		t.Fatalf("unfollow f1: %v", err)
	}
	if err := svc.Unfollow(ctx, f1, "ada"); err != nil { // idempotent
		t.Fatalf("unfollow f1 again: %v", err)
	}
	if n, _ := svc.FollowerCount(ctx, ch.ID); n != 1 {
		t.Errorf("follower count after unfollow = %d, want 1", n)
	}
}

func TestFollowUnknownChannelNotFound(t *testing.T) {
	svc := NewService(newFakeRepo())
	if _, _, err := svc.Follow(context.Background(), uuid.New(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListFollowed(t *testing.T) {
	svc := NewService(newFakeRepo())
	ctx := context.Background()
	owner := uuid.New()
	_, _ = svc.Create(ctx, owner, CreateInput{Handle: "ada", DisplayName: "Ada"})
	_, _ = svc.Create(ctx, owner, CreateInput{Handle: "north", DisplayName: "North"})
	_, _ = svc.Create(ctx, owner, CreateInput{Handle: "field", DisplayName: "Field"})

	follower := uuid.New()
	// A follower with no follows gets an empty (non-nil) slice.
	if got, err := svc.ListFollowed(ctx, follower, 20, 0); err != nil || len(got) != 0 {
		t.Fatalf("ListFollowed empty = (%v, %v), want ([], nil)", got, err)
	}

	// Follow in order: ada, north, field. Newest-first → field, north, ada.
	for _, h := range []string{"ada", "north", "field"} {
		if _, _, err := svc.Follow(ctx, follower, h); err != nil {
			t.Fatalf("follow %s: %v", h, err)
		}
	}
	// A second follower on "ada" bumps its follower_count to 2.
	if _, _, err := svc.Follow(ctx, uuid.New(), "ada"); err != nil {
		t.Fatalf("second follow ada: %v", err)
	}

	got, err := svc.ListFollowed(ctx, follower, 20, 0)
	if err != nil {
		t.Fatalf("ListFollowed: %v", err)
	}
	wantOrder := []string{"field", "north", "ada"}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d followed, want %d", len(got), len(wantOrder))
	}
	for i, w := range wantOrder {
		if got[i].Channel.Handle != w {
			t.Errorf("position %d = %q, want %q", i, got[i].Channel.Handle, w)
		}
		if got[i].FollowedAt.IsZero() {
			t.Errorf("%s FollowedAt is zero", w)
		}
	}
	if got[2].FollowerCount != 2 { // ada
		t.Errorf("ada follower_count = %d, want 2", got[2].FollowerCount)
	}

	// Pagination: limit 1 offset 1 → the second newest ("north").
	page, err := svc.ListFollowed(ctx, follower, 1, 1)
	if err != nil {
		t.Fatalf("ListFollowed page: %v", err)
	}
	if len(page) != 1 || page[0].Channel.Handle != "north" {
		t.Fatalf("limit=1 offset=1 = %+v, want [north]", page)
	}
}

func TestListOwn(t *testing.T) {
	svc := NewService(newFakeRepo())
	owner := uuid.New()
	_, _ = svc.Create(context.Background(), owner, CreateInput{Handle: "one", DisplayName: "One"})
	_, _ = svc.Create(context.Background(), owner, CreateInput{Handle: "two", DisplayName: "Two"})
	_, _ = svc.Create(context.Background(), uuid.New(), CreateInput{Handle: "other", DisplayName: "Other"})

	chans, err := svc.ListOwn(context.Background(), owner)
	if err != nil {
		t.Fatalf("ListOwn: %v", err)
	}
	if len(chans) != 2 {
		t.Errorf("got %d channels, want 2", len(chans))
	}
}

// TestCreateChannelMaxPerUser covers the max_channels_per_user runtime cap
// (config-parity W8): at-cap creation is refused with ErrMaxReached, 0 =
// unlimited, the provider is re-read per Create (a runtime flip applies
// without reconstruction), and the cap is per-OWNER — other users are
// unaffected.
func TestCreateChannelMaxPerUser(t *testing.T) {
	ctx := context.Background()
	owner, other := uuid.New(), uuid.New()
	max := int64(2)
	svc := NewService(newFakeRepo(), WithMaxPerUserFunc(func() int64 { return max }))

	if _, err := svc.Create(ctx, owner, CreateInput{Handle: "one", DisplayName: "One"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := svc.Create(ctx, owner, CreateInput{Handle: "two", DisplayName: "Two"}); err != nil {
		t.Fatalf("second create: %v", err)
	}
	if _, err := svc.Create(ctx, owner, CreateInput{Handle: "three", DisplayName: "Three"}); !errors.Is(err, ErrMaxReached) {
		t.Fatalf("at-cap create err = %v, want ErrMaxReached", err)
	}
	// Per-owner: another user is untouched by owner's count.
	if _, err := svc.Create(ctx, other, CreateInput{Handle: "elsewhere", DisplayName: "E"}); err != nil {
		t.Fatalf("other user create: %v", err)
	}
	// 0 = unlimited (runtime flip, same service instance).
	max = 0
	if _, err := svc.Create(ctx, owner, CreateInput{Handle: "three", DisplayName: "Three"}); err != nil {
		t.Fatalf("unlimited create: %v", err)
	}
	// No provider wired at all → unlimited (shipped behaviour preserved).
	unwired := NewService(newFakeRepo())
	for i, h := range []string{"a", "b", "c", "d"} {
		if _, err := unwired.Create(ctx, owner, CreateInput{Handle: h, DisplayName: h}); err != nil {
			t.Fatalf("unwired create %d: %v", i, err)
		}
	}
}
