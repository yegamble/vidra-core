package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/delivery"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// maxPlaylistBytes bounds an m3u8 read while references are rewritten. Dense
// trick-play indexes add one byte-range entry per second, so multi-hour videos
// can legitimately be larger than an ordinary variant playlist.
const maxPlaylistBytes = 16 << 20

// HLS media content types (RFC 8216).
const (
	contentTypeM3U8 = "application/vnd.apple.mpegurl"
	contentTypeTS   = "video/mp2t"
	contentTypeMP4  = "video/mp4"
	// contentTypeMPD is the MPEG-DASH manifest type (ISO/IEC 23009-1).
	contentTypeMPD  = "application/dash+xml"
	hlsVersionParam = "v"

	// HLS routes remain authorization gates, so their immutable cache entries
	// are browser-private. A deployment may promote these to shared-CDN entries
	// only when it also purges old generations on privacy changes and deletion —
	// which is why delivery.Resolver carries a Purge hook from day one. The
	// values live in internal/delivery so every media route's cache policy has
	// exactly one definition.
	hlsVersionedCacheControl = delivery.CacheVersionedImmutable
	hlsStableCacheControl    = delivery.CacheStableRevalidate
)

// The file shapes reachable under a video's HLS prefix. Anything else —
// including any traversal attempt — is 404.
//
// hlsRenditionName/hlsFileName are the MPEG-TS tree: rendition directories named
// "<height>p" holding a variant playlist, the trick-play pair, and numbered
// segments.
//
// hlsCMAFFileName is the CMAF tree, which lives in ONE directory ("cmaf") under
// the same prefix because HLS and DASH must resolve the SAME segments relative
// to their own manifests. It is deliberately an exhaustive list of the six names
// the packager emits rather than a permissive "anything that looks like a media
// file": this route is an authorization boundary, and the only reason the
// PeerTube pass-through below gets a loose pattern is that its tree was written
// by another program whose naming Vidra does not control.
//
// The representation index is unbounded on purpose. A tree has one video
// representation per ladder rung PER CODEC (TRANSCODING_HEVC_ENABLED /
// TRANSCODING_AV1_ENABLED) plus the shared audio one, so "how many are there"
// is a packaging decision this route must not restate — the names are what is
// pinned, not the count. media's own tests assert every stored object matches
// this pattern.
var (
	hlsRenditionName = regexp.MustCompile(`^[0-9]{2,4}p$`)
	hlsFileName      = regexp.MustCompile(`^(playlist\.m3u8|iframe\.m3u8|iframe\.ts|seg_[0-9]+\.ts)$`)
	hlsCMAFFileName  = regexp.MustCompile(
		`^(stream\.mpd|media_[0-9]+\.m3u8|init-[0-9]+\.mp4|chunk-[0-9]+-[0-9]+\.m4s|iframe-[0-9]+\.m3u8|iframe-[0-9]+\.mp4)$`)
	hlsPeerTubeFileName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(m3u8|mp4|m4s|ts)$`)
	hlsPlaylistURIAttrRE = regexp.MustCompile(`URI="([^"]+)"`)
)

// hlsCMAFRendition is the pseudo-rendition the CMAF directory is served under,
// so /videos/{id}/hls/cmaf/stream.mpd is the DASH manifest and every relative
// URI inside it resolves against its siblings with no rewriting at all.
const hlsCMAFRendition = "cmaf"

// hlsCMAFManifestFile is the manifest's name within that directory, so the
// canonical DASH URL is /api/v1/videos/{id}/hls/cmaf/stream.mpd.
const hlsCMAFManifestFile = "stream.mpd"

