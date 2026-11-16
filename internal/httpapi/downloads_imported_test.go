package httpapi

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

const importedDir = "streaming-playlists/hls/11111111-1111-1111-1111-111111111111"

func byteRangeVariant(object string) string {
	return "#EXTM3U\n#EXT-X-MAP:URI=\"" + object + "\",BYTERANGE=\"100@0\"\n#EXTINF:2,\n#EXT-X-BYTERANGE:200@100\n" + object + "\n#EXT-X-ENDLIST\n"
}

// seedImportedHLS stores the shape an instance that imported from PeerTube has:
// every rendition row shares ONE key_prefix and no Vidra remux object exists.
// Portrait 720x1280 plus a 240x426 rung. split selects the audio-group layout.
func seedImportedHLS(t *testing.T, repo *transcodeFakeRepo, blobs storage.Backend, videoID string, split bool) {
	t.Helper()
	id := uuid.MustParse(videoID)
	repo.playlists[id] = sqlcgen.StreamingPlaylist{VideoID: id, MasterKey: importedDir + "/u-master.m3u8", State: "ready"}
	repo.renditions[id] = []sqlcgen.VideoRendition{
		{ID: uuid.New(), VideoID: id, Height: 1280, Width: 720, KeyPrefix: importedDir, SizeBytes: 999},
		{ID: uuid.New(), VideoID: id, Height: 426, Width: 240, KeyPrefix: importedDir, SizeBytes: 999},
	}
	master := "#EXTM3U\n"
	audioAttr := ""
	if split {
		master += "#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"Audio\",DEFAULT=YES,URI=\"u-0.m3u8\"\n"
		audioAttr = ",AUDIO=\"audio\""
	}
	master += "#EXT-X-STREAM-INF:BANDWIDTH=2000,RESOLUTION=720x1280" + audioAttr + "\nu-1280.m3u8\n"
	master += "#EXT-X-STREAM-INF:BANDWIDTH=900,RESOLUTION=240x426" + audioAttr + "\nu-426.m3u8\n"
	put := func(name, content string) {
		if _, err := blobs.Put(context.Background(), importedDir+"/"+name, strings.NewReader(content)); err != nil {
			t.Fatalf("Put %q: %v", name, err)
		}
	}
	put("u-master.m3u8", master)
	put("u-1280.m3u8", byteRangeVariant("u-1280.mp4"))
	put("u-426.m3u8", byteRangeVariant("u-426.mp4"))
	put("u-1280.mp4", "VIDEO-1280-BYTES") // 16
	put("u-426.mp4", "VIDEO-426")         // 9
	if split {
		put("u-0.m3u8", byteRangeVariant("u-0.mp4"))
		put("u-0.mp4", "AUDIO-BYTES") // 11
	}
}

// fakeMuxer stands in for ffmpeg. mode picks the behaviour of Mux.
type fakeMuxer struct {
	mu       sync.Mutex
	calls    [][2]string
	body     string
	startErr error
	readErr  error // returned by the first Read, with no bytes
	hang     bool  // after body, block until ctx ends
	endErr   error // returned instead of EOF once the body is consumed
	endless  bool  // never end: keep producing bytes
	started  chan struct{}
	closed   chan struct{}
	ctxDone  chan struct{}
}

func newFakeMuxer(body string) *fakeMuxer {
	return &fakeMuxer{body: body, started: make(chan struct{}, 4), closed: make(chan struct{}, 4), ctxDone: make(chan struct{}, 4)}
}

func (f *fakeMuxer) Mux(ctx context.Context, videoKey, audioKey string) (io.ReadCloser, error) {
	f.mu.Lock()
	f.calls = append(f.calls, [2]string{videoKey, audioKey})
	f.mu.Unlock()
	f.started <- struct{}{}
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &fakeMuxStream{f: f, ctx: ctx, r: strings.NewReader(f.body)}, nil
}

