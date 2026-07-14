package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// hlsSegmentSeconds is the target MPEG-TS segment duration. Six seconds follows
// Apple's VOD authoring guidance while keeping enough switching points for ABR.
const hlsSegmentSeconds = 6

// Stable filenames and content types for the progressive download assets
// emitted alongside HLS playlists. The muxed and video-only files live in a
// rendition directory; the audio file lives beside the master playlist.
const (
	HLSMuxedDownloadFilename     = "video.mp4"
	HLSVideoOnlyDownloadFilename = "video-only.mp4"
	HLSAudioDownloadFilename     = "audio.m4a"
	HLSIFramePlaylistFilename    = "iframe.m3u8"
	HLSIFrameMediaFilename       = "iframe.ts"
	HLSMP4ContentType            = "video/mp4"
	HLSM4AContentType            = "audio/mp4"
)

// hlsRungBitrates is the canonical H.264/AAC bitrate table per ladder rung
// height (config-parity W10). The 1080/720/480/360 values are the original
// hardcoded ladder, unchanged; the other rungs extend the same curve so an
// admin-selected ladder (transcoding_resolutions) has defined bitrates.
var hlsRungBitrates = map[int]struct{ VideoKbps, AudioKbps int }{
	2160: {16000, 160},
	1440: {9000, 160},
	1080: {5000, 160},
	720:  {2800, 128},
	480:  {1400, 128},
	360:  {800, 96},
	240:  {500, 64},
	144:  {250, 64},
}

// HLSCanonicalRungHeights is the full rung universe an admin may enable via
// transcoding_resolutions, tallest first (PeerTube's resolution set minus the
// deliberately absent 0p audio-only rung).
var HLSCanonicalRungHeights = []int{2160, 1440, 1080, 720, 480, 360, 240, 144}

// DefaultHLSResolutionHeights is the shipped default ladder (the original
// hardcoded ladder), tallest first.
var DefaultHLSResolutionHeights = []int{1080, 720, 480, 360}

// IsHLSRungHeight reports whether h is one of the canonical ladder rung
// heights (the transcoding_resolutions validation set).
func IsHLSRungHeight(h int) bool {
	_, ok := hlsRungBitrates[h]
	return ok
}

// HLSEncodeSettings are the runtime-tunable encode knobs (config-parity W10),
// resolved ONCE per transcode job so an admin change applies to the next job
// without a restart and never mid-job.
type HLSEncodeSettings struct {
	// Resolutions is the enabled ladder rung heights (any order; only canonical
	// rungs are honoured). Empty falls back to DefaultHLSResolutionHeights.
	Resolutions []int
	// MaxFPS caps the output frame rate on every generated rung when the SOURCE
	// frame rate is known to exceed it (an fps filter never upsamples a slower
	// source). 0 = no cap. Applied uniformly to all rungs — a documented
	// deviation from PeerTube, whose per-rung fps rules depend on its own
	// ladder machinery; vidra's rungs are planned uniformly.
	MaxFPS int
	// Threads is ffmpeg's -threads value per encode. 0 = ffmpeg's default.
	Threads int
	// OriginalResolution additionally plans a rung at the source's own
	// (even-rounded) size when the source is taller than every enabled rung
	// (transcoding_original_resolution) so ABR playback can reach full quality;
	// the retained original keeps serving progressively regardless.
	OriginalResolution bool
}

// DefaultHLSEncodeSettings is the boot/back-compat encode configuration: the
// original hardcoded ladder with no fps cap, default threading, and no
// original-resolution rung.
func DefaultHLSEncodeSettings() HLSEncodeSettings {
	return HLSEncodeSettings{Resolutions: DefaultHLSResolutionHeights}
}

// HLSRung is one planned rendition of the HLS ladder. Width/Height are the
// exact (even) output dimensions; the bitrates drive both the encoder caps and
// the master playlist's BANDWIDTH attribute. FPS, when positive, caps the
// rung's output frame rate via an fps filter (0 = keep the source rate).
type HLSRung struct {
	Height    int
	Width     int
	VideoKbps int
	AudioKbps int
	FPS       int
}

// Name is the rung's directory name under the video's playlist prefix ("720p").
func (r HLSRung) Name() string { return fmt.Sprintf("%dp", r.Height) }

