package media

import (
	"context"
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

	"github.com/vidra/vidra-core/internal/blobsink"
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

// rungBitrates is one rung's encode budget: the video bitrate that drives -b:v
// (and, doubled, -bufsize) and the AAC bitrate for its audio.
type rungBitrates struct{ VideoKbps, AudioKbps int }

// hlsRungBitrates is the canonical H.264/AAC bitrate table per ladder rung
// height (config-parity W10). The 1080/720/480/360 values are the original
// hardcoded ladder, unchanged; the other rungs extend the same curve so an
// admin-selected ladder (transcoding_resolutions) has defined bitrates.
//
// IT IS A ~30fps TABLE. Every value here is the budget for STANDARD-RATE
// content; a high-frame-rate source needs more bits for the same picture at the
// same resolution because there are simply more pictures. See
// fpsVideoBitrateMultiplier for how the table is scaled at planning time.
var hlsRungBitrates = map[int]rungBitrates{
	2160: {16000, 160},
	1440: {9000, 160},
	1080: {5000, 160},
	720:  {2800, 128},
	480:  {1400, 128},
	360:  {800, 96},
	240:  {500, 64},
	144:  {250, 64},
}

// --- frame-rate-aware bitrates -----------------------------------------------
//
// The table above is authored for standard-rate content (23.976–30fps). Handing
// a 50/60fps source the same budget does not produce a 60fps version of the same
// quality; it produces the same total bits spread over twice as many pictures,
// which x264 pays for in blocking and smearing on exactly the motion that made
// the source high-frame-rate in the first place. PeerTube, Apple's HLS authoring
// specification and Bitmovin's ladder guidance all land in the same place: a
// high-frame-rate rung wants roughly 1.5–2x the standard-rate budget for the
// same resolution.
//
// vidra scales the VIDEO budget only, and does it at PLANNING time so the
// decision is packager-independent — the scaled number reaches -b:v/-maxrate/
// -bufsize, the master playlist's BANDWIDTH, the CMAF variant bandwidth, the VP9
// alternate and the progressive MP4s through the one HLSRung every one of them
// already reads. AUDIO IS UNTOUCHED: an AAC stream costs what it costs
// regardless of how many video frames sit beside it.
//
// The rate it scales by is the EFFECTIVE OUTPUT rate, min(sourceFPS, MaxFPS) —
// not the source rate. transcoding_max_fps puts an fps filter on every output
// that renders pictures: each branch of the ladder's shared filter graph, and
// the VP9 alternate, which encodes from the source and so carries its own (see
// vp9WebMArgs). A 60fps source under a 30fps cap therefore really does emit
// 30fps everywhere, and must be budgeted as standard-rate. That the cap is
// UNIFORM is also why one multiplier serves the whole ladder.
const (
	// hlsBaselineFPS is the highest effective rate that still gets the table
	// verbatim. 32 rather than 30 so the 30000/1001 and 25fps families — and the
	// odd 31.x average a variable-rate 30fps source probes as — are unambiguously
	// baseline.
	hlsBaselineFPS = 32.0
	// hlsHighFPS is where the full high-frame-rate budget applies: the 48/50/60fps
	// tier, and everything above it.
	hlsHighFPS = 40.0
	// hlsHighFPSVideoMultiplier is that budget, 1.6x. It sits inside the 1.5–2x
	// band the references agree on and deliberately below a naive doubling: a
	// 60fps encode's frames are far more temporally redundant than a 30fps
	// encode's, so the marginal frame costs well under a full frame's worth of
	// bits.
	hlsHighFPSVideoMultiplier = 1.6
)

// fpsVideoBitrateMultiplier is the video-budget multiplier for an effective
// output frame rate: 1.0 at or below hlsBaselineFPS, hlsHighFPSVideoMultiplier
// at or above hlsHighFPS, and linearly interpolated between the two so the 36fps
// oddities that exist in the wild do not fall off a cliff in either direction.
//
// An UNKNOWN rate (0 — a probe that found no frame rate, or a variable-rate
// source ffprobe could not average) is not high-frame-rate as far as this is
// concerned: it returns 1.0 and the ladder is budgeted exactly as it was before
// this existed. Degrading to the shipped table is the only safe direction, since
// the alternative is over-spending on every source whose rate we cannot read.
func fpsVideoBitrateMultiplier(fps float64) float64 {
	switch {
	case fps <= hlsBaselineFPS:
		return 1
	case fps >= hlsHighFPS:
		return hlsHighFPSVideoMultiplier
	default:
		return 1 + (hlsHighFPSVideoMultiplier-1)*(fps-hlsBaselineFPS)/(hlsHighFPS-hlsBaselineFPS)
	}
}

// scaledForFPS applies a video-budget multiplier to one rung's bitrates, leaving
// the audio budget alone. A multiplier of 1 (the baseline and unknown-rate case)
// returns the table's own values unchanged, bit for bit.
func (b rungBitrates) scaledForFPS(multiplier float64) rungBitrates {
	if multiplier <= 1 {
		return b
	}
	b.VideoKbps = int(math.Round(float64(b.VideoKbps) * multiplier))
	return b
}

// effectiveOutputFPS is the frame rate the ladder will actually emit: the
// source's own rate, or the configured cap when the source is faster than it.
// Zero means unknown, which every caller treats as standard-rate.
func effectiveOutputFPS(settings HLSEncodeSettings, sourceFPS float64) float64 {
	if settings.MaxFPS > 0 && sourceFPS > float64(settings.MaxFPS) {
		return float64(settings.MaxFPS)
	}
	return sourceFPS
}

// hlsAudioOnlyKbps is the CEILING for a source that has no video: the whole
// deliverable is the audio, so it may have the ladder's top-rung audio budget
// rather than any lower tier's. There is no rung to read it from — an audio-only
// plan has none, deliberately: the canonical rung universe is video heights and
// adding a 0p rung to it was explicitly ruled out (see HLSCanonicalRungHeights
// and the transcoding_resolutions validator, which both refuse 0).
const hlsAudioOnlyKbps = 160

// hlsAudioSteps are the AAC budgets this pipeline allocates, ascending. They are
// exactly the values in the rung table, so an audio-only rendition never lands on
// a rate no video rendition could have.
var hlsAudioSteps = []int{64, 96, 128, 160}

// audioOnlyKbpsFor is the AAC budget for an audio-only rendition given the
// SOURCE's own audio bitrate in kbps (0 = the container did not say).
//
// THE RULE: the smallest step in hlsAudioSteps whose nominal rate, allowed a 10%
// overshoot, still covers the source — and hlsAudioOnlyKbps when nothing does.
// An unknown source rate takes the ceiling, because guessing low would degrade
// audio that is the entire content.
//
// It exists because the ceiling alone re-encodes a 64 kbps mono podcast to 160
// kbps stereo: two and a half times the bytes, none of them information the
// source ever had. Lossy-to-lossy transcoding cannot recover what the first
// encoder discarded, so spending above the source is spending on nothing.
//
// The 10% allowance is what makes the rule work on real files rather than on
// nominal ones: a stream authored at 64 kbps probes at 64276 bps once container
// overhead and VBR are counted, and must still be recognised as the 64 step
// instead of being promoted to 96. Real steps are 33-50% apart, so the allowance
// cannot reach the next one.
//
// The channel count is deliberately NOT part of this: output stays -ac 2 (a mono
// source is upmixed) because a single-channel HLS rendition is a compatibility
// question this is not the place to answer. The bitrate is where the waste was.
func audioOnlyKbpsFor(sourceKbps int) int {
	if sourceKbps <= 0 {
		return hlsAudioOnlyKbps
	}
	for _, step := range hlsAudioSteps {
		if step*11/10 >= sourceKbps {
			return step
		}
	}
	return hlsAudioOnlyKbps
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
// The EFFECTIVE output rate — the source's, or the cap when it bites — also
// scales every rung's video budget above hlsBaselineFPS (fpsVideoBitrateMultiplier).
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
	// One multiplier for the whole ladder, because the fps filter is uniform
	// across it: every rung emits the same effective rate, so every rung's budget
	// moves by the same factor.
	multiplier := fpsVideoBitrateMultiplier(effectiveOutputFPS(settings, sourceFPS))
	var rungs []HLSRung
	if settings.OriginalResolution && sourceHeight > heights[0] {
		h := evenDim(sourceHeight)
		br := rungBitratesForHeight(sourceHeight).scaledForFPS(multiplier)
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
		br := hlsRungBitrates[height].scaledForFPS(multiplier)
		rungs = append(rungs, HLSRung{
			Height:    evenDim(height),
			Width:     evenScaledWidth(sourceWidth, sourceHeight, height),
			VideoKbps: br.VideoKbps,
			AudioKbps: br.AudioKbps,
			FPS:       fps,
		})
	}
	if len(rungs) == 0 {
		lowest := hlsRungBitrates[heights[len(heights)-1]].scaledForFPS(multiplier)
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
func rungBitratesForHeight(height int) rungBitrates {
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
// so silent sources still transcode. A positive r.FPS appends an fps filter
// (transcoding_max_fps); a positive threads adds -threads
// (transcoding_threads; 0 leaves ffmpeg's own default).
//
// ORACLE, NOT PRODUCTION. Nothing in the pipeline calls this: the ladder builder
// below encodes every rung, including a single-rung ladder, from one decode. It
// is retained deliberately as the TEST ORACLE the ladder is asserted equivalent
// to (TestHLSLadderRungSettingsMatchSingleRungForm), and it holds that status
// only because it is RECOMPOSED from the same encode-args and packager pieces
// the ladder uses — the equivalence is true by construction, not by two lists
// being kept in sync by hand. Rebuild it that way if you change it.
func hlsRungArgs(src source, dir string, r HLSRung, threads int) []string {
	vf := fmt.Sprintf("scale=%d:%d", r.Width, r.Height)
	if r.FPS > 0 {
		vf += fmt.Sprintf(",fps=%d", r.FPS)
	}
	args := []string{"-y"}
	args = append(args, src.inputArgs()...)
	args = append(args,
		"-map", "0:v:0",
		"-map", "0:a:0?",
	)
	if threads > 0 {
		args = append(args, "-threads", fmt.Sprintf("%d", threads))
	}
	args = append(args, hlsRungEncodeArgs(r, vf)...)
	return append(args, defaultPackager.VariantMuxerArgs(localOutput(""), dirRef(dir), r)...)
}

// output is where an encoder's files go: a local scratch directory, or a
// blobsink that streams each file straight into the object store as ffmpeg
// writes it. A local output renders exactly the paths it always did, so the
// default pipeline is unchanged.
type output struct {
	root string // local scratch root; empty when sink is set
	sink *blobsink.Sink
}

func localOutput(root string) output        { return output{root: root} }
func sinkOutput(s *blobsink.Sink) output    { return output{sink: s} }
func (o output) streaming() bool            { return o.sink != nil }
func (o output) rel(parts ...string) string { return path.Join(parts...) }

// dest is the value ffmpeg writes to for a path relative to the output root.
func (o output) dest(rel string) string {
	if o.sink != nil {
		return o.sink.URL(rel)
	}
	return filepath.Join(o.root, filepath.FromSlash(rel))
}

// muxerArgs are the extra per-output options an HTTP destination needs. They
// must precede the output filename.
//
// -http_persistent is deliberately OFF. Reusing one connection for the whole
// ladder puts every write in a single queue: an HTTP server handles a
// connection's requests in order, so each segment's store must finish before the
// muxer's next request is even read. Against a real object store that serialises
// the encode behind Put latency, and it strands the muxer's LAST write — the
// final playlist rewrite, which ffmpeg pushes out and then exits without waiting
// for. Queued behind an unfinished segment store, that request is still unread
// in the socket when the process is gone: the sink cannot see it, cannot wait
// for it, and Flush writes a ladder with no playlist. A connection per object
// costs a loopback handshake and removes the queue entirely.
func (o output) muxerArgs() []string {
	if o.sink == nil {
		return nil
	}
	return []string{"-method", "PUT", "-http_persistent", "0"}
}

// hlsFlags adapts an -hls_flags value to the destination. Over HTTP the muxer's
// write-to-.tmp-then-rename dance is meaningless and would PUT a temp-named
// object the sink stores verbatim, so it is switched off.
func (o output) hlsFlags(base string) string {
	if o.sink == nil {
		return base
	}
	return base + "-temp_file"
}

// splitChain renders the filter-graph prologue that decodes the source's video
// stream once and forks it into n identically-timed branches labelled [b0]..
// [bN-1]. A single branch needs no split at all. Returns the chain (empty when
// n <= 1 and the caller should read [0:v] directly) and the branch labels.
func splitChain(n int) (chain string, labels []string) {
	labels = make([]string, n)
	if n == 1 {
		labels[0] = "0:v"
		return "", labels
	}
	var b strings.Builder
	b.WriteString("[0:v]split=")
	b.WriteString(strconv.Itoa(n))
	for i := range labels {
		labels[i] = fmt.Sprintf("b%d", i)
		b.WriteString("[")
		b.WriteString(labels[i])
		b.WriteString("]")
	}
	return b.String(), labels
}

// perOutputThreads splits a job's thread budget across the encoders that now run
// CONCURRENTLY inside one ffmpeg process. transcoding_threads is documented as a
// per-job value, and before decode-once the rungs ran sequentially so the whole
// budget belonged to whichever encoder was running. Handing every concurrent
// encoder the full number instead would multiply a deliberately-restrained
// setting by the ladder height. 0 (ffmpeg's own default) stays 0.
//
// THE FLOOR OF 1 IS AN OVERSHOOT, and always has been. Once the budget is
// smaller than the number of encoders, every encoder still gets one thread, so a
// 4-rung ladder under transcoding_threads=2 asks for 4 and a 3-codec 4-rung one
// asks for 12. The alternative is worse — a share of zero means "ffmpeg's own
// default", which is every core — so this is the honest local minimum rather
// than a bug to fix here. An operator running several codecs on a small budget
// should read transcoding_threads as a per-ENCODER floor, not a hard cap.
func perOutputThreads(threads, outputs int) int {
	if threads <= 0 || outputs <= 1 {
		return threads
	}
	if per := threads / outputs; per > 0 {
		return per
	}
	return 1
}

// hlsLadderArgs builds ONE ffmpeg argument vector that decodes the source a
// single time and writes every planned rung's HLS variant, packaged by the
// default (MPEG-TS) packager. It replaces a loop of one full decode per rung: a
// 4-rung ladder decoded the source four times to produce four scalings of the
// same frames.
//
// Each rung keeps byte-for-byte the same encoder and muxer settings it had as a
// standalone command (see hlsRungArgs, the oracle the ladder is asserted
// against) — only the decode is shared.
func hlsLadderArgs(src source, out output, rungs []HLSRung, threads int) []string {
	return hlsLadderArgsWith(defaultPackager, src, out, ladderPlan{rungs: rungs, threads: threads})
}

// hlsLadderArgsWith is hlsLadderArgs with the packaging format selected: the
// DECODE half (one input, one filter graph forking it into a branch per rung)
// comes from this file, and the OUTPUT half — how those branches are wired into
// outputs, with which encoder and container settings — from pkg. That is why a
// second packaging format is a second Packager rather than a second ladder
// builder, even though CMAF's output half is one shared muxer block rather than
// one per rung.
//
// THE FILTER GRAPH IS FORMAT-INDEPENDENT, and deliberately so. It is tempting to
// let a packager add filters here — CMAF's manifests publish an aspect ratio per
// representation where MPEG-TS playlists publish none, so "normalise the aspect"
// looks like a packaging concern. It is a trap. scale already sets each output's
// sample aspect ratio to whatever PRESERVES the source's display aspect through
// the rung's own rounding, which is the only correct answer and is what makes
// every rung agree with every other one. Anything added on top does not refine
// that; it overwrites it, and for a source whose coded dimensions are not its
// display dimensions — a rotated phone video, anamorphic SD — it silently
// re-renders the whole ladder at the wrong shape. Keeping the graph identical
// across formats is what makes CMAF geometry provably equal to MPEG-TS geometry.
func hlsLadderArgsWith(pkg Packager, src source, out output, plan ladderPlan) []string {
	args := []string{"-y"}
	args = append(args, src.inputArgs()...)

	// An audio-only plan has no video to fork, so it has no filter graph at all —
	// an empty -filter_complex is a syntax error, and a split into zero branches
	// is meaningless. The packager's output half maps the audio straight off the
	// input.
	if plan.audioOnly() {
		return append(args, pkg.LadderOutputArgs(out, plan)...)
	}

	chain, labels := splitChain(len(plan.rungs))
	chains := make([]string, 0, len(plan.rungs)+1)
	if chain != "" {
		chains = append(chains, chain)
	}
	// One filter-graph output per REPRESENTATION — rung × codec — laid out
	// h264 rungs first in ladder order, then hevc, then av1 (ladderPlan.repIndex).
	// The rung is SCALED once and the scaled frames are forked to that rung's
	// encoders, so adding a codec adds encoders and not scalers.
	profiles := plan.profiles()
	plan.labels = make([]string, plan.videoReps())
	for i, r := range plan.rungs {
		vf := fmt.Sprintf("scale=%d:%d", r.Width, r.Height)
		if r.FPS > 0 {
			vf += fmt.Sprintf(",fps=%d", r.FPS)
		}
		outs := make([]string, len(profiles))
		for c := range profiles {
			rep := plan.repIndex(c, i)
			plan.labels[rep] = fmt.Sprintf("v%d", rep)
			outs[c] = "[" + plan.labels[rep] + "]"
		}
		// A single codec — every ordinary install — has nothing to fork, and the
		// chain is byte-for-byte the one this package has always emitted. `split=1`
		// would be legal and would mean the same thing; it would also change the
		// graph for every deployment that never asked for a second codec.
		fork := ""
		if len(outs) > 1 {
			fork = fmt.Sprintf(",split=%d", len(outs))
		}
		chains = append(chains, fmt.Sprintf("[%s]%s%s%s", labels[i], vf, fork, strings.Join(outs, "")))
	}
	args = append(args, "-filter_complex", strings.Join(chains, ";"))

	return append(args, pkg.LadderOutputArgs(out, plan)...)
}

// hlsTrickPlayLadderArgs builds ONE ffmpeg argument vector emitting every rung's
// dense I-frame trick-play rendition from a single decode, for the same reason
// as hlsLadderArgs. It is a separate command from the variant ladder rather than
// more outputs on it: trick-play is a packaging-stage nicety whose failure is
// reported separately, and folding it in would double the number of encoders
// running concurrently in one process.
func hlsTrickPlayLadderArgs(src source, out output, rungs []HLSRung, threads int) []string {
	return hlsTrickPlayLadderArgsWith(defaultPackager, src, out, ladderPlan{rungs: rungs, threads: threads})
}

// hlsTrickPlayLadderArgsWith is hlsTrickPlayLadderArgs with the packaging format
// selected. Its graph is format-independent for the same reason the variant
// ladder's is.
func hlsTrickPlayLadderArgsWith(pkg Packager, src source, out output, plan ladderPlan) []string {
	args := []string{"-y"}
	args = append(args, src.inputArgs()...)

	chain, labels := splitChain(len(plan.rungs))
	chains := make([]string, 0, len(plan.rungs)+1)
	if chain != "" {
		chains = append(chains, chain)
	}
	plan.labels = make([]string, len(plan.rungs))
	for i, r := range plan.rungs {
		plan.labels[i] = fmt.Sprintf("t%d", i)
		chains = append(chains, fmt.Sprintf("[%s]scale=%d:%d,fps=1[%s]",
			labels[i], r.Width, r.Height, plan.labels[i]))
	}
	args = append(args, "-filter_complex", strings.Join(chains, ";"))

	return append(args, pkg.TrickPlayOutputArgs(out, plan)...)
}

// streamSpec renders the ffmpeg stream-specifier suffixes ONE stream's encoder
// options carry on a particular output.
//
// It exists because the two packaging formats wire their encoders into outputs
// differently. MPEG-TS gives every rung its own output holding exactly one video
// and one audio stream, so its options need no more qualification than the type
// they have always carried ("-c:v", "-preset"). CMAF puts EVERY rung on one dash
// output, where an unqualified "-b:v" would be applied to all of them and the
// ladder would silently collapse to a single bitrate — each option must name its
// stream ("-c:v:1", "-preset:v:1").
//
// typed suffixes options that are already type-qualified today; bare suffixes
// those written with no specifier at all. Keeping the two apart is what lets the
// MPEG-TS vectors stay byte-for-byte what they have always been.
type streamSpec struct {
	typed string
	bare  string
}

// soleVideoStream / soleAudioStream are the specifiers for an output carrying
// exactly one stream of that type: precisely the qualification this package
// emitted before CMAF existed.
var (
	soleVideoStream = streamSpec{typed: ":v"}
	soleAudioStream = streamSpec{typed: ":a"}
)

// sharedVideoStream is the specifier for video stream i of an output carrying
// several. sharedAudioStream is its audio counterpart.
func sharedVideoStream(i int) streamSpec {
	s := ":v:" + strconv.Itoa(i)
	return streamSpec{typed: s, bare: s}
}

func sharedAudioStream(i int) streamSpec {
	s := ":a:" + strconv.Itoa(i)
	return streamSpec{typed: s, bare: s}
}

// hlsRungEncodeArgs is one variant's ENCODER configuration — codec, rate control
// and the key-frame cadence — for an output that carries it alone, shared by the
// MPEG-TS ladder and its oracle so they cannot drift. It stops at the elementary
// streams: what container they land in is the packager's half of the vector. vf
// is the scale filter chain for the single-rung oracle; the ladder passes ""
// because its filter graph has already scaled the branch.
//
// It is H.264 by construction and not by default. This is the MPEG-TS shape: one
// self-contained variant per rung, which is the frozen compatibility/rollback
// path and stays single-codec (see codec.go).
func hlsRungEncodeArgs(r HLSRung, vf string) []string {
	return append(
		hlsRungVideoEncodeArgs(r, h264Profile, soleVideoStream, vf),
		hlsAudioEncodeArgs(r.AudioKbps, soleAudioStream)...,
	)
}

// hlsRungVideoEncodeArgs is the VIDEO half of ONE representation's encoder
// configuration: rung r encoded with codec profile p, qualified by spec so it can
// be one of several encoders on a shared output.
//
// The argument ORDER is fixed and the H.264 profile's fields are exactly the
// values this function used to spell inline, so the vector an ordinary install
// emits is byte-for-byte the one it emitted before the registry existed. The
// per-codec variation is entirely in the profile: a container tag (mandatory for
// HEVC, absent otherwise), whether the encoder has a -profile option at all, the
// preset's spelling, and whether it accepts capped VBR.
func hlsRungVideoEncodeArgs(r HLSRung, p codecProfile, spec streamSpec, vf string) []string {
	args := []string{"-c" + spec.typed, p.Encoder}
	if p.Tag != "" {
		// Not cosmetic: without it ffmpeg writes hev1 into fMP4, which Safari
		// refuses and which makes the muxer's own CODECS string come out empty.
		args = append(args, "-tag"+spec.typed, p.Tag)
	}
	if p.Profile != "" {
		args = append(args, "-profile"+spec.typed, p.Profile)
	}
	args = append(args,
		"-preset"+spec.bare, p.Preset,
		"-pix_fmt"+spec.bare, "yuv420p",
	)
	if vf != "" {
		args = append(args, "-vf", vf)
	}
	// Rate control. Both modes end at the SAME ceiling — -maxrate at the rung's
	// budget, -bufsize at twice it — because that ceiling is what the master
	// playlist's BANDWIDTH promises a player. What differs is what the encoder
	// aims at underneath it: a bitrate target for capped VBR, a quality target
	// for capped CRF. See rateControl for the measurements behind that split.
	kbps := p.videoKbps(r)
	switch p.Rate {
	case rateCappedCRF:
		args = append(args, "-crf"+spec.bare, strconv.Itoa(p.CRF))
	default:
		args = append(args, "-b"+spec.typed, fmt.Sprintf("%dk", kbps))
	}
	args = append(args,
		"-maxrate"+spec.bare, fmt.Sprintf("%dk", kbps),
		"-bufsize"+spec.bare, fmt.Sprintf("%dk", 2*kbps),
	)
	return append(args,
		// The key-frame cadence is an ENCODER setting with a packaging purpose:
		// every packaging format cuts segments on independently decodable IDR
		// frames, so one is forced at each segment boundary and the muxer cuts
		// there. It stays on this side of the seam because only the encoder can
		// place a key frame — and it is codec-independent, because it is ffmpeg's
		// own frame-level flag rather than any encoder's option.
		"-force_key_frames"+spec.bare, fmt.Sprintf("expr:gte(t,n_forced*%d)", hlsSegmentSeconds),
	)
}

// hlsAudioEncodeArgs is the AAC configuration for an output's audio stream.
//
// MPEG-TS encodes it once per rung (each variant is self-contained); CMAF
// encodes it ONCE for the whole ladder, as the single audio representation every
// video representation references — which is why the bitrate is a parameter
// rather than being read off a rung.
func hlsAudioEncodeArgs(kbps int, spec streamSpec) []string {
	return []string{
		"-c" + spec.typed, "aac",
		"-b" + spec.typed, fmt.Sprintf("%dk", kbps),
		"-ac" + spec.bare, "2",
	}
}

// trickPlayEncodeArgs is the dense-I-frame ENCODER configuration: one frame per
// second, every sample an IDR. As above it stops at the elementary stream; the
// packager decides how those frames are stored and indexed
// (Packager.TrickPlayMuxerArgs). vf is the filter chain for the single-rung
// oracle; the ladder passes "" because its graph already scaled.
func trickPlayEncodeArgs(vf string) []string {
	args := []string{
		"-c:v", "libx264",
		"-profile:v", "main",
		"-preset", "veryfast",
		"-pix_fmt", "yuv420p",
	}
	if vf != "" {
		args = append(args, "-vf", vf)
	}
	return append(args,
		"-g", "1",
		"-keyint_min", "1",
		"-sc_threshold", "0",
		"-crf", "28",
	)
}

// hlsTrickPlayArgs builds a dense I-frame rendition alongside one normal rung.
//
// ORACLE, NOT PRODUCTION — see hlsRungArgs. Recomposed from the same encode-args
// and packager pieces the trick-play ladder uses, so the two cannot drift.
func hlsTrickPlayArgs(src source, dir string, r HLSRung, threads int) []string {
	args := []string{"-y"}
	args = append(args, src.inputArgs()...)
	args = append(args,
		"-map", "0:v:0",
		"-an",
	)
	if threads > 0 {
		args = append(args, "-threads", fmt.Sprintf("%d", threads))
	}
	args = append(args, trickPlayEncodeArgs(fmt.Sprintf("scale=%d:%d,fps=1", r.Width, r.Height))...)
	return append(args, defaultPackager.TrickPlayMuxerArgs(localOutput(""), dirRef(dir), r)...)
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
	MasterKey string
	// Format is the packaging format this tree was written in (HLSFormatTS /
	// HLSFormatCMAF), recorded per video so serving can tell a CMAF tree from a
	// legacy MPEG-TS one. Empty from a caller that predates the record; the
	// store reads that as HLSFormatTS.
	Format     string
	Renditions []HLSRendition
	WebMKey    string
	WebMHeight int
	WebMWidth  int
	WebMBytes  int64
	// WebMSHA256 is the lowercase hex digest of the stored VP9/WebM object,
	// taken from its upload stream (phase-2 storage, work item 2). The HLS
	// segments and playlists under MasterKey deliberately carry no digest: they
	// have no video_files rows to hold one, and storage migration verifies them
	// in flight instead.
	WebMSHA256 string
	// WebVideos are the standalone progressive MP4s DERIVED from this tree's own
	// per-rung downloads, present only when the caller asked for them
	// (TranscodeAll). They ride out on the HLS result because they are produced
	// inside the HLS packaging window and cannot be produced anywhere else
	// without decoding the source again.
	WebVideos []WebVideoResult
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
	// streamOutput sends the HLS ladder straight into the blob store through a
	// loopback blobsink instead of writing it to scratch and uploading after
	// (SetStreamOutput). Default OFF: it trades scratch disk for object-store
	// bandwidth, because the pipeline reads its own output back to build the
	// progressive downloads, and that trade is only right on some deployments.
	streamOutput bool
	// settingsFn resolves the runtime encode knobs (config-parity W10). nil =
	// DefaultHLSEncodeSettings. Resolved once per Transcode call so an admin
	// change applies to the next job without a restart and never mid-job.
	settingsFn func() HLSEncodeSettings
	// pkg is the packaging format this transcoder emits: the muxer arguments the
	// encoders write through and the post-encode finalisation of what they
	// produced. nil = defaultPackager (MPEG-TS), which is every deployment today.
	pkg Packager
	// codecs is the video codec plan every ladder is encoded with, H.264 first
	// (SetVideoCodecs). nil = defaultVideoCodecs, the single-codec H.264 ladder
	// this package emitted before codec profiles existed.
	codecs []codecProfile
}

// packager resolves this transcoder's packaging implementation.
func (t *HLSTranscoder) packager() Packager {
	if t.pkg != nil {
		return t.pkg
	}
	return defaultPackager
}

// packageTools are the binaries and backend the packager's finalisation shells
// out to and stores through.
func (t *HLSTranscoder) packageTools() packageTools {
	return packageTools{blobs: t.blobs, ffmpeg: t.bin, ffprobe: t.probe.bin}
}

// SetPackager selects the packaging format this transcoder emits by name
// (PackagerTS / PackagerCMAF), the boot-baked TRANSCODING_PACKAGER choice. An
// unknown name is refused rather than silently falling back, so a typo cannot
// quietly re-package a whole deployment.
//
// It changes only what NEW transcodes produce. Every previously packaged video
// keeps serving from its own recorded format, which is what makes the rollback
// to MPEG-TS config-only.
func (t *HLSTranscoder) SetPackager(name string) error {
	pkg, ok := packagerByName(name)
	if !ok {
		return fmt.Errorf("media: unknown packager %q (want %s|%s)", name, PackagerCMAF, PackagerTS)
	}
	t.pkg = pkg
	return nil
}

// SetVideoCodecs selects the ADDITIONAL video codecs every ladder emits
// alongside H.264 (TRANSCODING_HEVC_ENABLED / TRANSCODING_AV1_ENABLED, both
// boot-baked). H.264 is not a choice — it is the compatibility floor and the
// source of the progressive downloads, the derived web videos, the audio-only
// extraction and trick-play — so this only ever ADDS representations.
//
// Both extras are false on an ordinary install, and this then stores nothing:
// the ladder is the single-codec one, encoder for encoder.
//
// It is fallible on purpose, and refuses at BOOT rather than per job, because
// both ways of getting this wrong are otherwise invisible until the first upload:
//
//   - a packaging format that cannot express several codecs (MPEG-TS — a variant
//     there is a rendition DIRECTORY named for a height, with no room for a
//     second encoding of the same height). Config refuses the combination too;
//     this is the second gate, at the point the transcoder is actually built.
//   - an ffmpeg build without the encoder. The image ships one that has both, but
//     a host binary, a distro rebuild or a slimmed image may not.
//
// Call it AFTER SetPackager: the first check reads the selected format.
//
// The encoder probe runs for EVERY plan, including the H.264-only default. It
// was once skipped there — nothing extra was enabled, so there was nothing to
// check — which made the H.264 branch of the probe unreachable and left the one
// encoder every deployment depends on unverified. An image built without libx264
// passes the ffmpeg-is-on-PATH check and then fails every transcode; one
// subprocess at boot is the whole price of catching that.
func (t *HLSTranscoder) SetVideoCodecs(hevc, av1 bool) error {
	profiles := videoCodecProfiles(hevc, av1)
	if len(profiles) > 1 && !t.packager().SupportsMultiCodec() {
		var extra []string
		for _, p := range profiles[1:] {
			extra = append(extra, p.Name)
		}
		return fmt.Errorf(
			"media: the %q packager emits H.264 only, so it cannot carry %s: use the %s packager or turn the extra codecs off",
			t.packager().Name(), strings.Join(extra, "+"), PackagerCMAF)
	}
	if err := verifyVideoEncoders(context.Background(), t.bin, profiles); err != nil {
		return err
	}
	t.codecs = profiles
	return nil
}

// videoCodecs resolves this transcoder's codec plan.
func (t *HLSTranscoder) videoCodecs() []codecProfile {
	if len(t.codecs) == 0 {
		return defaultVideoCodecs
	}
	return t.codecs
}

// packagerByName resolves a TRANSCODING_PACKAGER value to its implementation.
func packagerByName(name string) (Packager, bool) {
	switch name {
	case PackagerTS:
		return tsPackager{}, true
	case PackagerCMAF:
		return cmafPackager{}, true
	default:
		return nil, false
	}
}

// IsPackagerName reports whether name selects a packaging format (the
// TRANSCODING_PACKAGER validation set).
func IsPackagerName(name string) bool {
	_, ok := packagerByName(name)
	return ok
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

// Probe reports the source's dimensions, duration and frame rate. It is exposed
// so a caller running several targets against one source can probe ONCE and
// hand the result to each — probing is a full source read on a backend without
// local paths, so re-probing per target is pure waste.
func (t *HLSTranscoder) Probe(ctx context.Context, sourceKey string) (Metadata, error) {
	return t.probe.Probe(ctx, sourceKey)
}

// Transcode probes the source at sourceKey for its dimensions, encodes the
// planned ladder into a temp dir, then stores every playlist/segment under
// streaming-playlists/<videoID>/. All playlist URIs are relative, so the files
// serve correctly through the authenticated proxy endpoints.
func (t *HLSTranscoder) Transcode(ctx context.Context, videoID uuid.UUID, sourceKey string) (HLSResult, error) {
	md, err := t.Probe(ctx, sourceKey)
	if err != nil {
		return HLSResult{}, err
	}
	return t.TranscodeHLS(ctx, videoID, sourceKey, md, nil)
}

// TranscodeHLS is the progress-aware HLS path used by the durable worker. Each
// planned rung reports its own lifecycle so the operational job projection can
// render one execution per resolution. The source is always sourceKey (the
// retained original supplied by the queue), never a previous derivative.
//
// md is the caller's already-obtained probe of sourceKey; the worker probes once
// per job and shares it across targets.
func (t *HLSTranscoder) TranscodeHLS(ctx context.Context, videoID uuid.UUID, sourceKey string, md Metadata, progress ProgressFunc) (HLSResult, error) {
	return t.transcodeHLS(ctx, videoID, sourceKey, md, progress, false)
}

// TranscodeAll produces BOTH output classes from ONE ladder encode: the
// streaming tree, and the standalone progressive "web video" MP4s COPIED out of
// that tree's own per-rung downloads rather than encoded a second time.
//
// It is one method rather than two calls because the saving depends entirely on
// the two happening together: the bytes the web videos need exist only inside
// the packaging window, on local scratch, between the remux that writes them and
// the upload that frees them. Running the two targets independently is what made
// a target='all' job decode its source three times — see the comparison at the
// top of web_video.go for why the second encode was producing a strictly
// equivalent file.
//
// A failure is a failure of the whole thing: the derivation runs inside
// packaging, where a fault already fails the transcode without promoting
// anything.
func (t *HLSTranscoder) TranscodeAll(ctx context.Context, videoID uuid.UUID, sourceKey string, md Metadata, progress ProgressFunc) (HLSResult, []WebVideoResult, error) {
	res, err := t.transcodeHLS(ctx, videoID, sourceKey, md, progress, true)
	if err != nil {
		return HLSResult{}, nil, err
	}
	return res, res.WebVideos, nil
}

// transcodeHLS is TranscodeHLS with the web-video derivation switched on or off.
func (t *HLSTranscoder) transcodeHLS(ctx context.Context, videoID uuid.UUID, sourceKey string, md Metadata, progress ProgressFunc, deriveWebVideos bool) (HLSResult, error) {
	// Runtime encode knobs, resolved once per job (config-parity W10): a
	// settings change applies to the next job, never mid-job.
	settings := t.encodeSettings()
	rungs := PlanHLSLadderWith(settings, md.Width, md.Height, md.FPS)

	// The packaging format contributes the muxer half of every rung's output
	// arguments to the very same invocation that encodes it — ffmpeg packages as
	// it encodes, so there is no separate packaging pass to run afterwards (see
	// packager.go).
	pkg := t.packager()

	// A source with no video is not an unprobeable source. It used to be treated
	// as one — the ladder planner returns nothing for it, and "nothing to plan"
	// read as "nothing to read" — so a podcast retried five times and
	// dead-lettered. It is packaged as a single audio rendition instead, on the
	// formats that can express one.
	audioOnly := md.AudioOnly()
	if len(rungs) == 0 && !audioOnly {
		return HLSResult{}, fmt.Errorf("media: source %q has no probeable video dimensions", sourceKey)
	}
	if audioOnly && !pkg.SupportsAudioOnly() {
		// PERMANENT. The verdict is a function of the source and the deployment's
		// configured packager, neither of which a retry changes: five attempts
		// would spend a quarter of an hour of backoff arriving at this same
		// sentence. Dead-letter it now, with the sentence.
		return HLSResult{}, permanentf(
			"media: source %q is audio-only, which the %q packager cannot express: audio-only requires the %s packager",
			sourceKey, pkg.Name(), PackagerCMAF)
	}

	// What the per-resolution job projection reports one execution for. An
	// audio-only transcode has no resolution, so it reports a single 0x0
	// execution rather than none at all — a job with no steps renders as an empty
	// operational view, which reads as "nothing happened".
	steps := rungs
	if audioOnly {
		steps = []HLSRung{{}}
	}
	for _, r := range steps {
		reportProgress(progress, TranscodeProgress{
			Format: TranscodeFormatHLS, Height: r.Height, Width: r.Width,
			State: ProgressQueued, Stage: "queued", Percent: 0,
		})
	}

	src, cleanup, err := openSource(ctx, t.blobs, sourceKey)
	if err != nil {
		return HLSResult{}, err
	}
	defer cleanup()

	tmp, err := os.MkdirTemp("", "vidra-hls-*")
	if err != nil {
		return HLSResult{}, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	// ffmpeg opens its output files but never creates their directories, and the
	// progressive MP4s need a real local file even when the ladder streams
	// (+faststart rewinds to move the moov atom to the front, and an HTTP PUT
	// body cannot be rewound). Which directories those are is format-specific.
	for _, dir := range pkg.ScratchDirs(rungs) {
		if err := os.MkdirAll(filepath.Join(tmp, dir), 0o755); err != nil {
			return HLSResult{}, err
		}
	}

	// The output prefix is derived from the SOURCE key (W14): a replacement
	// source writes a fresh generation directory so the tree players are
	// currently streaming is never disturbed; promotion is the DB-row swap in
	// transcode.storeResult.
	prefix := HLSPrefixForSource(videoID, sourceKey)
	// A manual re-run of the same source version uses the same stable prefix, so
	// the prior generation is cleared before the new one is written -- otherwise
	// stale resolutions/segments survive the overwrite. Replacement uploads use a
	// fresh rN prefix, preserving uninterrupted playback until DB promotion.
	//
	// This runs BEFORE any output is written rather than immediately before a
	// single bulk store, which widens the window during which a re-run of the
	// SAME source has no serving tree. Replacements (the common case) are
	// unaffected because they write to a different prefix.
	if deleter, ok := t.blobs.(storage.PrefixDeleter); ok {
		if err := deleter.DeletePrefix(ctx, prefix); err != nil {
			return HLSResult{}, err
		}
	}

	// Where the ladder writes. The default is scratch, uploaded afterwards. With
	// SetStreamOutput the encoders PUT straight into a loopback blobsink that
	// signs each object into the blob store, so no segment ever lands on disk.
	out := localOutput(tmp)
	if t.streamOutput {
		sink, serr := blobsink.New(t.blobs, prefix)
		if serr != nil {
			return HLSResult{}, serr
		}
		defer func() { _ = sink.Close() }()
		// Output the muxer has to write and finalisation has to read, but that is
		// not part of the stored tree. Registered BEFORE the encode so it is never
		// written at all — storing it and deleting it afterwards leaves a billable
		// hide-marker per transcode on a versioned bucket.
		for _, rel := range pkg.EphemeralOutputs() {
			sink.Discard(rel)
		}
		out = sinkOutput(sink)
	}

	// Every rung is encoded by ONE ffmpeg process that decodes the source a
	// single time and forks the decoded frames through a filter graph. The
	// previous shape ran one process per rung, each decoding the whole source
	// again to produce a different scaling of the same frames — so a 4-rung
	// ladder decoded a 30-minute video four times. Because the rungs now advance
	// together rather than one after another, each reports the SHARED progress of
	// the single pass; the per-resolution projection is unchanged in shape.
	// Parameter order follows TranscodeProgress's own fields (state, stage,
	// percent), which is also the order webVideoDeriver.report takes — two
	// progress closures in one function reading the same two strings in opposite
	// orders is a transposition waiting to happen.
	reportAll := func(state, stage string, percent int) {
		for _, r := range steps {
			reportProgress(progress, TranscodeProgress{
				Format: TranscodeFormatHLS, Height: r.Height, Width: r.Width,
				State: state, Stage: stage, Percent: percent,
			})
		}
	}
	plan := ladderPlan{
		rungs:           rungs,
		codecs:          t.videoCodecs(),
		threads:         settings.Threads,
		hasAudio:        md.HasAudio,
		sourceAudioKbps: md.AudioKbps,
	}
	reportAll(ProgressRunning, "encoding", 1)
	stderr, runErr := runFFmpegWithProgress(ctx, t.bin, hlsLadderArgsWith(pkg, src, out, plan), md.DurationSeconds, func(percent int) {
		reportAll(ProgressRunning, "encoding", percent*9/10)
	})
	if runErr != nil {
		reportAll(ProgressFailed, "encoding", 0)
		return HLSResult{}, fmt.Errorf("media: ffmpeg hls ladder for %q: %w: %s", sourceKey, redactSource(src, runErr), tailOf(stderr))
	}

	// Trick-play is a second single-decode pass rather than more outputs on the
	// first: its failure belongs to the packaging stage, and folding it in would
	// double the encoders running concurrently in one process. There is nothing
	// to scrub through in an audio-only tree, so it is skipped outright rather
	// than run over an empty rung set.
	reportAll(ProgressRunning, "packaging", 92)
	if !plan.audioOnly() {
		trickStderr, trickErr := runFFmpegWithProgress(ctx, t.bin, hlsTrickPlayLadderArgsWith(pkg, src, out, plan), md.DurationSeconds, nil)
		if trickErr != nil {
			reportAll(ProgressFailed, "packaging", 92)
			return HLSResult{}, fmt.Errorf("media: trick-play ladder for %q: %w: %s", sourceKey, redactSource(src, trickErr), tailOf(trickStderr))
		}
	}

	// A streamed ladder is only durable once the coalesced playlists are written,
	// and a PUT failure reaches ffmpeg as a generic write error, so check both
	// before treating the encode as successful.
	if out.streaming() {
		if serr := out.sink.Err(); serr != nil {
			reportAll(ProgressFailed, "packaging", 92)
			return HLSResult{}, fmt.Errorf("media: streaming hls output for %q: %w", sourceKey, serr)
		}
		if ferr := out.sink.Flush(ctx); ferr != nil {
			reportAll(ProgressFailed, "packaging", 92)
			return HLSResult{}, fmt.Errorf("media: streaming hls output for %q: %w", sourceKey, ferr)
		}
	}

	// The encode is done; the rest is packaging. Everything below the seam is
	// per-rung but decode-free -- the remuxes are stream copies and the
	// trick-play finalisation is playlist bookkeeping -- and it ends with the
	// tree stored. The two callbacks let the packager report a fatal error
	// through this job's per-resolution progress projection without knowing
	// anything about it.
	// The standalone progressive MP4s, when this job wants them, are COPIED out
	// of each rung's packaged directory as it is finished — the only moment those
	// bytes exist locally. See web_video.go for why a copy is equivalent to the
	// second encode this replaces.
	var deriver *webVideoDeriver
	var onRungPackaged func(context.Context, HLSRung, string) error
	if deriveWebVideos && len(rungs) > 0 {
		webPrefix := WebVideoPrefixForSource(videoID, sourceKey)
		// The same prefix-clearing a standalone web-video job does, for the same
		// reason: a re-run of the SAME source version reuses this prefix, so a
		// shorter ladder must not leave the previous run's taller rungs behind.
		if deleter, ok := t.blobs.(storage.PrefixDeleter); ok {
			if derr := deleter.DeletePrefix(ctx, webPrefix); derr != nil {
				return HLSResult{}, derr
			}
		}
		deriver = &webVideoDeriver{
			blobs:  t.blobs,
			prefix: webPrefix,
			report: func(r HLSRung, state, stage string, percent int) {
				reportProgress(progress, TranscodeProgress{
					Format: TranscodeFormatWebVideo, Height: r.Height, Width: r.Width,
					State: state, Stage: stage, Percent: percent,
				})
			},
		}
		for _, r := range rungs {
			reportProgress(progress, TranscodeProgress{
				Format: TranscodeFormatWebVideo, Height: r.Height, Width: r.Width,
				State: ProgressQueued, Stage: "queued", Percent: 0,
			})
		}
		onRungPackaged = deriver.rungPackaged
	}

	packaged, err := pkg.Finalize(ctx, packageRequest{
		sourceKey: sourceKey,
		out:       out,
		scratch:   tmp,
		prefix:    prefix,
		rungs:     rungs,
		audioKbps: plan.audioBitrateKbps(),
		codecs:    plan.profiles(),
		tools:     t.packageTools(),
		onPackagingFailed: func() {
			reportAll(ProgressFailed, "packaging", 92)
		},
		onStoreFailed: func(failed []HLSRung) {
			for _, r := range failed {
				reportProgress(progress, TranscodeProgress{
					Format: TranscodeFormatHLS, Height: r.Height, Width: r.Width,
					State: ProgressFailed, Stage: "storing", Percent: 96,
				})
			}
		},
		onRungPackaged: onRungPackaged,
	})
	if err != nil {
		return HLSResult{}, err
	}
	// The HLS tree is stored; free the scratch NOW rather than at the deferred
	// cleanup. The VP9 encode below runs for minutes, and the deferred RemoveAll
	// would otherwise hold the entire tree on disk for its whole duration on top
	// of VP9's own output. RemoveAll on an already-removed path is not an error,
	// so the defer stays correct.
	if err := os.RemoveAll(tmp); err != nil {
		return HLSResult{}, err
	}

	// An audio-only tree contributes NO renditions: a video_renditions row
	// describes a resolution, and this has none. The detail response omits the
	// rendition list entirely (it is `omitempty`) and every serving route gates on
	// the playlist row rather than on rendition rows, so the video is servable
	// with none — see the master playlist, which advertises a single audio-only
	// variant.
	res := HLSResult{MasterKey: packaged.masterKey, Format: packaged.format}
	if deriver != nil {
		res.WebVideos = deriver.results
	}
	for _, r := range rungs {
		res.Renditions = append(res.Renditions, HLSRendition{
			Height:    r.Height,
			Width:     r.Width,
			KeyPrefix: prefix + "/" + r.Name(),
			SizeBytes: packaged.rungSizes[r.Height],
		})
	}
	for _, r := range steps {
		reportProgress(progress, TranscodeProgress{
			Format: TranscodeFormatHLS, Height: r.Height, Width: r.Width,
			State: ProgressSucceeded, Stage: "complete", Percent: 100,
		})
	}
	// VP9 is a progressive VIDEO alternate; an audio-only source has nothing to
	// encode one from (and rungs[0] would not exist to encode it at).
	if t.vp9 && !plan.audioOnly() {
		// Progressive VP9/WebM alternate at the top rung. Best-effort: a VP9
		// failure must not fail the H.264 HLS transcode (VP9 is an extra codec
		// option, not the primary deliverable).
		top := rungs[0]
		if key, size, sum, verr := t.encodeVP9(ctx, videoID, prefix, src, top); verr == nil {
			res.WebMKey = key
			res.WebMBytes = size
			res.WebMSHA256 = sum
			res.WebMHeight = top.Height
			res.WebMWidth = top.Width
		}
	}
	return res, nil
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
