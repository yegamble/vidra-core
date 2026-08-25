package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/transcode"
)

type capableAdminTranscoder struct{}

func (capableAdminTranscoder) Transcode(context.Context, uuid.UUID, string) (media.HLSResult, error) {
	return media.HLSResult{}, nil
}

// adminVideosBody parses GET /admin/videos.
type adminVideosBody struct {
	Videos []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Privacy  string `json:"privacy"`
		State    string `json:"state"`
		Blocked  bool   `json:"blocked"`
		IsLocal  bool   `json:"is_local"`
		Original bool   `json:"has_original"`
		Size     int64  `json:"size_bytes"`
	} `json:"videos"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func TestAdminVideosOverview(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pub := createPublishedVideo(t, srv, admin, "ada", `{"title":"alpha video","privacy":"public"}`)
	draft := createVideo(t, srv, admin, "ada", `{"title":"beta draft","privacy":"private"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Block the published video.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+pub+"/block", `{"reason":"spam"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}

	parse := func(rec *httptest.ResponseRecorder) adminVideosBody {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("admin videos = %d; body=%s", rec.Code, rec.Body.String())
		}
		var body adminVideosBody
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body
	}

	// The admin sees ALL videos — including the private draft — with block status.
	body := parse(getWithAuth(srv, "/api/v1/admin/videos", admin))
	if len(body.Videos) != 2 {
		t.Fatalf("admin videos = %d, want 2 (public + private draft); body=%+v", len(body.Videos), body)
	}
	byID := map[string]struct {
		privacy, state string
		blocked        bool
	}{}
	for _, v := range body.Videos {
		byID[v.ID] = struct {
			privacy, state string
			blocked        bool
		}{v.Privacy, v.State, v.Blocked}
	}
	if p := byID[pub]; !p.blocked || p.privacy != "public" {
		t.Errorf("published video = %+v, want blocked/public", p)
	}
	for _, video := range body.Videos {
		if !video.IsLocal {
			t.Errorf("local inventory row %s reported as federated", video.ID)
		}
		if video.ID == pub && (!video.Original || video.Size <= 0) {
			t.Errorf("published file facts = original:%v size:%d, want retained original with size", video.Original, video.Size)
		}
	}
	if d := byID[draft]; d.blocked || d.privacy != "private" || d.state != "draft" {
		t.Errorf("draft video = %+v, want unblocked/private/draft", d)
	}

	// The q filter matches on title.
	if got := parse(getWithAuth(srv, "/api/v1/admin/videos?q=beta", admin)); len(got.Videos) != 1 || got.Videos[0].ID != draft {
		t.Errorf("q=beta = %+v, want only the draft", got.Videos)
	}

	// Regular users are forbidden; anonymous is unauthorized.
	if rec := getWithAuth(srv, "/api/v1/admin/videos", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-mod = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/videos", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon = %d, want 401", rec.Code)
	}
}

func TestAdminRunTranscodingQueuesRetainedOriginal(t *testing.T) {
	srv, _, jobs, _, videos := videoServerFull(t, testConfig())
	// The standard handler harness wires a read-only transcode service. Swap in
	// a capable one after route registration; handlers resolve this field at call
	// time, exactly as production does.
	srv.transcodesvc = transcode.NewService(jobs, capableAdminTranscoder{})
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	videoID := createPublishedVideo(t, srv, admin, "ada", `{"title":"Source quality","privacy":"public"}`)
	id := uuid.MustParse(videoID)
	original, err := videos.GetVideoFileByKind(context.Background(), sqlcgen.GetVideoFileByKindParams{VideoID: id, Kind: "original"})
	if err != nil {
		t.Fatalf("original: %v", err)
	}

	rec := postJSONAuth(srv, "/api/v1/admin/videos/"+videoID+"/transcoding", `{"type":"hls"}`, admin)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run HLS = %d; body=%s", rec.Code, rec.Body.String())
	}
	var queued *sqlcgen.TranscodeJob
	for _, job := range jobs.jobs {
		if job.VideoID == id {
			queued = job
		}
	}
	if queued == nil || queued.SourceKey != original.StorageKey || queued.TranscodeType != transcode.TargetHLS {
		t.Fatalf("queued job = %+v, want HLS sourced from retained original %q", queued, original.StorageKey)
	}

	// One live job per video prevents simultaneous replacement work.
	rec = postJSONAuth(srv, "/api/v1/admin/videos/"+videoID+"/transcoding", `{"type":"web_video"}`, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second live transcode = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if rec := postJSONAuth(srv, "/api/v1/admin/videos/"+videoID+"/transcoding", `{"type":"unknown"}`, admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid target = %d, want 422", rec.Code)
	}

	regular := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	if rec := postJSONAuth(srv, "/api/v1/admin/videos/"+videoID+"/transcoding", `{"type":"hls"}`, regular); rec.Code != http.StatusForbidden {
		t.Errorf("regular user = %d, want 403", rec.Code)
	}
}

// adminVideosPage is the full GET /admin/videos envelope, including the total
// this endpoint used not to have.
type adminVideosPage struct {
	Videos []struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Privacy string `json:"privacy"`
		State   string `json:"state"`
		Views   int64  `json:"views"`
		Local   bool   `json:"is_local"`
		HasOrig bool   `json:"has_original"`
	} `json:"videos"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

func adminVideosGet(t *testing.T, srv *Server, query, token string) adminVideosPage {
	t.Helper()
	rec := getWithAuth(srv, "/api/v1/admin/videos"+query, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/videos%s = %d; body=%s", query, rec.Code, rec.Body.String())
	}
	var out adminVideosPage
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func adminVideoTitles(p adminVideosPage) []string {
	out := make([]string, 0, len(p.Videos))
	for _, v := range p.Videos {
		out = append(out, v.Title)
	}
	return out
}

// TestAdminVideosTotalSurvivesTheClamp is the regression test for the reported
// bug: the admin page rendered items.length off a page clamped to the endpoint
// ceiling, so an instance with far more videos reported exactly the page size.
// The total must describe the whole matching set, not the slice returned.
func TestAdminVideosTotalSurvivesTheClamp(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	for i := 0; i < 7; i++ {
		createVideo(t, srv, admin, "ada", `{"title":"clip `+strconv.Itoa(i)+`","privacy":"private"}`)
	}

	page := adminVideosGet(t, srv, "?limit=3", admin)
	if len(page.Videos) != 3 {
		t.Fatalf("page size = %d, want 3", len(page.Videos))
	}
	if page.Total != 7 {
		t.Errorf("total = %d, want 7 — the total must count the matching set, not the page", page.Total)
	}
	if page.Limit != 3 || page.Offset != 0 {
		t.Errorf("limit/offset = %d/%d, want 3/0", page.Limit, page.Offset)
	}

	// The last page is short but still reports the same total, which is the only
	// way a client can tell "last page" from "there is more".
	last := adminVideosGet(t, srv, "?limit=3&offset=6", admin)
	if len(last.Videos) != 1 || last.Total != 7 {
		t.Errorf("last page = %d rows / total %d, want 1 / 7", len(last.Videos), last.Total)
	}

	// A filtered page reports ITS OWN total, not the instance total.
	filtered := adminVideosGet(t, srv, "?q="+url.QueryEscape("clip 3"), admin)
	if filtered.Total != 1 {
		t.Errorf("filtered total = %d, want 1 — a filtered total must count the filtered set", filtered.Total)
	}
}

// TestAdminVideosSort covers every supported ordering plus the default, which
// must reproduce the previous fixed newest-first behaviour.
func TestAdminVideosSort(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	// Created oldest→newest: alpha, bravo, charlie.
	createVideo(t, srv, admin, "ada", `{"title":"alpha","privacy":"private"}`)
	createVideo(t, srv, admin, "ada", `{"title":"bravo","privacy":"private"}`)
	createVideo(t, srv, admin, "ada", `{"title":"charlie","privacy":"private"}`)

	for _, tc := range []struct {
		name, query string
		want        []string
	}{
		{name: "default is newest first", query: "", want: []string{"charlie", "bravo", "alpha"}},
		{name: "-created_at is the default spelled out", query: "?sort=-created_at", want: []string{"charlie", "bravo", "alpha"}},
		{name: "created_at ascending", query: "?sort=created_at", want: []string{"alpha", "bravo", "charlie"}},
		// published_at is an ALIAS of created_at here: videos carry no separate
		// published_at column and the federated arm projects its own into it.
		{name: "published_at aliases created_at", query: "?sort=published_at", want: []string{"alpha", "bravo", "charlie"}},
		{name: "-published_at aliases -created_at", query: "?sort=-published_at", want: []string{"charlie", "bravo", "alpha"}},
		{name: "title ascending", query: "?sort=title", want: []string{"alpha", "bravo", "charlie"}},
		{name: "title descending", query: "?sort=-title", want: []string{"charlie", "bravo", "alpha"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := adminVideoTitles(adminVideosGet(t, srv, tc.query, admin))
			if len(got) != len(tc.want) {
				t.Fatalf("got %d rows, want %d (%v)", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("sort%s = %v, want %v", tc.query, got, tc.want)
				}
			}
		})
	}
}

// TestAdminVideosFilters exercises each filter and, critically, that an
// unrecognised value is a 400 rather than a silently ignored no-op.
func TestAdminVideosFilters(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	pub := createPublishedVideo(t, srv, admin, "ada", `{"title":"published one","privacy":"public"}`)
	createVideo(t, srv, admin, "ada", `{"title":"draft one","privacy":"private"}`)
	createVideo(t, srv, bobTok, "bob", `{"title":"bob draft","privacy":"unlisted"}`)

	t.Run("state filter is repeatable and comma-separated", func(t *testing.T) {
		byState := adminVideosGet(t, srv, "?state=published", admin)
		if byState.Total != 1 || byState.Videos[0].ID != pub {
			t.Fatalf("state=published = %+v, want only the published video", byState)
		}
		both := adminVideosGet(t, srv, "?state=published,draft", admin)
		if both.Total != 3 {
			t.Errorf("state=published,draft total = %d, want 3", both.Total)
		}
		repeated := adminVideosGet(t, srv, "?state=published&state=draft", admin)
		if repeated.Total != both.Total {
			t.Errorf("repeated ?state = %d, want the same %d as the CSV form", repeated.Total, both.Total)
		}
	})

	t.Run("privacy filter", func(t *testing.T) {
		if got := adminVideosGet(t, srv, "?privacy=unlisted", admin); got.Total != 1 {
			t.Errorf("privacy=unlisted total = %d, want 1", got.Total)
		}
	})

	t.Run("channel filter", func(t *testing.T) {
		if got := adminVideosGet(t, srv, "?channel=bob", admin); got.Total != 1 {
			t.Errorf("channel=bob total = %d, want 1", got.Total)
		}
		if got := adminVideosGet(t, srv, "?channel=nobody", admin); got.Total != 0 {
			t.Errorf("channel=nobody total = %d, want 0", got.Total)
		}
	})

	t.Run("scope filter", func(t *testing.T) {
		local := adminVideosGet(t, srv, "?scope=local", admin)
		if local.Total != 3 {
			t.Errorf("scope=local total = %d, want 3", local.Total)
		}
		// No federated rows exist in this harness, so remote is empty — and
		// scope defaults to all, matching the previous unfiltered behaviour.
		if remote := adminVideosGet(t, srv, "?scope=remote", admin); remote.Total != 0 {
			t.Errorf("scope=remote total = %d, want 0", remote.Total)
		}
		if all := adminVideosGet(t, srv, "?scope=all", admin); all.Total != local.Total {
			t.Errorf("scope=all total = %d, want %d", all.Total, local.Total)
		}
	})

	t.Run("has_original is tri-state", func(t *testing.T) {
		// Only the published video has an uploaded original.
		if got := adminVideosGet(t, srv, "?has_original=true", admin); got.Total != 1 {
			t.Errorf("has_original=true total = %d, want 1", got.Total)
		}
		// The point of tri-state: "no original" is expressible at all.
		if got := adminVideosGet(t, srv, "?has_original=false", admin); got.Total != 2 {
			t.Errorf("has_original=false total = %d, want 2", got.Total)
		}
		// Absent means ALL, not false.
		if got := adminVideosGet(t, srv, "", admin); got.Total != 3 {
			t.Errorf("no file filter total = %d, want 3 (absent must mean all)", got.Total)
		}
	})

	t.Run("published_after and published_before", func(t *testing.T) {
		future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		if got := adminVideosGet(t, srv, "?published_after="+url.QueryEscape(future), admin); got.Total != 0 {
			t.Errorf("published_after=future total = %d, want 0", got.Total)
		}
		if got := adminVideosGet(t, srv, "?published_after="+url.QueryEscape(past), admin); got.Total != 3 {
			t.Errorf("published_after=past total = %d, want 3", got.Total)
		}
		if got := adminVideosGet(t, srv, "?published_before="+url.QueryEscape(past), admin); got.Total != 0 {
			t.Errorf("published_before=past total = %d, want 0", got.Total)
		}
	})

	// Every rejected value below would previously have been silently ignored,
	// which is the failure mode this endpoint is being fixed for.
	t.Run("unrecognised values are rejected, not ignored", func(t *testing.T) {
		for _, q := range []string{
			"?sort=bogus",
			"?sort=-object_storage",
			"?state=nonsense",
			"?privacy=nonsense",
			"?scope=nonsense",
			"?has_original=maybe",
			"?has_hls=maybe",
			"?has_web_files=maybe",
			"?published_after=yesterday",
			"?published_after=2030-01-01T00:00:00Z&published_before=2020-01-01T00:00:00Z",
		} {
			if rec := getWithAuth(srv, "/api/v1/admin/videos"+q, admin); rec.Code != http.StatusBadRequest {
				t.Errorf("GET /admin/videos%s = %d, want 400", q, rec.Code)
			}
		}
	})
}
