package watchword

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestAutomaticTags(t *testing.T) {
	own := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	cases := []struct {
		name string
		text string
		ms   []matched
		want []string
	}{
		{"plain", "hello world", nil, nil},
		{"https url", "see https://example.com/a", nil, []string{TagExternalLink}},
		{"www url, any case", "visit WWW.Example.com", nil, []string{TagExternalLink}},
		{"a bare domain is not a link", "example.com", nil, nil},
		{"instance list name, deduped and sorted", "x", []matched{{ListName: "Spam"}, {ListName: "Abuse"}, {ListName: "Spam"}}, []string{"Abuse", "Spam"}},
		{"a creator's private list yields no tag", "x", []matched{{ListName: "mine", ListOwner: own}}, nil},
		{"url plus list", "http://a.b", []matched{{ListName: "Spam"}}, []string{"Spam", TagExternalLink}},
	}
	for _, tc := range cases {
		if got := AutomaticTags(tc.text, tc.ms); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

type failingTagRepo struct{ *fakeRepo }

func (failingTagRepo) ReplaceCommentAutomaticTags(context.Context, sqlcgen.ReplaceCommentAutomaticTagsParams) error {
	return errors.New("tag table down")
}

// A tag-table failure must not suppress the match rows the review queue needs.
func TestFlagCommentSurvivesATagWriteFailure(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := NewService(failingTagRepo{repo})
	if _, err := svc.Add(ctx, "badword", uuid.New()); err != nil {
		t.Fatal(err)
	}
	n, err := svc.FlagComment(ctx, uuid.New(), "a badword here")
	if err != nil || n != 1 || len(repo.matches) != 1 {
		t.Fatalf("n=%d err=%v matches=%d, want 1 match recorded and no error", n, err, len(repo.matches))
	}
}
