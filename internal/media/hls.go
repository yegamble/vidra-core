package media

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// hlsSegmentSeconds is the target MPEG-TS segment duration.
const hlsSegmentSeconds = 4

// hlsLadder is the H.264/AAC encoding ladder, highest rung first. Rungs above
// the source height are skipped (never upscale); a source shorter than the
// lowest rung gets a single rung at its own (even-rounded) size.
var hlsLadder = []HLSRung{
	{Height: 1080, VideoKbps: 5000, AudioKbps: 160},
	{Height: 720, VideoKbps: 2800, AudioKbps: 128},
	{Height: 480, VideoKbps: 1400, AudioKbps: 128},
	{Height: 360, VideoKbps: 800, AudioKbps: 96},
}

// HLSRung is one planned rendition of the HLS ladder. Width/Height are the
// exact (even) output dimensions; the bitrates drive both the encoder caps and
// the master playlist's BANDWIDTH attribute.
type HLSRung struct {
	Height    int
	Width     int
	VideoKbps int
	AudioKbps int
}

// Name is the rung's directory name under the video's playlist prefix ("720p").
func (r HLSRung) Name() string { return fmt.Sprintf("%dp", r.Height) }

// Bandwidth is the master-playlist BANDWIDTH value in bits per second: the
// peak video+audio rate plus ~10% container overhead.
func (r HLSRung) Bandwidth() int {
	return (r.VideoKbps + r.AudioKbps) * 1000 * 11 / 10
}

// PlanHLSLadder plans the output ladder for a source of the given dimensions,
// highest rung first. Rungs taller than the source are skipped (no upscaling);
// when the source is shorter than every rung, a single rung at the source's
// own (even-rounded) size is planned with the lowest rung's bitrates. Widths
// preserve the source aspect ratio, rounded to even (required by H.264 4:2:0).
// Unknown (non-positive) dimensions cannot be planned and return nil.
func PlanHLSLadder(sourceWidth, sourceHeight int) []HLSRung {
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return nil
	}
	var rungs []HLSRung
	for _, r := range hlsLadder {
		if r.Height > sourceHeight {
			continue
		}
		r.Width = evenScaledWidth(sourceWidth, sourceHeight, r.Height)
		r.Height = evenDim(r.Height)
		rungs = append(rungs, r)
	}
	if len(rungs) == 0 {
		lowest := hlsLadder[len(hlsLadder)-1]
		h := evenDim(sourceHeight)
		rungs = append(rungs, HLSRung{
			Height:    h,
			Width:     evenScaledWidth(sourceWidth, sourceHeight, h),
			VideoKbps: lowest.VideoKbps,
			AudioKbps: lowest.AudioKbps,
		})
	}
	return rungs
}

// evenScaledWidth is the width matching targetHeight at the source aspect
// ratio, rounded to the nearest even integer (minimum 2): round(w/2)*2 with
// w = sourceWidth*targetHeight/sourceHeight, in integer arithmetic.
func evenScaledWidth(sourceWidth, sourceHeight, targetHeight int) int {
	n := sourceWidth * targetHeight
	w := (n + sourceHeight) / (2 * sourceHeight) * 2
	if w < 2 {
		return 2
	}
	return w
}

// evenDim rounds n down to an even value, with a floor of 2.
func evenDim(n int) int {
	n -= n % 2
	if n < 2 {
		return 2
	}
	return n
}

// hlsRungArgs builds the ffmpeg argument vector encoding one ladder rung from
// src into dir (variant playlist dir/playlist.m3u8 + segments dir/seg_NNNNN.ts).
// Pure (no exec) so it is unit-testable. The audio map is optional ("0:a:0?")
// so silent sources still transcode; segments reference each other by bare
// filename (they sit next to the variant playlist), which keeps every playlist
// URI relative so the API can proxy them.
func hlsRungArgs(src, dir string, r HLSRung) []string {
	return []string{
		"-y",
		"-i", src,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-c:v", "libx264",
		"-profile:v", "main",
		"-preset", "veryfast",
		"-pix_fmt", "yuv420p",
		"-vf", fmt.Sprintf("scale=%d:%d", r.Width, r.Height),
		"-b:v", fmt.Sprintf("%dk", r.VideoKbps),
		"-maxrate", fmt.Sprintf("%dk", r.VideoKbps),
		"-bufsize", fmt.Sprintf("%dk", 2*r.VideoKbps),
		"-c:a", "aac",
		"-b:a", fmt.Sprintf("%dk", r.AudioKbps),
		"-ac", "2",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", hlsSegmentSeconds),
		"-hls_playlist_type", "vod",
		"-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(dir, "seg_%05d.ts"),
		filepath.Join(dir, "playlist.m3u8"),
	}
}

// renderMasterPlaylist renders the HLS master playlist for the given rungs.
// Variant URIs are RELATIVE ("720p/playlist.m3u8") so the playlist works when
// proxied from any base path. Pure (no exec/IO) so it is unit-testable.
func renderMasterPlaylist(rungs []HLSRung) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	for _, r := range rungs {
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d\n", r.Bandwidth(), r.Width, r.Height)
		b.WriteString(r.Name() + "/playlist.m3u8\n")
	}
	return b.String()
}