// Bandwidth is the master-playlist BANDWIDTH value in bits per second: the
// peak video+audio rate plus ~10% container overhead.
func (r HLSRung) Bandwidth() int {
	return (r.VideoKbps + r.AudioKbps) * 1000 * 11 / 10
}

// PlanHLSLadder plans the output ladder for a source of the given dimensions
// with the default encode settings (the original hardcoded ladder). Kept as
// the zero-configuration path; PlanHLSLadderWith is the runtime-tunable form.
func PlanHLSLadder(sourceWidth, sourceHeight int) []HLSRung {
	return PlanHLSLadderWith(DefaultHLSEncodeSettings(), sourceWidth, sourceHeight, 0)
}

// PlanHLSLadderWith plans the output ladder for a source of the given
// dimensions under the supplied encode settings, highest rung first. Enabled
// rungs taller than the source are skipped (no upscaling); when the source is
// shorter than every enabled rung, a single rung at the source's own
// (even-rounded) size is planned with the lowest enabled rung's bitrates.
// When settings.OriginalResolution is on and the source is TALLER than every
// enabled rung, an extra rung at the source's own size is planned on top.
// A positive settings.MaxFPS below a KNOWN source frame rate caps every rung's
// output rate (never upsampling: an unknown or slower source keeps its rate).
// Widths preserve the source aspect ratio, rounded to even (required by H.264
// 4:2:0). Unknown (non-positive) dimensions cannot be planned and return nil.
func PlanHLSLadderWith(settings HLSEncodeSettings, sourceWidth, sourceHeight int, sourceFPS float64) []HLSRung {
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return nil
	}
	heights := normalizeRungHeights(settings.Resolutions)
	fps := 0
	if settings.MaxFPS > 0 && sourceFPS > float64(settings.MaxFPS) {
		fps = settings.MaxFPS
	}
	var rungs []HLSRung
	if settings.OriginalResolution && sourceHeight > heights[0] {
		h := evenDim(sourceHeight)
		br := rungBitratesForHeight(sourceHeight)
		rungs = append(rungs, HLSRung{
			Height:    h,
			Width:     evenScaledWidth(sourceWidth, sourceHeight, h),
			VideoKbps: br.VideoKbps,
			AudioKbps: br.AudioKbps,
			FPS:       fps,
		})
	}
	for _, height := range heights {
		if height > sourceHeight {
			continue
		}
		br := hlsRungBitrates[height]
		rungs = append(rungs, HLSRung{
			Height:    evenDim(height),
			Width:     evenScaledWidth(sourceWidth, sourceHeight, height),
			VideoKbps: br.VideoKbps,
			AudioKbps: br.AudioKbps,
			FPS:       fps,
		})
	}
	if len(rungs) == 0 {
		lowest := hlsRungBitrates[heights[len(heights)-1]]
		h := evenDim(sourceHeight)
		rungs = append(rungs, HLSRung{
			Height:    h,
			Width:     evenScaledWidth(sourceWidth, sourceHeight, h),
			VideoKbps: lowest.VideoKbps,
			AudioKbps: lowest.AudioKbps,
			FPS:       fps,
		})
	}
	return rungs
}

// normalizeRungHeights maps a runtime resolutions value to the planned rung
// heights: unknown heights and duplicates are dropped, the rest sorted tallest
// first. An empty/all-invalid value falls back to the default ladder so a
// defensive read can never plan an empty universe (the registry validator
// refuses to store one).
func normalizeRungHeights(resolutions []int) []int {
	seen := map[int]bool{}
	var heights []int
	for _, h := range resolutions {
		if IsHLSRungHeight(h) && !seen[h] {
			seen[h] = true
			heights = append(heights, h)
		}
	}
	if len(heights) == 0 {
		return DefaultHLSResolutionHeights
	}
	sort.Sort(sort.Reverse(sort.IntSlice(heights)))
	return heights
}

