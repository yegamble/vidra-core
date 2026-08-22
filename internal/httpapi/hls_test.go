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
		State: "pending", TranscodeType: a.TranscodeType,
		NextAttemptAt: time.Now().Add(-time.Second),
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
				ID: j.ID, VideoID: j.VideoID, SourceKey: j.SourceKey,
				TranscodeType: j.TranscodeType, Attempts: j.Attempts,
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

// DeferTranscodeJob returns a job to the queue WITHOUT consuming an attempt (the
// scratch-space guard's path).
// RenewTranscodeJobLease is the heartbeat that keeps a long job's claim alive.
func (f *transcodeFakeRepo) RenewTranscodeJobLease(_ context.Context, _ uuid.UUID) error { return nil }

func (f *transcodeFakeRepo) DeferTranscodeJob(_ context.Context, a sqlcgen.DeferTranscodeJobParams) error {
	j := f.jobs[a.ID]
	j.State = "pending"
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

func (f *transcodeFakeRepo) HasLiveTranscodeJob(_ context.Context, videoID uuid.UUID) (bool, error) {
	for _, j := range f.jobs {
		if j.VideoID == videoID && (j.State == "pending" || j.State == "running") {
			return true, nil
		}
	}
	return false, nil
}

func (f *transcodeFakeRepo) DeleteStreamingPlaylist(_ context.Context, videoID uuid.UUID) error {
	delete(f.playlists, videoID)
	return nil
}

func (f *transcodeFakeRepo) UpsertStreamingPlaylist(_ context.Context, a sqlcgen.UpsertStreamingPlaylistParams) (sqlcgen.StreamingPlaylist, error) {
	sp := sqlcgen.StreamingPlaylist{VideoID: a.VideoID, MasterKey: a.MasterKey, State: a.State, Format: a.Format}
	// The query folds an unset format to the pre-CMAF default rather than
	// violating the CHECK constraint; mirror that here or the fake would let a
	// caller store a format the database would reject.
	if sp.Format == "" {
		sp.Format = media.HLSFormatTS
	}
	f.playlists[a.VideoID] = sp
	return sp, nil
}

// MarkStreamingPlaylistFailed clears the tree reference and leaves the format
// alone, as the query does: a failure says nothing about what the last
// successful transcode wrote.
func (f *transcodeFakeRepo) MarkStreamingPlaylistFailed(_ context.Context, videoID uuid.UUID) error {
	sp := f.playlists[videoID]
	sp.VideoID, sp.MasterKey, sp.State = videoID, "", "failed"
	if sp.Format == "" {
		sp.Format = media.HLSFormatTS
	}
	f.playlists[videoID] = sp
	return nil
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
		Sha256: a.Sha256,
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
	put(prefix+"/master.m3u8", "#EXTM3U\n#EXT-X-VERSION:4\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-STREAM-INF:BANDWIDTH=985600,RESOLUTION=320x240\n240p/playlist.m3u8\n#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=60000,RESOLUTION=320x240,CODECS=\"avc1.4d4015\",URI=\"240p/iframe.m3u8\"\n")
	put(prefix+"/240p/playlist.m3u8", "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXTINF:2.0,\nseg_00000.ts\n#EXT-X-ENDLIST\n")
	put(prefix+"/240p/seg_00000.ts", "fake-ts-bytes")
	put(prefix+"/240p/iframe.m3u8", "#EXTM3U\n#EXT-X-VERSION:4\n#EXT-X-I-FRAMES-ONLY\n#EXTINF:1.0,\n#EXT-X-BYTERANGE:8@0\niframe.ts\n#EXT-X-ENDLIST\n")
	put(prefix+"/240p/iframe.ts", "fake-iframe-ts-bytes")
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
	master.WriteString("#EXTM3U\n#EXT-X-VERSION:4\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	for _, r := range rungs {
		repo.renditions[id] = append(repo.renditions[id], sqlcgen.VideoRendition{
			ID: uuid.New(), VideoID: id, Height: int32(r.h), Width: int32(r.w),
			KeyPrefix: prefix + "/" + itoa(r.h) + "p",
		})
		master.WriteString("#EXT-X-STREAM-INF:BANDWIDTH=" + itoa(r.bw) + ",RESOLUTION=" + itoa(r.w) + "x" + itoa(r.h) + "\n")
		master.WriteString(itoa(r.h) + "p/playlist.m3u8\n")
		master.WriteString("#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=60000,RESOLUTION=" + itoa(r.w) + "x" + itoa(r.h) + ",CODECS=\"avc1.4d401f\",URI=\"" + itoa(r.h) + "p/iframe.m3u8\"\n")
		put := func(key, content string) {
			if _, err := blobs.Put(context.Background(), key, strings.NewReader(content)); err != nil {
				t.Fatalf("Put %q: %v", key, err)
			}
		}
		put(prefix+"/"+itoa(r.h)+"p/playlist.m3u8",
			"#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXTINF:2.0,\nseg_00000.ts\n#EXT-X-ENDLIST\n")
		put(prefix+"/"+itoa(r.h)+"p/seg_00000.ts", "fake-ts-bytes")
		put(prefix+"/"+itoa(r.h)+"p/iframe.m3u8",
			"#EXTM3U\n#EXT-X-VERSION:4\n#EXT-X-I-FRAMES-ONLY\n#EXTINF:1.0,\n#EXT-X-BYTERANGE:8@0\niframe.ts\n#EXT-X-ENDLIST\n")
		put(prefix+"/"+itoa(r.h)+"p/iframe.ts", "fake-iframe-ts-bytes")
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
	version := hlsCacheVersion(tcRepo.playlists[uuid.MustParse(id)])

	rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/master.m3u8?v="+version, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("master = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("master Content-Type = %q, want application/vnd.apple.mpegurl", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != hlsVersionedCacheControl {
		t.Errorf("master Cache-Control = %q, want %q", cc, hlsVersionedCacheControl)
	}
	if !strings.Contains(rec.Body.String(), "240p/playlist.m3u8?v="+version) {
		t.Errorf("master should reference the variant relatively:\n%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `URI="240p/iframe.m3u8?v=`+version+`"`) {
		t.Errorf("master should advertise the versioned trick-play playlist:\n%s", rec.Body.String())
	}

	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/240p/playlist.m3u8?v="+version, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("variant = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("variant Content-Type = %q, want application/vnd.apple.mpegurl", ct)
	}
	if !strings.Contains(rec.Body.String(), "seg_00000.ts?v="+version) {
		t.Errorf("variant should list its segment:\n%s", rec.Body.String())
	}

	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts?v="+version, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("segment = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp2t" {
		t.Errorf("segment Content-Type = %q, want video/mp2t", ct)
	}
	if rec.Body.String() != "fake-ts-bytes" {
		t.Errorf("segment bytes = %q", rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != hlsVersionedCacheControl {
		t.Errorf("segment Cache-Control = %q, want %q", cc, hlsVersionedCacheControl)
	}

	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/240p/iframe.m3u8?v="+version, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "#EXT-X-I-FRAMES-ONLY") {
		t.Fatalf("I-frame playlist = %d body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "iframe.ts?v="+version) {
		t.Fatalf("I-frame playlist should propagate its generation:\n%s", rec.Body.String())
	}
	rangeRec := httptest.NewRecorder()
	rangeReq := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+"/hls/240p/iframe.ts?v="+version, nil)
	rangeReq.Header.Set("Range", "bytes=0-3")
	srv.Handler().ServeHTTP(rangeRec, rangeReq)
	if rangeRec.Code != http.StatusPartialContent || rangeRec.Body.String() != "fake" {
		t.Fatalf("I-frame byte range = %d body=%q, want 206/fake", rangeRec.Code, rangeRec.Body.String())
	}
	if cr := rangeRec.Header().Get("Content-Range"); cr != "bytes 0-3/20" {
		t.Errorf("I-frame Content-Range = %q, want bytes 0-3/20", cr)
	}
	if stale := getHLS(srv, "/api/v1/videos/"+id+"/hls/master.m3u8?v=stale", ""); stale.Code != http.StatusNotFound {
		t.Errorf("stale generation = %d, want 404", stale.Code)
	}
}

func TestHLSServesReferencedPeerTubeFlatFMP4Tree(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"PeerTube HLS","privacy":"public"}`)
	seedReadyPeerTubeHLS(t, tcRepo, blobs, id)
	version := hlsCacheVersion(tcRepo.playlists[uuid.MustParse(id)])

	rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/master.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("master = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\npeertube/v1-720.m3u8?v="+version+"\n") {
		t.Fatalf("master should route PeerTube flat variant through peertube/:\n%s", rec.Body.String())
	}

	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/peertube/v1-720.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("variant = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `URI="v1-init.mp4?v=`+version+`"`) ||
		!strings.Contains(rec.Body.String(), "v1-720-fragmented.mp4?v="+version) {
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
	if !strings.Contains(rec.Body.String(), "peertube/v1-720.m3u8?pt=secret&v="+version) {
		t.Fatalf("master should propagate playback token into rewritten URI:\n%s", rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Fatalf("tokenized master Cache-Control = %q, want private, no-store", cc)
	}
	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/peertube/v1-720.m3u8?pt=secret", "")
	if !strings.Contains(rec.Body.String(), `URI="v1-init.mp4?pt=secret&v=`+version+`"`) ||
		!strings.Contains(rec.Body.String(), "v1-720-fragmented.mp4?pt=secret&v="+version) {
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
	wantURL := "/api/v1/videos/" + id + "/hls/master.m3u8?v=" +
		hlsCacheVersion(tcRepo.playlists[uuid.MustParse(id)])
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
	bwMatches := regexp.MustCompile(`#EXT-X-STREAM-INF:BANDWIDTH=(\d+)`).FindAllStringSubmatch(master, -1)
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
	wantURL := "/api/v1/videos/" + id + "/hls/master.m3u8?v=" +
		hlsCacheVersion(tcRepo.playlists[uuid.MustParse(id)])
	if after.HLSURL == nil || *after.HLSURL != wantURL {
		t.Errorf("hls_url = %v, want %q", after.HLSURL, wantURL)
	}
	if len(after.Renditions) != 1 || after.Renditions[0].Height != 240 || after.Renditions[0].Width != 320 {
		t.Errorf("renditions = %+v, want [{240 320}]", after.Renditions)
	}
}

// seedReadyCMAF marks videoID's playlist ready with a CMAF tree: the Go-authored
// master at the usual key, and one flat "cmaf" directory holding the DASH
// manifest, the per-representation media playlists, init/media segments and the
// trick-play pair — the shape internal/media's cmafPackager stores.
func seedReadyCMAF(t *testing.T, repo *transcodeFakeRepo, blobs storage.Backend, videoID string) {
	t.Helper()
	id := uuid.MustParse(videoID)
	prefix := "streaming-playlists/" + videoID
	repo.playlists[id] = sqlcgen.StreamingPlaylist{
		VideoID: id, MasterKey: prefix + "/master.m3u8", State: "ready",
		Format: media.HLSFormatCMAF,
	}
	repo.renditions[id] = append(repo.renditions[id], sqlcgen.VideoRendition{
		ID: uuid.New(), VideoID: id, Height: 240, Width: 320, KeyPrefix: prefix + "/240p",
	})
	put := func(key, content string) {
		if _, err := blobs.Put(context.Background(), key, strings.NewReader(content)); err != nil {
			t.Fatalf("Put %q: %v", key, err)
		}
	}
	put(prefix+"/master.m3u8", "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n"+
		"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"audio\",DEFAULT=YES,AUTOSELECT=YES,CHANNELS=\"2\",URI=\"cmaf/media_1.m3u8\"\n"+
		"#EXT-X-STREAM-INF:BANDWIDTH=620400,RESOLUTION=320x240,CODECS=\"avc1.4d400d,mp4a.40.2\",AUDIO=\"audio\"\n"+
		"cmaf/media_0.m3u8\n"+
		"#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=60000,RESOLUTION=320x240,CODECS=\"avc1.4d400d\",URI=\"cmaf/iframe-0.m3u8\"\n")
	put(prefix+"/cmaf/stream.mpd", `<?xml version="1.0" encoding="utf-8"?><MPD type="static">`+
		`<SegmentTemplate initialization="init-$RepresentationID$.mp4" media="chunk-$RepresentationID$-$Number%05d$.m4s"/></MPD>`)
	put(prefix+"/cmaf/media_0.m3u8", "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-PLAYLIST-TYPE:VOD\n"+
		"#EXT-X-MAP:URI=\"init-0.mp4\"\n#EXTINF:6.000000,\nchunk-0-00001.m4s\n#EXT-X-ENDLIST\n")
	put(prefix+"/cmaf/media_1.m3u8", "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-PLAYLIST-TYPE:VOD\n"+
		"#EXT-X-MAP:URI=\"init-1.mp4\"\n#EXTINF:6.000000,\nchunk-1-00001.m4s\n#EXT-X-ENDLIST\n")
	put(prefix+"/cmaf/init-0.mp4", "fake-init-0")
	put(prefix+"/cmaf/init-1.mp4", "fake-init-1")
	put(prefix+"/cmaf/chunk-0-00001.m4s", "fake-video-segment")
	put(prefix+"/cmaf/chunk-1-00001.m4s", "fake-audio-segment")
	put(prefix+"/cmaf/iframe-0.m3u8", "#EXTM3U\n#EXT-X-I-FRAMES-ONLY\n#EXT-X-MAP:URI=\"iframe-0.mp4\",BYTERANGE=\"800@0\"\n"+
		"#EXTINF:1.0,\n#EXT-X-BYTERANGE:100@800\niframe-0.mp4\n#EXT-X-ENDLIST\n")
	put(prefix+"/cmaf/iframe-0.mp4", "fake-iframe-fmp4")
}

// TestHLSServesCMAFTreeThroughOnePseudoRendition walks a CMAF video the way a
// player does — master, media playlist, init, segment — and then asks for the
// DASH manifest. Both manifests are reached through the SAME directory, which is
// the entire reason DASH costs no extra storage: the segments a DASH player
// fetches are the segments an HLS player fetches.
func TestHLSServesCMAFTreeThroughOnePseudoRendition(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"CMAF","privacy":"public"}`)
	seedReadyCMAF(t, tcRepo, blobs, id)
	version := hlsCacheVersion(tcRepo.playlists[uuid.MustParse(id)])

	// The master is served from the same key as ever, and its relative URIs are
	// versioned into the cmaf pseudo-rendition.
	rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/master.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("master = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	master := rec.Body.String()
	for _, want := range []string{
		"#EXT-X-VERSION:7",
		`CODECS="avc1.4d400d,mp4a.40.2"`,
		"\ncmaf/media_0.m3u8?v=" + version + "\n",
		`URI="cmaf/media_1.m3u8?v=` + version + `"`,
		`URI="cmaf/iframe-0.m3u8?v=` + version + `"`,
	} {
		if !strings.Contains(master, want) {
			t.Errorf("master missing %q:\n%s", want, master)
		}
	}

	// A media playlist: still an m3u8, so it gets the same reference rewrite —
	// which is what makes its bare sibling names resolve back to this route.
	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/cmaf/media_0.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("media playlist = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/vnd.apple.mpegurl") {
		t.Errorf("media playlist Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), `URI="init-0.mp4?v=`+version+`"`) ||
		!strings.Contains(rec.Body.String(), "chunk-0-00001.m4s?v="+version) {
		t.Fatalf("media playlist should version its init and segment references:\n%s", rec.Body.String())
	}

	// The init segment and a media segment.
	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/cmaf/init-0.mp4", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "fake-init-0" {
		t.Fatalf("init segment = %d %q", rec.Code, rec.Body.String())
	}
	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/cmaf/chunk-0-00001.m4s", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "fake-video-segment" {
		t.Fatalf("media segment = %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("m4s Content-Type = %q, want video/mp4 (Apple's HLS authoring guidance for fMP4)", ct)
	}

	// The DASH manifest, at the canonical URL, served verbatim: its
	// SegmentTemplate patterns are expanded by the player, not followed as URIs,
	// so there is nothing here to rewrite.
	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/"+hlsCMAFRendition+"/"+hlsCMAFManifestFile, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("mpd = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/dash+xml") {
		t.Errorf("mpd Content-Type = %q, want application/dash+xml", ct)
	}
	if !strings.Contains(rec.Body.String(), `initialization="init-$RepresentationID$.mp4"`) {
		t.Fatalf("mpd should be served verbatim:\n%s", rec.Body.String())
	}
	// And it names the very files the HLS side just served, resolved as siblings.
	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/cmaf/chunk-1-00001.m4s", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "fake-audio-segment" {
		t.Fatalf("audio representation segment = %d %q", rec.Code, rec.Body.String())
	}

	// The playback token propagates through the whole HLS chain, exactly as it
	// does for MPEG-TS: without it, password-protected CMAF would not play in a
	// header-less native player.
	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/cmaf/media_0.m3u8?pt=secret", "")
	if !strings.Contains(rec.Body.String(), `URI="init-0.mp4?pt=secret&v=`+version+`"`) ||
		!strings.Contains(rec.Body.String(), "chunk-0-00001.m4s?pt=secret&v="+version) {
		t.Fatalf("media playlist should propagate the playback token:\n%s", rec.Body.String())
	}
}

// TestHLSCMAFNamesAreCrossCheckedAgainstTheVideosFormat is the fence. The cmaf
// pseudo-rendition names a whole family of files that mean nothing under an
// MPEG-TS tree; asking for them against a TS video must be refused on the
// RECORDED format, not by reaching object storage and hoping for a miss. The
// reverse — CMAF names that are not the ones the packager emits — must be
// refused before any lookup at all.
func TestHLSCMAFNamesAreCrossCheckedAgainstTheVideosFormat(t *testing.T) {
	t.Run("cmaf names against an MPEG-TS video are 404", func(t *testing.T) {
		srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
		tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
		id := createPublishedVideo(t, srv, tok, "ada", `{"title":"TS","privacy":"public"}`)
		seedReadyHLS(t, tcRepo, blobs, id)
		if got := tcRepo.playlists[uuid.MustParse(id)].Format; got == media.HLSFormatCMAF {
			t.Fatalf("fixture is stale: an MPEG-TS video must not record format %q", got)
		}
		for _, p := range []string{
			"/hls/cmaf/stream.mpd",
			"/hls/cmaf/media_0.m3u8",
			"/hls/cmaf/init-0.mp4",
			"/hls/cmaf/chunk-0-00001.m4s",
		} {
			if rec := getHLS(srv, "/api/v1/videos/"+id+p, ""); rec.Code != http.StatusNotFound {
				t.Errorf("GET %s on an MPEG-TS video = %d, want 404", p, rec.Code)
			}
		}
		// The MPEG-TS tree itself keeps serving, untouched.
		if rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/240p/playlist.m3u8", ""); rec.Code != http.StatusOK {
			t.Errorf("MPEG-TS variant = %d, want 200 — old videos must play unchanged", rec.Code)
		}
	})

	t.Run("names outside the packager's own naming are 404", func(t *testing.T) {
		srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
		tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
		id := createPublishedVideo(t, srv, tok, "ada", `{"title":"CMAF","privacy":"public"}`)
		seedReadyCMAF(t, tcRepo, blobs, id)
		for _, p := range []string{
			"/hls/cmaf/ffmpeg-master.m3u8", // discarded during finalisation; never served
			"/hls/cmaf/master.m3u8",        // the Go master is NOT in this directory
			"/hls/cmaf/stream.mpd.bak",
			"/hls/cmaf/media_.m3u8",
			"/hls/cmaf/media_0.m3u8.bak",
			"/hls/cmaf/init-.mp4",
			"/hls/cmaf/chunk-0.m4s",
			"/hls/cmaf/init-0.m4s",
			"/hls/cmaf/video.mp4",
			"/hls/cmaf/other.txt",
			"/hls/cmaf/..%2Fmaster.m3u8",
			"/hls/CMAF/stream.mpd",
			"/hls/240p/stream.mpd",
		} {
			if rec := getHLS(srv, "/api/v1/videos/"+id+p, ""); rec.Code != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", p, rec.Code)
			}
		}
	})

	t.Run("MPEG-TS names against a CMAF video are 404", func(t *testing.T) {
		// The mirror of the case above, and the reason the rule is stated as a
		// property of the route rather than a guard bolted on for the new format:
		// the two naming schemes are mutually exclusive, so each is refused on the
		// recorded format instead of being allowed to reach storage and miss.
		srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
		tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
		id := createPublishedVideo(t, srv, tok, "ada", `{"title":"CMAF","privacy":"public"}`)
		seedReadyCMAF(t, tcRepo, blobs, id)
		for _, p := range []string{
			"/hls/240p/playlist.m3u8",
			"/hls/240p/seg_00000.ts",
			"/hls/240p/iframe.m3u8",
			"/hls/240p/iframe.ts",
		} {
			if rec := getHLS(srv, "/api/v1/videos/"+id+p, ""); rec.Code != http.StatusNotFound {
				t.Errorf("GET %s on a CMAF video = %d, want 404", p, rec.Code)
			}
		}
		// The CMAF tree itself keeps serving.
		if rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/cmaf/media_0.m3u8", ""); rec.Code != http.StatusOK {
			t.Errorf("CMAF media playlist = %d, want 200", rec.Code)
		}
	})

	t.Run("a PeerTube import is fenced on its tree, not on a format it has none of", func(t *testing.T) {
		// A pass-through tree records the pre-CMAF default like any other legacy
		// row, so the format check must not be what decides its fate.
		srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
		tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
		id := createPublishedVideo(t, srv, tok, "ada", `{"title":"PeerTube","privacy":"public"}`)
		seedReadyPeerTubeHLS(t, tcRepo, blobs, id)
		if rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/peertube/v1-720.m3u8", ""); rec.Code != http.StatusOK {
			t.Errorf("PeerTube variant = %d, want 200", rec.Code)
		}
		if rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/cmaf/stream.mpd", ""); rec.Code != http.StatusNotFound {
			t.Errorf("cmaf name on a PeerTube tree = %d, want 404", rec.Code)
		}
	})

	t.Run("a stale generation version is 404 even for cmaf names", func(t *testing.T) {
		srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
		tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
		id := createPublishedVideo(t, srv, tok, "ada", `{"title":"CMAF","privacy":"public"}`)
		seedReadyCMAF(t, tcRepo, blobs, id)
		for _, p := range []string{"/hls/cmaf/stream.mpd?v=stale", "/hls/cmaf/chunk-0-00001.m4s?v=stale"} {
			if rec := getHLS(srv, "/api/v1/videos/"+id+p, ""); rec.Code != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", p, rec.Code)
			}
		}
	})

	t.Run("a private CMAF video is owner-only, manifest included", func(t *testing.T) {
		srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
		tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
		id := createPublishedVideo(t, srv, tok, "ada", `{"title":"CMAF","privacy":"private"}`)
		seedReadyCMAF(t, tcRepo, blobs, id)
		if rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/cmaf/stream.mpd", ""); rec.Code != http.StatusNotFound {
			t.Errorf("anonymous mpd on a private video = %d, want 404", rec.Code)
		}
		if rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/cmaf/stream.mpd", tok); rec.Code != http.StatusOK {
			t.Errorf("owner mpd = %d, want 200", rec.Code)
		}
	})
}

// --- phase-3 item 6.2: audio-only CMAF trees ---------------------------------

// seedReadyAudioOnlyCMAF marks videoID's playlist ready with the tree
// internal/media's cmafPackager stores for a source that has NO video: one audio
// representation, an audio-only master, an MPD with a single audio adaptation
// set, and — deliberately — NO video_renditions rows, no rung directories and no
// trick-play.
func seedReadyAudioOnlyCMAF(t *testing.T, repo *transcodeFakeRepo, blobs storage.Backend, videoID string) {
	t.Helper()
	id := uuid.MustParse(videoID)
	prefix := "streaming-playlists/" + videoID
	repo.playlists[id] = sqlcgen.StreamingPlaylist{
		VideoID: id, MasterKey: prefix + "/master.m3u8", State: "ready",
		Format: media.HLSFormatCMAF,
	}
	put := func(key, content string) {
		if _, err := blobs.Put(context.Background(), key, strings.NewReader(content)); err != nil {
			t.Fatalf("Put %q: %v", key, err)
		}
	}
	put(prefix+"/master.m3u8", "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n"+
		"#EXT-X-STREAM-INF:BANDWIDTH=176000,CODECS=\"mp4a.40.2\"\n"+
		"cmaf/media_0.m3u8\n")
	put(prefix+"/cmaf/stream.mpd", `<?xml version="1.0" encoding="utf-8"?><MPD type="static">`+
		`<AdaptationSet id="0" contentType="audio">`+
		`<SegmentTemplate initialization="init-$RepresentationID$.mp4" media="chunk-$RepresentationID$-$Number%05d$.m4s"/>`+
		`</AdaptationSet></MPD>`)
	put(prefix+"/cmaf/media_0.m3u8", "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-PLAYLIST-TYPE:VOD\n"+
		"#EXT-X-MAP:URI=\"init-0.mp4\"\n#EXTINF:6.000000,\nchunk-0-00001.m4s\n#EXT-X-ENDLIST\n")
	put(prefix+"/cmaf/init-0.mp4", "fake-audio-init")
	put(prefix+"/cmaf/chunk-0-00001.m4s", "fake-audio-segment")
	put(prefix+"/"+media.HLSAudioDownloadFilename, "fake-m4a-bytes")
}

// TestHLSServesAnAudioOnlyCMAFTree walks an audio-only video the way a player
// does. Nothing on the serving path needed changing for this — the routes gate
// on the playlist row and a filename regex, never on rendition rows, and the
// audio representation lands on names the CMAF regex already matched — so this
// pins that as a PROPERTY rather than an accident that could be refactored away.
func TestHLSServesAnAudioOnlyCMAFTree(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Podcast","privacy":"public"}`)
	seedReadyAudioOnlyCMAF(t, tcRepo, blobs, id)
	version := hlsCacheVersion(tcRepo.playlists[uuid.MustParse(id)])

	rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/master.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("master = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	master := rec.Body.String()
	if !strings.Contains(master, "\ncmaf/media_0.m3u8?v="+version+"\n") {
		t.Errorf("audio-only master's variant URI was not versioned into the cmaf pseudo-rendition:\n%s", master)
	}
	if strings.Contains(master, "RESOLUTION=") {
		t.Errorf("audio-only master declares a RESOLUTION:\n%s", master)
	}

	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/cmaf/media_0.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("audio media playlist = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `URI="init-0.mp4?v=`+version+`"`) ||
		!strings.Contains(rec.Body.String(), "chunk-0-00001.m4s?v="+version) {
		t.Errorf("audio media playlist should version its references:\n%s", rec.Body.String())
	}

	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/cmaf/init-0.mp4", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "fake-audio-init" {
		t.Fatalf("audio init segment = %d %q", rec.Code, rec.Body.String())
	}
	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/cmaf/chunk-0-00001.m4s", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "fake-audio-segment" {
		t.Fatalf("audio media segment = %d %q", rec.Code, rec.Body.String())
	}

	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/"+hlsCMAFRendition+"/"+hlsCMAFManifestFile, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("mpd = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/dash+xml") {
		t.Errorf("mpd Content-Type = %q", ct)
	}

	// Nothing invents a rung: the MPEG-TS rendition names stay 404 for this tree
	// exactly as they do for any other CMAF one.
	for _, p := range []string{"/hls/240p/playlist.m3u8", "/hls/cmaf/iframe-0.m3u8"} {
		if rec := getHLS(srv, "/api/v1/videos/"+id+p, ""); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 for an audio-only tree", p, rec.Code)
		}
	}
}

