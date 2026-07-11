package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// transcodeFakeRepo is an in-memory transcode.Repository with working queue
// semantics (dedupe, due filtering, state transitions), so the end-to-end
// integration test can run the real transcode.Service.DrainJobs against it.
type transcodeFakeRepo struct {
	jobs       map[uuid.UUID]*sqlcgen.TranscodeJob
	playlists  map[uuid.UUID]sqlcgen.StreamingPlaylist
	renditions map[uuid.UUID][]sqlcgen.VideoRendition
}

func newTranscodeFakeRepo() *transcodeFakeRepo {
	return &transcodeFakeRepo{
		jobs:       map[uuid.UUID]*sqlcgen.TranscodeJob{},
		playlists:  map[uuid.UUID]sqlcgen.StreamingPlaylist{},
		renditions: map[uuid.UUID][]sqlcgen.VideoRendition{},
	}
}

func (f *transcodeFakeRepo) EnqueueTranscodeJob(_ context.Context, a sqlcgen.EnqueueTranscodeJobParams) error {
	for _, j := range f.jobs {
		if j.VideoID == a.VideoID && (j.State == "pending" || j.State == "running") {
			return nil // partial unique index + ON CONFLICT DO NOTHING
		}
	}
	id := uuid.New()
	f.jobs[id] = &sqlcgen.TranscodeJob{
		ID: id, VideoID: a.VideoID, SourceKey: a.SourceKey,
		State: "pending", NextAttemptAt: time.Now().Add(-time.Second),
	}
	return nil
}

func (f *transcodeFakeRepo) ClaimDueTranscodeJobs(_ context.Context, limit int32) ([]sqlcgen.ClaimDueTranscodeJobsRow, error) {
	var rows []sqlcgen.ClaimDueTranscodeJobsRow
	for _, j := range f.jobs {
		if int32(len(rows)) >= limit {
			break
		}
		if j.State == "pending" && !j.NextAttemptAt.After(time.Now()) {
			j.State = "running"
			rows = append(rows, sqlcgen.ClaimDueTranscodeJobsRow{
				ID: j.ID, VideoID: j.VideoID, SourceKey: j.SourceKey, Attempts: j.Attempts,
			})
		}
	}
	return rows, nil
}

func (f *transcodeFakeRepo) CompleteTranscodeJob(_ context.Context, id uuid.UUID) error {
	f.jobs[id].State = "done"
	return nil
}

func (f *transcodeFakeRepo) RescheduleTranscodeJob(_ context.Context, a sqlcgen.RescheduleTranscodeJobParams) error {
	j := f.jobs[a.ID]
	j.State = "pending"
	j.Attempts++
	j.NextAttemptAt = a.NextAttemptAt
	j.LastError = a.LastError
	return nil
}

func (f *transcodeFakeRepo) FailTranscodeJob(_ context.Context, a sqlcgen.FailTranscodeJobParams) error {
	j := f.jobs[a.ID]
	j.State = "failed"
	j.Attempts++
	j.LastError = a.LastError
	return nil
}

func (f *transcodeFakeRepo) UpsertStreamingPlaylist(_ context.Context, a sqlcgen.UpsertStreamingPlaylistParams) (sqlcgen.StreamingPlaylist, error) {
	sp := sqlcgen.StreamingPlaylist{VideoID: a.VideoID, MasterKey: a.MasterKey, State: a.State}
	f.playlists[a.VideoID] = sp
	return sp, nil
}

func (f *transcodeFakeRepo) GetStreamingPlaylist(_ context.Context, videoID uuid.UUID) (sqlcgen.StreamingPlaylist, error) {
	sp, ok := f.playlists[videoID]
	if !ok {
		return sqlcgen.StreamingPlaylist{}, errors.New("no rows")
	}
	return sp, nil
}

func (f *transcodeFakeRepo) CreateVideoRendition(_ context.Context, a sqlcgen.CreateVideoRenditionParams) (sqlcgen.VideoRendition, error) {
	r := sqlcgen.VideoRendition{ID: uuid.New(), VideoID: a.VideoID, Height: a.Height, Width: a.Width, KeyPrefix: a.KeyPrefix}
	f.renditions[a.VideoID] = append(f.renditions[a.VideoID], r)
	return r, nil
}

func (f *transcodeFakeRepo) DeleteVideoRenditions(_ context.Context, videoID uuid.UUID) error {
	delete(f.renditions, videoID)
	return nil
}

