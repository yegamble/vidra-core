package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// --- videoFakeRepo chapter methods (mirror the video_chapters queries) ---

func (f *videoFakeRepo) ListVideoChapters(_ context.Context, videoID uuid.UUID) ([]sqlcgen.ListVideoChaptersRow, error) {
	rows := append([]sqlcgen.ListVideoChaptersRow(nil), f.chapters[videoID]...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].StartSeconds < rows[j].StartSeconds })
	return rows, nil
}

func (f *videoFakeRepo) VideoHasChapters(_ context.Context, videoID uuid.UUID) (bool, error) {
	return len(f.chapters[videoID]) > 0, nil
}

func (f *videoFakeRepo) DeleteVideoChapters(_ context.Context, videoID uuid.UUID) error {
	delete(f.chapters, videoID)
	return nil
}

func (f *videoFakeRepo) InsertVideoChapters(_ context.Context, a sqlcgen.InsertVideoChaptersParams) error {
	if f.chapters == nil {
		f.chapters = map[uuid.UUID][]sqlcgen.ListVideoChaptersRow{}
	}
	for i := range a.StartSeconds {
		f.chapters[a.VideoID] = append(f.chapters[a.VideoID], sqlcgen.ListVideoChaptersRow{
			StartSeconds: a.StartSeconds[i], Title: a.Title[i],
		})
	}
	return nil
}

// --- request helpers ---

func getChapters(srv *Server, id, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+"/chapters", nil)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func putChaptersNoAuth(srv *Server, id, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/"+id+"/chapters", strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeChapters(t *testing.T, rec *httptest.ResponseRecorder) []chapterView {
	t.Helper()
	var resp chaptersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode chapters: %v; body=%s", err, rec.Body.String())
	}
	return resp.Chapters
}

// --- tests ---

// TestChaptersGetEmpty proves a video with no chapters returns 200 with an empty
// (non-null) array.
func TestChaptersGetEmpty(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)

	rec := getChapters(srv, id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := decodeChapters(t, rec); len(got) != 0 {
		t.Fatalf("chapters = %+v, want empty", got)
	}
	if !strings.Contains(rec.Body.String(), `"chapters":[]`) {
		t.Fatalf("body = %s, want an empty JSON array", rec.Body.String())
	}
}

// TestChaptersPutAndGet proves the owner replaces the set, it reads back sorted,
// and the detail's has_chapters flag flips to true.
func TestChaptersPutAndGet(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)

	// Detail has_chapters is false before any chapters exist.
	if rec := getVideo(srv, id, tok); !strings.Contains(rec.Body.String(), `"has_chapters":false`) {
		t.Fatalf("detail before = %s, want has_chapters:false", rec.Body.String())
	}

	// Input must be strictly increasing (the server validates rather than sorts);
	// GET reads it back ascending from the store.
	rec := putJSONAuth(srv, "/api/v1/videos/"+id+"/chapters",
		`{"chapters":[{"start_seconds":0,"title":"Intro"},{"start_seconds":30,"title":"Body"},{"start_seconds":75,"title":"Outro"}]}`, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeChapters(t, rec)
	if len(got) != 3 || got[0].StartSeconds != 0 || got[0].Title != "Intro" || got[2].StartSeconds != 75 {
		t.Fatalf("put response = %+v", got)
	}

	// GET returns the same sorted set to anyone.
	got = decodeChapters(t, getChapters(srv, id, ""))
	if len(got) != 3 || got[1].StartSeconds != 30 || got[1].Title != "Body" {
		t.Fatalf("get = %+v", got)
	}

	if rec := getVideo(srv, id, tok); !strings.Contains(rec.Body.String(), `"has_chapters":true`) {
		t.Fatalf("detail after = %s, want has_chapters:true", rec.Body.String())
	}
}

// TestChaptersPutEmptyClears proves an empty array clears the set.
func TestChaptersPutEmptyClears(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)

	if rec := putJSONAuth(srv, "/api/v1/videos/"+id+"/chapters", `{"chapters":[{"start_seconds":0,"title":"Intro"}]}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("seed put = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := putJSONAuth(srv, "/api/v1/videos/"+id+"/chapters", `{"chapters":[]}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("clear put = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := decodeChapters(t, getChapters(srv, id, "")); len(got) != 0 {
		t.Fatalf("after clear = %+v, want empty", got)
	}
}

