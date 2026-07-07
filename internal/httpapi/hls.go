package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// maxPlaylistBytes bounds an m3u8 read when rewriting it for a ?pt= playback
// token. Playlists are tiny (a handful of KiB); 1 MiB is a generous ceiling.
const maxPlaylistBytes = 1 << 20

// HLS media content types (RFC 8216).
const (
	contentTypeM3U8 = "application/vnd.apple.mpegurl"
	contentTypeTS   = "video/mp2t"
)

// The two file shapes a rendition directory contains: its variant playlist and
// its numbered MPEG-TS segments (as written by the transcoder). Anything else —
// including any traversal attempt — is 404. Rendition directories are named
// "<height>p" ("720p").
var (
	hlsRenditionName = regexp.MustCompile(`^[0-9]{2,4}p$`)
	hlsFileName      = regexp.MustCompile(`^(playlist\.m3u8|seg_[0-9]+\.ts)$`)
)

// hlsPlaylistForView authorises serving a video's HLS assets and returns its
// streaming playlist row. Visibility mirrors the /original endpoint: the video
// must exist and be visible to the caller (private → owner only, blocked →
// moderators only), and its playlist must be ready; every other case is 404 so
// existence is not leaked.
func (s *Server) hlsPlaylistForView(c echo.Context, id uuid.UUID) (sqlcgen.StreamingPlaylist, error) {
	notFound := echo.NewHTTPError(http.StatusNotFound, "video not found")
	if s.transcodesvc == nil {
		return sqlcgen.StreamingPlaylist{}, notFound
	}
	v, err := s.videosvc.GetByID(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, video.ErrNotFound) {
			return sqlcgen.StreamingPlaylist{}, notFound
		}
		return sqlcgen.StreamingPlaylist{}, err
	}
	if v.Privacy == "private" {
		userID, _, ok := principalFromContext(c)
		if !ok || userID != v.OwnerID {
			return sqlcgen.StreamingPlaylist{}, notFound
		}
	}
	if hidden, err := s.videoHiddenByBlock(c, id); err != nil {
		return sqlcgen.StreamingPlaylist{}, err
	} else if hidden {
		return sqlcgen.StreamingPlaylist{}, notFound
	}
	if quarantineHidesVideo(c, v.State, v.OwnerID) {
		return sqlcgen.StreamingPlaylist{}, notFound
	}
	// Password-protected videos: owner/mod or a valid playback token (Bearer or
	// ?pt=) only; everyone else gets 401 password_required (CORE-17 / W1.C2).
	if err := s.passwordGate(c, id, v.Privacy, v.OwnerID); err != nil {
		return sqlcgen.StreamingPlaylist{}, err
	}
	sp, ok := s.transcodesvc.Playlist(c.Request().Context(), id)
	if !ok || sp.State != "ready" || sp.MasterKey == "" {
		return sqlcgen.StreamingPlaylist{}, notFound
	}
	return sp, nil
}

// handleGetHLSMaster serves a video's HLS master playlist. Behind optionalAuth:
// visibility mirrors the /original endpoint (private → owner only, else 404); a
// video whose playlist is not ready is 404 — the detail response's hls_url
// tells clients when one exists.
func (s *Server) handleGetHLSMaster(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	sp, err := s.hlsPlaylistForView(c, id)
	if err != nil {
		return err
	}
	return s.serveHLSPlaylist(c, sp.MasterKey)
}

// handleGetHLSFile serves one rendition file: a variant playlist
// (:rendition/playlist.m3u8) or an MPEG-TS segment (:rendition/seg_NNNNN.ts).
// Playlist URIs are relative, so players resolve segment requests to this same
// route. Same visibility as the master playlist; path parts that do not match
// the transcoder's fixed naming are 404 (nothing else under the prefix is
// reachable).
func (s *Server) handleGetHLSFile(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	rendition, file := c.Param("rendition"), c.Param("file")
	if !hlsRenditionName.MatchString(rendition) || !hlsFileName.MatchString(file) {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if _, err := s.hlsPlaylistForView(c, id); err != nil {
		return err
	}
	key := "streaming-playlists/" + id.String() + "/" + rendition + "/" + file
	// A variant playlist gets the same ?pt= URI rewrite as the master (so the
	// native player propagates the token to segment requests); a .ts segment is
	// binary and streamed as-is.
	if file == "playlist.m3u8" {
		return s.serveHLSPlaylist(c, key)
	}
	return s.serveStoredObject(c, key, contentTypeTS)
}

// serveHLSPlaylist streams an m3u8. When the request carries a ?pt= playback
// token, it reads the playlist and appends ?pt=<token> to every RELATIVE URI line
// (variant playlists in the master, segments in a variant) so a header-less
// native-HLS player keeps carrying the token through the whole chain — the
// adaptation that makes password-protected HLS work in Safari (CORE-17 / W1.C2).
// Without a ?pt= it streams the stored playlist unchanged (Range-capable).
func (s *Server) serveHLSPlaylist(c echo.Context, key string) error {
	token := c.QueryParam(playbackTokenParam)
	if token == "" {
		return s.serveStoredObject(c, key, contentTypeM3U8)
	}
	if s.media == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "media storage not configured")
	}
	rc, err := s.media.Open(c.Request().Context(), key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
		return err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, maxPlaylistBytes))
	if err != nil {
		return err
	}
	return c.Blob(http.StatusOK, contentTypeM3U8, rewritePlaylistToken(data, token))
}

// rewritePlaylistToken appends ?pt=<token> to every relative URI line of an m3u8.
// Tag lines (#...), blank lines, and absolute URLs (http(s):// or /-rooted) are
// left untouched. It preserves the original line order and terminator style.
func rewritePlaylistToken(playlist []byte, token string) []byte {
	esc := url.QueryEscape(token)
	lines := strings.Split(string(playlist), "\n")
	for i, line := range lines {
		cr := strings.HasSuffix(line, "\r")
		uri := strings.TrimSuffix(line, "\r")
		if uri == "" || strings.HasPrefix(uri, "#") ||
			strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") ||
			strings.HasPrefix(uri, "/") {
			continue
		}
		sep := "?"
		if strings.ContainsRune(uri, '?') {
			sep = "&"
		}
		uri += sep + playbackTokenParam + "=" + esc
		if cr {
			uri += "\r"
		}
		lines[i] = uri
	}
	return []byte(strings.Join(lines, "\n"))
}

// renditionView is the public projection of an available HLS rendition.
type renditionView struct {
	Height int32 `json:"height"`
	Width  int32 `json:"width"`
}

// hlsDetail returns the video-detail HLS fields: the master playlist URL and
// the available renditions, but only once the playlist is ready (nil/absent
// otherwise, including when transcoding is not wired).
func (s *Server) hlsDetail(c echo.Context, id uuid.UUID) (*string, []renditionView) {
	if s.transcodesvc == nil {
		return nil, nil
	}
	sp, ok := s.transcodesvc.Playlist(c.Request().Context(), id)
	if !ok || sp.State != "ready" || sp.MasterKey == "" {
		return nil, nil
	}
	url := "/api/v1/videos/" + id.String() + "/hls/master.m3u8"
	var rends []renditionView
	for _, r := range s.transcodesvc.Renditions(c.Request().Context(), id) {
		rends = append(rends, renditionView{Height: r.Height, Width: r.Width})
	}
	return &url, rends
}
