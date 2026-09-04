package federation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The AS Video object names a video TWICE, and the two must not be the same
// thing:
//
//   - `id` is its identity across the fediverse. Remote servers store it,
//     address Update/Delete to it, and thread replies against it. It is frozen
//     at the /videos/{uuid} form forever.
//   - `url` is the human landing page, and moves to /v/{code}.
//
// A change that moved `id` would re-identify every video on every remote server
// and orphan every existing reply, and nothing else in the suite would notice —
// the fakes carry no short code, so before this test the split was invisible.
func TestVideoObjectSplitsFrozenIdFromMovingURL(t *testing.T) {
	const code = "abcdefghijk"
	cid := uuid.New()
	vid := uuid.New()

	repo := fakeRepo{
		channels: map[string]sqlcgen.Channel{"films": {ID: cid, Handle: "films"}},
		outboxVideos: map[uuid.UUID][]sqlcgen.ListChannelOutboxVideosRow{
			cid: {{ID: vid, Title: "Clip", Description: "", ShortCode: code}},
		},
	}
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	pg, err := svc.ChannelOutboxPage(context.Background(), "films", 1)
	if err != nil {
		t.Fatalf("outbox page: %v", err)
	}
	if len(pg.OrderedItems) != 1 {
		t.Fatalf("items = %d, want 1", len(pg.OrderedItems))
	}
	raw, err := json.Marshal(pg.OrderedItems[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var item struct {
		Object struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"object"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	wantID := "https://videos.example/videos/" + vid.String()
	if item.Object.ID != wantID {
		t.Errorf("object id = %q, want the FROZEN %q — moving it re-identifies the video on every remote server", item.Object.ID, wantID)
	}
	wantURL := "https://videos.example/v/" + code
	if item.Object.URL != wantURL {
		t.Errorf("object url = %q, want %q", item.Object.URL, wantURL)
	}
	if item.Object.ID == item.Object.URL {
		t.Error("id and url are the same string; the split did not happen")
	}
}

// A video with no code still federates: the url falls back to the id form,
// which is what remote and older rows do.
func TestVideoObjectFallsBackToTheIDFormWithoutACode(t *testing.T) {
	cid := uuid.New()
	vid := uuid.New()
	repo := fakeRepo{
		channels: map[string]sqlcgen.Channel{"films": {ID: cid, Handle: "films"}},
		outboxVideos: map[uuid.UUID][]sqlcgen.ListChannelOutboxVideosRow{
			cid: {{ID: vid, Title: "Clip", Description: ""}},
		},
	}
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	pg, err := svc.ChannelOutboxPage(context.Background(), "films", 1)
	if err != nil {
		t.Fatalf("outbox page: %v", err)
	}
	raw, _ := json.Marshal(pg.OrderedItems[0])
	if !strings.Contains(string(raw), "https://videos.example/videos/"+vid.String()) {
		t.Errorf("expected the id form as the fallback url:\n%s", raw)
	}
	if strings.Contains(string(raw), "/v/") {
		t.Errorf("emitted a /v/ url for a video with no code:\n%s", raw)
	}
}