// rungBitratesForHeight resolves bitrates for an arbitrary (original-
// resolution) rung height: the smallest canonical rung at least as tall, or
// the tallest canonical rung's bitrates for anything taller than the table.
func rungBitratesForHeight(height int) struct{ VideoKbps, AudioKbps int } {
	best, found := 0, false
	for h := range hlsRungBitrates {
		if h >= height && (!found || h < best) {
			best, found = h, true
		}
	}
	if !found {
		best = HLSCanonicalRungHeights[0]
	}
	return hlsRungBitrates[best]
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
// URI relative so the API can proxy them. A positive r.FPS appends an fps
// filter (transcoding_max_fps); a positive threads adds -threads
// (transcoding_threads; 0 leaves ffmpeg's own default).
func hlsRungArgs(src, dir string, r HLSRung, threads int) []string {
	vf := fmt.Sprintf("scale=%d:%d", r.Width, r.Height)
	if r.FPS > 0 {
		vf += fmt.Sprintf(",fps=%d", r.FPS)
	}
	args := []string{
		"-y",
		"-i", src,
		"-map", "0:v:0",
		"-map", "0:a:0?",
	}
	if threads > 0 {
		args = append(args, "-threads", fmt.Sprintf("%d", threads))
	}
	return append(args,
		"-c:v", "libx264",
		"-profile:v", "main",
		"-preset", "veryfast",
		"-pix_fmt", "yuv420p",
		"-vf", vf,
		"-b:v", fmt.Sprintf("%dk", r.VideoKbps),
		"-maxrate", fmt.Sprintf("%dk", r.VideoKbps),
		"-bufsize", fmt.Sprintf("%dk", 2*r.VideoKbps),
		// HLS segments must begin on independently decodable IDR frames. Force an
		// IDR at every segment boundary; the muxer then cuts on those frames and
		// advertises the resulting independent-segment guarantee.
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", hlsSegmentSeconds),
		"-c:a", "aac",
		"-b:a", fmt.Sprintf("%dk", r.AudioKbps),
		"-ac", "2",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", hlsSegmentSeconds),
		"-hls_playlist_type", "vod",
		"-hls_list_size", "0",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(dir, "seg_%05d.ts"),
		filepath.Join(dir, "playlist.m3u8"),
	)
}

// hlsTrickPlayArgs builds a dense I-frame rendition alongside one normal rung.
// It samples one frame per second and makes every sample an IDR, then asks the
// HLS muxer for a single byte-range media file. FFmpeg's `iframes_only` flag
// currently emits incorrect repeated @0 offsets for an all-I-frame single-file
// stream, so we generate the valid byte ranges with `single_file` and add the
// standards tag after verifying the playlist shape (markIFramesOnlyPlaylist).
func hlsTrickPlayArgs(src, dir string, r HLSRung, threads int) []string {
	args := []string{
		"-y",
		"-i", src,
		"-map", "0:v:0",
		"-an",
	}
	if threads > 0 {
		args = append(args, "-threads", fmt.Sprintf("%d", threads))
	}
	return append(args,
		"-c:v", "libx264",
		"-profile:v", "main",
		"-preset", "veryfast",
		"-pix_fmt", "yuv420p",
		"-vf", fmt.Sprintf("scale=%d:%d,fps=1", r.Width, r.Height),
		"-g", "1",
		"-keyint_min", "1",
		"-sc_threshold", "0",
		"-crf", "28",
		"-f", "hls",
		"-hls_time", "1",
		"-hls_playlist_type", "vod",
		"-hls_list_size", "0",
		"-hls_flags", "single_file",
		"-hls_segment_filename", filepath.Join(dir, HLSIFrameMediaFilename),
		filepath.Join(dir, HLSIFramePlaylistFilename),
	)
}

// markIFramesOnlyPlaylist validates the FFmpeg single-file playlist and adds
// EXT-X-I-FRAMES-ONLY. The encode is one frame/second with GOP=1, so every
// byte-range segment contains exactly one independently decodable IDR frame.
func markIFramesOnlyPlaylist(playlist []byte) ([]byte, error) {
	s := string(playlist)
	if !strings.HasPrefix(s, "#EXTM3U\n") ||
		!strings.Contains(s, "#EXT-X-BYTERANGE:") ||
		!strings.Contains(s, HLSIFrameMediaFilename) {
		return nil, errors.New("media: malformed trick-play playlist")
	}
	if strings.Contains(s, "#EXT-X-I-FRAMES-ONLY") {
		return playlist, nil
	}
	return []byte(strings.Replace(s, "#EXTM3U\n", "#EXTM3U\n#EXT-X-I-FRAMES-ONLY\n", 1)), nil
}

// trickPlayPeakBandwidth calculates the declared peak bitrate from the actual
// byte ranges rather than guessing from encoder settings. The 10% allowance
// covers MPEG-TS/container overhead consistently with the normal ladder.
func trickPlayPeakBandwidth(playlist []byte) (int64, error) {
	var duration float64
	var peak int64
	for _, line := range strings.Split(string(playlist), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXTINF:") {
			raw := strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ",")
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil || parsed <= 0 {
				return 0, errors.New("media: invalid trick-play segment duration")
			}
			duration = parsed
			continue
		}
		if !strings.HasPrefix(line, "#EXT-X-BYTERANGE:") || duration <= 0 {
			continue
		}
		raw := strings.TrimPrefix(line, "#EXT-X-BYTERANGE:")
		if at := strings.IndexByte(raw, '@'); at >= 0 {
			raw = raw[:at]
		}
		bytes, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || bytes <= 0 {
			return 0, errors.New("media: invalid trick-play byte range")
		}
		bitrate := int64(math.Ceil(float64(bytes*8) / duration * 1.1))
		if bitrate > peak {
			peak = bitrate
		}
		duration = 0
	}
	if peak == 0 {
		return 0, errors.New("media: trick-play playlist has no byte ranges")
	}
	return peak, nil
}

