package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/vidra/vidra-core/internal/jobstatus"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// maxConcurrentHLSMuxes bounds how many video+audio downloads may be built on
// request at once, server-wide. Each is one ffmpeg stream-copy process; the route
// is public, so without a bound a handful of clients could start an unbounded
// number of processes. A constant rather than an env knob: it protects the host,
// and an operator who needs more is better served by stored remuxes.
const maxConcurrentHLSMuxes = 4

// hlsMuxRetryAfter is the Retry-After (seconds) on a refused mux admission.
const hlsMuxRetryAfter = "5"

// HLSMuxer builds one MP4 containing the video object's video and the audio
// object's audio, streaming it. It is an interface so handler tests need no
// ffmpeg. Implementations must stop work when ctx ends or the result is closed,
// and report a failed run as the error of the Read that reaches end of output.
type HLSMuxer interface {
	Mux(ctx context.Context, videoKey, audioKey string) (io.ReadCloser, error)
}

// muxAvailable reports whether video+audio downloads can actually be built: a
// muxer is wired and, when it can say, its backend can supply inputs (a muxer
// that would have to spool whole objects to disk declares itself unsupported).
// Listings and the byte route both use it, so a split-audio rung is never
// advertised when the build would fail.
func (s *Server) muxAvailable() bool {
	if s.hlsMuxer == nil {
		return false
	}
	if sup, ok := s.hlsMuxer.(interface{ Supported() bool }); ok {
		return sup.Supported()
	}
	return true
}

// WithHLSMuxer enables on-request video+audio downloads for imported (split
// audio) HLS renditions. Without it those rungs are offered video-only.
func WithHLSMuxer(m HLSMuxer) Option {
	return func(s *Server) { s.hlsMuxer = m }
}

// downloadProbe answers "how big is this object and does it exist" once per
// key per request. The listing and the byte routes ask about the same objects
// repeatedly (a rung's video object, the shared audio object); each costs a
// stat or a HEAD on a remote store.
type downloadProbe struct {
	s   *Server
	ctx context.Context
	m   map[string]probeResult
}

type probeResult struct {
	size   int64
	exists bool
	err    error
}

func (p *downloadProbe) size(key string) (int64, bool, error) {
	if r, ok := p.m[key]; ok {
		return r.size, r.exists, r.err
	}
	size, exists, err := p.s.downloadObjectSize(p.ctx, key)
	if p.m == nil {
		p.m = map[string]probeResult{}
	}
	p.m[key] = probeResult{size, exists, err}
	return size, exists, err
}

// importedSources resolves the single-file MP4 objects behind an IMPORTED
// playlist (PeerTube byte-range HLS), lazily and at most once per request.
//
// It only ever acts on that layout: a rendition qualifies when its key_prefix is
// the master playlist's own directory. Vidra's own ladders keep their rungs under
// <master dir>/<n>p/, so they never reach a manifest read here — including the
// silent video that has no audio remux, which would otherwise cost a master read
// on every listing.
type importedSources struct {
	s         *Server
	ctx       context.Context
	masterKey string
	// layout reports that at least one rendition uses the imported layout.
	layout bool
	res    *media.HLSDownloadResolver
}

func newImportedSources(s *Server, ctx context.Context, masterKey string, renditions []sqlcgen.VideoRendition) *importedSources {
	is := &importedSources{s: s, ctx: ctx, masterKey: masterKey}
	for _, r := range renditions {
		if is.owns(r) {
			is.layout = true
		}
	}
	return is
}

func (is *importedSources) owns(r sqlcgen.VideoRendition) bool {
	return r.KeyPrefix == path.Dir(is.masterKey)
}

func (is *importedSources) resolver() *media.HLSDownloadResolver {
	if is.res == nil {
		is.res = media.NewHLSDownloadResolver(is.ctx, is.s.media, is.masterKey)
	}
	return is.res
}

// rung finds the rendition's single-file rung, matching on HEIGHT (a rendition
// is unique per video and height; a width recorded at import can be a guess that
// differs from the manifest — the resolver prefers an equal width among same-
// height variants). needAudio also resolves the audio object.
func (is *importedSources) rung(r sqlcgen.VideoRendition, needAudio bool) (media.HLSDownloadRung, bool) {
	if !is.owns(r) || is.s.media == nil {
		return media.HLSDownloadRung{}, false
	}
	rung, ok, err := is.resolver().Rung(int(r.Height), int(r.Width), needAudio)
	if err != nil {
		// Not an error for the caller: the video just has no fallback download.
		is.s.logger.Debug("hls download fallback unavailable", "error", jobstatus.RedactDetail(err.Error()))
		return media.HLSDownloadRung{}, false
	}
	return rung, ok
}