func (f *fakeMuxer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeMuxStream struct {
	f   *fakeMuxer
	ctx context.Context
	r   *strings.Reader
}

func (s *fakeMuxStream) Read(p []byte) (int, error) {
	if s.f.readErr != nil {
		return 0, s.f.readErr
	}
	if s.r.Len() > 0 {
		return s.r.Read(p)
	}
	if s.f.endless {
		for i := range p {
			p[i] = 'x'
		}
		return len(p), nil
	}
	if s.f.endErr != nil {
		return 0, s.f.endErr
	}
	if s.f.hang {
		<-s.ctx.Done()
		s.f.ctxDone <- struct{}{}
		return 0, s.ctx.Err()
	}
	return 0, io.EOF
}

func (s *fakeMuxStream) Close() error { s.f.closed <- struct{}{}; return nil }

func importedServer(t *testing.T, split bool, mux *fakeMuxer) (srv *Server, repo *transcodeFakeRepo, id, tok string) {
	t.Helper()
	var opts []Option
	if mux != nil {
		opts = append(opts, WithHLSMuxer(mux))
	}
	srv, blobs, tcRepo, _, _ := videoServerFullWith(t, testConfig(), opts)
	tok = createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id = createPublishedVideo(t, srv, tok, "ada", `{"title":"Imported","privacy":"public"}`)
	seedImportedHLS(t, tcRepo, blobs, id, split)
	return srv, tcRepo, id, tok
}

func findFile(files []videoDownloadFileView, kind string, height int32) *videoDownloadFileView {
	for i := range files {
		if files[i].Kind == kind && files[i].Height == height {
			return &files[i]
		}
	}
	return nil
}

func TestImportedDownloadsListSplitAudio(t *testing.T) {
	srv, _, id, _ := importedServer(t, true, newFakeMuxer("x"))
	base := "/api/v1/videos/" + id + "/download"
	body := decodeDownloads(t, getDownloads(srv, id, ""))

	hls := findFile(body.Files, "hls", 1280)
	if hls == nil {
		t.Fatalf("no 1280 hls entry: %+v", body.Files)
	}
	if hls.URL != base+"/hls/1280" || hls.VideoOnlyURL != base+"/hls/1280?audio=false" ||
		hls.Width != 720 || hls.ContentType != media.HLSMP4ContentType ||
		hls.Filename != "video-"+id+"-1280p.mp4" || hls.SizeBytes != 16+11 {
		t.Errorf("1280 entry = %+v", hls)
	}
	if low := findFile(body.Files, "hls", 426); low == nil || low.SizeBytes != 9+11 {
		t.Errorf("426 entry = %+v", low)
	}
	audio := findFile(body.Files, "audio", 0)
	if audio == nil || audio.URL != base+"/audio" || audio.SizeBytes != 11 || audio.ContentType != media.HLSM4AContentType {
		t.Errorf("audio entry = %+v", audio)
	}
}

func TestImportedDownloadsListMuxedRungsWithoutAudioGroup(t *testing.T) {
	srv, _, id, _ := importedServer(t, false, newFakeMuxer("x"))
	body := decodeDownloads(t, getDownloads(srv, id, ""))
	hls := findFile(body.Files, "hls", 1280)
	if hls == nil || hls.VideoOnlyURL != "" || hls.SizeBytes != 16 || hls.URL != "/api/v1/videos/"+id+"/download/hls/1280" {
		t.Errorf("entry = %+v", hls)
	}
	if findFile(body.Files, "audio", 0) != nil {
		t.Errorf("no audio entry expected without an audio group: %+v", body.Files)
	}
	// The rung's own MP4 is served for the plain URL; there is no video-only copy.
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download/hls/1280", "", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "VIDEO-1280-BYTES" {
		t.Errorf("plain = %d %q", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download/hls/1280?audio=false", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("audio=false on a rung with embedded audio = %d, want 404", rec.Code)
	}
}

// Without ffmpeg a split-audio rung must not be offered at all: its only file is
// silent, and listing it as "the 1280p video" would hand people a file that is
// not what the row says. The audio entry stays, so the parts are still reachable.
func TestImportedDownloadsListWithoutMuxer(t *testing.T) {
	srv, _, id, _ := importedServer(t, true, nil)
	body := decodeDownloads(t, getDownloads(srv, id, ""))
	if findFile(body.Files, "hls", 1280) != nil || findFile(body.Files, "hls", 426) != nil {
		t.Errorf("split-audio rungs must be omitted without a muxer: %+v", body.Files)
	}
	if audio := findFile(body.Files, "audio", 0); audio == nil || audio.SizeBytes != 11 {
		t.Errorf("audio entry should still be listed: %+v", body.Files)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download/hls/1280", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("muxed route without a muxer = %d, want 404", rec.Code)
	}
}

func TestImportedVideoOnlyAndAudioRoutesServeObjectsWithRanges(t *testing.T) {
	srv, _, id, _ := importedServer(t, true, newFakeMuxer("x"))
	for _, tc := range []struct{ path, body, ctype string }{
		{"/download/hls/1280?audio=false", "VIDEO-1280-BYTES", media.HLSMP4ContentType},
		{"/download/audio", "AUDIO-BYTES", media.HLSM4AContentType},
	} {
		rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+tc.path, "", "")
		if rec.Code != http.StatusOK || rec.Body.String() != tc.body ||
			!strings.HasPrefix(rec.Header().Get("Content-Type"), tc.ctype) ||
			!strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") {
			t.Errorf("GET %s = %d %q type=%q disp=%q", tc.path, rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"), rec.Header().Get("Content-Disposition"))
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+tc.path, nil)
		req.Header.Set("Range", "bytes=0-4")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusPartialContent || rr.Body.String() != tc.body[:5] {
			t.Errorf("range GET %s = %d %q, want 206 %q", tc.path, rr.Code, rr.Body.String(), tc.body[:5])
		}
	}
	for _, p := range []string{"/download/hls/720", "/download/hls/0", "/download/hls/abc"} {
		if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+p, "", ""); rec.Code != http.StatusNotFound {
			t.Errorf("guessed %s = %d, want 404", p, rec.Code)
		}
	}
}

