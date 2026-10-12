package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestListUserVideos pins the profile Videos tab: an account's public videos
// from EVERY channel it owns, behind the profile's own gate, and never a
// private, unlisted, draft or moderator-blocked video.
func TestListUserVideos(t *testing.T) {
	srv, _, _, _, repo := videoServerFull(t, testConfig())
	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	if rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada2","display_name":"Ada Two"}`, ada); rec.Code != http.StatusCreated {
		t.Fatalf("second channel = %d", rec.Code)
	}
	ch1, _ := repo.channels.GetChannelByHandle(context.Background(), "ada")
	ch2, _ := repo.channels.GetChannelByHandle(context.Background(), "ada2")
	bob := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	chBob, _ := repo.channels.GetChannelByHandle(context.Background(), "bob")

	pub1 := seedVideo(repo, ch1, "Pub One", "", "public", "published", 30)
	pub2 := seedVideo(repo, ch2, "Pub Two", "", "public", "published", 10)
	seedVideo(repo, ch1, "Private", "", "private", "published", 5)
	seedVideo(repo, ch2, "Unlisted", "", "unlisted", "published", 5)
	seedVideo(repo, ch1, "Draft", "", "public", "draft", 5)
	seedVideo(repo, ch1, "Quarantined", "", "public", "quarantined", 5)
	blocked := seedVideo(repo, ch2, "Blocked", "", "public", "published", 5)
	seedVideo(repo, chBob, "Bobs", "", "public", "published", 5)
	if _, err := repo.blocks.BlockVideo(context.Background(), sqlcgen.BlockVideoParams{VideoID: blocked, Reason: "x"}); err != nil {
		t.Fatalf("block: %v", err)
	}

	// Private profile: same 404 as an unknown one, to everyone but the owner.
	for _, who := range []string{"", bob} {
		if rec := getWithAuth(srv, "/api/v1/users/ada/videos", who); rec.Code != http.StatusNotFound {
			t.Fatalf("private profile videos = %d, want 404", rec.Code)
		}
	}
	if rec := getWithAuth(srv, "/api/v1/users/nobody/videos", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown profile videos = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"profile_public":true}`, ada); rec.Code != http.StatusOK {
		t.Fatalf("publish profile = %d", rec.Code)
	}

	get := func(query, tok string) videoListResponse {
		t.Helper()
		rec := getWithAuth(srv, "/api/v1/users/ada/videos"+query, tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET videos = %d; body=%s", rec.Code, rec.Body.String())
		}
		var out videoListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}
	for name, tok := range map[string]string{"anonymous": "", "other user": bob, "owner": ada} {
		got := get("", tok)
		if got.Total != 2 || len(got.Videos) != 2 || got.Videos[0].ID != pub2.String() || got.Videos[1].ID != pub1.String() {
			t.Errorf("%s sees %d videos total %d, want exactly the two public ones across both channels, newest first", name, len(got.Videos), got.Total)
		}
	}
	page := get("?limit=1&offset=1", "")
	if len(page.Videos) != 1 || page.Total != 2 || page.Videos[0].ID != pub1.String() {
		t.Errorf("page 2 = %+v total %d, want the older public video of 2", page.Videos, page.Total)
	}
	if old := get("?sort=published_at", ""); old.Videos[0].ID != pub1.String() {
		t.Errorf("sort=published_at must list oldest first")
	}
}