// hlsPlaylistForView authorises serving a video's HLS assets and returns its
// streaming playlist row together with the video row the authorization decision
// was made on. Visibility mirrors the /original endpoint: the video must exist
// and be visible to the caller (private → owner only, blocked → moderators
// only), and its playlist must be ready; every other case is 404 so existence is
// not leaked.
//
// The video row is returned rather than discarded because segment delivery needs
// the SAME row the gate used to decide whether these bytes are public — deriving
// that from a second lookup is how an eligibility fence and an auth check drift
// apart.
func (s *Server) hlsPlaylistForView(c echo.Context, id uuid.UUID) (sqlcgen.StreamingPlaylist, sqlcgen.GetVideoByIDRow, error) {
	notFound := echo.NewHTTPError(http.StatusNotFound, "video not found")
	if s.transcodesvc == nil {
		return sqlcgen.StreamingPlaylist{}, sqlcgen.GetVideoByIDRow{}, notFound
	}
	v, err := s.videoVisibleForMedia(c, id)
	if err != nil {
		return sqlcgen.StreamingPlaylist{}, sqlcgen.GetVideoByIDRow{}, err
	}
	sp, ok := s.transcodesvc.Playlist(c.Request().Context(), id)
	if !ok || sp.State != "ready" || sp.MasterKey == "" {
		return sqlcgen.StreamingPlaylist{}, sqlcgen.GetVideoByIDRow{}, notFound
	}
	return sp, v, nil
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
	sp, _, err := s.hlsPlaylistForView(c, id)
	if err != nil {
		return err
	}
	if err := validateHLSVersion(c, sp); err != nil {
		return err
	}
	if isPeerTubeHLSMasterKey(sp.MasterKey) {
		return s.servePeerTubeHLSMaster(c, sp)
	}
	return s.serveHLSPlaylist(c, sp.MasterKey, sp)
}