func TestImportedDownloadsGatesAndPlaylistState(t *testing.T) {
	srv, repo, id, tok := importedServer(t, true, newFakeMuxer("x"))
	paths := []string{"/download/hls/1280?audio=false", "/download/hls/1280", "/download/audio"}

	// Non-ready playlist: nothing is listed and nothing is served.
	vid := uuid.MustParse(id)
	pl := repo.playlists[vid]
	pl.State = "failed"
	repo.playlists[vid] = pl
	if body := decodeDownloads(t, getDownloads(srv, id, "")); findFile(body.Files, "hls", 1280) != nil || findFile(body.Files, "audio", 0) != nil {
		t.Errorf("failed playlist still listed: %+v", body.Files)
	}
	for _, p := range paths {
		if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+p, "", ""); rec.Code != http.StatusNotFound || rec.Header().Get("Content-Disposition") != "" {
			t.Errorf("failed playlist GET %s = %d", p, rec.Code)
		}
	}
	pl.State = "ready"
	repo.playlists[vid] = pl

	// A missing object (playlist intact, MP4 gone) is not listed and 404s
	// without an attachment header.
	srvM, blobsM, repoM, _, _ := videoServerFullWith(t, testConfig(), []Option{WithHLSMuxer(newFakeMuxer("x"))})
	tokM := createChannelFor(t, srvM, "ada", "ada@example.test", "ada")
	idM := createPublishedVideo(t, srvM, tokM, "ada", `{"title":"Gone","privacy":"public"}`)
	seedImportedHLS(t, repoM, blobsM, idM, true)
	if err := blobsM.Delete(context.Background(), importedDir+"/u-1280.mp4"); err != nil {
		t.Fatal(err)
	}
	if body := decodeDownloads(t, getDownloads(srvM, idM, "")); findFile(body.Files, "hls", 1280) != nil || findFile(body.Files, "hls", 426) == nil {
		t.Errorf("missing object listed: %+v", body.Files)
	}
	for _, p := range []string{"/download/hls/1280", "/download/hls/1280?audio=false"} {
		if rec := sendJSONAuth(srvM, http.MethodGet, "/api/v1/videos/"+idM+p, "", ""); rec.Code != http.StatusNotFound || rec.Header().Get("Content-Disposition") != "" {
			t.Errorf("missing-object GET %s = %d", p, rec.Code)
		}
	}

	// Downloads off: every new path is gated like the old ones.
	setToggle(t, srv, tok, "downloads_enabled", false)
	for _, p := range append([]string{""}, paths...) {
		if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download"+strings.TrimPrefix(p, "/download"), "", ""); rec.Code != http.StatusForbidden || errorCode(t, rec) != "feature_disabled" {
			t.Errorf("downloads disabled GET %q = %d/%s, want 403/feature_disabled", p, rec.Code, errorCode(t, rec))
		}
	}
}

