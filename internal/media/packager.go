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
	"sort"
	"strconv"
	"strings"

	"github.com/vidra/vidra-core/internal/storage"
)

// --- the packager seam -------------------------------------------------------
//
// A transcode has two halves that used to be written as one: ENCODING (what the
// codec does to the pixels and samples — libx264 settings, bitrates, the filter
// graph) and PACKAGING (what container those elementary streams land in, how
// they are cut, and everything that has to happen to the resulting files before
// they can be served). hls.go owns the first half; a Packager owns the second.
//
// WHY THE SEAM HAS THIS SHAPE. The obvious factoring — encode to a mezzanine,
// then run a packager over it — is the wrong one here, and would be a
// performance regression. ffmpeg packages AS it encodes: the HLS muxer is an
// output of the same invocation that runs libx264, so a rung's container
// settings are just more arguments on that rung's output. The CMAF
// implementation this seam exists for (decided 2026-08-22: ffmpeg's dash muxer
// with -hls_playlist 1, emitting fMP4 segments plus HLS playlists) is fused into
// the encode pass in exactly the same way. So the contract is:
//
//	(1) contribute the per-rung OUTPUT/MUXER arguments to an encode invocation,
//	    given the output TRANSPORT and the rung; and
//	(2) own post-encode finalisation — everything from trick-play playlist
//	    fixups to storing the tree — returning what TranscodeHLS needs to build
//	    an HLSResult.
//
// It is deliberately NOT "run a transmux pass over the encoder's output".
//
// A future Shaka/DRM packager genuinely does want a mezzanine (it cannot be
// fused into ffmpeg's output at all), and would need a third mode where the
// encode writes one intermediate and the packager runs afterwards. We are not
// building that now, and the seam should be widened when there is a concrete
// implementation to widen it for — not speculatively.
//
// There is exactly one implementation today: tsPackager, the MPEG-TS behaviour
// this package has always had, extracted unchanged.

// Packager is the packaging half of a transcode: the muxer/container arguments
// each ladder rung's encoder writes through, and the post-encode finalisation of
// what those muxers produced.
//
// Its method parameters are package-internal types (output, rungRef) on purpose:
// packaging composes with the output TRANSPORT seam (scratch directory vs
// blobsink over HTTP), which is an implementation detail of this package, so
// implementations belong here too.
type Packager interface {
	// Name identifies the packaging format for logs and diagnostics ("ts").
	Name() string

	// VariantMuxerArgs are one ladder rung's container arguments and output
	// filenames, appended directly after that rung's ENCODER arguments on a
	// multi-output ffmpeg vector. dest resolves a filename inside the rung's
	// own output directory; out supplies the transport-specific muxer options
	// (an HTTP destination needs -method PUT) and flag adaptations.
	VariantMuxerArgs(out output, dest rungRef, r HLSRung) []string

	// TrickPlayMuxerArgs is the same contribution for the dense-I-frame
	// trick-play pass, whose container settings are format-specific.
	TrickPlayMuxerArgs(out output, dest rungRef, r HLSRung) []string

	// Finalize turns a finished encode into a stored, servable tree: per-rung
	// derivative assets, playlist fixups, the master playlist, and the uploads
	// themselves. It is called once, after every encode pass has completed and
	// (when streaming) the sink has been flushed.
	Finalize(ctx context.Context, req packageRequest) (packageResult, error)
}

// rungRef resolves a filename inside ONE rung's output directory to the value
// ffmpeg should write to. It is the single point where a packager meets the
// output transport: rungDest routes through the output seam (scratch path or
// blobsink URL), dirRef writes into a plain directory.
type rungRef func(name string) string

// rungDest is the rung reference for a ladder output written through out.
func rungDest(out output, r HLSRung) rungRef {
	return func(name string) string { return out.dest(out.rel(r.Name(), name)) }
}

// dirRef is the rung reference for output written straight into dir.
func dirRef(dir string) rungRef {
	return func(name string) string { return filepath.Join(dir, name) }
}

// packageTools are the external dependencies finalisation needs. They are
// passed per request rather than held on the packager so a packager is stateless
// and its argument-contributing half can be used (by the ladder builders, and by
// tests) without wiring up a transcoder.
type packageTools struct {
	blobs   storage.Backend
	ffmpeg  string
	ffprobe string
}

