package peertubeimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

// ── fetching the families PeerTube keeps OFF its object store ──
//
// Most media the importer moves is read through srcMedia, the source's object
// store. Three families are not there to read: actor images (avatars, banners),
// video thumbnails and storyboards. PeerTube's object-storage config covers
// exactly five things — streaming playlists, web videos, user exports, original
// files and captions — and none of those is one of them. They stay on the source
// host's local filesystem no matter how the rest of the instance is configured,
// so --source-storage=s3 cannot see them and --source-local-root cannot either
// unless the import happens to run on the source host itself.
//
// They ARE served publicly by the source instance, under /lazy-static/, which
// from another machine is the only way to get those bytes at all.
//
// NOTE the path: /static/<family>/<filename> answers 200 with the single-page
// app's HTML shell, not a 404. An implementation that trusted the status code
// would store 62 KB of HTML as every account's avatar and every video's poster.
// Hence the content-type gate below, which checks what the bytes ARE and not
// what the response claims.

// The lazy-static route prefixes (PeerTube v8.2.4 LAZY_STATIC_PATHS).
//
// /lazy-static/previews/ still exists at 8.2 and resolves against the same
// unified `thumbnail` table, but it is marked "deprecated, remove in v9" in
// PeerTube's own source. /lazy-static/thumbnails/ answers for EVERY row of that
// table including the ex-previews, so it is the one used here — it is both
// correct today and the one that survives v9.
const (
	lazyStaticAvatars     = "/lazy-static/avatars/"
	lazyStaticThumbnails  = "/lazy-static/thumbnails/"
	lazyStaticStoryboards = "/lazy-static/storyboards/"
)

// Fetch tuning. The source is a LIVE PRODUCTION instance during a migration —
// the operator re-runs this on a schedule right up to cutover — so the client is
// deliberately unhurried: a handful of connections, a bounded wait, and a size
// cap well under the source's own upload limit.
//
// The cap covers storyboard sprite sheets too: PeerTube clamps a sheet's long
// edge to 192px per sprite across at most an 11x11 grid, so the biggest sheet it
// can produce is 2112px on its long edge — comfortably inside 8 MiB as a JPEG.
const (
	lazyStaticConcurrency = 4
	lazyStaticTimeout     = 20 * time.Second
	maxLazyStaticBytes    = 8 << 20 // 8 MiB, matching HTTP_BODY_LIMIT's default
	lazyStaticUserAgent   = "vidra-peertube-import/1.0"
)

// errNotAnImage is the content-type gate's rejection: the source answered, but
// with something that is not an image Vidra stores. It is recorded as
// unsupported (a fact about the source) rather than failed (a transient problem
// worth retrying).
var errNotAnImage = errors.New("peertubeimport: response is not a supported image")

// errImageTooLarge is the size cap's rejection, and it is the SAME KIND of
// answer as the one above: how big a file the source holds is a fact about the
// source, and it will be exactly as big on every future run. Left classified as
// a generic failure it produced 5 rows that were re-fetched from a live
// production instance on every single run and failed identically every time — a
// permanent, self-inflicted load on somebody else's server. Genuine transients
// (a 500, a dropped connection, a storage error) stay retryable; only this one
// is terminal.
var errImageTooLarge = errors.New("peertubeimport: source image exceeds the size cap")

// lazyStaticFetcher pulls one family's bytes from the source instance's public
// origin. Both the origin and the route prefix are PINNED for the whole run: one
// host is contacted, and a filename from the source database can only ever
// select a leaf under one known prefix (path.Base strips any directory the
// source recorded), so no row in the source database can steer a fetch somewhere
// else.
type lazyStaticFetcher struct {
	origin string
	prefix string
	client *http.Client
}

func newLazyStaticFetcher(origin, prefix string) *lazyStaticFetcher {
	// AllowPrivate is set on purpose. A migration source is routinely reachable
	// only on a LAN or through a tunnel, and this URL is not user input: it is
	// derived from the operator's own source database, the same database this
	// tool already reads password hashes and actor private keys out of. The
	// scheme/userinfo checks and the per-redirect re-validation still apply.
	client := urlsafety.Guard{AllowPrivate: true}.NewClient(lazyStaticTimeout)
	return &lazyStaticFetcher{origin: strings.TrimSuffix(origin, "/"), prefix: prefix, client: client}
}

// fetch returns the image bytes and the extension they should be stored under.
// The extension comes from what the bytes ARE, never from the source filename:
// the stored content type is derived from the extension downstream, so trusting
// a filename would let a mislabelled source file be served as the wrong type.
func (f *lazyStaticFetcher) fetch(ctx context.Context, filename string) ([]byte, string, error) {
	name := path.Base(strings.TrimSpace(filename))
	if name == "" || name == "." || name == "/" {
		return nil, "", fmt.Errorf("%w: empty filename", errNotAnImage)
	}
	target := f.origin + f.prefix + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", lazyStaticUserAgent)
	req.Header.Set("Accept", "image/*")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("peertubeimport: source answered %d for %s", resp.StatusCode, strings.Trim(f.prefix, "/"))
	}
	// First gate, before a single byte of body is read: the SPA fallback answers
	// text/html, and there is no reason to download a page to find that out. A
	// response that declares NOTHING falls through to the sniff instead of being
	// rejected — the header is here to save a download, not to be the ruling.
	declared, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	declared = strings.ToLower(strings.TrimSpace(declared))
	if declared != "" && !strings.HasPrefix(declared, "image/") {
		return nil, "", fmt.Errorf("%w: declared %q", errNotAnImage, declared)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLazyStaticBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > maxLazyStaticBytes {
		return nil, "", fmt.Errorf("%w of %d bytes", errImageTooLarge, int64(maxLazyStaticBytes))
	}
	if len(body) == 0 {
		return nil, "", fmt.Errorf("%w: empty body", errNotAnImage)
	}
	// Second gate, and the authoritative one: sniff the bytes. A source that
	// declares image/png and serves anything else is caught here, and the
	// extension the object is stored under comes from this answer.
	ext := imageExtForSniffedType(sniffContentType(body))
	if ext == "" {
		return nil, "", fmt.Errorf("%w: sniffed %q", errNotAnImage, sniffContentType(body))
	}
	return body, ext, nil
}

// sniffContentType classifies the leading bytes of a response body. It is
// http.DetectContentType with the standard 512-byte window.
func sniffContentType(body []byte) string {
	head := body
	if len(head) > 512 {
		head = head[:512]
	}
	return strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(head), ";", 2)[0]))
}