func TestImportedDownloadsPrivateVideo(t *testing.T) {
	var mux = newFakeMuxer("x")
	srv, blobs, tcRepo, _, _ := videoServerFullWith(t, testConfig(), []Option{WithHLSMuxer(mux)})
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Secret","privacy":"private"}`)
	seedImportedHLS(t, tcRepo, blobs, id, true)
	for _, p := range []string{"", "/hls/1280", "/hls/1280?audio=false", "/audio"} {
		if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download"+p, "", ""); rec.Code != http.StatusNotFound {
			t.Errorf("anonymous private GET %q = %d, want 404", p, rec.Code)
		}
	}
	if mux.callCount() != 0 {
		t.Errorf("muxer ran for an unauthorised request")
	}
	body := decodeDownloads(t, getDownloads(srv, id, tok))
	if findFile(body.Files, "hls", 1280) == nil {
		t.Errorf("owner should see the imported rungs: %+v", body.Files)
	}
}

func TestImportedDownloadsUnsafeVariantIsNotListed(t *testing.T) {
	srv, blobs, tcRepo, _, _ := videoServerFullWith(t, testConfig(), nil)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"X","privacy":"public"}`)
	seedImportedHLS(t, tcRepo, blobs, id, false)
	// Make the 1280 rung multi-object; the 426 rung stays good.
	if _, err := blobs.Put(context.Background(), importedDir+"/u-1280.m3u8", strings.NewReader(
		strings.Replace(byteRangeVariant("u-1280.mp4"), "#EXT-X-ENDLIST", "other.mp4\n#EXT-X-ENDLIST", 1))); err != nil {
		t.Fatal(err)
	}
	body := decodeDownloads(t, getDownloads(srv, id, ""))
	if findFile(body.Files, "hls", 1280) != nil || findFile(body.Files, "hls", 426) == nil {
		t.Errorf("files = %+v", body.Files)
	}
}

func TestHLSMuxDownloadStreams(t *testing.T) {
	mux := newFakeMuxer("MUXED-MP4-BYTES")
	srv, _, id, _ := importedServer(t, true, mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+"/download/hls/1280", nil)
	req.Header.Set("Range", "bytes=0-3") // ignored: the length is unknown
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "MUXED-MP4-BYTES" {
		t.Fatalf("mux = %d %q", rec.Code, rec.Body.String())
	}
	h := rec.Header()
	if h.Get("Content-Type") != "video/mp4" || h.Get("Cache-Control") != "no-store" ||
		h.Get("Content-Length") != "" || h.Get("Content-Range") != "" || h.Get("Location") != "" ||
		h.Get("Content-Disposition") != attachmentHeader("video-"+id+"-1280p.mp4") {
		t.Errorf("headers = %v", h)
	}
	if mux.callCount() != 1 || mux.calls[0] != [2]string{importedDir + "/u-1280.mp4", importedDir + "/u-0.mp4"} {
		t.Errorf("muxer inputs = %v", mux.calls)
	}
	select {
	case <-mux.closed:
	default:
		t.Errorf("stream was not closed")
	}
	// HEAD must not start a process.
	before := mux.callCount()
	head := sendJSONAuth(srv, http.MethodHead, "/api/v1/videos/"+id+"/download/hls/1280", "", "")
	if head.Code != http.StatusOK || mux.callCount() != before {
		t.Errorf("HEAD = %d, muxer calls %d -> %d", head.Code, before, mux.callCount())
	}
}

func TestHLSMuxDownloadFailureBeforeFirstByte(t *testing.T) {
	for name, mux := range map[string]*fakeMuxer{
		"start fails":    {startErr: errors.New("ffmpeg: not found"), started: make(chan struct{}, 4), closed: make(chan struct{}, 4)},
		"exits no bytes": {readErr: errors.New("ffmpeg mux: exit status 1: https://s3.test/x?sig=SECRET broke"), started: make(chan struct{}, 4), closed: make(chan struct{}, 4)},
		"empty output":   newFakeMuxer(""),
	} {
		t.Run(name, func(t *testing.T) {
			srv, _, id, _ := importedServer(t, true, mux)
			rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download/hls/1280", "", "")
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
			}
			if rec.Header().Get("Content-Disposition") != "" || strings.Contains(rec.Body.String(), "SECRET") || strings.Contains(rec.Body.String(), "ffmpeg") {
				t.Errorf("error response leaked or looks like a download: %v %s", rec.Header(), rec.Body.String())
			}
		})
	}
}

func TestHLSMuxDownloadBusy(t *testing.T) {
	mux := newFakeMuxer("x")
	srv, _, id, _ := importedServer(t, true, mux)
	for i := 0; i < maxConcurrentHLSMuxes; i++ {
		srv.muxSlots <- struct{}{}
	}
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download/hls/1280", "", "")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || errorCode(t, rec) != "download_busy" {
		t.Fatalf("busy = %d retry=%q code=%s", rec.Code, rec.Header().Get("Retry-After"), errorCode(t, rec))
	}
	if mux.callCount() != 0 {
		t.Errorf("muxer started while full")
	}
	// The video-only route needs no process and is unaffected.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download/hls/1280?audio=false", "", ""); rec.Code != http.StatusOK {
		t.Errorf("video-only while busy = %d", rec.Code)
	}
	<-srv.muxSlots
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download/hls/1280", "", ""); rec.Code != http.StatusOK {
		t.Errorf("after a slot freed = %d", rec.Code)
	}
	// The slot is released when the request ends.
	if len(srv.muxSlots) != maxConcurrentHLSMuxes-1 {
		t.Errorf("slots held = %d, want %d", len(srv.muxSlots), maxConcurrentHLSMuxes-1)
	}
}

func TestHLSMuxDownloadClientDisconnectStopsRunner(t *testing.T) {
	mux := newFakeMuxer("PARTIAL")
	mux.hang = true
	srv, _, id, _ := importedServer(t, true, mux)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+"/download/hls/1280", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { srv.Handler().ServeHTTP(rec, req); close(done) }()
	select {
	case <-mux.started:
	case <-time.After(5 * time.Second):
		t.Fatal("muxer never started")
	}
	time.Sleep(50 * time.Millisecond) // let the first bytes through
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after the request context ended")
	}
	select {
	case <-mux.ctxDone:
	default:
		t.Errorf("runner never observed the cancelled context")
	}
	select {
	case <-mux.closed:
	default:
		t.Errorf("stream not closed")
	}
	if len(srv.muxSlots) != 0 {
		t.Errorf("slot leaked: %d", len(srv.muxSlots))
	}
}

// ---- review fixes ----------------------------------------------------------

// openCounter counts manifest reads.
type openCounter struct {
	storage.Backend
	mu    sync.Mutex
	opens []string
}

func (o *openCounter) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if strings.HasSuffix(key, ".m3u8") {
		o.mu.Lock()
		o.opens = append(o.opens, key)
		o.mu.Unlock()
	}
	return o.Backend.Open(ctx, key)
}

func (o *openCounter) manifestOpens() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.opens...)
}

// D: video_renditions is unique per (video, height); an imported width can be a
// guess that differs from the manifest. Height alone must match.
func TestImportedRungMatchesByHeightWhenWidthDiffers(t *testing.T) {
	srv, repo, id, _ := importedServer(t, true, newFakeMuxer("x"))
	vid := uuid.MustParse(id)
	rows := repo.renditions[vid]
	rows[0].Width = 721
	rows[1].Width = 0
	repo.renditions[vid] = rows
	body := decodeDownloads(t, getDownloads(srv, id, ""))
	if findFile(body.Files, "hls", 1280) == nil || findFile(body.Files, "hls", 426) == nil {
		t.Errorf("height-only match failed: %+v", body.Files)
	}
}

// E: Vidra's own ladders never read a manifest for the download fallback, with
// or without an audio remux (a silent video has none).
func TestVidraTranscodedDownloadsReadNoManifests(t *testing.T) {
	for _, withAudio := range []bool{true, false} {
		srv, blobs, tcRepo, _, _ := videoServerFull(t, testConfig())
		counter := &openCounter{Backend: blobs}
		srv.media = counter
		tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
		id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Own","privacy":"public"}`)
		seedReadyHLS(t, tcRepo, blobs, id)
		if !withAudio {
			if err := blobs.Delete(context.Background(), media.HLSAudioDownloadKey("streaming-playlists/"+id+"/master.m3u8")); err != nil {
				t.Fatal(err)
			}
		}
		body := decodeDownloads(t, getDownloads(srv, id, ""))
		if (findFile(body.Files, "audio", 0) != nil) != withAudio {
			t.Fatalf("withAudio=%v files=%+v", withAudio, body.Files)
		}
		for _, p := range []string{"/download/audio", "/download/hls/240", "/download/hls/240?audio=false"} {
			sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+p, "", "")
		}
		if got := counter.manifestOpens(); len(got) != 0 {
			t.Errorf("withAudio=%v: Vidra-transcoded video read manifests: %v", withAudio, got)
		}
	}
}

