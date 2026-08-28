package videoimport

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/vidra/vidra-core/internal/safeerr"
	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/ytdlp"
)

// Resolver values persisted on import_jobs.resolver. 'auto' is the requested
// value only; the worker resolves it to a concrete choice during the 'resolving'
// stage and rewrites the column so the stored/echoed value is always what ran.
const (
	ResolverAuto   = "auto"
	ResolverDirect = "direct"
	ResolverYtdlp  = "ytdlp"
)

// Stage values persisted on import_jobs.stage for honest UI progress. Empty
// means queued (or a terminal state).
const (
	StageResolving   = "resolving"
	StageDownloading = "downloading"
	StageProcessing  = "processing"
)

// Extractor is the yt-dlp seam driven by the ytdlp resolver. *ytdlp.Client
// satisfies it; tests inject a stub so the platform-import path is exercised
// without the real binary. Implementations MUST NOT write into blob storage —
// bytes reach storage only through the caller's AttachOriginal → Process.
type Extractor interface {
	// Metadata runs the sandboxed -J probe (title/description/thumbnail).
	Metadata(ctx context.Context, url string) (ytdlp.Meta, error)
	// Download runs the sandboxed bounded download into workdir and returns the
	// media file path.
	Download(ctx context.Context, url, workdir string) (string, error)
}

// resolvedMedia is a fetched original ready for AttachOriginal. body is read
// then Closed by the caller; Close also removes any per-job ytdlp workdir.
type resolvedMedia struct {
	filename    string
	contentType string
	body        io.ReadCloser
	// knownSize is the byte size when known up front (direct Content-Length, or a
	// yt-dlp file stat); < 0 when unknown. The caller still streams under a hard
	// cap regardless.
	knownSize int64
	// title/description prefill an EMPTY draft field only (ytdlp fills these;
	// direct leaves them blank).
	title       string
	description string
}

// resolver turns a validated URL into a resolvedMedia. The direct HTTP fetch and
// the sandboxed yt-dlp extractor are the two implementations.
type resolver interface {
	resolve(ctx context.Context, target *url.URL) (*resolvedMedia, error)
}

// ---- direct resolver -------------------------------------------------------

// directResolver fetches a plain HTTP(S) media file (the pre-W2 behaviour).
type directResolver struct {
	client *http.Client
}

func (d *directResolver) resolve(ctx context.Context, target *url.URL) (*resolvedMedia, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, safeerr.New("could not fetch the URL")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		// Blocked address (SSRF guard), DNS failure, timeout, TLS error, etc. The
		// URL is never echoed back.
		return nil, safeerr.New("could not fetch the URL")
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, safeerr.New("the URL did not return a downloadable file")
	}
	return &resolvedMedia{
		filename:    path.Base(target.Path),
		contentType: resp.Header.Get("Content-Type"),
		body:        resp.Body,
		knownSize:   resp.ContentLength, // -1 when the origin omits Content-Length
	}, nil
}

// ---- ytdlp resolver --------------------------------------------------------

// ytdlpResolver drives the sandboxed yt-dlp extractor: a best-effort metadata
// probe (draft prefill) followed by a bounded download into a PRIVATE per-job
// tmp workdir. The downloaded bytes never touch blob storage directly — they
// flow to the caller as a file reader for AttachOriginal → Process (scan hook).
type ytdlpResolver struct {
	ext      Extractor
	workRoot string // parent dir for per-job workdirs (os.TempDir() when empty)
}

func (y *ytdlpResolver) resolve(ctx context.Context, target *url.URL) (*resolvedMedia, error) {
	root := y.workRoot
	if root == "" {
		root = os.TempDir()
	}
	// A private 0700 per-job workdir that is always removed (MkdirTemp is 0700).
	workdir, err := os.MkdirTemp(root, "vidra-ytdlp-*")
	if err != nil {
		return nil, safeerr.New("import failed")
	}

	rawURL := target.String()
	// Metadata is best-effort prefill: a probe failure must not sink the import
	// (the authoritative step is the download).
	var meta ytdlp.Meta
	if m, mErr := y.ext.Metadata(ctx, rawURL); mErr == nil {
		meta = m
	}

	mediaPath, err := y.ext.Download(ctx, rawURL, workdir)
	if err != nil {
		_ = os.RemoveAll(workdir)
		return nil, safeerr.New("the URL could not be imported from this platform")
	}
	f, err := os.Open(mediaPath) //nolint:gosec // path is inside our private workdir
	if err != nil {
		_ = os.RemoveAll(workdir)
		return nil, safeerr.New("import failed")
	}
	size := int64(-1)
	if st, sErr := f.Stat(); sErr == nil {
		size = st.Size()
	}
	return &resolvedMedia{
		filename:    filepath.Base(mediaPath),
		contentType: "", // let AttachOriginal derive the type from the file extension
		body:        &workdirCloser{f: f, workdir: workdir},
		knownSize:   size,
		title:       meta.Title,
		description: meta.Description,
	}, nil
}

// workdirCloser closes the media file AND removes the private per-job workdir on
// Close, so no downloaded bytes outlive the import.
type workdirCloser struct {
	f       *os.File
	workdir string
}

func (w *workdirCloser) Read(p []byte) (int, error) { return w.f.Read(p) }
func (w *workdirCloser) Close() error {
	err := w.f.Close()
	_ = os.RemoveAll(w.workdir)
	return err
}

// isAcceptedVideoContentType reports whether ct names a video container the
// upload pipeline accepts (media type only; parameters/case ignored).
func isAcceptedVideoContentType(ct string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	_, ok := video.AcceptedVideoContentType(mediaType)
	return ok
}