func (f *transcodeFakeRepo) ListVideoRenditions(_ context.Context, videoID uuid.UUID) ([]sqlcgen.VideoRendition, error) {
	return f.renditions[videoID], nil
}

func (f *transcodeFakeRepo) CreateVideoFile(_ context.Context, a sqlcgen.CreateVideoFileParams) (sqlcgen.VideoFile, error) {
	return sqlcgen.VideoFile{
		ID: uuid.New(), VideoID: a.VideoID, Kind: a.Kind, StorageKey: a.StorageKey,
		ContentType: a.ContentType, OriginalName: a.OriginalName, SizeBytes: a.SizeBytes,
	}, nil
}

func (f *transcodeFakeRepo) DeleteVideoFilesByVideoAndKind(_ context.Context, _ sqlcgen.DeleteVideoFilesByVideoAndKindParams) error {
	return nil
}

// seedReadyHLS marks videoID's playlist ready with one 240p rendition and
// writes the master playlist, variant playlist, and one segment into blobs
// under the canonical key layout.
func seedReadyHLS(t *testing.T, repo *transcodeFakeRepo, blobs storage.Backend, videoID string) {
	t.Helper()
	id := uuid.MustParse(videoID)
	prefix := "streaming-playlists/" + videoID
	repo.playlists[id] = sqlcgen.StreamingPlaylist{VideoID: id, MasterKey: prefix + "/master.m3u8", State: "ready"}
	repo.renditions[id] = []sqlcgen.VideoRendition{
		{ID: uuid.New(), VideoID: id, Height: 240, Width: 320, KeyPrefix: prefix + "/240p"},
	}
	put := func(key, content string) {
		if _, err := blobs.Put(context.Background(), key, strings.NewReader(content)); err != nil {
			t.Fatalf("Put %q: %v", key, err)
		}
	}
	put(prefix+"/master.m3u8", "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-STREAM-INF:BANDWIDTH=985600,RESOLUTION=320x240\n240p/playlist.m3u8\n")
	put(prefix+"/240p/playlist.m3u8", "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXTINF:2.0,\nseg_00000.ts\n#EXT-X-ENDLIST\n")
	put(prefix+"/240p/seg_00000.ts", "fake-ts-bytes")
	put(media.HLSDownloadKey(prefix+"/240p", true), "fake-muxed-mp4")
	put(media.HLSDownloadKey(prefix+"/240p", false), "fake-video-only-mp4")
	put(media.HLSAudioDownloadKey(prefix+"/master.m3u8"), "fake-audio-m4a")
}

