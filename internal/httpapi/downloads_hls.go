package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

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

// WithHLSMuxer enables on-request video+audio downloads for imported (split
// audio) HLS renditions. Without it those rungs are offered video-only.
func WithHLSMuxer(m HLSMuxer) Option {
	return func(s *Server) { s.hlsMuxer = m }
}

// importedSources lazily resolves a ready playlist's single-file MP4 objects, at
// most once per request: the master and variant manifests are read the first
// time anything asks, and never when every rung has a stored remux.
type importedSources struct {
	s         *Server
	ctx       context.Context
	masterKey string
	done      bool
	val       media.HLSDownloadSources
}

func (is *importedSources) get() media.HLSDownloadSources {
	if is.done {
		return is.val
	}
	is.done = true
	if is.s.media == nil {
		return is.val
	}
	val, err := media.ResolveHLSDownloadSources(is.ctx, is.s.media, is.masterKey)
	if err != nil {
		// Not an error for the caller: the video just has no fallback downloads.
		// The message never carries a presigned URL (manifests are read through
		// the storage backend) and is redacted like every other logged error.
		is.s.logger.Debug("hls download fallback unavailable", "error", jobstatus.RedactDetail(err.Error()))
		return is.val
	}
	is.val = val
	return val
}

// rung finds the resolved single-file rung for a rendition row, matching BOTH
// height and width (a portrait 720x1280 rendition is height 1280 / width 720).
// A row with no recorded width matches on height alone.
func (is *importedSources) rung(r sqlcgen.VideoRendition) (media.HLSDownloadRung, bool) {
	for _, candidate := range is.get().Rungs {
		if candidate.Height == int(r.Height) && (r.Width == 0 || candidate.Width == int(r.Width)) {
			return candidate, true
		}
	}
	return media.HLSDownloadRung{}, false
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
	imported := &importedSources{s: s, ctx: ctx, masterKey: sp.MasterKey}
	for _, rendition := range s.transcodesvc.Renditions(ctx, id) {
		if int(rendition.Height) != height {
			continue
		}
		key := media.HLSDownloadKey(rendition.KeyPrefix, includeAudio)
		if _, exists, err := s.downloadObjectSize(ctx, key); err != nil {
			return hlsDownload{}, err
		} else if exists {
			return hlsDownload{rendition: rendition, key: key}, nil
		}
		rung, ok := imported.rung(rendition)
		if !ok {
			return hlsDownload{}, notFound
		}
		if _, exists, err := s.downloadObjectSize(ctx, rung.VideoKey); err != nil {
			return hlsDownload{}, err
		} else if !exists {
			return hlsDownload{}, notFound
		}
		switch {
		case rung.AudioKey == "" && includeAudio:
			// The rung's own MP4 already carries its audio.
			return hlsDownload{rendition: rendition, key: rung.VideoKey}, nil
		case rung.AudioKey == "":
			// There is no video-only copy of a rung whose audio is inside it.
			return hlsDownload{}, notFound
		case !includeAudio:
			return hlsDownload{rendition: rendition, key: rung.VideoKey}, nil
		case s.hlsMuxer == nil:
			return hlsDownload{}, notFound
		}
		if _, exists, err := s.downloadObjectSize(ctx, rung.AudioKey); err != nil {
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
	imported := &importedSources{s: s, ctx: ctx, masterKey: sp.MasterKey}
	audioKey := imported.get().AudioKey
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

// serveHLSMux streams a video+audio MP4 built on request from two stored
// objects by ffmpeg stream copy. Nothing is stored, nothing is redirected (a
// presigned URL cannot point at a file that does not exist), and there is no
// Content-Length or Range support — the length is unknown until the process
// ends, so a Range request is answered 200 with the full body.
//
// Order matters: the caller has already authorised the request and resolved both
// objects; the slot is taken BEFORE the process starts; and the 200 header is
// not written until ffmpeg has produced its first bytes, so a failure to start
// (or an immediate exit) is a clean 502 rather than a 200 with a broken body.
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

	select {
	case s.muxSlots <- struct{}{}:
	default:
		header.Set("Retry-After", hlsMuxRetryAfter)
		return &DownloadBusyError{}
	}
	defer func() { <-s.muxSlots }()

	rc, err := s.hlsMuxer.Mux(c.Request().Context(), videoKey, audioKey)
	if err != nil {
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
	if _, werr := c.Response().Write(buf[:n]); werr != nil {
		return nil
	}
	// Headers are committed: whatever happens next (client gone, ffmpeg died
	// mid-file) can only end the connection, never change the status.
	if _, cerr := io.Copy(c.Response(), rc); cerr != nil && c.Request().Context().Err() == nil {
		s.logger.Warn("hls mux download ended early", "error", jobstatus.RedactDetail(cerr.Error()))
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
//     request, video_only_url the video object. size_bytes is the SUM of the two
//     objects — approximate, since the muxed file adds container overhead (and on
//     a remote backend an object's size is not known without a GET, so the
//     rendition row's recorded size stands in for the video part).
//   - audio split but no muxer wired (no ffmpeg on this host): the muxed URL
//     would be a 404, so the entry's url IS the video-only file, with a
//     video-only filename and no video_only_url. The separate audio entry is
//     still listed, so the viewer can take both parts instead of being offered a
//     link that cannot work.
func (s *Server) importedRenditionEntry(ctx context.Context, imported *importedSources, id uuid.UUID, base string, rendition sqlcgen.VideoRendition) (videoDownloadFileView, bool, error) {
	rung, ok := imported.rung(rendition)
	if !ok {
		return videoDownloadFileView{}, false, nil
	}
	videoSize, exists, err := s.downloadObjectSize(ctx, rung.VideoKey)
	if err != nil || !exists {
		return videoDownloadFileView{}, false, err
	}
	if videoSize == 0 {
		videoSize = rendition.SizeBytes
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
	if rung.AudioKey == "" {
		return entry, true, nil
	}
	audioSize, exists, err := s.downloadObjectSize(ctx, rung.AudioKey)
	if err != nil || !exists {
		return videoDownloadFileView{}, false, err
	}
	entry.SizeBytes += audioSize
	videoOnly := hlsDownloadURL(base, rendition.Height) + "?audio=false"
	if s.hlsMuxer != nil {
		entry.VideoOnlyURL = videoOnly
		return entry, true, nil
	}
	entry.URL = videoOnly
	entry.Filename = fmt.Sprintf("video-%s-%dp-video-only.mp4", id, rendition.Height)
	entry.SizeBytes = videoSize
	return entry, true, nil
}
