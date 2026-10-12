package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The PeerTube "support" text (migration 0159): a markdown message on a channel
// and on a video. These tests pin the HTTP contract end to end — the channel
// PATCH, the length cap, and a new video inheriting its channel's text unless it
// states its own.

func patchChannelSupport(t *testing.T, srv *Server, tok, handle, support string) channelView {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"support": support})
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/channels/"+handle, string(body), tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch channel support = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var v channelView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	return v
}

func TestChannelSupportRoundTripsAndClears(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	const msg = "Support me on [Liberapay](https://liberapay.com/ada)"
	if got := patchChannelSupport(t, srv, tok, "ada", msg).Support; got != msg {
		t.Fatalf("patch response support = %q, want %q", got, msg)
	}
	var detail channelView
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodGet, "/api/v1/channels/ada", "", "").Body.Bytes(), &detail)
	if detail.Support != msg {
		t.Fatalf("public channel GET support = %q, want %q", detail.Support, msg)
	}
	// An empty string clears; the field disappears from the view (omitempty).
	if got := patchChannelSupport(t, srv, tok, "ada", "").Support; got != "" {
		t.Fatalf("support after clearing = %q, want empty", got)
	}
}

func TestSupportIsAnUpdatableFieldAndCapped(t *testing.T) {
	ok, long := "Thanks for watching", strings.Repeat("x", 1001)
	if fes := (updateChannelRequest{Support: &ok}).Validate(); len(fes) != 0 {
		t.Fatalf("channel support alone = %v, want accepted", fes)
	}
	if fes := (updateChannelRequest{Support: &long}).Validate(); len(fes) != 1 || fes[0].Field != "support" {
		t.Fatalf("channel support of 1001 chars = %v, want one support error", fes)
	}
	if fes := (updateVideoRequest{Support: &ok}).Validate(); len(fes) != 0 {
		t.Fatalf("video support alone = %v, want accepted", fes)
	}
	if fes := (updateVideoRequest{Support: &long}).Validate(); len(fes) != 1 || fes[0].Field != "support" {
		t.Fatalf("video support of 1001 chars = %v, want one support error", fes)
	}
	if fes := (createVideoRequest{Title: "t", Support: &long}).Validate(); len(fes) != 1 || fes[0].Field != "support" {
		t.Fatalf("create with 1001-char support = %v, want one support error", fes)
	}
	// 1000 multi-byte characters is within the limit: it counts characters.
	multi := strings.Repeat("é", 1000)
	if fes := (updateVideoRequest{Support: &multi}).Validate(); len(fes) != 0 {
		t.Fatalf("1000 multi-byte chars = %v, want accepted", fes)
	}
}

func TestNewVideoInheritsChannelSupportUnlessItStatesItsOwn(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	const chText = "Support the channel on Patreon"
	patchChannelSupport(t, srv, tok, "ada", chText)

	create := func(body string) videoView {
		t.Helper()
		rec := postJSONAuth(srv, "/api/v1/channels/ada/videos", body, tok)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create video = %d; body=%s", rec.Code, rec.Body.String())
		}
		var v videoView
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		return v
	}

	// Field omitted: the channel's text is copied.
	inherited := create(`{"title":"a","privacy":"private"}`)
	if inherited.Support == nil || *inherited.Support != chText {
		t.Fatalf("omitted support = %v, want inherited %q", inherited.Support, chText)
	}
	// Explicit text wins.
	own := create(`{"title":"b","privacy":"private","support":"Just for this one"}`)
	if own.Support == nil || *own.Support != "Just for this one" {
		t.Fatalf("explicit support = %v, want its own", own.Support)
	}
	// Explicit empty string means "none", not "inherit".
	none := create(`{"title":"c","privacy":"private","support":""}`)
	if none.Support != nil {
		t.Fatalf("explicit empty support = %q, want none", *none.Support)
	}

	// It is a snapshot: editing the channel afterwards leaves the video alone.
	patchChannelSupport(t, srv, tok, "ada", "changed later")
	var detail videoView
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+inherited.ID, "", tok).Body.Bytes(), &detail)
	if detail.Support == nil || *detail.Support != chText {
		t.Fatalf("detail support after channel edit = %v, want the snapshot %q", detail.Support, chText)
	}

	// PATCH sets then clears it on the video.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+inherited.ID, `{"support":"New text here"}`, tok)
	var patched videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if rec.Code != http.StatusOK || patched.Support == nil || *patched.Support != "New text here" {
		t.Fatalf("patch video support = %d %v", rec.Code, patched.Support)
	}
	rec = sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+inherited.ID, `{"support":""}`, tok)
	patched = videoView{}
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if rec.Code != http.StatusOK || patched.Support != nil {
		t.Fatalf("clearing video support = %d %v, want 200 and none", rec.Code, patched.Support)
	}
}
