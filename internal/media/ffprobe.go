package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

// FFProbe extracts media metadata by shelling out to ffprobe. It satisfies
// video.Prober. A probe failure (non-media file, unreadable, ffprobe error)
// surfaces as an error so the caller can mark the video failed.
type FFProbe struct {
	blobs storage.Backend
	bin   string
}

// NewFFProbe builds an FFProbe reading objects from blobs via the "ffprobe"
// binary on PATH.
func NewFFProbe(blobs storage.Backend) *FFProbe {
	return &FFProbe{blobs: blobs, bin: "ffprobe"}
}

// DetectFFProbe returns an FFProbe backed by blobs when the ffprobe binary is on
// PATH, else (nil, false) so callers can fall back to publishing unprobed
// instead of failing every upload on a host without ffmpeg installed.
func DetectFFProbe(blobs storage.Backend) (*FFProbe, bool) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return nil, false
	}
	return NewFFProbe(blobs), true
}

// Probe runs ffprobe against the object at key and returns its metadata. On a
// backend that can presign, ffprobe reads the object over HTTP with range
// requests — it needs the header and a little stream data, not the whole file,
// so this is a few KiB instead of a full download to a temp file.
func (f *FFProbe) Probe(ctx context.Context, key string) (Metadata, error) {
	src, cleanup, err := openSource(ctx, f.blobs, key)
	if err != nil {
		return Metadata{}, err
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, f.bin, ffprobeArgs(src)...)
	out, err := cmd.Output()
	if err != nil {
		// err may carry ffprobe's stderr, which echoes the input URL — and a
		// presigned URL is a credential. Report the key, never the source.
		return Metadata{}, fmt.Errorf("media: ffprobe %q failed: %w", key, redactSource(src, err))
	}
	return parseFFProbe(out)
}

// ffprobeArgs builds the ffprobe argument vector reading src. ffprobe takes its
// input positionally, so protocol options precede the bare input value (there is
// no -i). Pure (no exec) so it is unit-testable.
func ffprobeArgs(src source) []string {
	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
	}
	args = append(args, src.opts...)
	return append(args, src.arg)
}

// source is a resolved ffmpeg/ffprobe input for a stored object: the value to
// pass after -i, plus any protocol options that must precede it. A local path
// renders exactly "-i <path>", so local behaviour is byte-identical to the
// pre-remote form.
type source struct {
	// arg is the value passed after -i: a filesystem path or a URL.
	arg string
	// opts are protocol options that must precede -i (empty for a local path).
	opts []string
	// remote reports whether arg is a presigned URL. Such a URL carries a
	// signature and is a credential for the object — it must never be logged,
	// traced, or put in an error message.
	remote bool
}

// localSource is an input already present on the filesystem.
func localSource(path string) source { return source{arg: path} }

// redactSource strips a presigned source URL out of an error. ffmpeg and ffprobe
// echo the input they were given in their diagnostics, so an error from a remote
// source can carry the signature. Local paths are safe and pass through.
func redactSource(s source, err error) error {
	if err == nil || !s.remote || s.arg == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), s.arg, "<presigned source url>")
	return errors.New(msg)
}

// inputArgs renders the "<opts...> -i <arg>" fragment that opens this source.
func (s source) inputArgs() []string {
	out := make([]string, 0, len(s.opts)+2)
	out = append(out, s.opts...)
	return append(out, "-i", s.arg)
}

// remoteSourceTTL is how long a presigned source URL stays valid. It must
// comfortably outlast the longest single encode that reads it: ffmpeg holds the
// URL for the whole run and re-requests ranges (and reconnects) throughout, so a
// URL that expires mid-encode fails the job near the end, which is the most
// expensive moment to fail.
const remoteSourceTTL = 6 * time.Hour

// httpSourceOpts are the ffmpeg http protocol options a remote source needs.
// Without them a multi-hour read of a remote object is fragile: a single
// transient TCP reset ends the encode.
//
//   - seekable 1        — the store serves Range requests; don't rely on probing
//   - multiple_requests — reuse one connection across the many range requests a
//     demuxer makes, instead of a fresh TLS handshake each time
//   - reconnect*        — survive transient network and TLS errors mid-read
//   - rw_timeout        — 30s (microseconds), so a hung socket fails rather than
//     wedging the worker for the job's lifetime
var httpSourceOpts = []string{
	"-seekable", "1",
	"-multiple_requests", "1",
	"-reconnect", "1",
	"-reconnect_streamed", "1",
	"-reconnect_on_network_error", "1",
	"-reconnect_delay_max", "30",
	"-rw_timeout", "30000000",
}