// audioKey is the audio group's single MP4, "" when there is none.
func (is *importedSources) audioKey() string {
	if !is.layout || is.s.media == nil {
		return ""
	}
	key, err := is.resolver().Audio()
	if err != nil {
		is.s.logger.Debug("hls audio fallback unavailable", "error", jobstatus.RedactDetail(err.Error()))
		return ""
	}
	return key
}

// hlsDownload is what a rendition download request resolved to: either one
// stored object to serve, or a pair to mux on request.
type hlsDownload struct {
	rendition          sqlcgen.VideoRendition
	key                string // serve this object (empty when muxing)
	muxVideo, muxAudio string // build video+audio from these (both set when muxing)
}

// resolveHLSDownload accepts only assets from a ready playlist and proves the
// selected object exists before the handler attaches download headers. Failed
// retranscodes may leave rendition rows behind; those must not expose stale or
// partial files through a guessed URL. A stored remux always wins; only a
// rendition without one falls back to the single-file MP4 the variant playlist
// proves (imported PeerTube HLS).
func (s *Server) resolveHLSDownload(ctx context.Context, id uuid.UUID, height int, includeAudio bool) (hlsDownload, error) {
	notFound := echo.NewHTTPError(http.StatusNotFound, "download not found")
	if s.transcodesvc == nil {
		return hlsDownload{}, notFound
	}
	sp, ok := s.transcodesvc.Playlist(ctx, id)
	if !ok || sp.State != "ready" || sp.MasterKey == "" {
		return hlsDownload{}, notFound
	}
	renditions := s.transcodesvc.Renditions(ctx, id)
	probe := &downloadProbe{s: s, ctx: ctx}
	imported := newImportedSources(s, ctx, sp.MasterKey, renditions)
	for _, rendition := range renditions {
		if int(rendition.Height) != height {
			continue
		}
		key := media.HLSDownloadKey(rendition.KeyPrefix, includeAudio)
		if _, exists, err := probe.size(key); err != nil {
			return hlsDownload{}, err
		} else if exists {
			return hlsDownload{rendition: rendition, key: key}, nil
		}
		rung, ok := imported.rung(rendition, includeAudio)
		if !ok {
			return hlsDownload{}, notFound
		}
		if _, exists, err := probe.size(rung.VideoKey); err != nil {
			return hlsDownload{}, err
		} else if !exists {
			return hlsDownload{}, notFound
		}
		switch {
		case !rung.SplitAudio && includeAudio:
			// The rung's own MP4 already carries its audio.
			return hlsDownload{rendition: rendition, key: rung.VideoKey}, nil
		case !rung.SplitAudio:
			// There is no video-only copy of a rung whose audio is inside it.
			return hlsDownload{}, notFound
		case !includeAudio:
			return hlsDownload{rendition: rendition, key: rung.VideoKey}, nil
		case !s.muxAvailable():
			return hlsDownload{}, notFound
		}
		if _, exists, err := probe.size(rung.AudioKey); err != nil {
			return hlsDownload{}, err
		} else if !exists {
			return hlsDownload{}, notFound
		}
		return hlsDownload{rendition: rendition, muxVideo: rung.VideoKey, muxAudio: rung.AudioKey}, nil
	}
	return hlsDownload{}, notFound
}

// resolveHLSAudioDownload returns the audio-only object for a ready playlist:
// the stored remux when present, else the audio group's MP4.
func (s *Server) resolveHLSAudioDownload(ctx context.Context, id uuid.UUID) (string, error) {
	notFound := echo.NewHTTPError(http.StatusNotFound, "download not found")
	if s.transcodesvc == nil {
		return "", notFound
	}
	sp, ok := s.transcodesvc.Playlist(ctx, id)
	if !ok || sp.State != "ready" || sp.MasterKey == "" {
		return "", notFound
	}
	key := media.HLSAudioDownloadKey(sp.MasterKey)
	if _, exists, err := s.downloadObjectSize(ctx, key); err != nil {
		return "", err
	} else if exists {
		return key, nil
	}
	imported := newImportedSources(s, ctx, sp.MasterKey, s.transcodesvc.Renditions(ctx, id))
	audioKey := imported.audioKey()
	if audioKey == "" {
		return "", notFound
	}
	if _, exists, err := s.downloadObjectSize(ctx, audioKey); err != nil {
		return "", err
	} else if !exists {
		return "", notFound
	}
	return audioKey, nil
}

