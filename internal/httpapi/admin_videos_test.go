package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