// openSource resolves key to something an external tool can read, preferring in
// order:
//
//  1. a local filesystem path, when the backend exposes one (the local backend);
//  2. a presigned URL ffmpeg reads directly over HTTP, when the backend can mint
//     one (the S3 backend) — the object never touches this instance's disk;
//  3. a full download to a temp file, for any backend that can do neither.
//
// Option 2 is the difference between a transcode staging the whole original
// locally and streaming it: on S3 the download fallback wrote a complete copy of
// the source per tool invocation.
//
// The returned cleanup removes any temp file and is always safe to call.
//
// Note on exposure: a presigned URL passed in argv is visible in the process
// list to a local user on the host. It is read-only, scoped to one object, and
// time-limited, which is an acceptable trade for not staging every original on
// local disk — but it is a trade, not a free win.
func openSource(ctx context.Context, blobs storage.Backend, key string) (source, func(), error) {
	if pp, ok := blobs.(storage.PathProvider); ok {
		p, err := pp.Path(key)
		if err != nil {
			return source{}, func() {}, err
		}
		return localSource(p), func() {}, nil
	}

	if pr, ok := blobs.(storage.Presigner); ok {
		// A presign failure is not fatal: fall through to the download path so a
		// misconfigured or unsupported store still transcodes, just less leanly.
		if u, err := pr.PresignGet(ctx, key, remoteSourceTTL); err == nil {
			return source{arg: u, opts: httpSourceOpts, remote: true}, func() {}, nil
		}
	}

	path, cleanup, err := downloadToTemp(ctx, blobs, key)
	if err != nil {
		return source{}, func() {}, err
	}
	return localSource(path), cleanup, nil
}

// objectPath returns a filesystem path an external tool can read for key. It is
// the path-or-download resolver, kept for tools that genuinely need a seekable
// local FILE rather than any readable input. Prefer openSource.
func objectPath(ctx context.Context, blobs storage.Backend, key string) (string, func(), error) {
	if pp, ok := blobs.(storage.PathProvider); ok {
		p, err := pp.Path(key)
		if err != nil {
			return "", func() {}, err
		}
		return p, func() {}, nil
	}
	return downloadToTemp(ctx, blobs, key)
}

// downloadToTemp streams the object at key to a temp file and returns its path
// plus a cleanup that removes it.
func downloadToTemp(ctx context.Context, blobs storage.Backend, key string) (string, func(), error) {
	rc, err := blobs.Open(ctx, key)
	if err != nil {
		return "", func() {}, err
	}
	defer func() { _ = rc.Close() }()
	tmp, err := os.CreateTemp("", "vidra-media-*")
	if err != nil {
		return "", func() {}, err
	}
	if _, err := io.Copy(tmp, rc); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", func() {}, err
	}
	_ = tmp.Close()
	return tmp.Name(), func() { _ = os.Remove(tmp.Name()) }, nil
}