// HLSRendition describes one produced rendition: its dimensions and the
// storage-key prefix holding its variant playlist + segments.
type HLSRendition struct {
	Height    int
	Width     int
	KeyPrefix string
}

// HLSResult is a completed transcode: the master playlist's storage key and
// the renditions written under it.
type HLSResult struct {
	MasterKey  string
	Renditions []HLSRendition
}

// HLSKeyPrefix is the storage-key directory holding a video's HLS output
// (PeerTube-aligned layout: streaming-playlists/<video_id>/ — see
// .ralph/specs/storage-layout.md).
func HLSKeyPrefix(videoID uuid.UUID) string {
	return "streaming-playlists/" + videoID.String()
}

// HLSTranscoder produces an H.264/AAC HLS ladder for a stored original by
// shelling out to ffmpeg, writing outputs to a temp dir and then storing them
// under streaming-playlists/<video_id>/. It satisfies transcode.Transcoder.
// The argument/ladder/master-playlist builders above are pure and unit-tested;
// the exec path is covered by a -tags=integration test.
type HLSTranscoder struct {
	blobs storage.Backend
	probe *FFProbe
	bin   string
}

// NewHLSTranscoder builds an HLSTranscoder reading/writing objects on blobs via
// the "ffmpeg" (and, for source dimensions, "ffprobe") binaries on PATH.
func NewHLSTranscoder(blobs storage.Backend) *HLSTranscoder {
	return &HLSTranscoder{blobs: blobs, probe: NewFFProbe(blobs), bin: "ffmpeg"}
}

// DetectHLSTranscoder returns an HLSTranscoder when both ffmpeg and ffprobe are
// on PATH, else (nil, false) so callers can leave transcoding off.
func DetectHLSTranscoder(blobs storage.Backend) (*HLSTranscoder, bool) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, false
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return nil, false
	}
	return NewHLSTranscoder(blobs), true
}

// Transcode probes the source at sourceKey for its dimensions, encodes the
// planned ladder into a temp dir, then stores every playlist/segment under
// streaming-playlists/<videoID>/. All playlist URIs are relative, so the files
// serve correctly through the authenticated proxy endpoints.
func (t *HLSTranscoder) Transcode(ctx context.Context, videoID uuid.UUID, sourceKey string) (HLSResult, error) {
	md, err := t.probe.Probe(ctx, sourceKey)
	if err != nil {
		return HLSResult{}, err
	}
	rungs := PlanHLSLadder(md.Width, md.Height)
	if len(rungs) == 0 {
		return HLSResult{}, fmt.Errorf("media: source %q has no probeable video dimensions", sourceKey)
	}

	src, cleanup, err := objectPath(ctx, t.blobs, sourceKey)
	if err != nil {
		return HLSResult{}, err
	}
	defer cleanup()

	tmp, err := os.MkdirTemp("", "vidra-hls-*")
	if err != nil {
		return HLSResult{}, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	for _, r := range rungs {
		dir := filepath.Join(tmp, r.Name())
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return HLSResult{}, err
		}
		cmd := exec.CommandContext(ctx, t.bin, hlsRungArgs(src, dir, r)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return HLSResult{}, fmt.Errorf("media: ffmpeg hls %s for %q: %w: %s", r.Name(), sourceKey, err, tailOf(stderr.String()))
		}
	}
	master := renderMasterPlaylist(rungs)
	if err := os.WriteFile(filepath.Join(tmp, "master.m3u8"), []byte(master), 0o644); err != nil {
		return HLSResult{}, err
	}

	prefix := HLSKeyPrefix(videoID)
	if err := t.storeTree(ctx, tmp, prefix); err != nil {
		return HLSResult{}, err
	}
	res := HLSResult{MasterKey: prefix + "/master.m3u8"}
	for _, r := range rungs {
		res.Renditions = append(res.Renditions, HLSRendition{
			Height:    r.Height,
			Width:     r.Width,
			KeyPrefix: prefix + "/" + r.Name(),
		})
	}
	return res, nil
}

// storeTree Puts every regular file under root into blobs at
// keyPrefix/<relative-path>, walking in deterministic order.
func (t *HLSTranscoder) storeTree(ctx context.Context, root, keyPrefix string) error {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, perr := t.blobs.Put(ctx, keyPrefix+"/"+filepath.ToSlash(rel), f)
		_ = f.Close()
		if perr != nil {
			return perr
		}
	}
	return nil
}

// tailOf returns the last few hundred bytes of s (ffmpeg stderr is long; the
// failure reason is at the end). Never includes user data beyond the media path.
func tailOf(s string) string {
	const keep = 400
	s = strings.TrimSpace(s)
	if len(s) <= keep {
		return s
	}
	return "…" + s[len(s)-keep:]
}