// packageRequest is one finished encode, handed to a packager for finalisation.
type packageRequest struct {
	// sourceKey is the original's storage key. Used only in error messages.
	sourceKey string
	// out is the transport the ladder was written through, so finalisation can
	// read and rewrite its own output wherever it landed.
	out output
	// scratch is the local scratch root; rung i's directory is
	// scratch/<rung.Name()>. It exists even in streaming mode, because the
	// progressive MP4s need a real rewindable file.
	scratch string
	// prefix is the storage-key directory the packaged tree is stored under.
	prefix string
	rungs  []HLSRung
	tools  packageTools
	// onPackagingFailed and onStoreFailed report a fatal error through the
	// caller's per-rung progress projection before it is returned. Both are
	// optional.
	onPackagingFailed func()
	onStoreFailed     func(rungs []HLSRung)
}

func (req packageRequest) packagingFailed() {
	if req.onPackagingFailed != nil {
		req.onPackagingFailed()
	}
}

func (req packageRequest) storeFailed(rungs ...HLSRung) {
	if req.onStoreFailed != nil {
		req.onStoreFailed(rungs)
	}
}

// packageResult is what a finalised package contributes to HLSResult: the master
// playlist's storage key and the stored size of each rung, keyed by rung height.
type packageResult struct {
	masterKey string
	rungSizes map[int]int64
}

// --- MPEG-TS packager --------------------------------------------------------

// Stable names inside one rung's packaged output.
const (
	hlsVariantPlaylistFilename = "playlist.m3u8"
	hlsMasterPlaylistFilename  = "master.m3u8"
	tsSegmentPattern           = "seg_%05d.ts"
)

// tsPackager packages a ladder as MPEG-TS segments with HLS playlists: the
// behaviour this package has always had, and still the only implementation. It
// is stateless; everything it needs arrives on the request.
type tsPackager struct{}

// defaultPackager is the packaging format a transcode uses unless told
// otherwise. There is one today; when CMAF lands, this is the default and
// TranscodeHLS selects the other from configuration.
var defaultPackager Packager = tsPackager{}

// Name implements Packager.
func (tsPackager) Name() string { return "ts" }

// VariantMuxerArgs implements Packager: the HLS muxer configuration for one
// variant.
//
// HLS segments must begin on independently decodable IDR frames; the encoder
// side forces one at every segment boundary (see hlsRungEncodeArgs) and the
// muxer then cuts on those frames and advertises the resulting guarantee with
// independent_segments. Segments reference each other by bare filename — they
// sit next to the variant playlist — which keeps every playlist URI relative so
// the API can proxy them.
func (tsPackager) VariantMuxerArgs(out output, dest rungRef, r HLSRung) []string {
	args := []string{
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", hlsSegmentSeconds),
		"-hls_playlist_type", "vod",
		"-hls_list_size", "0",
		"-hls_flags", out.hlsFlags("independent_segments"),
	}
	args = append(args, out.muxerArgs()...)
	return append(args,
		"-hls_segment_filename", dest(tsSegmentPattern),
		dest(hlsVariantPlaylistFilename),
	)
}

// TrickPlayMuxerArgs implements Packager: a single byte-range media file plus
// its playlist.
//
// FFmpeg's `iframes_only` flag currently emits incorrect repeated @0 offsets for
// an all-I-frame single-file stream, so the valid byte ranges are generated with
// `single_file` and the standards tag is added during finalisation, after the
// playlist shape has been verified (markIFramesOnlyPlaylist).
func (tsPackager) TrickPlayMuxerArgs(out output, dest rungRef, r HLSRung) []string {
	args := []string{
		"-f", "hls",
		"-hls_time", "1",
		"-hls_playlist_type", "vod",
		"-hls_list_size", "0",
		"-hls_flags", out.hlsFlags("single_file"),
	}
	args = append(args, out.muxerArgs()...)
	return append(args,
		"-hls_segment_filename", dest(HLSIFrameMediaFilename),
		dest(HLSIFramePlaylistFilename),
	)
}

