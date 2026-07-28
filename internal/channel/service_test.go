package channel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory channel.Repository keyed by lowercased handle.
type fakeRepo struct {
	byHandle    map[string]sqlcgen.Channel
	follows     map[string]bool                  // "followerID|channelID"
	bells       map[string]string                // follow key -> notification_setting
	followedAt  map[string]time.Time             // when each follow was created
	followSeq   []string                         // follow keys in follow order
	members     map[string]sqlcgen.ChannelMember // "channelID|userID"
	usersByName map[string]sqlcgen.User          // lowercased username -> user
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byHandle:    map[string]sqlcgen.Channel{},
		follows:     map[string]bool{},
		bells:       map[string]string{},
		followedAt:  map[string]time.Time{},
		members:     map[string]sqlcgen.ChannelMember{},
		usersByName: map[string]sqlcgen.User{},
	}
}

func memberKey(channelID, userID uuid.UUID) string {
	return channelID.String() + "|" + userID.String()
}

func (f *fakeRepo) GetUserByUsername(_ context.Context, username string) (sqlcgen.User, error) {
	if u, ok := f.usersByName[strings.ToLower(strings.TrimSpace(username))]; ok {
		return u, nil
	}
	return sqlcgen.User{}, pgx.ErrNoRows
}

func (f *fakeRepo) AddChannelMember(_ context.Context, a sqlcgen.AddChannelMemberParams) (sqlcgen.ChannelMember, error) {
	m := sqlcgen.ChannelMember{
		ChannelID: a.ChannelID, UserID: a.UserID, Role: a.Role,
		InvitedBy: a.InvitedBy, CreatedAt: time.Now(),
	}
	f.members[memberKey(a.ChannelID, a.UserID)] = m
	return m, nil
}

func (f *fakeRepo) GetChannelMember(_ context.Context, a sqlcgen.GetChannelMemberParams) (sqlcgen.ChannelMember, error) {
	if m, ok := f.members[memberKey(a.ChannelID, a.UserID)]; ok {
		return m, nil
	}
	return sqlcgen.ChannelMember{}, pgx.ErrNoRows
}

func (f *fakeRepo) DeleteChannelMember(_ context.Context, a sqlcgen.DeleteChannelMemberParams) (int64, error) {
	key := memberKey(a.ChannelID, a.UserID)
	if _, ok := f.members[key]; ok {
		delete(f.members, key)
		return 1, nil
	}
	return 0, nil
}

