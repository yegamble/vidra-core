package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/live"
)

// Live HLS is written by the RTMP media server into LIVE_HLS_ROOT keyed by stream
// ID (the on-publish redirect renames the session to the id, so the raw key never
// lands on disk): the media playlist is "<id>.m3u8" and its segments are
// "<id>-<n>.ts". The api serves them read-only, gated by the stream's privacy and
// live state — mirroring the VOD /hls serving but sourced from the shared media
// volume rather than the object store.

// liveHLSPlaylistName is the on-disk playlist file for a stream id.
func liveHLSPlaylistName(id uuid.UUID) string { return id.String() + ".m3u8" }

// liveHLSFileAllowed reports whether a requested HLS file name belongs to the
// stream id and is a playlist or numbered segment — the only shapes the media
// server writes. The name must be a plain file (no separators / traversal) whose
// base is the id (playlist) or "<id>-<digits>" (segment). Anything else is 404,
// so nothing else under LIVE_HLS_ROOT is reachable.
func liveHLSFileAllowed(id uuid.UUID, name string) bool {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return false
	}
	ids := id.String()
	switch {
	case name == ids+".m3u8":
		return true
	case strings.HasPrefix(name, ids+"-") && strings.HasSuffix(name, ".ts"):
		seq := strings.TrimSuffix(strings.TrimPrefix(name, ids+"-"), ".ts")
		return seq != "" && isAllDigits(seq)
	default:
		return false
	}
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// liveStreamForHLS authorises serving a stream's live HLS assets: HLS serving
// must be configured (LIVE_HLS_ROOT set), the stream must exist and be visible to
// the caller (private → owner only), and it must currently be live. Every other
// case is 404 so a stream's existence/privacy is not leaked. Mirrors the VOD
// hlsPlaylistForView gate.
func (s *Server) liveStreamForHLS(c echo.Context, id uuid.UUID) (live.Stream, error) {
	notFound := echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	if s.livesvc == nil || s.cfg.LiveHLSRoot == "" {
		return live.Stream{}, notFound
	}
	stream, err := s.livesvc.Get(c.Request().Context(), id)
	if err != nil {
		return live.Stream{}, notFound
	}
	if stream.Privacy == "private" {
		userID, _, ok := principalFromContext(c)
		if !ok || userID != stream.OwnerID {
			return live.Stream{}, notFound
		}
	}
	if stream.State != live.StateLive {
		return live.Stream{}, notFound
	}
	return stream, nil
}

// handleGetLiveHLSMaster serves a live stream's HLS playlist ("<id>.m3u8"),
// exposed at the stable master.m3u8 path (a single-bitrate live playlist is
// served directly). Behind optionalAuth; gated by privacy + live state; 404 when
// LIVE_HLS_ROOT is unset.
func (s *Server) handleGetLiveHLSMaster(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	if _, err := s.liveStreamForHLS(c, id); err != nil {
		return err
	}
	return s.serveLiveHLSFile(c, id, liveHLSPlaylistName(id), contentTypeM3U8)
}

// handleGetLiveHLSFile serves one live HLS file: the media playlist referenced as
// "<id>.m3u8" or a numbered segment "<id>-<n>.ts". Names outside that fixed shape
// are 404. Same visibility/state gate as the master.
func (s *Server) handleGetLiveHLSFile(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	name := c.Param("file")
	if !liveHLSFileAllowed(id, name) {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	if _, err := s.liveStreamForHLS(c, id); err != nil {
		return err
	}
	contentType := contentTypeTS
	if strings.HasSuffix(name, ".m3u8") {
		contentType = contentTypeM3U8
	}
	return s.serveLiveHLSFile(c, id, name, contentType)
}

// serveLiveHLSFile streams a validated live HLS file from LIVE_HLS_ROOT. The name
// has already been checked to be a plain, id-scoped file, and the resolved path
// is re-verified to stay within the configured root before opening (defence in
// depth against traversal). Playlists are never stored because they mutate as
// the live session progresses. Sequence-named segments receive only a tiny
// private TTL: nginx-rtmp can reuse the same names after a stream restarts.
func (s *Server) serveLiveHLSFile(c echo.Context, id uuid.UUID, name, contentType string) error {
	notFound := echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	root, err := filepath.Abs(s.cfg.LiveHLSRoot)
	if err != nil {
		return err
	}
	path := filepath.Join(root, name)
	if path != root && !strings.HasPrefix(path, root+string(os.PathSeparator)) {
		return notFound
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return notFound
		}
		return err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return notFound
	}
	c.Response().Header().Set("Content-Type", contentType)
	if strings.HasSuffix(name, ".m3u8") {
		c.Response().Header().Set("Cache-Control", "no-cache, no-store")
	} else {
		c.Response().Header().Set("Cache-Control", "private, max-age=12")
	}
	http.ServeContent(c.Response(), c.Request(), info.Name(), info.ModTime(), file)
	return nil
}