// muxIdleWriteTimeout is how long one write to the client may block before the
// connection is dropped. It is renewed on every chunk, so it bounds a STALLED
// reader (which would otherwise hold a mux slot, and an ffmpeg process, for the
// whole 30-minute cap) without bounding a slow but moving one. A variable only so
// a test can shorten it.
var muxIdleWriteTimeout = 60 * time.Second

// admitMux takes the per-client and global slots, without queueing. One
// concurrent mux per client, keyed on the same client address the rate limiter
// uses (c.RealIP(), which honours forwarded headers only from trusted proxies,
// folded by limiterIPKey so an IPv6 /64 is one client);
// behind a CDN with no trusted-proxy list every visitor shares the edge address
// and so shares one slot — the safe failure for a process-spawning route.
func (s *Server) admitMux(client string) (release func(), ok bool) {
	s.muxMu.Lock()
	defer s.muxMu.Unlock()
	if _, busy := s.muxClients[client]; busy {
		return nil, false
	}
	select {
	case s.muxSlots <- struct{}{}:
	default:
		return nil, false
	}
	if s.muxClients == nil {
		s.muxClients = map[string]struct{}{}
	}
	s.muxClients[client] = struct{}{}
	return func() {
		s.muxMu.Lock()
		delete(s.muxClients, client)
		s.muxMu.Unlock()
		<-s.muxSlots
	}, true
}

// serveHLSMux streams a video+audio MP4 built on request from two stored
// objects by ffmpeg stream copy. Nothing is stored, nothing is redirected (a
// presigned URL cannot point at a file that does not exist), and there is no
// Content-Length or Range support — the length is unknown until the process
// ends, so a Range request is answered 200 with the full body.
//
// Order matters: the caller has already authorised the request and resolved both
// objects; the slots are taken BEFORE the process starts; and the 200 header is
// not written until ffmpeg has produced its first bytes, so a failure to start
// (or an immediate exit) is a clean 502 rather than a 200 with a broken body.
// After that point a failure can only end the connection: the handler aborts it,
// so the client's download fails instead of being saved as a truncated file.
func (s *Server) serveHLSMux(c *echo.Context, v sqlcgen.GetVideoByIDRow, filename, videoKey, audioKey string) error {
	header := c.Response().Header()
	// Success headers go on only once the response is known to be a 200, so an
	// error body never inherits an attachment disposition.
	successHeaders := func() {
		header.Set(echo.HeaderContentType, media.HLSMP4ContentType)
		header.Set(echo.HeaderContentDisposition, attachmentHeader(filename))
		header.Set("Cache-Control", "no-store")
		setMediaCORS(c, s.publicDownload(v))
	}
	if c.Request().Method == http.MethodHead {
		// A HEAD must never start a process.
		successHeaders()
		return c.NoContent(http.StatusOK)
	}

	release, ok := s.admitMux(limiterIPKey(c.RealIP()))
	if !ok {
		header.Set("Retry-After", hlsMuxRetryAfter)
		return &DownloadBusyError{}
	}
	defer release()

	ctx := c.Request().Context()
	rc, err := s.hlsMuxer.Mux(ctx, videoKey, audioKey)
	if err != nil {
		if errors.Is(err, media.ErrMuxUnsupported) {
			// Nothing was fetched or written: this backend cannot feed ffmpeg
			// without spooling objects to disk, which a public route must not do.
			return &DownloadUnavailableError{}
		}
		return s.muxFailed(c, err)
	}
	defer func() { _ = rc.Close() }()

	buf := make([]byte, 64<<10)
	var n int
	for n == 0 && err == nil {
		n, err = rc.Read(buf)
	}
	if n == 0 {
		if err == nil || errors.Is(err, io.EOF) {
			err = errors.New("ffmpeg produced no output")
		}
		return s.muxFailed(c, err)
	}
	successHeaders()
	c.Response().WriteHeader(http.StatusOK)
	controller := http.NewResponseController(c.Response())
	write := func(b []byte) error {
		// Renewed per chunk. It replaces the long streaming-route deadline the
		// request middleware set; the overall bound is the request context and
		// the mux timeout, so nothing is lost by tightening it here.
		_ = controller.SetWriteDeadline(time.Now().Add(muxIdleWriteTimeout))
		_, werr := c.Response().Write(b)
		return werr
	}
	for {
		if werr := write(buf[:n]); werr != nil {
			return nil // the client is gone or stalled; nothing left to tell it
		}
		if err != nil {
			break
		}
		n, err = rc.Read(buf)
		for n == 0 && err == nil {
			n, err = rc.Read(buf)
		}
		if n == 0 && err != nil {
			break
		}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		// ANY error but a clean EOF aborts, including an expired request deadline:
		// a file cut short by a timeout must not look complete either. The context
		// check only decides whether this is worth a log line (a client that went
		// away is not a server fault).
		if ctx.Err() == nil {
			s.logger.Warn("hls mux download failed mid-stream", "error", jobstatus.RedactDetail(err.Error()))
		}
		// Headers are committed, so the status can no longer change. Returning
		// normally would end the chunked body cleanly and the browser would save
		// a truncated MP4 as a finished download; aborting severs the connection
		// so the download fails. net/http suppresses this sentinel's stack trace,
		// and echo's Recover re-panics it, so the deferred close and slot release
		// above still run.
		panic(http.ErrAbortHandler)
	}
	return nil
}