func (f *fakeRepo) ListChannelMembers(_ context.Context, channelID uuid.UUID) ([]sqlcgen.ListChannelMembersRow, error) {
	var out []sqlcgen.ListChannelMembersRow
	for _, m := range f.members {
		if m.ChannelID != channelID {
			continue
		}
		row := sqlcgen.ListChannelMembersRow{
			UserID: m.UserID, Role: m.Role, InvitedBy: m.InvitedBy, CreatedAt: m.CreatedAt,
		}
		for _, u := range f.usersByName {
			if u.ID == m.UserID {
				row.Username, row.DisplayName = u.Username, u.DisplayName
				break
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeRepo) IsChannelManager(_ context.Context, a sqlcgen.IsChannelManagerParams) (bool, error) {
	for _, ch := range f.byHandle {
		if ch.ID == a.ChannelID {
			if ch.OwnerID == a.UserID {
				return true, nil
			}
			_, ok := f.members[memberKey(a.ChannelID, a.UserID)]
			return ok, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) ListChannelsForMember(_ context.Context, userID uuid.UUID) ([]sqlcgen.ListChannelsForMemberRow, error) {
	var out []sqlcgen.ListChannelsForMemberRow
	for _, m := range f.members {
		if m.UserID != userID {
			continue
		}
		for _, ch := range f.byHandle {
			if ch.ID != m.ChannelID {
				continue
			}
			out = append(out, sqlcgen.ListChannelsForMemberRow{
				ID: ch.ID, OwnerID: ch.OwnerID, Handle: ch.Handle,
				DisplayName: ch.DisplayName, Description: ch.Description,
				ActivitypubEnabled: ch.ActivitypubEnabled, AtprotoEnabled: ch.AtprotoEnabled,
				CreatedAt: ch.CreatedAt, UpdatedAt: ch.UpdatedAt, Role: m.Role,
			})
		}
	}
	return out, nil
}

func followKey(follower, channel uuid.UUID) string { return follower.String() + "|" + channel.String() }

func (f *fakeRepo) FollowChannel(_ context.Context, a sqlcgen.FollowChannelParams) (int64, error) {
	key := followKey(a.FollowerID, a.ChannelID)
	if f.follows[key] {
		return 0, nil // already following
	}
	f.follows[key] = true
	f.bells[key] = NotifyAll // the column default a new follow gets
	f.followedAt[key] = time.Now()
	f.followSeq = append(f.followSeq, key)
	return 1, nil
}

func (f *fakeRepo) GetFollowNotificationSetting(_ context.Context, a sqlcgen.GetFollowNotificationSettingParams) (string, error) {
	key := followKey(a.FollowerID, a.ChannelID)
	if !f.follows[key] {
		return "", pgx.ErrNoRows
	}
	return f.bells[key], nil
}

func (f *fakeRepo) SetFollowNotificationSetting(_ context.Context, a sqlcgen.SetFollowNotificationSettingParams) (int64, error) {
	key := followKey(a.FollowerID, a.ChannelID)
	if !f.follows[key] {
		return 0, nil
	}
	f.bells[key] = a.NotificationSetting
	return 1, nil
}

func (f *fakeRepo) UnfollowChannel(_ context.Context, a sqlcgen.UnfollowChannelParams) error {
	key := followKey(a.FollowerID, a.ChannelID)
	delete(f.follows, key)
	delete(f.bells, key)
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
			NotificationSetting: f.bells[key],
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

func (f *fakeRepo) CountFollowersByOwner(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.CountFollowersByOwnerRow, error) {
	var rows []sqlcgen.CountFollowersByOwnerRow
	for _, ch := range f.byHandle {
		if ch.OwnerID != ownerID {
			continue
		}
		n, _ := f.CountChannelFollowers(ctx, ch.ID)
		rows = append(rows, sqlcgen.CountFollowersByOwnerRow{ChannelID: ch.ID, Followers: n})
	}
	return rows, nil
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

func TestChannelMembersLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := NewService(repo)
	owner := uuid.New()
	editor := uuid.New()
	stranger := uuid.New()
	repo.usersByName["bob"] = sqlcgen.User{ID: editor, Username: "bob", DisplayName: "Bob", IsActive: true}

	ch, _ := svc.Create(ctx, owner, CreateInput{Handle: "ada", DisplayName: "Ada"})

	// Owner invites bob as an editor.
	m, err := svc.AddMember(ctx, owner, "ada", "bob", "")
	if err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	if m.UserID != editor || m.Role != RoleEditor {
		t.Errorf("member = %+v", m)
	}

	// The editor may now manage content; the owner always could; a stranger cannot.
	for _, tc := range []struct {
		user uuid.UUID
		want bool
	}{{owner, true}, {editor, true}, {stranger, false}} {
		got, err := svc.CanManageContent(ctx, ch.ID, tc.user)
		if err != nil {
			t.Fatalf("CanManageContent: %v", err)
		}
		if got != tc.want {
			t.Errorf("CanManageContent(%v) = %v, want %v", tc.user, got, tc.want)
		}
	}

	// ListManaged: owner sees it as "owner"; editor sees it as "editor".
	ownerList, _ := svc.ListManaged(ctx, owner)
	if len(ownerList) != 1 || ownerList[0].Role != RoleOwner {
		t.Errorf("owner ListManaged = %+v", ownerList)
	}
	editorList, _ := svc.ListManaged(ctx, editor)
	if len(editorList) != 1 || editorList[0].Role != RoleEditor || editorList[0].Channel.ID != ch.ID {
		t.Errorf("editor ListManaged = %+v", editorList)
	}

	// ListMembers: owner and editor may view; a stranger gets ErrForbidden.
	if members, err := svc.ListMembers(ctx, owner, "ada"); err != nil || len(members) != 1 {
		t.Errorf("owner ListMembers = %+v, err %v", members, err)
	}
	if _, err := svc.ListMembers(ctx, editor, "ada"); err != nil {
		t.Errorf("editor ListMembers err = %v, want nil", err)
	}
	if _, err := svc.ListMembers(ctx, stranger, "ada"); !errors.Is(err, ErrForbidden) {
		t.Errorf("stranger ListMembers err = %v, want ErrForbidden", err)
	}

	// Owner removes the member; content authority is revoked.
	if err := svc.RemoveMember(ctx, owner, "ada", editor); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if got, _ := svc.CanManageContent(ctx, ch.ID, editor); got {
		t.Error("editor still manages content after removal")
	}
}

func TestAddMemberErrors(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := NewService(repo)
	owner := uuid.New()
	other := uuid.New()
	bob := uuid.New()
	repo.usersByName["bob"] = sqlcgen.User{ID: bob, Username: "bob", DisplayName: "Bob", IsActive: true}
	_, _ = svc.Create(ctx, owner, CreateInput{Handle: "ada", DisplayName: "Ada"})

	// Non-owner cannot invite.
	if _, err := svc.AddMember(ctx, other, "ada", "bob", ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-owner AddMember err = %v, want ErrForbidden", err)
	}
	// Unknown target user → 404.
	if _, err := svc.AddMember(ctx, owner, "ada", "ghost", ""); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("unknown user AddMember err = %v, want ErrUserNotFound", err)
	}
	// Inviting the owner as a member → 409.
	repo.usersByName["ada_owner"] = sqlcgen.User{ID: owner, Username: "ada_owner", IsActive: true}
	if _, err := svc.AddMember(ctx, owner, "ada", "ada_owner", ""); !errors.Is(err, ErrAlreadyMember) {
		t.Errorf("owner-as-member err = %v, want ErrAlreadyMember", err)
	}
	// Duplicate invite → 409.
	if _, err := svc.AddMember(ctx, owner, "ada", "bob", ""); err != nil {
		t.Fatalf("first AddMember: %v", err)
	}
	if _, err := svc.AddMember(ctx, owner, "ada", "bob", ""); !errors.Is(err, ErrAlreadyMember) {
		t.Errorf("duplicate AddMember err = %v, want ErrAlreadyMember", err)
	}
	// Unknown channel → 404.
	if _, err := svc.AddMember(ctx, owner, "ghost", "bob", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown channel AddMember err = %v, want ErrNotFound", err)
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

// TestFollowNotificationBell covers the per-channel bell (migration 0101): a new
// follow starts at "all", the mode round-trips through the service, an
// unsupported mode is refused before any write, and unfollowing takes the bell
// with it.
func TestFollowNotificationBell(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	ctx := context.Background()
	owner, follower := uuid.New(), uuid.New()
	_, _ = svc.Create(ctx, owner, CreateInput{Handle: "ada", DisplayName: "Ada"})
	ch, _ := svc.GetByHandle(ctx, "ada")

	if _, _, err := svc.Follow(ctx, follower, "ada"); err != nil {
		t.Fatalf("follow: %v", err)
	}
	// A fresh follow means "tell me about new videos".
	following, setting, err := svc.FollowState(ctx, follower, ch.ID)
	if err != nil || !following || setting != NotifyAll {
		t.Fatalf("FollowState after follow = (%v, %q, %v), want (true, %q, nil)", following, setting, err, NotifyAll)
	}

	if err := svc.SetFollowNotification(ctx, follower, "ada", NotifyNone); err != nil {
		t.Fatalf("mute bell: %v", err)
	}
	following, setting, err = svc.FollowState(ctx, follower, ch.ID)
	if err != nil || !following || setting != NotifyNone {
		t.Fatalf("FollowState after mute = (%v, %q, %v), want (true, %q, nil)", following, setting, err, NotifyNone)
	}
	// Muting the bell must NOT drop the subscription.
	if n, _ := svc.FollowerCount(ctx, ch.ID); n != 1 {
		t.Errorf("follower count after muting the bell = %d, want 1 (a muted bell is still a follow)", n)
	}
	if err := svc.SetFollowNotification(ctx, follower, "ada", NotifyAll); err != nil {
		t.Fatalf("unmute bell: %v", err)
	}

	// An unsupported mode is refused, and nothing is written.
	if err := svc.SetFollowNotification(ctx, follower, "ada", "personalized"); !errors.Is(err, ErrInvalidNotificationSetting) {
		t.Fatalf("err = %v, want ErrInvalidNotificationSetting", err)
	}
	if _, setting, _ := svc.FollowState(ctx, follower, ch.ID); setting != NotifyAll {
		t.Errorf("bell after a rejected mode = %q, want %q (unchanged)", setting, NotifyAll)
	}

	// Unfollowing clears the relationship AND its bell.
	if err := svc.Unfollow(ctx, follower, "ada"); err != nil {
		t.Fatalf("unfollow: %v", err)
	}
	following, setting, err = svc.FollowState(ctx, follower, ch.ID)
	if err != nil || following || setting != "" {
		t.Fatalf("FollowState after unfollow = (%v, %q, %v), want (false, \"\", nil)", following, setting, err)
	}
}

// TestSetFollowNotificationRequiresAFollow proves the bell is a property of a
// subscription: setting it without following, or on a channel that does not
// exist, is ErrNotFound either way — the caller cannot use the endpoint to probe
// which handles exist.
func TestSetFollowNotificationRequiresAFollow(t *testing.T) {
	svc := NewService(newFakeRepo())
	ctx := context.Background()
	owner, stranger := uuid.New(), uuid.New()
	_, _ = svc.Create(ctx, owner, CreateInput{Handle: "ada", DisplayName: "Ada"})

	if err := svc.SetFollowNotification(ctx, stranger, "ada", NotifyNone); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not-following err = %v, want ErrNotFound", err)
	}
	if err := svc.SetFollowNotification(ctx, stranger, "ghost", NotifyNone); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown-channel err = %v, want ErrNotFound", err)
	}
}

// TestListFollowedCarriesTheBell keeps the FOLLOWING list self-sufficient: each
// row reports the caller's bell, so the list can render its state without a
// request per channel.
func TestListFollowedCarriesTheBell(t *testing.T) {
	svc := NewService(newFakeRepo())
	ctx := context.Background()
	owner, follower := uuid.New(), uuid.New()
	for _, h := range []string{"ada", "bob"} {
		_, _ = svc.Create(ctx, owner, CreateInput{Handle: h, DisplayName: h})
		if _, _, err := svc.Follow(ctx, follower, h); err != nil {
			t.Fatalf("follow %s: %v", h, err)
		}
	}
	if err := svc.SetFollowNotification(ctx, follower, "ada", NotifyNone); err != nil {
		t.Fatalf("mute ada: %v", err)
	}

	followed, err := svc.ListFollowed(ctx, follower, 20, 0)
	if err != nil {
		t.Fatalf("ListFollowed: %v", err)
	}
	got := map[string]string{}
	for _, f := range followed {
		got[f.Channel.Handle] = f.NotificationSetting
	}
	if got["ada"] != NotifyNone || got["bob"] != NotifyAll {
		t.Fatalf("bells = %v, want ada=%s bob=%s", got, NotifyNone, NotifyAll)
	}
}