func TestImportedByteRoutesReadOnlyWhatTheyNeed(t *testing.T) {
	srv, blobs, tcRepo, _, _ := videoServerFullWith(t, testConfig(), []Option{WithHLSMuxer(newFakeMuxer("x"))})
	counter := &openCounter{Backend: blobs}
	srv.media = counter
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Imp","privacy":"public"}`)
	seedImportedHLS(t, tcRepo, blobs, id, true)
	for _, tc := range []struct{ path, want string }{
		{"/download/hls/1280?audio=false", importedDir + "/u-master.m3u8," + importedDir + "/u-1280.m3u8"},
		{"/download/audio", importedDir + "/u-master.m3u8," + importedDir + "/u-0.m3u8"},
	} {
		counter.opens = nil
		if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+tc.path, "", ""); rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", tc.path, rec.Code)
		}
		if got := strings.Join(counter.manifestOpens(), ","); got != tc.want {
			t.Errorf("GET %s read manifests %s, want %s", tc.path, got, tc.want)
		}
	}
}

// statOnly looks like an object store: no local path, but a Stat that returns
// the size from one metadata call.
type statOnly struct{ storage.Backend }

func (s statOnly) Stat(ctx context.Context, key string) (int64, bool, error) {
	rc, err := s.Backend.Open(ctx, key)
	if err != nil {
		return 0, false, nil
	}
	defer func() { _ = rc.Close() }()
	n, err := io.Copy(io.Discard, rc)
	return n, true, err
}

