package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/video"
)

// T26: the studio's account-wide "My videos" list (GET /me/videos) and the
// bulk endpoint (POST /me/videos/bulk).

func listMine(t *testing.T, srv *Server, tok string, q url.Values) (int, videoListResponse) {
	t.Helper()
	rec := doJSON(srv, http.MethodGet, "/api/v1/me/videos?"+q.Encode(), tok, "")
	var body videoListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func titles(vs []videoView) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Title)
	}
	return out
}

func TestListMyVideosFiltersCombine(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	createVideo(t, srv, tok, "ada", `{"title":"Holiday vlog","privacy":"public"}`)
	createVideo(t, srv, tok, "ada", `{"title":"Holiday notes","privacy":"private"}`)
	createVideo(t, srv, tok, "ada", `{"title":"100% real_talk","privacy":"public"}`)
	chRec := doJSON(srv, http.MethodPost, "/api/v1/channels", tok, `{"handle":"ada2","display_name":"Ada Two"}`)
	if chRec.Code != http.StatusCreated {
		t.Fatalf("second channel = %d; %s", chRec.Code, chRec.Body.String())
	}
	var ch2 channelView
	_ = json.Unmarshal(chRec.Body.Bytes(), &ch2)
	createVideo(t, srv, tok, "ada2", `{"title":"Holiday two","privacy":"public"}`)

	if code, all := listMine(t, srv, tok, url.Values{}); code != 200 || all.Total != 4 {
		t.Fatalf("unfiltered = %d total=%d, want 200/4", code, all.Total)
	}
	_, got := listMine(t, srv, tok, url.Values{"q": {"holiday"}})
	if got.Total != 3 {
		t.Errorf("q=holiday total = %d (%v), want 3 (case-insensitive substring)", got.Total, titles(got.Videos))
	}
	_, got = listMine(t, srv, tok, url.Values{"q": {"holiday"}, "privacy": {"public"}})
	if got.Total != 2 {
		t.Errorf("q+privacy total = %d (%v), want 2", got.Total, titles(got.Videos))
	}
	_, got = listMine(t, srv, tok, url.Values{"q": {"holiday"}, "privacy": {"public"}, "channel_id": {ch2.ID}})
	if got.Total != 1 || got.Videos[0].Title != "Holiday two" {
		t.Errorf("q+privacy+channel = %v, want [Holiday two]", titles(got.Videos))
	}
	_, got = listMine(t, srv, tok, url.Values{"state": {"draft"}, "privacy": {"private"}})
	if got.Total != 1 {
		t.Errorf("state+privacy total = %d, want 1", got.Total)
	}
	// LIKE wildcards in the search box are literal characters.
	_, got = listMine(t, srv, tok, url.Values{"q": {"100%"}})
	if got.Total != 1 {
		t.Errorf("q=100%% matched %v, want only the literal title", titles(got.Videos))
	}
	_, got = listMine(t, srv, tok, url.Values{"q": {"_"}})
	if got.Total != 1 {
		t.Errorf("q=_ matched %v, want only the literal underscore", titles(got.Videos))
	}
	// Paging reports the filtered total.
	_, got = listMine(t, srv, tok, url.Values{"q": {"holiday"}, "limit": {"1"}, "offset": {"2"}})
	if got.Total != 3 || len(got.Videos) != 1 {
		t.Errorf("paged = total %d len %d, want 3/1", got.Total, len(got.Videos))
	}
}