// TestVideoDetailForAnAudioOnlyTreeHasHLSAndNoRenditions pins the detail shape:
// hls_url is present (there IS something to play) and the rendition list is
// absent rather than empty — an audio-only presentation has no resolutions, and
// saying so by omission is what `omitempty` already does.
func TestVideoDetailForAnAudioOnlyTreeHasHLSAndNoRenditions(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Podcast","privacy":"public"}`)
	seedReadyAudioOnlyCMAF(t, tcRepo, blobs, id)

	var view videoView
	rec := getVideo(srv, id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail = %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantURL := "/api/v1/videos/" + id + "/hls/master.m3u8?v=" +
		hlsCacheVersion(tcRepo.playlists[uuid.MustParse(id)])
	if view.HLSURL == nil || *view.HLSURL != wantURL {
		t.Errorf("hls_url = %v, want %q", view.HLSURL, wantURL)
	}
	if view.Renditions != nil {
		t.Errorf("renditions = %+v, want absent for an audio-only tree", view.Renditions)
	}
	if strings.Contains(rec.Body.String(), `"renditions"`) {
		t.Errorf("detail body should omit the renditions key entirely:\n%s", rec.Body.String())
	}
}

// TestDownloadsForAnAudioOnlyTreeOfferTheAudioAsset closes the loop on the
// deliverable: the download list gains an `audio` entry and no `hls` ones, and
// the byte route serves it.
func TestDownloadsForAnAudioOnlyTreeOfferTheAudioAsset(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Podcast","privacy":"public"}`)
	seedReadyAudioOnlyCMAF(t, tcRepo, blobs, id)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+"/download", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("downloads = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Files []struct {
			Kind      string `json:"kind"`
			URL       string `json:"url"`
			SizeBytes int64  `json:"size_bytes"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	kinds := map[string]int{}
	for _, f := range resp.Files {
		kinds[f.Kind]++
	}
	if kinds["hls"] != 0 {
		t.Errorf("audio-only downloads offered %d hls rungs: %+v", kinds["hls"], resp.Files)
	}
	if kinds["audio"] != 1 {
		t.Fatalf("audio-only downloads offered %d audio assets, want 1: %+v", kinds["audio"], resp.Files)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+"/download/audio", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "fake-m4a-bytes" {
		t.Fatalf("audio download = %d %q", rec.Code, rec.Body.String())
	}
}
