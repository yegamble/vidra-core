//go:build integration

// Integration test (T05): the PeerTube-facing facts — owning account, views,
// hashtags, caption languages — come out of the REAL SQL (GetVideoFederationFacts
// and the extended ListChannelOutboxVideos), and the dereferenced object and
// the outbox page render the same document from them.
package federation_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestVideoObjectPeerTubeFactsFromSQL(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{Username: "pt_" + suf, Email: "pt_" + suf + "@example.test", PasswordHash: "x", Role: "user"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	ch, err := q.CreateChannel(ctx, sqlcgen.CreateChannelParams{OwnerID: u.ID, Handle: "ptch" + suf, DisplayName: "Ch"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	cat, lang := "2", "en"
	v, err := q.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID: ch.ID, Title: "Facts from SQL", Description: "A real description.", Privacy: "public",
		Category: &cat, Language: &lang, IsSensitive: true, CommentsPolicy: "disabled", DownloadEnabled: true,
	})
	if err != nil {
		t.Fatalf("CreateVideo: %v", err)
	}
	if _, err := q.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: v.ID, State: "published"}); err != nil {
		t.Fatalf("SetVideoState: %v", err)
	}
	if err := q.InsertVideoTags(ctx, sqlcgen.InsertVideoTagsParams{VideoID: v.ID, Tags: []string{"kayak", "fjord"}}); err != nil {
		t.Fatalf("InsertVideoTags: %v", err)
	}
	for _, l := range []string{"fr", "en"} {
		if _, err := q.UpsertCaption(ctx, sqlcgen.UpsertCaptionParams{VideoID: v.ID, Language: l, StorageKey: "captions/" + l}); err != nil {
			t.Fatalf("UpsertCaption: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := q.IncrementVideoViews(ctx, v.ID); err != nil {
			t.Fatalf("IncrementVideoViews: %v", err)
		}
	}

	svc := federation.NewService(q, federation.WithBaseURL("https://videos.example"))
	obj, err := svc.VideoObject(ctx, v.ID)
	if err != nil {
		t.Fatalf("VideoObject: %v", err)
	}
	var got struct {
		UUID         string `json:"uuid"`
		Views        int64  `json:"views"`
		Sensitive    bool   `json:"sensitive"`
		Comments     int    `json:"commentsPolicy"`
		AttributedTo []struct{ Type, ID string }
		Tag          []struct{ Name string }
		Subtitles    []struct {
			Identifier string `json:"identifier"`
		} `json:"subtitleLanguage"`
	}
	raw, _ := json.Marshal(obj)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.UUID != v.ID.String() || got.Views != 3 || !got.Sensitive || got.Comments != 2 {
		t.Errorf("uuid/views/sensitive/commentsPolicy = %s/%d/%v/%d", got.UUID, got.Views, got.Sensitive, got.Comments)
	}
	if len(got.AttributedTo) != 2 || got.AttributedTo[1].Type != "Person" || got.AttributedTo[1].ID != "https://videos.example/accounts/"+u.Username {
		t.Errorf("attributedTo = %+v, want the channel Group then the owner Person", got.AttributedTo)
	}
	if len(got.Tag) != 2 || got.Tag[0].Name != "fjord" || got.Tag[1].Name != "kayak" {
		t.Errorf("tag = %+v, want fjord, kayak (sorted)", got.Tag)
	}
	if len(got.Subtitles) != 2 || got.Subtitles[0].Identifier != "en" || got.Subtitles[1].Identifier != "fr" {
		t.Errorf("subtitleLanguage = %+v, want en, fr", got.Subtitles)
	}

	// The walked outbox renders the SAME object from its own row.
	page, err := svc.ChannelOutboxPage(ctx, ch.Handle, 1)
	if err != nil || len(page.OrderedItems) != 1 {
		t.Fatalf("ChannelOutboxPage: %v (%d items)", err, len(page.OrderedItems))
	}
	delete(obj, "@context")
	want, _ := json.Marshal(obj)
	outbox, _ := json.Marshal(page.OrderedItems[0]["object"])
	if string(outbox) != string(want) {
		t.Errorf("outbox object differs from the dereferenced one\n outbox: %s\n object: %s", outbox, want)
	}
}