// muxFailed answers a mux that never produced a byte. The detail (a bounded,
// URL-redacted ffmpeg stderr tail) is logged; the client gets a fixed message.
func (s *Server) muxFailed(c *echo.Context, err error) error {
	if c.Request().Context().Err() != nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "download cancelled")
	}
	s.logger.Warn("hls mux download failed", "error", jobstatus.RedactDetail(err.Error()))
	return echo.NewHTTPError(http.StatusBadGateway, "could not prepare this download")
}

func hlsDownloadURL(base string, height int32) string {
	return base + "/hls/" + strconv.Itoa(int(height))
}

// importedRenditionEntry builds the listing entry for a rendition that has no
// stored remux but whose variant playlist proves a single complete MP4 (an
// instance that imported from PeerTube). ok=false means nothing is offered.
//
//   - audio inside the rung's MP4: one entry, the object itself.
//   - audio split into its own object: url is the video+audio file built on
//     request, video_only_url the video object, and size_bytes the SUM of the two
//     objects (approximate: the built file differs by container overhead).
//   - audio split but no muxer wired (no ffmpeg on this host): the rung is NOT
//     listed. Its only file is silent, and an entry labelled with the rung's
//     quality would hand people a video that is not the one the row describes;
//     the muxed URL would be a dead link. The separate audio entry is still
//     listed, so nothing is hidden that could be downloaded honestly.
func (s *Server) importedRenditionEntry(probe *downloadProbe, imported *importedSources, id uuid.UUID, base string, rendition sqlcgen.VideoRendition) (videoDownloadFileView, bool, error) {
	rung, ok := imported.rung(rendition, true)
	if !ok {
		return videoDownloadFileView{}, false, nil
	}
	if rung.SplitAudio && !s.muxAvailable() {
		return videoDownloadFileView{}, false, nil
	}
	videoSize, exists, err := probe.size(rung.VideoKey)
	if err != nil || !exists {
		return videoDownloadFileView{}, false, err
	}
	if videoSize == 0 {
		videoSize = rendition.SizeBytes // a backend that cannot size an object
	}
	entry := videoDownloadFileView{
		Kind:        "hls",
		URL:         hlsDownloadURL(base, rendition.Height),
		Filename:    fmt.Sprintf("video-%s-%dp.mp4", id, rendition.Height),
		ContentType: media.HLSMP4ContentType,
		SizeBytes:   videoSize,
		Height:      rendition.Height,
		Width:       rendition.Width,
	}
	if !rung.SplitAudio {
		return entry, true, nil
	}
	audioSize, exists, err := probe.size(rung.AudioKey)
	if err != nil || !exists {
		return videoDownloadFileView{}, false, err
	}
	entry.SizeBytes += audioSize
	entry.VideoOnlyURL = hlsDownloadURL(base, rendition.Height) + "?audio=false"
	return entry, true, nil
}
