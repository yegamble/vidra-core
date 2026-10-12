package video

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// channelSupportRepo is the unit fake plus a channel lookup that knows one
// channel's support text (the base fake knows no channels at all).
type channelSupportRepo struct {
	*fakeRepo
	channel sqlcgen.Channel
	err     error
}

func (r channelSupportRepo) GetChannelByID(_ context.Context, id uuid.UUID) (sqlcgen.Channel, error) {
	if r.err != nil {
		return sqlcgen.Channel{}, r.err
	}
	return r.channel, nil
}

func strp(s string) *string { return &s }

// A new video's support text defaults to its channel's (PeerTube behaviour),
// unless the creator states their own — including an explicit "none".
func TestCreateDraftSupportInheritance(t *testing.T) {
	chID := uuid.New()
	repo := channelSupportRepo{
		fakeRepo: newFakeRepo(uuid.New()),
		channel:  sqlcgen.Channel{ID: chID, Support: strp("Support me on Liberapay")},
	}
	svc := NewService(repo, nil)
	ctx := context.Background()

	cases := []struct {
		name string
		in   *string
		want *string
	}{
		{"omitted inherits the channel text", nil, strp("Support me on Liberapay")},
		{"explicit text wins", strp("Only for this video"), strp("Only for this video")},
		{"explicit empty string means none", strp(""), nil},
		{"whitespace-only means none", strp("   "), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := svc.CreateDraft(ctx, chID, CreateInput{Title: "T", Privacy: "private", Support: tc.in})
			if err != nil {
				t.Fatalf("CreateDraft: %v", err)
			}
			switch {
			case tc.want == nil && v.Support != nil:
				t.Fatalf("support = %q, want none", *v.Support)
			case tc.want != nil && (v.Support == nil || *v.Support != *tc.want):
				t.Fatalf("support = %v, want %q", v.Support, *tc.want)
			}
		})
	}
}

// A channel with no support text gives the video none, and a failed channel
// lookup must not block creating the video.
func TestCreateDraftSupportInheritanceIsBestEffort(t *testing.T) {
	ctx := context.Background()
	empty := NewService(channelSupportRepo{fakeRepo: newFakeRepo(uuid.New())}, nil)
	if v, err := empty.CreateDraft(ctx, uuid.New(), CreateInput{Title: "T", Privacy: "private"}); err != nil || v.Support != nil {
		t.Fatalf("channel without support: v.Support=%v err=%v, want none/nil", v.Support, err)
	}
	broken := NewService(channelSupportRepo{fakeRepo: newFakeRepo(uuid.New()), err: errors.New("db down")}, nil)
	if v, err := broken.CreateDraft(ctx, uuid.New(), CreateInput{Title: "T", Privacy: "private"}); err != nil || v.Support != nil {
		t.Fatalf("failed lookup: v.Support=%v err=%v, want the video created without it", v.Support, err)
	}
}