// seedReadyPeerTubeHLS marks videoID ready with a PeerTube object-storage HLS
// tree: one flat directory under streaming-playlists/hls/<source-video-uuid>/.
func seedReadyPeerTubeHLS(t *testing.T, repo *transcodeFakeRepo, blobs storage.Backend, videoID string) {
	t.Helper()
	id := uuid.MustParse(videoID)
	prefix := "streaming-playlists/hls/11111111-1111-1111-1111-111111111111"
	repo.playlists[id] = sqlcgen.StreamingPlaylist{VideoID: id, MasterKey: prefix + "/v1-master.m3u8", State: "ready"}
	put := func(key, content string) {
		if _, err := blobs.Put(context.Background(), key, strings.NewReader(content)); err != nil {
			t.Fatalf("Put %q: %v", key, err)
		}
	}
	put(prefix+"/v1-master.m3u8", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=1280x720\nv1-720.m3u8\n")
	put(prefix+"/v1-720.m3u8", "#EXTM3U\n#EXT-X-MAP:URI=\"v1-init.mp4\"\n#EXTINF:4.0,\nv1-720-fragmented.mp4\n#EXT-X-ENDLIST\n")
	put(prefix+"/v1-init.mp4", "fake-init")
	put(prefix+"/v1-720-fragmented.mp4", "fake-fmp4")
}

// seedReadyLadder marks videoID's playlist ready with a MULTI-rung ladder
// (720p/480p/360p) and writes a multi-variant master playlist plus each
// variant's playlist into blobs. This is the regression fixture for a real HD
// upload: the tiny-source path (seedReadyHLS) only ever exercises a single
// rendition, so nothing else proves the multi-resolution ladder is exposed.
func seedReadyLadder(t *testing.T, repo *transcodeFakeRepo, blobs storage.Backend, videoID string) {
	t.Helper()
	id := uuid.MustParse(videoID)
	prefix := "streaming-playlists/" + videoID
	repo.playlists[id] = sqlcgen.StreamingPlaylist{VideoID: id, MasterKey: prefix + "/master.m3u8", State: "ready"}
	// Stored tallest-first (matches ListVideoRenditions' ORDER BY height DESC).
	rungs := []struct {
		h, w int
		bw   int
	}{{720, 1280, 3220800}, {480, 854, 1680800}, {360, 640, 985600}}
	var master strings.Builder
	master.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	for _, r := range rungs {
		repo.renditions[id] = append(repo.renditions[id], sqlcgen.VideoRendition{
			ID: uuid.New(), VideoID: id, Height: int32(r.h), Width: int32(r.w),
			KeyPrefix: prefix + "/" + itoa(r.h) + "p",
		})
		master.WriteString("#EXT-X-STREAM-INF:BANDWIDTH=" + itoa(r.bw) + ",RESOLUTION=" + itoa(r.w) + "x" + itoa(r.h) + "\n")
		master.WriteString(itoa(r.h) + "p/playlist.m3u8\n")
		put := func(key, content string) {
			if _, err := blobs.Put(context.Background(), key, strings.NewReader(content)); err != nil {
				t.Fatalf("Put %q: %v", key, err)
			}
		}
		put(prefix+"/"+itoa(r.h)+"p/playlist.m3u8",
			"#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXTINF:2.0,\nseg_00000.ts\n#EXT-X-ENDLIST\n")
		put(prefix+"/"+itoa(r.h)+"p/seg_00000.ts", "fake-ts-bytes")
		put(media.HLSDownloadKey(prefix+"/"+itoa(r.h)+"p", true), "fake-muxed-mp4")
		put(media.HLSDownloadKey(prefix+"/"+itoa(r.h)+"p", false), "fake-video-only-mp4")
	}
	put := func(key, content string) {
		if _, err := blobs.Put(context.Background(), key, strings.NewReader(content)); err != nil {
			t.Fatalf("Put %q: %v", key, err)
		}
	}
	put(media.HLSAudioDownloadKey(prefix+"/master.m3u8"), "fake-audio-m4a")
	if _, err := blobs.Put(context.Background(), prefix+"/master.m3u8", strings.NewReader(master.String())); err != nil {
		t.Fatalf("Put master: %v", err)
	}
}

// itoa is a tiny local strconv.Itoa alias to keep the fixture readable.
func itoa(n int) string { return strconv.Itoa(n) }

func getHLS(srv *Server, path, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestHLSServesMasterVariantAndSegment(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)
	seedReadyHLS(t, tcRepo, blobs, id)

	rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/master.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("master = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("master Content-Type = %q, want application/vnd.apple.mpegurl", ct)
	}
	if !strings.Contains(rec.Body.String(), "240p/playlist.m3u8") {
		t.Errorf("master should reference the variant relatively:\n%s", rec.Body.String())
	}

	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/240p/playlist.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("variant = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("variant Content-Type = %q, want application/vnd.apple.mpegurl", ct)
	}
	if !strings.Contains(rec.Body.String(), "seg_00000.ts") {
		t.Errorf("variant should list its segment:\n%s", rec.Body.String())
	}

	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("segment = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp2t" {
		t.Errorf("segment Content-Type = %q, want video/mp2t", ct)
	}
	if rec.Body.String() != "fake-ts-bytes" {
		t.Errorf("segment bytes = %q", rec.Body.String())
	}
}

func TestHLSServesReferencedPeerTubeFlatFMP4Tree(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"PeerTube HLS","privacy":"public"}`)
	seedReadyPeerTubeHLS(t, tcRepo, blobs, id)

	rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/master.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("master = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\npeertube/v1-720.m3u8\n") {
		t.Fatalf("master should route PeerTube flat variant through peertube/:\n%s", rec.Body.String())
	}

	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/peertube/v1-720.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("variant = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `URI="v1-init.mp4"`) || !strings.Contains(rec.Body.String(), "v1-720-fragmented.mp4") {
		t.Fatalf("variant should keep flat relative media references:\n%s", rec.Body.String())
	}

	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/peertube/v1-720-fragmented.mp4", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("fmp4 = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("fmp4 Content-Type = %q, want video/mp4", ct)
	}
	if rec.Body.String() != "fake-fmp4" {
		t.Errorf("fmp4 bytes = %q", rec.Body.String())
	}

	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/master.m3u8?pt=secret", "")
	if !strings.Contains(rec.Body.String(), "peertube/v1-720.m3u8?pt=secret") {
		t.Fatalf("master should propagate playback token into rewritten URI:\n%s", rec.Body.String())
	}
	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/peertube/v1-720.m3u8?pt=secret", "")
	if !strings.Contains(rec.Body.String(), `URI="v1-init.mp4?pt=secret"`) ||
		!strings.Contains(rec.Body.String(), "v1-720-fragmented.mp4?pt=secret") {
		t.Fatalf("variant should propagate playback token to URI attrs and media lines:\n%s", rec.Body.String())
	}
}

// TestHLSMultiRenditionLadderExposed is the regression guard for the reported
// bug ("can't switch resolutions during playback"): a real HD upload must
// expose MULTIPLE renditions on the detail response AND serve a master playlist
// with multiple distinct variant streams, which is exactly what the player's
// quality selector consumes. A single-rendition fixture (seedReadyHLS) can pass
// while multi-resolution switching is silently broken, so this asserts the
// multi-variant shape end-to-end through the API.
func TestHLSMultiRenditionLadderExposed(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"HD","privacy":"public"}`)
	seedReadyLadder(t, tcRepo, blobs, id)

	// Detail advertises every rung, tallest-first, alongside the master URL.
	var v videoView
	rec := getVideo(srv, id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail = %d", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	wantURL := "/api/v1/videos/" + id + "/hls/master.m3u8"
	if v.HLSURL == nil || *v.HLSURL != wantURL {
		t.Fatalf("hls_url = %v, want %q", v.HLSURL, wantURL)
	}
	gotHeights := make([]int32, 0, len(v.Renditions))
	for _, r := range v.Renditions {
		gotHeights = append(gotHeights, r.Height)
	}
	wantHeights := []int32{720, 480, 360}
	if len(gotHeights) != len(wantHeights) {
		t.Fatalf("detail renditions = %+v, want 3 (720/480/360)", v.Renditions)
	}
	for i, h := range wantHeights {
		if gotHeights[i] != h {
			t.Errorf("rendition[%d].height = %d, want %d (tallest-first)", i, gotHeights[i], h)
		}
	}

	// The served master playlist is genuinely multivariant — the player parses
	// these #EXT-X-STREAM-INF entries into its selectable quality levels. Assert
	// the full contract the quality selector consumes (W1.C0 checklist item 1a):
	// one STREAM-INF per rung, each with a DISTINCT RESOLUTION and BANDWIDTH, and
	// a RELATIVE variant URI whose set equals the detail's tallest-first rungs.
	master := getHLS(srv, wantURL, "").Body.String()
	if n := strings.Count(master, "#EXT-X-STREAM-INF"); n != 3 {
		t.Fatalf("master has %d variant streams, want 3:\n%s", n, master)
	}
	for _, want := range []string{"RESOLUTION=1280x720", "RESOLUTION=854x480", "RESOLUTION=640x360"} {
		if !strings.Contains(master, want) {
			t.Errorf("master missing %q:\n%s", want, master)
		}
	}
	// Distinct BANDWIDTH per rung — an ABR client collapses two rungs advertised
	// at the same bitrate, so the ladder must carry three distinct values.
	bwMatches := regexp.MustCompile(`BANDWIDTH=(\d+)`).FindAllStringSubmatch(master, -1)
	if len(bwMatches) != 3 {
		t.Fatalf("master has %d BANDWIDTH attrs, want 3:\n%s", len(bwMatches), master)
	}
	seenBW := map[string]bool{}
	for _, m := range bwMatches {
		if seenBW[m[1]] {
			t.Errorf("duplicate BANDWIDTH=%s in master (rungs must be distinct):\n%s", m[1], master)
		}
		seenBW[m[1]] = true
	}
	// Variant URIs are RELATIVE ("720p/playlist.m3u8"), match the detail's rungs
	// exactly, and carry no absolute/api-prefixed path — native HLS resolves them
	// against the master's own URL.
	for _, h := range wantHeights {
		if uri := strconv.Itoa(int(h)) + "p/playlist.m3u8"; !strings.Contains(master, "\n"+uri) {
			t.Errorf("master missing relative variant URI %q:\n%s", uri, master)
		}
	}
	if strings.Contains(master, "/api/v1/") || strings.Contains(master, "http://") || strings.Contains(master, "https://") {
		t.Errorf("master variant URIs must be relative, found an absolute reference:\n%s", master)
	}
	// Every advertised rendition's variant playlist AND its first segment serve
	// 200 (W1.C0 checklist item 1c — players resolve both relatively).
	for _, h := range wantHeights {
		base := "/api/v1/videos/" + id + "/hls/" + strconv.Itoa(int(h)) + "p/"
		if rec := getHLS(srv, base+"playlist.m3u8", ""); rec.Code != http.StatusOK {
			t.Errorf("variant GET %splaylist.m3u8 = %d, want 200", base, rec.Code)
		}
		if rec := getHLS(srv, base+"seg_00000.ts", ""); rec.Code != http.StatusOK {
			t.Errorf("first segment GET %sseg_00000.ts = %d, want 200", base, rec.Code)
		}
	}
}

func TestHLSNotReadyIs404(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	// No playlist row at all → 404.
	if rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/master.m3u8", ""); rec.Code != http.StatusNotFound {
		t.Errorf("no playlist = %d, want 404", rec.Code)
	}
	// A pending (not ready) playlist is still 404, even with objects in storage.
	seedReadyHLS(t, tcRepo, blobs, id)
	vid := uuid.MustParse(id)
	sp := tcRepo.playlists[vid]
	sp.State = "pending"
	tcRepo.playlists[vid] = sp
	for _, p := range []string{"/hls/master.m3u8", "/hls/240p/playlist.m3u8", "/hls/240p/seg_00000.ts"} {
		if rec := getHLS(srv, "/api/v1/videos/"+id+p, ""); rec.Code != http.StatusNotFound {
			t.Errorf("pending playlist GET %s = %d, want 404", p, rec.Code)
		}
	}
	// Ready-flag gating on the detail view too: no hls_url while pending.
	var v videoView
	_ = json.Unmarshal(getVideo(srv, id, "").Body.Bytes(), &v)
	if v.HLSURL != nil || v.Renditions != nil {
		t.Errorf("detail = (hls_url %v, renditions %v), want absent while pending", v.HLSURL, v.Renditions)
	}
}

