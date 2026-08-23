package httpapi

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/delivery"
	"github.com/vidra/vidra-core/internal/live"
	"github.com/vidra/vidra-core/internal/playback"
)

// Live HLS is written by the RTMP media server into LIVE_HLS_ROOT keyed by stream
// ID (the on-publish redirect renames the session to the id, so the raw key never
// lands on disk): the media playlist is "<id>.m3u8" and its segments are
// "<id>-<n>.ts". The api serves them read-only, gated by the stream's privacy and
// live state — mirroring the VOD /hls serving but sourced from the shared media
// volume rather than the object store.

// liveHLSPlaylistName is the on-disk playlist file for a stream id.
func liveHLSPlaylistName(id uuid.UUID) string { return id.String() + ".m3u8" }

// liveHLSMasterURL is the origin-relative path a client fetches a live stream's
// playlist from. One function so the live detail, the "Live now" rail and the
// playback session cannot drift into advertising three different URLs.
func liveHLSMasterURL(id uuid.UUID) string {
	return "/api/v1/live/" + id.String() + "/hls/master.m3u8"
}

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
// the caller (private → owner or a live playback token), and it must currently be
// live. Every other case is 404 so a stream's existence/privacy is not leaked.
// Mirrors the VOD hlsPlaylistForView gate.
//
// The live state check is deliberately AFTER the credential check and applies to
// everyone: a playback token widens WHO may watch, never WHEN. That is what
// bounds the credential — it grants nothing once the broadcast ends, so ending a
// stream revokes every token outstanding against it.
func (s *Server) liveStreamForHLS(c echo.Context, id uuid.UUID) (live.Stream, error) {
	notFound := echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	if s.livesvc == nil || s.cfg.LiveHLSRoot == "" {
		return live.Stream{}, notFound
	}
	stream, err := s.livesvc.Get(c.Request().Context(), id)
	if err != nil {
		return live.Stream{}, notFound
	}
	if liveStreamRequiresPlaybackToken(stream) && !s.liveViewerAuthorized(c, stream) {
		return live.Stream{}, notFound
	}
	if stream.State != live.StateLive {
		return live.Stream{}, notFound
	}
	return stream, nil
}

// liveStreamRequiresPlaybackToken reports whether this stream's media is
// unreachable without either an account identity or a ?pt= credential — which is
// exactly the private tier. Public and unlisted live streams are readable by
// anyone holding the id, so minting a token for one would buy nothing and would
// mark every subsequent segment request credentialed (no-store, never
// redirected), which is the conditional-token rule the VOD session established.
//
// It is also the ONLY predicate that decides whether a live session hands out a
// credential, so the gate and the mint can never disagree about which streams
// need one.
func liveStreamRequiresPlaybackToken(stream live.Stream) bool {
	return stream.Privacy == "private"
}

// liveViewerAuthorized reports whether the caller may watch a private live
// stream: its owner (by account identity, which a media element carries in the
// session cookie), or the bearer of a live-scoped playback token for this exact
// stream.
//
// The token is what gives live a private-but-shareable tier. Live has no
// `password` privacy tier — validateLiveFields allows only public/unlisted/
// private — so before this there was no way to let one other person watch a
// private broadcast, and no way to hand out an access that expires.
func (s *Server) liveViewerAuthorized(c echo.Context, stream live.Stream) bool {
	if userID, _, ok := principalFromContext(c); ok && userID == stream.OwnerID {
		return true
	}
	return s.hasPlaybackToken(c, stream.ID, playback.ScopeLive)
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
	return s.serveLiveHLSFile(c, liveHLSPlaylistName(id))
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
	return s.serveLiveHLSFile(c, name)
}

// serveLiveHLSFile streams a validated live HLS file from LIVE_HLS_ROOT. The name
// has already been checked to be a plain, id-scoped file, and the resolved path
// is re-verified to stay within the configured root before opening (defence in
// depth against traversal).
//
// Cache policy comes from internal/delivery, not from literals here, so "why is
// this response private?" has one answer for live and VOD alike — including the
// rule that matters most: a request carrying ?pt= or an Authorization header is
// credentialed and its response is never stored anywhere.
//
// Live does NOT go through the delivery RESOLVER, and that is deliberate. These
// bytes never enter storage.Backend during a broadcast: they are ephemeral, live
// in a ~12-second window, have no ObjectKey and no generation version, and
// nginx-rtmp reuses their names after a restart. There is nothing to presign,
// nothing to mirror and nothing to version, so a delivery.Request describing them
// would have to invent a non-storage source kind. See docs/operations.md, "The
// live plane is single-host".
func (s *Server) serveLiveHLSFile(c echo.Context, name string) error {
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
	if strings.HasSuffix(name, ".m3u8") {
		return s.serveLiveHLSPlaylist(c, file)
	}
	// A segment is opaque bytes on a stable, unversioned URL, so it revalidates
	// rather than carrying a TTL. That is stricter than the 12-second window this
	// route used to hand out, and deliberately: nginx-rtmp reuses segment names
	// after a restart, so any freshness window is a window in which a viewer can
	// be served the PREVIOUS broadcast's bytes under the right name. Revalidation
	// costs a conditional request that ServeContent answers with a 304, and a live
	// player fetches each segment once anyway.
	setMediaCacheControl(c, delivery.ClassHLSSegment)
	c.Response().Header().Set("Content-Type", contentTypeTS)
	http.ServeContent(c.Response(), c.Request(), info.Name(), info.ModTime(), file)
	return nil
}

// serveLiveHLSPlaylist serves the live media playlist, rewriting its relative
// segment URIs to carry the request's ?pt= token.
//
// The rewrite is what makes a live playback token work at all, and it is not
// optional. A relative URI inside a playlist resolves against the playlist's URL
// per RFC 3986 §5.2.2, which DISCARDS the base's query string — so "?pt=" on
// master.m3u8 does not reach "<id>-7.ts", in any client. Safari's native HLS
// player, the one client that cannot set an Authorization header and the entire
// reason ?pt= exists, has no hook to add it back. This is the same reason
// serveHLSPlaylist rewrites the VOD playlists (CORE-17), reusing the same
// rewriter.
//
// Rewriting a file nginx-rtmp replaces every fragment is safe: this is a read,
// not a read-modify-write — nothing is written back — and the module writes each
// new playlist to "<name>.bak" and rename(2)s it into place, so a reader sees one
// complete generation or the other, never a torn one. The playlist is a handful
// of lines and it is revalidated on every use anyway, so re-rendering it per
// request costs nothing worth measuring.
//
// It is served with c.Blob rather than http.ServeContent for the same reason the
// VOD playlists are: the bytes on the wire are not the bytes on disk, so the
// file's size and mtime are not validators for what is being sent.
func (s *Server) serveLiveHLSPlaylist(c echo.Context, file *os.File) error {
	data, err := io.ReadAll(io.LimitReader(file, maxPlaylistBytes))
	if err != nil {
		return err
	}
	setMediaCacheControl(c, delivery.ClassHLSPlaylist)
	return c.Blob(http.StatusOK, contentTypeM3U8,
		rewritePlaylistToken(data, c.QueryParam(playbackTokenParam)))
}