// ffprobeOutput is the subset of `ffprobe -print_format json` we read.
type ffprobeOutput struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
		RFrameRate   string `json:"r_frame_rate"`
		BitRate      string `json:"bit_rate"`
		Disposition  struct {
			// AttachedPic marks a stream that is a still image carried inside a
			// media file — album art, a podcast cover — rather than video to play.
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// maxProbeDurationSeconds caps a parsed duration (~100 days) so an absurd or
// overflowing value a crafted file makes ffprobe emit can't yield a nonsensical
// (or, via float->int overflow, negative) DurationSeconds.
const maxProbeDurationSeconds = 100 * 24 * 60 * 60

// parseFFProbe maps ffprobe JSON to Metadata. It is pure (no exec) so it can be
// unit-tested with canned fixtures. Missing/blank fields are left zero.
func parseFFProbe(b []byte) (Metadata, error) {
	var out ffprobeOutput
	if err := json.Unmarshal(b, &out); err != nil {
		return Metadata{}, fmt.Errorf("media: parse ffprobe output: %w", err)
	}
	var m Metadata
	if d, err := strconv.ParseFloat(strings.TrimSpace(out.Format.Duration), 64); err == nil && d > 0 && d < maxProbeDurationSeconds {
		m.DurationSeconds = int(d + 0.5) // round to nearest second
	}
	// The video scan stops at the first usable stream (as it always has); the
	// audio scan cannot, because an audio stream may follow it.
	//
	// COVER ART IS NOT VIDEO, and this is the single most important thing this
	// loop gets right. ffprobe reports an attached picture — the album art on a
	// music file, the cover on a podcast episode — as a codec_type "video" stream
	// WITH REAL DIMENSIONS (mjpeg 600x600 is typical). Taking it at face value
	// means an audio file is never classified audio-only, a whole ABR ladder is
	// planned from one still image, and the trick-play pass then fails on a
	// stream with no meaningful timeline — five retries and a dead letter, which
	// is precisely the failure the audio-only path exists to eliminate, for the
	// shape most podcast and music uploads actually have. The disposition flag is
	// the only reliable signal: dimensions, duration and frame rate all look
	// plausible on an attached picture.
	video := false
	for _, s := range out.Streams {
		switch {
		case !video && s.CodecType == "video" && s.Width > 0 && s.Height > 0 && s.Disposition.AttachedPic == 0:
			video = true
			m.Width = s.Width
			m.Height = s.Height
			m.FPS = streamFrameRate(s.AvgFrameRate, s.RFrameRate)
		case s.CodecType == "audio":
			m.HasAudio = true
			if m.AudioKbps == 0 {
				m.AudioKbps = parseBitrateKbps(s.BitRate)
			}
		}
	}
	return m, nil
}

// maxFallbackFPS bounds the r_frame_rate FALLBACK. r_frame_rate is not a frame
// rate at all — it is the smallest tick the stream's timestamps are expressible
// in — so for a container that reports no usable average it can be the timebase
// (90000/1 on MPEG-TS-derived streams, 1000/1 on some MP4s) rather than
// anything the pictures do. 120 is above every real acquisition rate this
// pipeline plans for and far below those ticks.
const maxFallbackFPS = 120

// streamFrameRate resolves a video stream's rate: the average when the container
// gives a usable one, else r_frame_rate — but only when that is PLAUSIBLE.
//
// The fallback exists for a real case: a short single-GOP clip, or an MPEG-TS
// capture, where ffprobe cannot average and reports "0/0" while r_frame_rate is
// the genuine 25/30/60. It must keep working. What it must not do is let a
// container's timebase through as a frame rate — that value now drives the
// ladder's bitrate budget (fpsVideoBitrateMultiplier), so a 90000/1 read would
// silently buy every rung the 1.6x high-frame-rate budget for standard-rate
// pictures. Unknown is the safe answer, and unknown means the shipped table.
func streamFrameRate(avg, r string) float64 {
	if fps := parseFrameRate(avg); fps > 0 {
		return fps
	}
	if fps := parseFrameRate(r); fps > 0 && fps <= maxFallbackFPS {
		return fps
	}
	return 0
}

// parseBitrateKbps converts ffprobe's bits-per-second string to whole kbps,
// rounded to nearest. 0 for absent/unparseable/absurd values ("unknown"), which
// every caller reads as "no usable hint".
func parseBitrateKbps(v string) int {
	bps, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || bps <= 0 || bps > maxProbeBitrateBps {
		return 0
	}
	return int(bps/1000 + 0.5)
}

// maxProbeBitrateBps caps a parsed bitrate (1 Gbit/s) so an absurd value a
// crafted file makes ffprobe emit cannot drive an encode decision.
const maxProbeBitrateBps = 1e9

// maxProbeFPS caps a parsed frame rate so an absurd value a crafted file makes
// ffprobe emit can't drive planning decisions (real content tops out well
// below 1000 fps).
const maxProbeFPS = 1000

// parseFrameRate decodes ffprobe's rational frame-rate form ("30000/1001",
// "25/1") or a plain decimal into frames per second. Unknown/degenerate values
// ("0/0", "N/A", junk, non-positive, absurd) return 0 ("unknown").
func parseFrameRate(v string) float64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	num, den := v, "1"
	if i := strings.IndexByte(v, '/'); i >= 0 {
		num, den = v[:i], v[i+1:]
	}
	n, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0
	}
	d, err := strconv.ParseFloat(den, 64)
	if err != nil || d <= 0 {
		return 0
	}
	fps := n / d
	if fps <= 0 || fps >= maxProbeFPS {
		return 0
	}
	return fps
}
