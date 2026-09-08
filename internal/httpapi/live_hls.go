package httpapi

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// publicLiveStream is the live plane's public-and-servable assertion — the same
// meaning publicVideoForIPFS carries for VOD, and the input to both the shared
// cache policy and the cross-origin CORS header. A live stream is public only
// when its privacy says so; unlisted and private streams are reachable by id or
// token but are NOT anonymous public media, so they carry no CORS header.
// liveStreamForHLS has already refused anything that is not live.
func publicLiveStream(stream live.Stream) bool {
	return stream.Privacy == "public"
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
	id, err := pathUUID(c, "id", "live stream not found")
	if err != nil {
		return err
	}
	stream, err := s.liveStreamForHLS(c, id)
	if err != nil {
		return err
	}
	s.countLiveViewer(c, id)
	return s.serveLiveHLSFile(c, liveHLSPlaylistName(id), publicLiveStream(stream))
}

// handleGetLiveHLSFile serves one live HLS file: the media playlist referenced as
// "<id>.m3u8" or a numbered segment "<id>-<n>.ts". Names outside that fixed shape
// are 404. Same visibility/state gate as the master.
func (s *Server) handleGetLiveHLSFile(c echo.Context) error {
	id, err := pathUUID(c, "id", "live stream not found")
	if err != nil {
		return err
	}
	name := c.Param("file")
	if !liveHLSFileAllowed(id, name) {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	stream, err := s.liveStreamForHLS(c, id)
	if err != nil {
		return err
	}
	// The same playlist the master route serves, reached by its on-disk name —
	// a player that followed the master's own URI lands here, so counting only
	// the master route would miss every one of them. Segments are deliberately
	// not counted; see countLiveViewer.
	if strings.HasSuffix(name, ".m3u8") {
		s.countLiveViewer(c, id)
	}
	return s.serveLiveHLSFile(c, name, publicLiveStream(stream))
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
func (s *Server) serveLiveHLSFile(c echo.Context, name string, eligible bool) error {
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
		return s.serveLiveHLSPlaylist(c, file, eligible)
	}
	// A segment is opaque bytes on a stable, unversioned URL, so it revalidates
	// rather than carrying a TTL. That is stricter than the 12-second window this
	// route used to hand out, and deliberately: nginx-rtmp reuses segment names
	// after a restart, so any freshness window is a window in which a viewer can
	// be served the PREVIOUS broadcast's bytes under the right name. Revalidation
	// costs a conditional request that ServeContent answers with a 304, and a live
	// player fetches each segment once anyway.
	setMediaCacheControl(c, delivery.ClassHLSSegment, eligible)
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
func (s *Server) serveLiveHLSPlaylist(c echo.Context, file *os.File, eligible bool) error {
	data, err := io.ReadAll(io.LimitReader(file, maxPlaylistBytes))
	if err != nil {
		return err
	}
	setMediaCacheControl(c, delivery.ClassHLSPlaylist, eligible)
	return c.Blob(http.StatusOK, contentTypeM3U8,
		rewritePlaylistToken(data, c.QueryParam(playbackTokenParam)))
}

// liveViewerDigest resolves the pseudonym one live viewer is counted under.
//
// It reuses qoeViewerDigest's consent question — searchConsent, the SAME
// predicate the settings page's promise is made of — but NOT its answer for an
// opted-out viewer. QoE stores the EMPTY digest for them, which is right for a
// persisted telemetry row: the row still lands, and the field that identifies
// anybody is simply blank.
//
// Blank cannot work here, because this digest is a SET MEMBER. Every opted-out
// viewer on the instance would collapse into one member and the count would
// under-report them — and A35's rule for this surface is that opted-out viewers
// COUNT. So they are digested as the anonymous visitor A13 says they asked to be
// treated as: no account-derived value, a distinct member, an honest count.
//
// WHO ACTUALLY REACHES THE SIGNED-IN BRANCH: on a PRIVATE stream, whose playlist
// is fetched with the playback token, and nobody else. A26 measured this
// directly — an anonymous viewer and a signed-in viewer on one address produced
// ONE set member — because hls.js sends no session credential with a public
// stream's playlist: it sets an Authorization header only when a playback token
// exists (lib/use-playback-engine.ts), never `withCredentials`, and the session
// is a bearer access token rather than a cookie, so a same-origin fetch carries
// nothing either. A signed-in viewer of a PUBLIC stream is therefore counted as
// an anonymous one, and the only way to change that would be a per-viewer
// credential on the playlist — which A08 ruled out for the delivery path and
// which would be a new tracking surface bought for a rounder number.
//
// So the anonymous principal is what almost every live viewer is counted by, and
// it is the IP AND the User-Agent rather than the IP alone. One household, one
// office or one mobile carrier NAT behind a single address collapsed into a
// single viewer; the UA splits the common cases (a phone and a laptop, Safari
// and Chrome) at no cost to anyone's privacy — the two are HMAC'd together into
// the same day-scoped digest, the UA is never stored or logged, and a value that
// was already re-derived every UTC day stays exactly as ephemeral. It is not a
// fix, and nothing here pretends it is: two identical phones on one Wi-Fi still
// count once. It is a smaller undercount than the one A26 measured.
//
// The half of the principal the client controls. A User-Agent is chosen by the
// client, so one host can mint several members by rotating it — the exact
// objection that ruled out the client-minted `?s=` session id. It is accepted
// here because the IP-only digest was never inflation-proof either: RealIP is
// per request, and any client on an IPv6 /64 already had 2^64 addresses to spend
// on it. The UA does not change the class of the threat, only how cheaply an
// IPv4-bound client reaches it — and that client is precisely the one this
// change exists to stop under-counting. What keeps it proportionate is what the
// number is for: a creator-facing estimate on a page, never an input to billing,
// ranking or moderation.
func (s *Server) liveViewerDigest(c echo.Context, now time.Time) string {
	if s.liveViewers == nil {
		return ""
	}
	viewerID, prefs, authed := s.searchUserPrefs(c)
	if authed {
		if allowHistory, allowPersonalization := s.searchConsent(prefs, authed); !allowHistory && !allowPersonalization {
			authed = false
		}
	}
	if authed && viewerID != uuid.Nil {
		return s.liveViewers.Of(now, "u:"+viewerID.String())
	}
	return s.liveViewers.Of(now, anonymousViewerPrincipal(c))
}

// anonymousViewerPrincipal is the pre-digest principal for a viewer with no
// usable account identity: the client address and the User-Agent, separated by a
// byte neither can contain.
//
// The separator matters more than it looks. Concatenating the two without one
// lets a crafted UA impersonate another address's principal ("203.0.113.7" +
// "x" vs "203.0.113." + "7x"), and while the worst that buys is a miscount of a
// live audience, the fix is one byte. The UA is bounded because it is
// attacker-controlled and a header-sized principal has no reason to reach the
// MAC.
func anonymousViewerPrincipal(c echo.Context) string {
	ua := strings.TrimSpace(c.Request().UserAgent())
	if len(ua) > maxViewerUserAgentBytes {
		ua = ua[:maxViewerUserAgentBytes]
	}
	return "ip:" + strings.TrimSpace(c.RealIP()) + "\x00ua:" + ua
}

// maxViewerUserAgentBytes bounds the attacker-controlled half of the principal.
// Real User-Agents are well under 256 bytes; a longer one is a client trying to
// be several viewers, and truncation costs it nothing it should have had.
const maxViewerUserAgentBytes = 256

// countLiveViewer records that whoever made this request is watching.
//
// It runs on the PLAYLIST fetch only — a live player refetches the playlist
// every couple of seconds for exactly as long as it is watching, which is the
// only request in the live path that means "still here". Segment fetches would
// measure bandwidth and GET /live/{id} would measure page views.
//
// It is fire-and-forget: a Redis failure degrades the count, never the
// playback, so the error is logged at debug and the playlist is served
// regardless. Nothing downstream waits on it.
func (s *Server) countLiveViewer(c echo.Context, streamID uuid.UUID) {
	if s.livesvc == nil {
		return
	}
	counter := s.livesvc.Viewers()
	if counter == nil {
		return
	}
	digest := s.liveViewerDigest(c, time.Now())
	if digest == "" {
		return
	}
	if err := counter.Touch(c.Request().Context(), streamID, digest); err != nil {
		s.logger.DebugContext(c.Request().Context(), "live viewer count: could not record a viewer",
			"stream_id", streamID.String(), "error", err)
	}
}