// handleGetHLSFile serves one file under a video's streaming prefix: an MPEG-TS
// rendition's variant playlist or segment (:rendition/playlist.m3u8,
// :rendition/seg_NNNNN.ts), or — under the "cmaf" pseudo-rendition — the shared
// CMAF tree's DASH manifest, media playlists, init segments and media segments.
// Playlist and manifest URIs are relative, so players resolve every follow-up
// request back to this same route. Same visibility as the master playlist; path
// parts that do not match the transcoder's fixed naming are 404 (nothing else
// under the prefix is reachable).
func (s *Server) handleGetHLSFile(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	rendition, file := c.Param("rendition"), c.Param("file")
	canonical := hlsRenditionName.MatchString(rendition) && hlsFileName.MatchString(file)
	cmaf := rendition == hlsCMAFRendition && hlsCMAFFileName.MatchString(file)
	peertube := rendition == "peertube" && hlsPeerTubeFileName.MatchString(file)
	if !canonical && !cmaf && !peertube {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	sp, v, err := s.hlsPlaylistForView(c, id)
	if err != nil {
		return err
	}
	if err := validateHLSVersion(c, sp); err != nil {
		return err
	}
	// Cross-checks: a rendition names a tree SHAPE, and asking for one shape's
	// names against a video stored in the other must not reach storage and hope
	// for a miss. Both directions, so the rule is a property of the route rather
	// than a guard that happens to exist for the new format: the CMAF and MPEG-TS
	// naming schemes are mutually exclusive and each is refused on the format the
	// transcode recorded. The PeerTube pass-through has no format of its own and
	// is fenced on the imported tree's master-key prefix instead.
	if cmaf != (sp.Format == media.HLSFormatCMAF) && !peertube {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if peertube && !isPeerTubeHLSMasterKey(sp.MasterKey) {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	prefix := path.Dir(sp.MasterKey)
	key := prefix + "/" + rendition + "/" + file
	if peertube {
		key = prefix + "/" + file
	}
	// A variant playlist gets the same ?pt= URI rewrite as the master (so the
	// native player propagates the token to segment requests), which is exactly
	// why a playlist can never be delivered from anywhere but here; a segment is
	// opaque binary and goes through the delivery resolver.
	if strings.HasSuffix(file, ".m3u8") {
		return s.serveHLSPlaylist(c, key, sp)
	}
	if cmaf && file == hlsCMAFManifestFile {
		return s.serveCMAFManifest(c, key, sp)
	}
	// .m4s segments are labelled video/mp4 rather than the registered
	// video/iso.segment: Apple's HLS authoring specification asks for video/mp4
	// on fMP4 segments, Safari is the client the CMAF master's CODECS attributes
	// exist for in the first place, and it matches what the PeerTube
	// pass-through route already returns for the same suffix.
	contentType := contentTypeMP4
	if strings.HasSuffix(file, ".ts") {
		contentType = contentTypeTS
	}
	// No mirror class: the pin ledger mirrors an HLS ladder as one directory
	// CID, not per-segment objects, so there is no per-segment gateway URL to
	// look up. Segments still qualify for a presigned redirect.
	return s.serveMediaAsset(c, mediaAsset{
		key:         key,
		contentType: contentType,
		class:       delivery.ClassHLSSegment,
		eligible:    publicVideoForIPFS(v.Privacy, v.State),
		versioned:   hlsVersionMatches(c, sp),
		notFound:    "video not found",
	})
}

// serveHLSPlaylist streams an m3u8. When the request carries a ?pt= playback
// token, it reads the playlist and appends ?pt=<token> to every RELATIVE URI line
// (variant playlists in the master, segments in a variant) so a header-less
// native-HLS player keeps carrying the token through the whole chain — the
// adaptation that makes password-protected HLS work in Safari (CORE-17 / W1.C2).
// Every response also rewrites relative references with the playlist generation
// version. That makes variant and segment URLs immutable within a generation;
// the unversioned compatibility route remains revalidated on every use.
func (s *Server) serveHLSPlaylist(c echo.Context, key string, sp sqlcgen.StreamingPlaylist) error {
	token := c.QueryParam(playbackTokenParam)
	version := hlsCacheVersion(sp)
	setHLSCacheControl(c, sp)
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
	return c.Blob(http.StatusOK, contentTypeM3U8, rewritePlaylistReferences(data, "", token, version, false))
}

// serveCMAFManifest streams a video's DASH manifest from the origin.
//
// Unlike an m3u8 it is served VERBATIM. Its segment references are
// SegmentTemplate patterns ("init-$RepresentationID$.mp4"), not URIs a player
// follows literally, so the ?pt=/?v= rewrite that makes header-less native HLS
// work has nothing to rewrite here — a DASH player expands the template itself
// and requests the sibling unversioned. That is correct but it does mean DASH
// playback of a password-protected video needs a player that can attach the
// token, which is phase-4's problem when one is wired; the bytes themselves are
// gated by exactly the same authorization as every other route here.
//
// Origin-served rather than presign-redirected for the same reason playlists
// are: a manifest is the thing that decides what a client asks for next, so it
// stays on the authoritative path.
func (s *Server) serveCMAFManifest(c echo.Context, key string, sp sqlcgen.StreamingPlaylist) error {
	setHLSCacheControl(c, sp)
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
	return c.Blob(http.StatusOK, contentTypeMPD, data)
}

func (s *Server) servePeerTubeHLSMaster(c echo.Context, sp sqlcgen.StreamingPlaylist) error {
	setHLSCacheControl(c, sp)
	if s.media == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "media storage not configured")
	}
	rc, err := s.media.Open(c.Request().Context(), sp.MasterKey)
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
	return c.Blob(http.StatusOK, contentTypeM3U8, rewritePeerTubeMasterPlaylist(
		data,
		c.QueryParam(playbackTokenParam),
		hlsCacheVersion(sp),
	))
}

func isPeerTubeHLSMasterKey(key string) bool {
	return strings.HasPrefix(key, "streaming-playlists/hls/")
}

// rewritePlaylistToken appends ?pt=<token> to every relative URI in an m3u8,
// including URI="..." tag attributes such as EXT-X-MAP. Blank lines and
// absolute/rooted URLs are left untouched. It preserves line order.
func rewritePlaylistToken(playlist []byte, token string) []byte {
	return rewritePlaylistReferences(playlist, "", token, "", false)
}

// rewritePeerTubeMasterPlaylist maps PeerTube's flat HLS master playlist entries
// into Vidra's compatibility route: peertube/<basename>. Variant playlists then
// keep resolving their own segment/media URIs relative to /hls/peertube/.
func rewritePeerTubeMasterPlaylist(playlist []byte, token, version string) []byte {
	return rewritePlaylistReferences(playlist, "peertube", token, version, true)
}

func rewritePlaylistReferences(playlist []byte, uriPrefix, token, version string, forceBasename bool) []byte {
	query := url.Values{}
	if token != "" {
		query.Set(playbackTokenParam, token)
	}
	if version != "" {
		query.Set(hlsVersionParam, version)
	}
	escapedQuery := query.Encode()
	lines := strings.Split(string(playlist), "\n")
	for i, line := range lines {
		cr := strings.HasSuffix(line, "\r")
		uri := strings.TrimSuffix(line, "\r")
		if uri == "" {
			continue
		}
		if strings.HasPrefix(uri, "#") {
			rewritten := rewritePlaylistURIAttributes(uri, uriPrefix, escapedQuery, forceBasename)
			if cr {
				rewritten += "\r"
			}
			lines[i] = rewritten
			continue
		}
		rewritten, ok := rewritePlaylistURI(uri, uriPrefix, escapedQuery, forceBasename)
		if !ok {
			continue
		}
		if cr {
			rewritten += "\r"
		}
		lines[i] = rewritten
	}
	return []byte(strings.Join(lines, "\n"))
}

func rewritePlaylistURIAttributes(line, uriPrefix, escapedQuery string, forceBasename bool) string {
	return hlsPlaylistURIAttrRE.ReplaceAllStringFunc(line, func(match string) string {
		raw := strings.TrimSuffix(strings.TrimPrefix(match, `URI="`), `"`)
		rewritten, ok := rewritePlaylistURI(raw, uriPrefix, escapedQuery, forceBasename)
		if !ok {
			return match
		}
		return `URI="` + rewritten + `"`
	})
}

func rewritePlaylistURI(raw, uriPrefix, escapedQuery string, forceBasename bool) (string, bool) {
	if raw == "" {
		return raw, false
	}
	if uriPrefix == "" && !forceBasename && !isRelativePlaylistURI(raw) {
		return raw, false
	}
	head, suffix := splitURISuffix(raw)
	if forceBasename {
		head = uriBasename(head)
	}
	if uriPrefix != "" {
		head = strings.TrimSuffix(uriPrefix, "/") + "/" + path.Base(head)
	}
	out := head + suffix
	if escapedQuery == "" {
		return out, true
	}
	frag := ""
	if idx := strings.IndexRune(out, '#'); idx >= 0 {
		frag = out[idx:]
		out = out[:idx]
	}
	sep := "?"
	if strings.ContainsRune(out, '?') {
		sep = "&"
	}
	return out + sep + escapedQuery + frag, true
}

// hlsCacheVersion identifies one persisted HLS generation. UpdatedAt changes on
// every playlist upsert; the hash fallback keeps hand-built/import fixtures and
// legacy zero timestamps deterministic without exposing a storage key.
func hlsCacheVersion(sp sqlcgen.StreamingPlaylist) string {
	if !sp.UpdatedAt.IsZero() {
		return strconv.FormatInt(sp.UpdatedAt.UnixNano(), 36)
	}
	sum := sha256.Sum256([]byte(sp.MasterKey))
	return hex.EncodeToString(sum[:8])
}

func validateHLSVersion(c echo.Context, sp sqlcgen.StreamingPlaylist) error {
	requested := c.QueryParam(hlsVersionParam)
	if requested == "" || requested == hlsCacheVersion(sp) {
		return nil
	}
	// Never serve a new generation under an old immutable URL.
	return echo.NewHTTPError(http.StatusNotFound, "video not found")
}

// setHLSCacheControl applies the HLS cache policy to a playlist response. The
// rules are unchanged; they are now expressed once, in internal/delivery, so a
// playlist served here and a segment served through the delivery resolver
// cannot drift apart.
func setHLSCacheControl(c echo.Context, sp sqlcgen.StreamingPlaylist) {
	c.Response().Header().Set("Cache-Control", delivery.CacheControl(
		delivery.ClassHLSPlaylist,
		hlsVersionMatches(c, sp),
		credentialedMediaRequest(c),
	))
}

// hlsVersionMatches reports whether the request carries this playlist
// generation's version, which is what makes an HLS URL immutable.
func hlsVersionMatches(c echo.Context, sp sqlcgen.StreamingPlaylist) bool {
	return c.QueryParam(hlsVersionParam) == hlsCacheVersion(sp)
}

func isRelativePlaylistURI(raw string) bool {
	return !strings.HasPrefix(raw, "http://") &&
		!strings.HasPrefix(raw, "https://") &&
		!strings.HasPrefix(raw, "/")
}

func splitURISuffix(raw string) (string, string) {
	if idx := strings.IndexAny(raw, "?#"); idx >= 0 {
		return raw[:idx], raw[idx:]
	}
	return raw, ""
}

func uriBasename(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Path != "" && (u.Scheme != "" || strings.HasPrefix(raw, "/")) {
		return path.Base(u.Path)
	}
	return path.Base(raw)
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
	url := "/api/v1/videos/" + id.String() + "/hls/master.m3u8?" +
		hlsVersionParam + "=" + hlsCacheVersion(sp)
	var rends []renditionView
	for _, r := range s.transcodesvc.Renditions(c.Request().Context(), id) {
		rends = append(rends, renditionView{Height: r.Height, Width: r.Width})
	}
	return &url, rends
}