func TestHLSPrivateVideoOwnerOnly(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Secret","privacy":"private"}`)
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "tiny", tok); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d", rec.Code)
	}
	seedReadyHLS(t, tcRepo, blobs, id)
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	for _, p := range []string{"/hls/master.m3u8", "/hls/240p/playlist.m3u8", "/hls/240p/seg_00000.ts"} {
		if rec := getHLS(srv, "/api/v1/videos/"+id+p, ""); rec.Code != http.StatusNotFound {
			t.Errorf("anon GET %s = %d, want 404 (private)", p, rec.Code)
		}
		if rec := getHLS(srv, "/api/v1/videos/"+id+p, otherTok); rec.Code != http.StatusNotFound {
			t.Errorf("non-owner GET %s = %d, want 404 (existence not leaked)", p, rec.Code)
		}
		if rec := getHLS(srv, "/api/v1/videos/"+id+p, tok); rec.Code != http.StatusOK {
			t.Errorf("owner GET %s = %d, want 200", p, rec.Code)
		}
	}
}

func TestHLSRejectsNonCanonicalNames(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)
	seedReadyHLS(t, tcRepo, blobs, id)

	for _, p := range []string{
		"/hls/240p/other.txt",        // not a playlist/segment name
		"/hls/240p/..%2Fmaster.m3u8", // traversal attempt
		"/hls/notarung/playlist.m3u8",
		"/hls/240p/playlist.m3u8.bak",
	} {
		if rec := getHLS(srv, "/api/v1/videos/"+id+p, ""); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
	}
	if rec := getHLS(srv, "/api/v1/videos/not-a-uuid/hls/master.m3u8", ""); rec.Code != http.StatusNotFound {
		t.Errorf("bad id = %d, want 404", rec.Code)
	}
}

func TestVideoDetailCarriesHLSWhenReady(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	var before videoView
	_ = json.Unmarshal(getVideo(srv, id, "").Body.Bytes(), &before)
	if before.HLSURL != nil || before.Renditions != nil {
		t.Errorf("detail before transcode = (hls_url %v, renditions %v), want absent", before.HLSURL, before.Renditions)
	}

	seedReadyHLS(t, tcRepo, blobs, id)
	var after videoView
	rec := getVideo(srv, id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail = %d", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &after)
	wantURL := "/api/v1/videos/" + id + "/hls/master.m3u8"
	if after.HLSURL == nil || *after.HLSURL != wantURL {
		t.Errorf("hls_url = %v, want %q", after.HLSURL, wantURL)
	}
	if len(after.Renditions) != 1 || after.Renditions[0].Height != 240 || after.Renditions[0].Width != 320 {
		t.Errorf("renditions = %+v, want [{240 320}]", after.Renditions)
	}
}
