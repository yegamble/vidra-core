// Package ytdlp is a HARD-SANDBOXED wrapper around the yt-dlp platform-URL
// extractor (backport W2.C1, UPLOAD-09). It is the ONLY place the repo shells
// out to yt-dlp, and it is built to make that subprocess as small an attack
// surface as possible (see .ralph/specs/backport-w2-upload-import.md §5):
//
//   - FIXED argv allowlist built by pure functions (metadataArgs/downloadArgs);
//     the user-supplied URL is the SINGLE attacker-influenced argument and is
//     always the last positional — never a flag value, never shell-interpolated.
//   - exec.CommandContext only: NO shell, so URL contents can never be
//     word-split or command-substituted.
//   - --ignore-config so a planted user/system yt-dlp config cannot inject flags.
//   - NO --exec (which would run an arbitrary post-download command) and NO
//     --update / -U (no runtime self-update; the binary is pinned in the image).
//   - --no-playlist so one URL yields at most one media file.
//   - --max-filesize bounds the download; the caller re-checks the on-disk size.
//   - a hard wall-clock deadline (Config.Timeout) via the context: on expiry the
//     process group is killed.
//   - the caller owns a private 0700 per-job tmp workdir and removes it; this
//     package only writes under the caller-provided output template.
//
// The URL MUST already have passed internal/urlsafety before it reaches here —
// this package does not (and cannot) re-pin yt-dlp's own outbound sockets, which
// is the documented residual risk the OFF-by-default flag + egress proxy address.
package ytdlp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors callers can match. They never carry the URL.
var (
	// ErrRun means the subprocess exited non-zero, timed out, or could not start.
	ErrRun = errors.New("ytdlp: extractor run failed")
	// ErrNoMedia means the download produced no media file.
	ErrNoMedia = errors.New("ytdlp: no media file produced")
)

// Config is the boot-validated yt-dlp configuration. The zero value is not
// usable; build it from internal/config.
type Config struct {
	// Path is the yt-dlp executable (absolute path or a PATH lookup name).
	Path string
	// Timeout is the hard wall-clock bound on a single yt-dlp invocation.
	Timeout time.Duration
	// Proxy, when non-empty, is passed as --proxy: the egress proxy that a
	// production deployment SHOULD point at an allow-listed forward proxy so
	// yt-dlp's own fetches cannot reach internal addresses (residual-risk
	// mitigation §5). It is an operator-supplied value, never attacker-supplied.
	Proxy string
	// MaxHeight caps the selected video resolution (yt-dlp -f height<=N).
	MaxHeight int
	// MaxBytes caps the download (--max-filesize); 0 disables the flag (the
	// caller still enforces its own streaming cap on the resulting file).
	MaxBytes int64
}

// Meta is the subset of yt-dlp -J metadata this repo consumes. Unknown fields
// are ignored. Strings are untrusted display text; the caller sanitises/uses
// them only to prefill an empty draft field.
type Meta struct {
	Title       string
	Description string
	// DurationSeconds is 0 when yt-dlp did not report a duration.
	DurationSeconds int
	// Thumbnail is the best thumbnail URL yt-dlp reported (may be empty). It is
	// NOT fetched by this package — the caller decides whether to fetch it, and
	// only ever through the urlsafety-guarded client.
	Thumbnail string
}

// runFunc is the exec seam. Production uses runCommand; tests inject a fake so
// argv construction, JSON parsing, and error mapping are exercised without the
// real binary.
type runFunc func(ctx context.Context, name string, args ...string) (stdout []byte, err error)

// Client drives a sandboxed yt-dlp. Construct it only when YTDLP_IMPORT_ENABLED
// is on (the enable gate lives in the import service, not here).
type Client struct {
	cfg Config
	run runFunc
}

// New builds a Client from a validated Config.
func New(cfg Config) *Client { return &Client{cfg: cfg, run: runCommand} }

// metadataArgs builds the FIXED argv for the metadata probe. url is the only
// caller-influenced value and is always the final positional argument. Kept as a
// pure function so the security-critical shape is unit-tested directly.
func metadataArgs(cfg Config, url string) []string {
	args := []string{
		"--ignore-config", // ignore any user/system yt-dlp config file
		"--no-playlist",   // a channel/playlist URL still yields a single entry
		"--no-warnings",
		"-J",            // dump a single JSON object to stdout
		"--no-download", // metadata only
	}
	if cfg.Proxy != "" {
		args = append(args, "--proxy", cfg.Proxy)
	}
	// The URL is the LAST positional; "--" ends option parsing so a URL that
	// begins with "-" can never be read as a flag.
	args = append(args, "--", url)
	return args
}

// downloadArgs builds the FIXED argv for the bounded download. outTemplate is a
// caller-owned path template inside the private workdir; url is the only
// attacker-influenced value and is again the final positional after "--".
func downloadArgs(cfg Config, url, outTemplate string) []string {
	format := "bestvideo[ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best[ext=webm]/best"
	if cfg.MaxHeight > 0 {
		h := strconv.Itoa(cfg.MaxHeight)
		format = "bestvideo[height<=" + h + "][ext=mp4]+bestaudio[ext=m4a]/" +
			"best[height<=" + h + "][ext=mp4]/best[height<=" + h + "][ext=webm]/best[height<=" + h + "]"
	}
	args := []string{
		"--ignore-config",
		"--no-playlist",
		"--no-warnings",
		"--restrict-filenames", // ASCII-only, no shell metacharacters in names
		"--no-part",
		"-f", format,
		"-o", outTemplate,
	}
	if cfg.MaxBytes > 0 {
		args = append(args, "--max-filesize", strconv.FormatInt(cfg.MaxBytes, 10))
	}
	if cfg.Proxy != "" {
		args = append(args, "--proxy", cfg.Proxy)
	}
	args = append(args, "--", url)
	return args
}

// Metadata runs the -J probe and returns the parsed subset. url MUST already be
// urlsafety-validated. On any subprocess failure it returns ErrRun (never the
// URL or raw stderr).
func (c *Client) Metadata(ctx context.Context, url string) (Meta, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	out, err := c.run(ctx, c.cfg.Path, metadataArgs(c.cfg, url)...)
	if err != nil {
		return Meta{}, ErrRun
	}
	return parseMeta(out)
}

// parseMeta decodes the yt-dlp -J document into Meta. Trailing/leading noise is
// tolerated by scanning to the first JSON object.
func parseMeta(out []byte) (Meta, error) {
	var raw struct {
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Duration    float64 `json:"duration"`
		Thumbnail   string  `json:"thumbnail"`
	}
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(out)))
	if err := dec.Decode(&raw); err != nil {
		return Meta{}, fmt.Errorf("%w: metadata decode", ErrRun)
	}
	return Meta{
		Title:           strings.TrimSpace(raw.Title),
		Description:     strings.TrimSpace(raw.Description),
		DurationSeconds: int(raw.Duration),
		Thumbnail:       strings.TrimSpace(raw.Thumbnail),
	}, nil
}

// Download runs the bounded download into workdir and returns the absolute path
// of the single media file produced. The caller owns workdir (a private 0700
// dir) and removes it. url MUST already be urlsafety-validated.
func (c *Client) Download(ctx context.Context, url, workdir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	outTemplate := filepathJoin(workdir, "media.%(ext)s")
	if _, err := c.run(ctx, c.cfg.Path, downloadArgs(c.cfg, url, outTemplate)...); err != nil {
		return "", ErrRun
	}
	media, err := findMedia(workdir)
	if err != nil {
		return "", err
	}
	return media, nil
}
