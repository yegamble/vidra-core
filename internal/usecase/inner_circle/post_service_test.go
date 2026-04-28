package inner_circle

import (
	"context"
	"errors"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

type fakePostRepo struct {
	posts        []domain.ChannelPost
	createdBody  string
	createdTier  *string
	createCalled bool
	getResult    *domain.ChannelPost
	deleteCalled bool
}

func (f *fakePostRepo) Create(_ context.Context, channelID uuid.UUID, body string, tierID *string) (*domain.ChannelPost, error) {
	f.createCalled = true
	f.createdBody = body
	f.createdTier = tierID
	return &domain.ChannelPost{ID: uuid.New(), ChannelID: channelID, Body: body, TierID: tierID, CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
}
func (f *fakePostRepo) Update(_ context.Context, postID, channelID uuid.UUID, body *string, tierID *string, clearTier bool) (*domain.ChannelPost, error) {
	final := &domain.ChannelPost{ID: postID, ChannelID: channelID}
	if body != nil {
		final.Body = *body
	}
	if clearTier {
		final.TierID = nil
	} else if tierID != nil {
		final.TierID = tierID
	}
	return final, nil
}
func (f *fakePostRepo) Get(_ context.Context, postID, channelID uuid.UUID) (*domain.ChannelPost, error) {
	if f.getResult != nil {
		return f.getResult, nil
	}
	return &domain.ChannelPost{ID: postID, ChannelID: channelID}, nil
}
func (f *fakePostRepo) Delete(_ context.Context, _, _ uuid.UUID) error {
	f.deleteCalled = true
	return nil
}
func (f *fakePostRepo) List(_ context.Context, _ uuid.UUID, _ *uuid.UUID, _ int) ([]domain.ChannelPost, error) {
	return f.posts, nil
}

type fakePostMemberLookup struct{ tier string }

func (f *fakePostMemberLookup) GetActiveTier(_ context.Context, _, _ uuid.UUID) (string, error) {
	return f.tier, nil
}

func TestPostService_Create_AttachmentsRejected(t *testing.T) {
	owner := uuid.New()
	svc := NewPostService(&fakePostRepo{}, &fakePostMemberLookup{}, &fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	_, err := svc.Create(context.Background(), uuid.New(), owner, CreateInput{Body: "hello", HasAttachmentsRaw: true})
	if !errors.Is(err, ErrAttachmentsRejected) {
		t.Fatalf("err = %v, want ErrAttachmentsRejected", err)
	}
}

func TestPostService_Create_BodyEmpty(t *testing.T) {
	owner := uuid.New()
	svc := NewPostService(&fakePostRepo{}, &fakePostMemberLookup{}, &fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	_, err := svc.Create(context.Background(), uuid.New(), owner, CreateInput{Body: "  "})
	if !errors.Is(err, ErrPostBodyEmpty) {
		t.Fatalf("err = %v, want ErrPostBodyEmpty", err)
	}
}

func TestPostService_Create_BodyTooLong(t *testing.T) {
	owner := uuid.New()
	long := make([]byte, 4097)
	for i := range long {
		long[i] = 'a'
	}
	svc := NewPostService(&fakePostRepo{}, &fakePostMemberLookup{}, &fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	_, err := svc.Create(context.Background(), uuid.New(), owner, CreateInput{Body: string(long)})
	if !errors.Is(err, ErrPostBodyTooLong) {
		t.Fatalf("err = %v, want ErrPostBodyTooLong", err)
	}
}

func TestPostService_Create_NonOwner_403(t *testing.T) {
	owner := uuid.New()
	caller := uuid.New()
	svc := NewPostService(&fakePostRepo{}, &fakePostMemberLookup{}, &fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	_, err := svc.Create(context.Background(), uuid.New(), caller, CreateInput{Body: "hi"})
	if !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("err = %v, want ErrNotChannelOwner", err)
	}
}

func TestPostService_Create_BadTier(t *testing.T) {
	owner := uuid.New()
	svc := NewPostService(&fakePostRepo{}, &fakePostMemberLookup{}, &fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	bad := "diamond"
	_, err := svc.Create(context.Background(), uuid.New(), owner, CreateInput{Body: "hi", TierID: &bad})
	if err == nil {
		t.Fatalf("expected error for bad tier id")
	}
}

func TestPostService_Create_HappyPath(t *testing.T) {
	owner := uuid.New()
	repo := &fakePostRepo{}
	svc := NewPostService(repo, &fakePostMemberLookup{}, &fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	tier := "vip"
	post, err := svc.Create(context.Background(), uuid.New(), owner, CreateInput{Body: "hello supporters", TierID: &tier})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if post == nil || post.Body != "hello supporters" {
		t.Fatalf("post body wrong: %+v", post)
	}
	if !repo.createCalled {
		t.Fatalf("repo.Create not called")
	}
	if repo.createdTier == nil || *repo.createdTier != "vip" {
		t.Fatalf("tier passed to repo wrong: %v", repo.createdTier)
	}
}

func TestPostService_Update_AttachmentsRejected(t *testing.T) {
	owner := uuid.New()
	svc := NewPostService(&fakePostRepo{}, &fakePostMemberLookup{}, &fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	body := "new"
	_, err := svc.Update(context.Background(), uuid.New(), uuid.New(), owner, UpdateInput{Body: &body, HasAttachmentsRaw: true})
	if !errors.Is(err, ErrAttachmentsRejected) {
		t.Fatalf("err = %v, want ErrAttachmentsRejected", err)
	}
}

func TestPostService_Update_NonOwner_403(t *testing.T) {
	owner := uuid.New()
	caller := uuid.New()
	svc := NewPostService(&fakePostRepo{}, &fakePostMemberLookup{}, &fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	body := "hi"
	_, err := svc.Update(context.Background(), uuid.New(), uuid.New(), caller, UpdateInput{Body: &body})
	if !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("err = %v, want ErrNotChannelOwner", err)
	}
}

func TestPostService_Update_ClearTier(t *testing.T) {
	owner := uuid.New()
	svc := NewPostService(&fakePostRepo{}, &fakePostMemberLookup{}, &fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	empty := ""
	post, err := svc.Update(context.Background(), uuid.New(), uuid.New(), owner, UpdateInput{TierID: &empty})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if post.TierID != nil {
		t.Fatalf("tier_id should be cleared, got %v", *post.TierID)
	}
}

func TestPostService_Delete_NonOwner_403(t *testing.T) {
	owner := uuid.New()
	caller := uuid.New()
	svc := NewPostService(&fakePostRepo{}, &fakePostMemberLookup{}, &fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	if err := svc.Delete(context.Background(), uuid.New(), uuid.New(), caller); !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("err = %v, want ErrNotChannelOwner", err)
	}
}

func TestPostService_Delete_HappyPath(t *testing.T) {
	owner := uuid.New()
	repo := &fakePostRepo{}
	svc := NewPostService(repo, &fakePostMemberLookup{}, &fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	if err := svc.Delete(context.Background(), uuid.New(), uuid.New(), owner); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !repo.deleteCalled {
		t.Fatalf("repo.Delete not called")
	}
}

func TestPostService_List_NonMember_GetsLockedStubs(t *testing.T) {
	owner := uuid.New()
	tier := "vip"
	repo := &fakePostRepo{posts: []domain.ChannelPost{
		{ID: uuid.New(), Body: "secret VIP post", TierID: &tier},
		{ID: uuid.New(), Body: "public post"}, // tier_id nil = public
	}}
	svc := NewPostService(repo, &fakePostMemberLookup{tier: ""},
		&fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	views, err := svc.List(context.Background(), uuid.New(), uuid.Nil, nil, 25)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("len = %d, want 2", len(views))
	}
	// Gated post: locked, body empty.
	if !views[0].Locked {
		t.Fatalf("vip post should be locked for non-member")
	}
	if views[0].Post.Body != "" {
		t.Fatalf("locked post body should be empty, got %q", views[0].Post.Body)
	}
	// Public post: not locked, body present.
	if views[1].Locked {
		t.Fatalf("public post should not be locked")
	}
	if views[1].Post.Body != "public post" {
		t.Fatalf("public post body wrong: %q", views[1].Post.Body)
	}
}

func TestPostService_List_Member_FullBody(t *testing.T) {
	owner := uuid.New()
	tier := "supporter"
	repo := &fakePostRepo{posts: []domain.ChannelPost{
		{ID: uuid.New(), Body: "supporter post", TierID: &tier},
	}}
	svc := NewPostService(repo, &fakePostMemberLookup{tier: "supporter"},
		&fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	views, err := svc.List(context.Background(), uuid.New(), uuid.New(), nil, 25)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if views[0].Locked {
		t.Fatalf("supporter member should see supporter post body")
	}
	if views[0].Post.Body != "supporter post" {
		t.Fatalf("body wrong: %q", views[0].Post.Body)
	}
}

func TestPostService_List_Owner_SeesAll(t *testing.T) {
	owner := uuid.New()
	tier := "elite"
	repo := &fakePostRepo{posts: []domain.ChannelPost{{Body: "elite secret", TierID: &tier}}}
	svc := NewPostService(repo, &fakePostMemberLookup{tier: ""},
		&fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	// Caller is the channel owner — should see body even without membership.
	views, err := svc.List(context.Background(), uuid.New(), owner, nil, 25)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if views[0].Locked {
		t.Fatalf("channel owner must always see body")
	}
}

// Compile-time guard.
var _ PostRepo = (*fakePostRepo)(nil)
var _ PostMembershipLookup = (*fakePostMemberLookup)(nil)