// Finalize implements Packager. Everything here is per-rung but decode-free: the
// remuxes are stream copies and the trick-play finalisation is playlist
// bookkeeping.
//
// When the ladder was written to scratch, each rung is uploaded and freed as
// soon as it is finished rather than accumulating the whole tree for one bulk
// store at the end. That matters because remuxHLSDownloads writes a full
// progressive video.mp4 AND a full video-only.mp4 per rung on top of its
// segments -- roughly three times the rung's own encoded size -- so holding
// every rung's derivatives at once was the single largest contributor to peak
// scratch. When the ladder streamed, only those MP4s were ever local.
func (tsPackager) Finalize(ctx context.Context, req packageRequest) (packageResult, error) {
	rungSizes := make(map[int]int64, len(req.rungs))
	trickPlay := make(map[int]hlsTrickPlayInfo, len(req.rungs))
	for i, r := range req.rungs {
		dir := filepath.Join(req.scratch, r.Name())
		rio := rungIO{out: req.out, rung: r, scratch: dir}
		playlist := rio.ref(hlsVariantPlaylistFilename)
		if err := remuxHLSDownloads(ctx, req.tools.ffmpeg, req.sourceKey, playlist, dir, r); err != nil {
			req.packagingFailed()
			return packageResult{}, err
		}
		trickInfo, err := finalizeTrickPlay(ctx, req.tools.ffprobe, rio)
		if err != nil {
			req.packagingFailed()
			return packageResult{}, fmt.Errorf("media: trick-play %s for %q: %w", r.Name(), req.sourceKey, err)
		}
		trickPlay[r.Height] = trickInfo
		if i == 0 {
			// Audio-only is an optional convenience asset. A silent source (or
			// any extraction failure) must not fail the canonical HLS transcode.
			// It is written at the TOP level, so it survives this rung's cleanup.
			dst := filepath.Join(req.scratch, HLSAudioDownloadFilename)
			cmd := exec.CommandContext(ctx, req.tools.ffmpeg, hlsAudioM4AArgs(playlist, dst)...)
			if err := cmd.Run(); err != nil {
				_ = os.Remove(dst)
			}
		}
		// Measured before the upload frees the directory. A streamed ladder is
		// already in the store, so only the local progressive MP4s remain to add.
		local, err := directorySize(dir)
		if err != nil {
			return packageResult{}, err
		}
		rungSizes[r.Height] = local
		if req.out.streaming() {
			rungSizes[r.Height] += req.out.sink.BytesUnder(r.Name())
		}
		if err := storeTree(ctx, req.tools.blobs, dir, req.prefix+"/"+r.Name()); err != nil {
			req.storeFailed(r)
			return packageResult{}, err
		}
		if err := os.RemoveAll(dir); err != nil {
			return packageResult{}, err
		}
	}

	master := renderMasterPlaylist(req.rungs, trickPlay)
	if err := os.WriteFile(filepath.Join(req.scratch, hlsMasterPlaylistFilename), []byte(master), 0o644); err != nil {
		return packageResult{}, err
	}
	// Attribute top-level HLS assets (master playlist and optional audio-only
	// download) to the top rendition so summing video_renditions.size_bytes is
	// the exact stored HLS-tree size, not just the variant subdirectories. The
	// rung directories are already uploaded and removed, so what remains in the
	// scratch root IS exactly the top-level extra.
	extra, err := directorySize(req.scratch)
	if err != nil {
		return packageResult{}, err
	}
	if extra > 0 {
		rungSizes[req.rungs[0].Height] += extra
	}

	// A replacement source may be silent even when the previous generation had
	// audio. storeTree only overwrites files present in the new tree, so remove
	// the old optional derivative before storing a generation that omits it.
	// (Redundant when the backend is a PrefixDeleter -- the prefix was already
	// cleared before the encode -- but backends without that capability still
	// need it.)
	if _, statErr := os.Stat(filepath.Join(req.scratch, HLSAudioDownloadFilename)); os.IsNotExist(statErr) {
		if err := req.tools.blobs.Delete(ctx, req.prefix+"/"+HLSAudioDownloadFilename); err != nil {
			return packageResult{}, err
		}
	} else if statErr != nil {
		return packageResult{}, statErr
	}
	// Only the master playlist and the optional audio-only download are left.
	if err := storeTree(ctx, req.tools.blobs, req.scratch, req.prefix); err != nil {
		req.storeFailed(req.rungs...)
		return packageResult{}, err
	}
	return packageResult{
		masterKey: req.prefix + "/" + hlsMasterPlaylistFilename,
		rungSizes: rungSizes,
	}, nil
}

// --- finalisation steps ------------------------------------------------------