type h264CodecProbe struct {
	Streams []struct {
		CodecName string `json:"codec_name"`
		Profile   string `json:"profile"`
		Level     int    `json:"level"`
	} `json:"streams"`
}

func probeH264CodecString(ctx context.Context, ffprobeBin, mediaPath string) (string, error) {
	cmd := exec.CommandContext(ctx, ffprobeBin,
		"-v", "error",
		"-print_format", "json",
		"-show_entries", "stream=codec_name,profile,level",
		"-select_streams", "v:0",
		mediaPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("media: ffprobe trick-play codec: %w", err)
	}
	return parseH264CodecString(out)
}

func parseH264CodecString(data []byte) (string, error) {
	var probe h264CodecProbe
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", fmt.Errorf("media: parse trick-play codec: %w", err)
	}
	for _, stream := range probe.Streams {
		if stream.CodecName != "h264" || stream.Profile != "Main" || stream.Level <= 0 || stream.Level > 255 {
			continue
		}
		// libx264's Main profile sets constraint_set1_flag (0x40); level is
		// ffprobe's decimal level_idc and is rendered as the final hex byte.
		return fmt.Sprintf("avc1.4d40%02x", stream.Level), nil
	}
	return "", errors.New("media: trick-play output is not H.264 Main profile")
}

// hlsProgressiveMP4Args builds an ffmpeg argument vector that remuxes an HLS
// rendition into a progressive MP4 without re-encoding. The audio map is
// optional for the muxed asset so a silent source still produces a valid MP4.
func hlsProgressiveMP4Args(playlist, dst string, includeAudio bool) []string {
	args := []string{
		"-y",
		"-i", playlist,
		"-map", "0:v:0",
	}
	if includeAudio {
		args = append(args,
			"-map", "0:a:0?",
			"-c", "copy",
		)
	} else {
		args = append(args,
			"-c:v", "copy",
			"-an",
		)
	}
	return append(args,
		"-movflags", "+faststart",
		dst,
	)
}

// hlsAudioM4AArgs builds an ffmpeg argument vector that extracts the top HLS
// rendition's AAC stream into an M4A without re-encoding. Unlike the muxed MP4
// builder, its audio map is required: no-audio inputs fail this best-effort
// command and do not leave an advertised asset behind.
func hlsAudioM4AArgs(playlist, dst string) []string {
	return []string{
		"-y",
		"-i", playlist,
		"-map", "0:a:0",
		"-vn",
		"-c:a", "copy",
		"-movflags", "+faststart",
		dst,
	}
}

// renderMasterPlaylist renders the HLS master playlist for the given rungs.
// Variant URIs are RELATIVE ("720p/playlist.m3u8") so the playlist works when
// proxied from any base path. Pure (no exec/IO) so it is unit-testable.
type hlsTrickPlayInfo struct {
	Bandwidth int64
	Codec     string
}

