package federation

// Comment @mentions over ActivityPub: outbound Notes carry Mention tags for the
// LOCAL accounts they name, and an inbound Note whose Mention tag targets one of
// our actors raises a comment_mention notification through the wired notifier.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type mentionTag struct {
	Type string `json:"type"`
	Href string `json:"href"`
	Name string `json:"name"`
}

func TestAnnounceCommentCarriesMentionTags(t *testing.T) {
	repo, c := newCommentOutboxRepo([]string{"https://a.example/inbox"})
	repo.usersByName = map[string]sqlcgen.GetUserActorByUsernameRow{"cara": {ID: uuid.New(), Username: "cara"}}
	c.Body = "hi @Cara, @ghost, @far@elsewhere.test and mail x@cara.test"
	repo.commentsByID[c.ID] = c
	svc := NewService(repo, WithBaseURL(noteBase))

	if err := svc.AnnounceComment(context.Background(), c.ID); err != nil {
		t.Fatalf("AnnounceComment: %v", err)
	}
	for _, d := range repo.deliveries {
		var a struct {
			Object struct {
				Tag []mentionTag `json:"tag"`
			} `json:"object"`
		}
		if err := json.Unmarshal(d.row.Payload, &a); err != nil {
			t.Fatal(err)
		}
		want := mentionTag{Type: "Mention", Href: noteBase + "/accounts/cara", Name: "@cara@videos.example"}
		if len(a.Object.Tag) != 1 || a.Object.Tag[0] != want {
			t.Fatalf("tags = %+v, want exactly [%+v] (unknown, remote and email names get none)", a.Object.Tag, want)
		}
	}

	// The dereferenceable Note carries the same tag.
	note, err := svc.NoteObject(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("NoteObject: %v", err)
	}
	if tags, _ := note["tag"].([]map[string]any); len(tags) != 1 || tags[0]["href"] != noteBase+"/accounts/cara" {
		t.Fatalf("NoteObject tag = %#v", note["tag"])
	}
}

func TestAnnounceCommentWithoutMentionsHasNoTagKey(t *testing.T) {
	repo, c := newCommentOutboxRepo([]string{"https://a.example/inbox"})
	svc := NewService(repo, WithBaseURL(noteBase))
	if err := svc.AnnounceComment(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	for _, d := range repo.deliveries {
		var raw struct {
			Object map[string]json.RawMessage `json:"object"`
		}
		_ = json.Unmarshal(d.row.Payload, &raw)
		if _, has := raw.Object["tag"]; has {
			t.Fatalf("a comment with no mentions must not carry a tag key: %s", d.row.Payload)
		}
	}
}

type mentionCall struct {
	comment uuid.UUID
	names   []string
}

func inboundNoteWithTag(videoID uuid.UUID, tag string) string {
	return `{"id":"https://remote.example/act/m1","type":"Create","actor":"` + remoteBob + `","object":{` +
		`"id":"https://remote.example/comments/m1","type":"Note","attributedTo":"` + remoteBob + `",` +
		`"inReplyTo":"` + noteBase + "/videos/" + videoID.String() + `","content":"hello @ada",` +
		`"tag":` + tag + `}}`
}

func TestInboundMentionTagNotifiesLocalUsers(t *testing.T) {
	cases := []struct {
		name string
		tag  string
		want []string
	}{
		{"local actor", `[{"type":"Mention","href":"` + noteBase + `/accounts/Ada","name":"@ada@videos.example"}]`, []string{"ada"}},
		{"single object form", `{"type":"Mention","href":"` + noteBase + `/accounts/ada"}`, []string{"ada"}},
		{"remote actor ignored", `[{"type":"Mention","href":"https://remote.example/accounts/zed"}]`, nil},
		{"other tag types ignored", `[{"type":"Hashtag","href":"` + noteBase + `/accounts/ada"}]`, nil},
		{"path tricks ignored", `[{"type":"Mention","href":"` + noteBase + `/accounts/ada/outbox"},{"type":"Mention","href":"` + noteBase + `/accounts/"}]`, nil},
		{"malformed", `"oops"`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			videoID := uuid.New()
			repo := newNotesRepo(videoID)
			var calls []mentionCall
			svc := NewService(repo, WithBaseURL(noteBase),
				WithCommentMentionNotifier(func(_ context.Context, id uuid.UUID, names []string) (int64, error) {
					calls = append(calls, mentionCall{id, names})
					return 1, nil
				}))
			if err := svc.HandleInbox(context.Background(), remoteBob, []byte(inboundNoteWithTag(videoID, tc.tag))); err != nil {
				t.Fatalf("HandleInbox: %v", err)
			}
			if tc.want == nil {
				if len(calls) != 0 {
					t.Fatalf("notifier ran for %+v", calls)
				}
				return
			}
			c := onlyComment(t, repo)
			if len(calls) != 1 || calls[0].comment != c.ID || len(calls[0].names) != 1 || calls[0].names[0] != tc.want[0] {
				t.Fatalf("calls = %+v, want one for comment %s naming %v", calls, c.ID, tc.want)
			}
		})
	}
}

// A notifier failure is best-effort: the comment is still stored and the
// activity still succeeds.
func TestInboundMentionNotifierFailureDoesNotFailIngest(t *testing.T) {
	videoID := uuid.New()
	repo := newNotesRepo(videoID)
	svc := NewService(repo, WithBaseURL(noteBase),
		WithCommentMentionNotifier(func(context.Context, uuid.UUID, []string) (int64, error) { return 0, errors.New("db down") }))
	tag := `[{"type":"Mention","href":"` + noteBase + `/accounts/ada"}]`
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(inboundNoteWithTag(videoID, tag))); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	_ = onlyComment(t, repo)
}