// TestChaptersPutRequiresAuth proves a token is required (401 without one).
func TestChaptersPutRequiresAuth(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)

	if rec := putChaptersNoAuth(srv, id, `{"chapters":[]}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-auth put = %d, want 401", rec.Code)
	}
}

// TestChaptersPutOwnerOnly proves a non-owner and an unknown id are both 404 (a
// private video's existence is never leaked).
func TestChaptersPutOwnerOnly(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"v","privacy":"public"}`)

	if rec := putJSONAuth(srv, "/api/v1/videos/"+id+"/chapters", `{"chapters":[{"start_seconds":0,"title":"x"}]}`, otherTok); rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner put = %d, want 404", rec.Code)
	}
	if rec := putJSONAuth(srv, "/api/v1/videos/"+uuid.New().String()+"/chapters", `{"chapters":[]}`, ownerTok); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown-id put = %d, want 404", rec.Code)
	}
}

// TestChaptersPutValidation proves each contract violation returns 400.
func TestChaptersPutValidation(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)

	overCap := `{"chapters":[`
	for i := 0; i <= video.MaxChapters; i++ { // 101 rows
		if i > 0 {
			overCap += ","
		}
		overCap += `{"start_seconds":` + strconv.Itoa(i) + `,"title":"c"}`
	}
	overCap += `]}`

	longTitle := `{"chapters":[{"start_seconds":0,"title":"` + strings.Repeat("x", video.MaxChapterTitleLen+1) + `"}]}`

	cases := []struct {
		name string
		body string
	}{
		{"non-ascending", `{"chapters":[{"start_seconds":50,"title":"a"},{"start_seconds":10,"title":"b"}]}`},
		{"duplicate start", `{"chapters":[{"start_seconds":0,"title":"a"},{"start_seconds":0,"title":"b"}]}`},
		{"negative start", `{"chapters":[{"start_seconds":-5,"title":"a"}]}`},
		{"empty title", `{"chapters":[{"start_seconds":0,"title":""}]}`},
		{"title too long", longTitle},
		{"over cap", overCap},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := putJSONAuth(srv, "/api/v1/videos/"+id+"/chapters", tc.body, tok)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
				t.Fatalf("body = %s, want bad_request code", rec.Body.String())
			}
		})
	}
}

// TestChaptersPutDurationClamp proves a start at/after the probed duration is
// rejected with 400, while a start inside it is accepted.
func TestChaptersPutDurationClamp(t *testing.T) {
	srv := videoServerCfg(t, testConfig(),
		video.WithProber(fakeProber{md: media.Metadata{DurationSeconds: 60, Width: 320, Height: 240}}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)

	if rec := putJSONAuth(srv, "/api/v1/videos/"+id+"/chapters", `{"chapters":[{"start_seconds":0,"title":"a"},{"start_seconds":59,"title":"b"}]}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("within duration = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec := putJSONAuth(srv, "/api/v1/videos/"+id+"/chapters", `{"chapters":[{"start_seconds":60,"title":"a"}]}`, tok); rec.Code != http.StatusBadRequest {
		t.Fatalf("at duration = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestChaptersGetVisibilityMatrix proves the read follows the detail's
// visibility: a private video's chapters are owner-only (404 to anon/others).
func TestChaptersGetVisibilityMatrix(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"v","privacy":"private"}`)
	if rec := putJSONAuth(srv, "/api/v1/videos/"+id+"/chapters", `{"chapters":[{"start_seconds":0,"title":"Intro"}]}`, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("owner seed = %d; body=%s", rec.Code, rec.Body.String())
	}

	if rec := getChapters(srv, id, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("owner get private = %d, want 200", rec.Code)
	}
	if rec := getChapters(srv, id, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("anon get private = %d, want 404", rec.Code)
	}
	if rec := getChapters(srv, id, otherTok); rec.Code != http.StatusNotFound {
		t.Fatalf("other get private = %d, want 404", rec.Code)
	}
	// A malformed id is 404, not 500.
	if rec := getChapters(srv, "not-a-uuid", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("malformed id = %d, want 404", rec.Code)
	}
}