func TestListMyVideosValidationAndIsolation(t *testing.T) {
	srv := videoServer(t)
	adaTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	createVideo(t, srv, adaTok, "ada", `{"title":"ada video","privacy":"public"}`)
	createVideo(t, srv, bobTok, "bob", `{"title":"bob video","privacy":"public"}`)
	var bobCh channelView
	rec := doJSON(srv, http.MethodGet, "/api/v1/channels/bob", bobTok, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &bobCh)

	// Another user's channel_id yields nothing — never their data.
	code, got := listMine(t, srv, adaTok, url.Values{"channel_id": {bobCh.ID}})
	if code != 200 || got.Total != 0 || len(got.Videos) != 0 {
		t.Errorf("foreign channel_id = %d total=%d, want 200/0", code, got.Total)
	}
	// And the unfiltered list never contains it.
	if _, all := listMine(t, srv, adaTok, url.Values{}); all.Total != 1 || all.Videos[0].Title != "ada video" {
		t.Errorf("ada's list = %v, want only her own video", titles(all.Videos))
	}
	for name, q := range map[string]url.Values{
		"bad privacy":    {"privacy": {"secret"}},
		"bad state":      {"state": {"bogus"}},
		"bad channel_id": {"channel_id": {"not-a-uuid"}},
		"long q":         {"q": {string(bytes.Repeat([]byte("a"), 201))}},
	} {
		if r := doJSON(srv, http.MethodGet, "/api/v1/me/videos?"+q.Encode(), adaTok, ""); r.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, r.Code)
		}
	}
	// internal is a valid privacy filter value.
	if code, _ := listMine(t, srv, adaTok, url.Values{"privacy": {"internal"}}); code != 200 {
		t.Errorf("privacy=internal = %d, want 200", code)
	}
	if r := doJSON(srv, http.MethodGet, "/api/v1/me/videos", "", ""); r.Code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d, want 401", r.Code)
	}
}