// F: on a backend that is not a PathProvider, listed sizes are real.
func TestImportedDownloadSizesOnRemoteBackend(t *testing.T) {
	srv, blobs, tcRepo, _, _ := videoServerFullWith(t, testConfig(), []Option{WithHLSMuxer(newFakeMuxer("x"))})
	srv.media = statOnly{blobs}
	if _, ok := srv.media.(storage.PathProvider); ok {
		t.Fatal("fixture must not be a PathProvider")
	}
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"S3","privacy":"public"}`)
	seedImportedHLS(t, tcRepo, blobs, id, true)
	body := decodeDownloads(t, getDownloads(srv, id, ""))
	if hls := findFile(body.Files, "hls", 1280); hls == nil || hls.SizeBytes != 16+11 {
		t.Errorf("hls = %+v, want size 27", hls)
	}
	if audio := findFile(body.Files, "audio", 0); audio == nil || audio.SizeBytes != 11 {
		t.Errorf("audio = %+v, want size 11", audio)
	}
}

// B: a mux that dies after the 200 was committed must not end like a complete
// file. The connection is aborted, so the client sees an error, not a short body.
func TestHLSMuxMidStreamFailureAbortsTheConnection(t *testing.T) {
	mux := newFakeMuxer("PARTIAL-BYTES")
	mux.endErr = errors.New("ffmpeg mux: exit status 1")
	srv, _, id, _ := importedServer(t, true, mux)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/v1/videos/" + id + "/download/hls/1280")
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
		_, err = io.ReadAll(resp.Body)
	}
	if err == nil {
		t.Fatal("a mux that failed mid-stream read as a complete download")
	}
	// The deferred close and slot release still ran.
	deadline := time.Now().Add(3 * time.Second)
	for len(srv.muxSlots) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(srv.muxSlots) != 0 {
		t.Errorf("slot leaked after abort: %d", len(srv.muxSlots))
	}
	select {
	case <-mux.closed:
	default:
		t.Errorf("stream not closed after abort")
	}
}

// C: one concurrent mux per client on top of the global bound.
func TestHLSMuxOnePerClient(t *testing.T) {
	mux := newFakeMuxer("x")
	mux.hang = true
	srv, _, id, _ := importedServer(t, true, mux)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	do := func(addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+"/download/hls/1280", nil).WithContext(ctx)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	done := make(chan struct{}, 2)
	go func() { do("198.51.100.1:1000"); done <- struct{}{} }()
	<-mux.started
	time.Sleep(50 * time.Millisecond)
	rec := do("198.51.100.1:2000")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || errorCode(t, rec) != "download_busy" {
		t.Fatalf("second request from the same client = %d code=%s", rec.Code, errorCode(t, rec))
	}
	go func() { do("203.0.113.9:1"); done <- struct{}{} }()
	select {
	case <-mux.started:
	case <-time.After(3 * time.Second):
		t.Fatal("a different client was refused")
	}
	cancel()
	<-done
	<-done
	srv.muxMu.Lock()
	left := len(srv.muxClients)
	srv.muxMu.Unlock()
	if left != 0 {
		t.Errorf("client entries leaked: %d", left)
	}
}

// C: a reader that stops reading frees its slot via the per-write idle deadline.
func TestHLSMuxStalledReaderIsReleased(t *testing.T) {
	old := muxIdleWriteTimeout
	muxIdleWriteTimeout = 300 * time.Millisecond
	defer func() { muxIdleWriteTimeout = old }()
	mux := newFakeMuxer("")
	mux.endless = true
	srv, _, id, _ := importedServer(t, true, mux)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("GET /api/v1/videos/" + id + "/download/hls/1280 HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	// Never read: the socket buffers fill, the next write blocks.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-mux.closed:
			if len(srv.muxSlots) != 0 {
				time.Sleep(50 * time.Millisecond)
			}
			if len(srv.muxSlots) != 0 {
				t.Errorf("slot still held after close: %d", len(srv.muxSlots))
			}
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("a stalled reader kept its mux slot")
}
