package watchword

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
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