type bulkResp struct {
	Results []struct {
		VideoID string `json:"video_id"`
		OK      bool   `json:"ok"`
		Status  int    `json:"status"`
		Error   string `json:"error"`
	} `json:"results"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

func bulk(srv *Server, tok string, body string) (*httptest.ResponseRecorder, bulkResp) {
	rec := doJSON(srv, http.MethodPost, "/api/v1/me/videos/bulk", tok, body)
	var out bulkResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func bulkBody(ids []string, action, privacy string) string {
	b, _ := json.Marshal(map[string]any{"video_ids": ids, "action": action, "privacy": privacy})
	return string(b)
}

func TestBulkForeignIDChangesNothing(t *testing.T) {
	srv := videoServer(t)
	adaTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	mine := createVideo(t, srv, adaTok, "ada", `{"title":"mine","privacy":"public"}`)
	theirs := createVideo(t, srv, bobTok, "bob", `{"title":"theirs","privacy":"public"}`)

	foreign, _ := bulk(srv, adaTok, bulkBody([]string{mine, theirs}, "delete", ""))
	unknown, _ := bulk(srv, adaTok, bulkBody([]string{mine, uuid.NewString()}, "delete", ""))
	if foreign.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound {
		t.Fatalf("foreign=%d unknown=%d, want 404/404", foreign.Code, unknown.Code)
	}
	// No existence oracle: a foreign id and a nonexistent id answer identically.
	strip := func(rec *httptest.ResponseRecorder) string {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		return e.Error.Code + "|" + e.Error.Message
	}
	if strip(foreign) != strip(unknown) {
		t.Errorf("foreign and unknown ids answer differently: %q vs %q", strip(foreign), strip(unknown))
	}
	if r := getVideo(srv, mine, adaTok); r.Code != http.StatusOK {
		t.Errorf("own video after refused bulk = %d, want untouched", r.Code)
	}
	if r := getVideo(srv, theirs, bobTok); r.Code != http.StatusOK {
		t.Errorf("foreign video after refused bulk = %d, want untouched", r.Code)
	}
	rec, _ := bulk(srv, adaTok, bulkBody([]string{mine, theirs}, "set_privacy", "private"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("set_privacy with foreign id = %d, want 404", rec.Code)
	}
	var v videoView
	_ = json.Unmarshal(getVideo(srv, mine, adaTok).Body.Bytes(), &v)
	if v.Privacy != "public" {
		t.Errorf("own video privacy = %q after refused bulk, want public", v.Privacy)
	}
}

func TestBulkDeleteFiresSingleDeleteSideEffects(t *testing.T) {
	var mu sync.Mutex
	var hooked []uuid.UUID
	srv := videoServerCfg(t, testConfig(), video.WithDeleteHook(func(_ context.Context, id, _ uuid.UUID, _ bool) {
		mu.Lock()
		hooked = append(hooked, id)
		mu.Unlock()
	}))
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	a := createVideo(t, srv, tok, "ada", `{"title":"a","privacy":"public"}`)
	b := createVideo(t, srv, tok, "ada", `{"title":"b","privacy":"private"}`)
	keep := createVideo(t, srv, tok, "ada", `{"title":"keep","privacy":"public"}`)

	rec, out := bulk(srv, tok, bulkBody([]string{a, b}, "delete", ""))
	if rec.Code != http.StatusOK || out.Succeeded != 2 || out.Failed != 0 || len(out.Results) != 2 {
		t.Fatalf("bulk delete = %d %+v; %s", rec.Code, out, rec.Body.String())
	}
	if len(hooked) != 2 {
		t.Errorf("delete hook (federation/search/storage) fired %d times, want 2", len(hooked))
	}
	n := 0
	for _, e := range auditEvents(t, &buf) {
		if e["action"] == observability.ActionVideoDelete && e["result"] == observability.ResultSuccess {
			n++
		}
	}
	if n != 2 {
		t.Errorf("video.delete audit events = %d, want 2 (one per video, as the single route)", n)
	}
	for _, id := range []string{a, b} {
		if r := getVideo(srv, id, tok); r.Code != http.StatusNotFound {
			t.Errorf("deleted %s = %d, want 404", id, r.Code)
		}
	}
	if r := getVideo(srv, keep, tok); r.Code != http.StatusOK {
		t.Errorf("untouched video = %d, want 200", r.Code)
	}
}

func TestBulkSetPrivacy(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	a := createVideo(t, srv, tok, "ada", `{"title":"a","privacy":"public"}`)
	b := createVideo(t, srv, tok, "ada", `{"title":"b","privacy":"public"}`)

	rec, out := bulk(srv, tok, bulkBody([]string{a, b}, "set_privacy", "internal"))
	if rec.Code != http.StatusOK || out.Succeeded != 2 {
		t.Fatalf("set_privacy internal = %d %+v; %s", rec.Code, out, rec.Body.String())
	}
	var v videoView
	_ = json.Unmarshal(getVideo(srv, a, tok).Body.Bytes(), &v)
	if v.Privacy != "internal" {
		t.Errorf("privacy = %q, want internal", v.Privacy)
	}
	// Same validation as the single PATCH: unknown value and a missing privacy.
	for _, body := range []string{bulkBody([]string{a}, "set_privacy", "secret"), bulkBody([]string{a}, "set_privacy", ""), bulkBody([]string{a}, "explode", "")} {
		if r, _ := bulk(srv, tok, body); r.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", body, r.Code)
		}
	}
	// privacy=password without a stored password is refused PER ITEM, exactly as
	// the single route refuses it (400); the request itself is still a 200.
	rec, out = bulk(srv, tok, bulkBody([]string{a, b}, "set_privacy", "password"))
	if rec.Code != http.StatusOK || out.Failed != 2 || out.Results[0].Status != http.StatusBadRequest || out.Results[0].OK {
		t.Errorf("password without password = %d %+v, want 200 with two 400 item failures", rec.Code, out)
	}
}

func TestBulkCapAndShape(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = uuid.NewString()
	}
	if r, _ := bulk(srv, tok, bulkBody(ids, "delete", "")); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("101 ids = %d, want 422", r.Code)
	}
	if r, _ := bulk(srv, tok, bulkBody(nil, "delete", "")); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("0 ids = %d, want 422", r.Code)
	}
	if r, _ := bulk(srv, tok, bulkBody([]string{"nope"}, "delete", "")); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("malformed id = %d, want 422", r.Code)
	}
	one := createVideo(t, srv, tok, "ada", `{"title":"x","privacy":"public"}`)
	if r, _ := bulk(srv, tok, bulkBody([]string{one, one}, "delete", "")); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("duplicate ids = %d, want 422", r.Code)
	}
	// Exactly 100 is within the cap: it gets past validation and is refused as
	// not-found (none of these ids exist), never as 422.
	if r, _ := bulk(srv, tok, bulkBody(ids[:100], "delete", "")); r.Code != http.StatusNotFound {
		t.Errorf("100 unknown ids = %d, want 404", r.Code)
	}
	if r := doJSON(srv, http.MethodPost, "/api/v1/me/videos/bulk", "", bulkBody([]string{one}, "delete", "")); r.Code != http.StatusUnauthorized {
		t.Errorf("anonymous bulk = %d, want 401", r.Code)
	}
}