func renderMasterPlaylist(rungs []HLSRung, trickPlay map[int]hlsTrickPlayInfo) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:4\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	for _, r := range rungs {
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d\n", r.Bandwidth(), r.Width, r.Height)
		b.WriteString(r.Name() + "/playlist.m3u8\n")
		if tp, ok := trickPlay[r.Height]; ok && tp.Bandwidth > 0 && tp.Codec != "" {
			fmt.Fprintf(&b,
				"#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=%q,URI=%q\n",
				tp.Bandwidth, r.Width, r.Height, tp.Codec, r.Name()+"/"+HLSIFramePlaylistFilename,
			)
		}
	}
	return b.String()
}

// HLSRendition describes one produced rendition: its dimensions and the
// storage-key prefix holding its variant playlist + segments.
type HLSRendition struct {
	Height    int
	Width     int
	KeyPrefix string
	SizeBytes int64
}

// HLSResult is a completed transcode: the master playlist's storage key and
// the renditions written under it. When VP9 is enabled it also carries the
// progressive VP9/WebM alternate's storage key + size (empty/zero otherwise).
type HLSResult struct {
	MasterKey  string
	Renditions []HLSRendition
	WebMKey    string
	WebMHeight int
	WebMWidth  int
	WebMBytes  int64
}

// HLSKeyPrefix is the storage-key directory holding a video's HLS output
// (PeerTube-aligned layout: streaming-playlists/<video_id>/ — see
// .ralph/specs/storage-layout.md).
func HLSKeyPrefix(videoID uuid.UUID) string {
	return "streaming-playlists/" + videoID.String()
}

// --- source versions & HLS generations (video file replacement, W14) --------
//
// SEMANTICS (the source-version model, documented here once): the VIDEO is the
// stable identity — its id, URLs and metadata never change across a source
// replacement. What versions is the SOURCE BLOB and the HLS tree derived from
// it:
//
//   - source version 0 is the original upload at the legacy key
//     web-videos/<id><ext>; replacement N stores web-videos/<id>.rN<ext>.
//   - the transcoder derives its output prefix from the SOURCE key: version 0
//     keeps the legacy streaming-playlists/<id>/ layout, version N writes a
//     fresh GENERATION directory streaming-playlists/<id>/rN/.
//
// Because playback resolves every HLS key through the DB-recorded master key
// (streaming_playlists.master_key) and rendition prefixes, writing a new
// generation never disturbs the tree players are streaming; the atomic
// promotion is transcode.storeResult swapping those DB rows to the new
// generation. mediagc then collects the superseded generation and the old
// source blob (both unreferenced). Keys stay opaque to the DB — this scheme is
// parsed only here and in mediagc.

// hlsGenerationDirRE matches a generation directory name ("r3").
var hlsGenerationDirRE = regexp.MustCompile(`^r[1-9][0-9]*$`)

// sourceVersionSuffixRE matches the version tag in a source key's basename
// (the ".r3" in "web-videos/<id>.r3.mp4").
var sourceVersionSuffixRE = regexp.MustCompile(`\.r([1-9][0-9]*)\.[A-Za-z0-9]+$`)

// OriginalVideoKey is the storage key for source version n of a video's
// original file: the legacy web-videos/<id><ext> for version 0 (every pre-W14
// upload), web-videos/<id>.rN<ext> for replacement N. ext includes the dot.
func OriginalVideoKey(videoID uuid.UUID, version int, ext string) string {
	if version <= 0 {
		return "web-videos/" + videoID.String() + ext
	}
	return "web-videos/" + videoID.String() + "." + HLSGenerationName(version) + ext
}