// rungIO is where one rung's just-encoded output actually lives, so the
// decode-free post-processing (progressive remux, trick-play finalisation, size
// accounting) can read and rewrite it without caring whether the ladder was
// written to scratch or streamed into the blob store.
type rungIO struct {
	out  output
	rung HLSRung
	// scratch is always a local directory: even in streaming mode the
	// progressive MP4s must be written to a file, because +faststart rewinds to
	// move the moov atom to the front and an HTTP PUT body cannot be rewound.
	scratch string
}

// ref is what an external tool (ffmpeg/ffprobe) should open to read name.
func (io rungIO) ref(name string) string {
	if io.out.streaming() {
		return io.out.sink.URL(path.Join(io.rung.Name(), name))
	}
	return filepath.Join(io.scratch, name)
}

// read returns the bytes of one of this rung's outputs.
func (io rungIO) read(ctx context.Context, name string) ([]byte, error) {
	if io.out.streaming() {
		return io.out.sink.Get(ctx, path.Join(io.rung.Name(), name))
	}
	return os.ReadFile(filepath.Join(io.scratch, name))
}

// write replaces one of this rung's outputs.
func (io rungIO) write(ctx context.Context, name string, body []byte) error {
	if io.out.streaming() {
		return io.out.sink.Replace(ctx, path.Join(io.rung.Name(), name), body)
	}
	return os.WriteFile(filepath.Join(io.scratch, name), body, 0o644)
}

// finalizeTrickPlay turns one rung's freshly-encoded trick-play output into the
// playlist clients can use: FFmpeg's `iframes_only` flag emits incorrect
// repeated @0 offsets, so the byte ranges are generated with `single_file` and
// the standards tag is added here after the playlist shape is verified. It also
// measures the declared peak bandwidth and probes the codec string for the
// master playlist. No decoding happens here, so it is cheap to run per rung
// after a single shared encode pass.
func finalizeTrickPlay(ctx context.Context, ffprobeBin string, rio rungIO) (hlsTrickPlayInfo, error) {
	playlist, err := rio.read(ctx, HLSIFramePlaylistFilename)
	if err != nil {
		return hlsTrickPlayInfo{}, err
	}
	playlist, err = markIFramesOnlyPlaylist(playlist)
	if err != nil {
		return hlsTrickPlayInfo{}, err
	}
	if err := rio.write(ctx, HLSIFramePlaylistFilename, playlist); err != nil {
		return hlsTrickPlayInfo{}, err
	}
	bandwidth, err := trickPlayPeakBandwidth(playlist)
	if err != nil {
		return hlsTrickPlayInfo{}, err
	}
	codec, err := probeH264CodecString(ctx, ffprobeBin, rio.ref(HLSIFrameMediaFilename))
	if err != nil {
		return hlsTrickPlayInfo{}, err
	}
	return hlsTrickPlayInfo{Bandwidth: bandwidth, Codec: codec}, nil
}

// remuxHLSDownloads emits the two required progressive MP4 assets for a
// rendition. Both are canonical download outputs, so either command failing
// fails the transcode rather than storing a partial tree.
func remuxHLSDownloads(ctx context.Context, ffmpegBin, sourceKey, playlist, dir string, r HLSRung) error {
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
		cmd := exec.CommandContext(ctx, ffmpegBin, hlsProgressiveMP4Args(playlist, dst, output.includeAudio)...)
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
func storeTree(ctx context.Context, blobs storage.Backend, root, keyPrefix string) error {
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
		// Deliberately unhashed (phase-2 storage, work item 2): segments and
		// playlists have no video_files rows, so there is nowhere to record a
		// per-object digest and nothing that would ever read one back. Storage
		// migration verifies this tree object-by-object while it copies.
		_, perr := blobs.Put(ctx, keyPrefix+"/"+filepath.ToSlash(rel), f)
		_ = f.Close()
		if perr != nil {
			return perr
		}
	}
	return nil
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

// --- packaged-asset argument builders & playlist rendering -------------------

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
		b.WriteString(r.Name() + "/" + hlsVariantPlaylistFilename + "\n")
		if tp, ok := trickPlay[r.Height]; ok && tp.Bandwidth > 0 && tp.Codec != "" {
			fmt.Fprintf(&b,
				"#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=%q,URI=%q\n",
				tp.Bandwidth, r.Width, r.Height, tp.Codec, r.Name()+"/"+HLSIFramePlaylistFilename,
			)
		}
	}
	return b.String()
}

// --- trick-play playlist fixups ----------------------------------------------

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