// OriginalKeyVersion parses the source version out of an original-file storage
// key (0 for the legacy unversioned layout or anything unparseable).
func OriginalKeyVersion(key string) int {
	m := sourceVersionSuffixRE.FindStringSubmatch(path.Base(key))
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// HLSGenerationName is the generation directory name for source version n
// ("r3"); "" for version 0 (the legacy in-place layout).
func HLSGenerationName(version int) string {
	if version <= 0 {
		return ""
	}
	return "r" + strconv.Itoa(version)
}

// IsHLSGenerationName reports whether an HLS path segment names a replacement
// generation directory (mediagc uses it to tell generation trees apart from
// legacy rendition dirs like "720p").
func IsHLSGenerationName(segment string) bool {
	return hlsGenerationDirRE.MatchString(segment)
}

// HLSPrefixForSource is the storage-key directory a transcode of the given
// source writes into: the legacy per-video prefix for a version-0 source, a
// fresh generation directory (streaming-playlists/<id>/rN) for replacement N —
// so a re-transcode never overwrites the tree players are currently streaming.
func HLSPrefixForSource(videoID uuid.UUID, sourceKey string) string {
	if gen := HLSGenerationName(OriginalKeyVersion(sourceKey)); gen != "" {
		return HLSKeyPrefix(videoID) + "/" + gen
	}
	return HLSKeyPrefix(videoID)
}

// HLSDownloadKey returns the stable object key for a rendition's progressive
// download asset. includeAudio selects the muxed asset; false selects video
// only. renditionKeyPrefix is the HLSRendition.KeyPrefix value.
func HLSDownloadKey(renditionKeyPrefix string, includeAudio bool) string {
	name := HLSVideoOnlyDownloadFilename
	if includeAudio {
		name = HLSMuxedDownloadFilename
	}
	return path.Join(renditionKeyPrefix, name)
}

// HLSAudioDownloadKey returns the stable object key for the top rendition's
// audio-only asset from the corresponding HLSResult.MasterKey.
func HLSAudioDownloadKey(masterKey string) string {
	return path.Join(path.Dir(masterKey), HLSAudioDownloadFilename)
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
	vp9   bool // also emit a progressive VP9/WebM alternate (SetVP9)
	// settingsFn resolves the runtime encode knobs (config-parity W10). nil =
	// DefaultHLSEncodeSettings. Resolved once per Transcode call so an admin
	// change applies to the next job without a restart and never mid-job.
	settingsFn func() HLSEncodeSettings
}

// SetEncodeSettingsFunc wires the runtime encode-settings provider
// (transcoding_resolutions / transcoding_max_fps / transcoding_threads /
// transcoding_original_resolution). cmd/api points it at the instance-settings
// overlay; nil keeps the shipped defaults.
func (t *HLSTranscoder) SetEncodeSettingsFunc(f func() HLSEncodeSettings) { t.settingsFn = f }

// encodeSettings resolves the current encode knobs (once per job).
func (t *HLSTranscoder) encodeSettings() HLSEncodeSettings {
	if t.settingsFn != nil {
		return t.settingsFn()
	}
	return DefaultHLSEncodeSettings()
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
	return t.TranscodeHLS(ctx, videoID, sourceKey, nil)
}

// TranscodeHLS is the progress-aware HLS path used by the durable worker. Each
// planned rung reports its own lifecycle so the operational job projection can
// render one execution per resolution. The source is always sourceKey (the
// retained original supplied by the queue), never a previous derivative.
func (t *HLSTranscoder) TranscodeHLS(ctx context.Context, videoID uuid.UUID, sourceKey string, progress ProgressFunc) (HLSResult, error) {
	md, err := t.probe.Probe(ctx, sourceKey)
	if err != nil {
		return HLSResult{}, err
	}
	// Runtime encode knobs, resolved once per job (config-parity W10): a
	// settings change applies to the next job, never mid-job.
	settings := t.encodeSettings()
	rungs := PlanHLSLadderWith(settings, md.Width, md.Height, md.FPS)
	if len(rungs) == 0 {
		return HLSResult{}, fmt.Errorf("media: source %q has no probeable video dimensions", sourceKey)
	}
	for _, r := range rungs {
		reportProgress(progress, TranscodeProgress{
			Format: TranscodeFormatHLS, Height: r.Height, Width: r.Width,
			State: ProgressQueued, Stage: "queued", Percent: 0,
		})
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

	rungSizes := make(map[int]int64, len(rungs))
	trickPlay := make(map[int]hlsTrickPlayInfo, len(rungs))
	for i, r := range rungs {
		dir := filepath.Join(tmp, r.Name())
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return HLSResult{}, err
		}
		reportProgress(progress, TranscodeProgress{
			Format: TranscodeFormatHLS, Height: r.Height, Width: r.Width,
			State: ProgressRunning, Stage: "encoding", Percent: 1,
		})
		stderr, runErr := runFFmpegWithProgress(ctx, t.bin, hlsRungArgs(src, dir, r, settings.Threads), md.DurationSeconds, func(percent int) {
			reportProgress(progress, TranscodeProgress{
				Format: TranscodeFormatHLS, Height: r.Height, Width: r.Width,
				State: ProgressRunning, Stage: "encoding", Percent: percent * 9 / 10,
			})
		})
		if runErr != nil {
			reportProgress(progress, TranscodeProgress{
				Format: TranscodeFormatHLS, Height: r.Height, Width: r.Width,
				State: ProgressFailed, Stage: "encoding", Percent: 0,
			})
			return HLSResult{}, fmt.Errorf("media: ffmpeg hls %s for %q: %w: %s", r.Name(), sourceKey, runErr, tailOf(stderr))
		}
		reportProgress(progress, TranscodeProgress{
			Format: TranscodeFormatHLS, Height: r.Height, Width: r.Width,
			State: ProgressRunning, Stage: "packaging", Percent: 92,
		})
		playlist := filepath.Join(dir, "playlist.m3u8")
		if err := t.remuxHLSDownloads(ctx, sourceKey, playlist, dir, r); err != nil {
			reportProgress(progress, TranscodeProgress{
				Format: TranscodeFormatHLS, Height: r.Height, Width: r.Width,
				State: ProgressFailed, Stage: "packaging", Percent: 92,
			})
			return HLSResult{}, err
		}
		trickInfo, err := t.encodeHLSTrickPlay(ctx, src, dir, r, settings.Threads, md.DurationSeconds)
		if err != nil {
			reportProgress(progress, TranscodeProgress{
				Format: TranscodeFormatHLS, Height: r.Height, Width: r.Width,
				State: ProgressFailed, Stage: "packaging", Percent: 92,
			})
			return HLSResult{}, fmt.Errorf("media: trick-play %s for %q: %w", r.Name(), sourceKey, err)
		}
		trickPlay[r.Height] = trickInfo
		rungSizes[r.Height], err = directorySize(dir)
		if err != nil {
			return HLSResult{}, err
		}
		if i == 0 {
			// Audio-only is an optional convenience asset. A silent source (or
			// any extraction failure) must not fail the canonical HLS transcode.
			dst := filepath.Join(tmp, HLSAudioDownloadFilename)
			cmd := exec.CommandContext(ctx, t.bin, hlsAudioM4AArgs(playlist, dst)...)
			if err := cmd.Run(); err != nil {
				_ = os.Remove(dst)
			}
		}
	}
	master := renderMasterPlaylist(rungs, trickPlay)
	if err := os.WriteFile(filepath.Join(tmp, "master.m3u8"), []byte(master), 0o644); err != nil {
		return HLSResult{}, err
	}
	// Attribute top-level HLS assets (master playlist and optional audio-only
	// download) to the top rendition so summing video_renditions.size_bytes is
	// the exact stored HLS-tree size, not just the variant subdirectories.
	totalSize, err := directorySize(tmp)
	if err != nil {
		return HLSResult{}, err
	}
	var rungTotal int64
	for _, size := range rungSizes {
		rungTotal += size
	}
	if extra := totalSize - rungTotal; extra > 0 {
		rungSizes[rungs[0].Height] += extra
	}

	// The output prefix is derived from the SOURCE key (W14): a replacement
	// source writes a fresh generation directory so the tree players are
	// currently streaming is never disturbed; promotion is the DB-row swap in
	// transcode.storeResult.
	prefix := HLSPrefixForSource(videoID, sourceKey)
	// A manual re-run of the same source version uses the same stable prefix.
	// Remove that prior generation immediately before promotion so stale
	// resolutions/segments cannot survive the overwrite. Replacement uploads use
	// a fresh rN prefix, preserving uninterrupted playback until DB promotion.
	if deleter, ok := t.blobs.(storage.PrefixDeleter); ok {
		if err := deleter.DeletePrefix(ctx, prefix); err != nil {
			return HLSResult{}, err
		}
	}
	// A replacement source may be silent even when the previous generation had
	// audio. storeTree only overwrites files present in the new tree, so remove
	// the old optional derivative before storing a generation that omits it.
	if _, statErr := os.Stat(filepath.Join(tmp, HLSAudioDownloadFilename)); os.IsNotExist(statErr) {
		if err := t.blobs.Delete(ctx, prefix+"/"+HLSAudioDownloadFilename); err != nil {
			return HLSResult{}, err
		}
	} else if statErr != nil {
		return HLSResult{}, statErr
	}
	if err := t.storeTree(ctx, tmp, prefix); err != nil {
		for _, r := range rungs {
			reportProgress(progress, TranscodeProgress{
				Format: TranscodeFormatHLS, Height: r.Height, Width: r.Width,
				State: ProgressFailed, Stage: "storing", Percent: 96,
			})
		}
		return HLSResult{}, err
	}
	res := HLSResult{MasterKey: prefix + "/master.m3u8"}
	for _, r := range rungs {
		res.Renditions = append(res.Renditions, HLSRendition{
			Height:    r.Height,
			Width:     r.Width,
			KeyPrefix: prefix + "/" + r.Name(),
			SizeBytes: rungSizes[r.Height],
		})
		reportProgress(progress, TranscodeProgress{
			Format: TranscodeFormatHLS, Height: r.Height, Width: r.Width,
			State: ProgressSucceeded, Stage: "complete", Percent: 100,
		})
	}
	if t.vp9 {
		// Progressive VP9/WebM alternate at the top rung. Best-effort: a VP9
		// failure must not fail the H.264 HLS transcode (VP9 is an extra codec
		// option, not the primary deliverable).
		top := rungs[0]
		if key, size, verr := t.encodeVP9(ctx, videoID, prefix, src, top); verr == nil {
			res.WebMKey = key
			res.WebMBytes = size
			res.WebMHeight = top.Height
			res.WebMWidth = top.Width
		}
	}
	return res, nil
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func (t *HLSTranscoder) encodeHLSTrickPlay(
	ctx context.Context,
	src, dir string,
	r HLSRung,
	threads, durationSeconds int,
) (hlsTrickPlayInfo, error) {
	stderr, err := runFFmpegWithProgress(
		ctx,
		t.bin,
		hlsTrickPlayArgs(src, dir, r, threads),
		durationSeconds,
		nil,
	)
	if err != nil {
		return hlsTrickPlayInfo{}, fmt.Errorf("ffmpeg: %w: %s", err, tailOf(stderr))
	}
	playlistPath := filepath.Join(dir, HLSIFramePlaylistFilename)
	playlist, err := os.ReadFile(playlistPath)
	if err != nil {
		return hlsTrickPlayInfo{}, err
	}
	playlist, err = markIFramesOnlyPlaylist(playlist)
	if err != nil {
		return hlsTrickPlayInfo{}, err
	}
	if err := os.WriteFile(playlistPath, playlist, 0o644); err != nil {
		return hlsTrickPlayInfo{}, err
	}
	bandwidth, err := trickPlayPeakBandwidth(playlist)
	if err != nil {
		return hlsTrickPlayInfo{}, err
	}
	codec, err := probeH264CodecString(ctx, t.probe.bin, filepath.Join(dir, HLSIFrameMediaFilename))
	if err != nil {
		return hlsTrickPlayInfo{}, err
	}
	return hlsTrickPlayInfo{Bandwidth: bandwidth, Codec: codec}, nil
}

// remuxHLSDownloads emits the two required progressive MP4 assets for a
// rendition. Both are canonical download outputs, so either command failing
// fails the transcode rather than storing a partial tree.
func (t *HLSTranscoder) remuxHLSDownloads(ctx context.Context, sourceKey, playlist, dir string, r HLSRung) error {
	outputs := []struct {
		includeAudio bool
		filename     string
		label        string
	}{
		{includeAudio: true, filename: HLSMuxedDownloadFilename, label: "muxed"},
		{includeAudio: false, filename: HLSVideoOnlyDownloadFilename, label: "video-only"},
	}
	for _, output := range outputs {
		dst := filepath.Join(dir, output.filename)
		cmd := exec.CommandContext(ctx, t.bin, hlsProgressiveMP4Args(playlist, dst, output.includeAudio)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("media: ffmpeg hls %s %s download for %q: %w: %s", r.Name(), output.label, sourceKey, err, tailOf(stderr.String()))
		}
	}
	return nil
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
