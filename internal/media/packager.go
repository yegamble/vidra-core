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
// performance regression. ffmpeg packages AS it encodes: the muxer is an output
// of the same invocation that runs libx264, so container settings are just more
// arguments on that invocation. The CMAF implementation this seam exists for
// (decided 2026-08-22: ffmpeg's dash muxer with -hls_playlist 1, emitting fMP4
// segments plus HLS playlists) is fused into the encode pass in exactly the same
// way. So the contract is:
//
//	(1) compose the OUTPUT half of an encode invocation — how the filter graph's
//	    branches are wired into outputs, with which per-stream encoder settings
//	    and which container arguments — given the output TRANSPORT; and
//	(2) own post-encode finalisation — everything from trick-play playlist
//	    fixups to storing the tree — returning what TranscodeHLS needs to build
//	    an HLSResult.
//
// It is deliberately NOT "run a transmux pass over the encoder's output".
//
// WHY (1) IS "COMPOSE" AND NOT "APPEND PER RUNG". The seam was first drawn for
// MPEG-TS, where every rung is its own self-contained -f hls output and the
// packager need only contribute a suffix to each rung's block. CMAF cannot be
// expressed that way: ffmpeg's dash muxer consumes the WHOLE ladder into ONE
// output with adaptation sets, so the maps and encoder options must be
// stream-qualified and there is exactly one muxer block for all of them —
// including a single shared audio representation rather than audio muxed into
// every rung. A packager therefore composes the output half from the encode-arg
// builders hls.go owns (hlsRungVideoEncodeArgs / hlsAudioEncodeArgs /
// trickPlayEncodeArgs); codec settings still live on the encode side of the
// seam, they are simply assembled by the side that knows the output shape.
//
// A future Shaka/DRM packager genuinely does want a mezzanine (it cannot be
// fused into ffmpeg's output at all), and would need a third mode where the
// encode writes one intermediate and the packager runs afterwards. We are not
// building that now, and the seam should be widened when there is a concrete
// implementation to widen it for — not speculatively.
//
// Two implementations today: tsPackager (the MPEG-TS behaviour this package has
// always had, byte-for-byte) and cmafPackager (cmaf.go).

// Packager is the packaging half of a transcode: how a ladder's encoders are
// wired into ffmpeg outputs, the container arguments they write through, and the
// post-encode finalisation of what those muxers produced.
//
// Its method parameters are package-internal types (output, rungRef) on purpose:
// packaging composes with the output TRANSPORT seam (scratch directory vs
// blobsink over HTTP), which is an implementation detail of this package, so
// implementations belong here too.
type Packager interface {
	// Name identifies the packaging format for logs, diagnostics and the
	// operator-facing TRANSCODING_PACKAGER value ("ts", "cmaf").
	Name() string

	// SupportsAudioOnly reports whether this format can package a source that has
	// no video stream at all — a podcast, a music track — as a single audio
	// rendition instead of failing for want of a ladder to build.
	//
	// It is a capability rather than an assumption because the two formats differ
	// on it for a STRUCTURAL reason, not an unfinished one. A CMAF tree already
	// carries audio as its own representation in its own adaptation set, so
	// dropping the video representations leaves a manifest that is still exactly
	// the shape it was. An MPEG-TS ladder has no such thing: audio is muxed INTO
	// each variant, a variant is a rendition directory named for its height, and
	// the file route only reaches "<NNN>p" directories — an audio-only MPEG-TS
	// tree would need a rendition naming scheme, a route change and a master
	// shape that none of the format's serving path has today. Since MPEG-TS
	// exists only as the config-only rollback from CMAF, building all that for it
	// would be building a second first-class format.
	SupportsAudioOnly() bool

	// SupportsMultiCodec reports whether this format can carry the SAME ladder
	// encoded with several video codecs at once — an H.264 rung and an HEVC rung
	// and an AV1 rung of the same resolution, side by side, for a player to pick
	// between.
	//
	// Like SupportsAudioOnly it is a structural property, not an unfinished one. A
	// CMAF tree addresses representations by index inside one shared directory and
	// groups them into adaptation sets, so a second codec is a second adaptation
	// set over more representations and nothing else moves. An MPEG-TS variant IS
	// a rendition directory named for its height, reached by a route that only
	// resolves "<NNN>p" — there is no room in that naming for a second encoding of
	// the same height, and inventing one would make the rollback path a second
	// first-class format to maintain.
	SupportsMultiCodec() bool

	// ScratchDirs are the directories, relative to the transcode's scratch root,
	// that must exist before the encode starts. ffmpeg's muxers open their output
	// files but do not create the parent directories, and the progressive MP4s
	// are always written locally — even when the ladder streams, because
	// +faststart rewinds to move the moov atom and an HTTP PUT body cannot be
	// rewound.
	ScratchDirs(rungs []HLSRung) []string

	// EphemeralOutputs are files the muxer is made to write that the pipeline
	// READS during finalisation but that must never become part of the stored
	// tree — relative to the output prefix. A streaming transcode needs them
	// named up front, because an object written into a blobsink is durable the
	// moment it is flushed and deleting it afterwards leaves a delete marker on
	// a versioned bucket (and costs two requests to store nothing).
	EphemeralOutputs() []string

	// LadderOutputArgs is the OUTPUT half of the ladder encode vector: for every
	// planned rung, the stream maps consuming its filter-graph branch, that
	// rung's ENCODER arguments, and the container arguments those encoders write
	// through.
	LadderOutputArgs(out output, plan ladderPlan) []string

	// TrickPlayOutputArgs is the same composition for the dense-I-frame
	// trick-play pass, whose container settings are format-specific.
	TrickPlayOutputArgs(out output, plan ladderPlan) []string

	// Finalize turns a finished encode into a stored, servable tree: per-rung
	// derivative assets, playlist fixups, the master playlist, and the uploads
	// themselves. It is called once, after every encode pass has completed and
	// (when streaming) the sink has been flushed.
	Finalize(ctx context.Context, req packageRequest) (packageResult, error)
}

// ladderPlan is everything hls.go decided about ONE encode vector, handed to a
// packager so it can compose the output half around it. The DECODE half — one
// input, one filter graph forking it into a branch per rung — has already been
// written by the time a packager sees this.
//
// It is a struct rather than a parameter list because the two formats need
// genuinely different subsets of it, and the set has already grown once: CMAF
// needs to know whether the source HAS audio (to decide whether to declare an
// audio adaptation set at all) where MPEG-TS never does, because MPEG-TS maps
// audio optionally into every rung and simply gets nothing.
type ladderPlan struct {
	rungs []HLSRung
	// codecs is the video codec plan: this ladder emits one encoded
	// representation per codec per rung. Empty means the single-codec H.264
	// ladder every ordinary install produces, which is what a plan built before
	// codec profiles existed (and every MPEG-TS plan) resolves to.
	codecs []codecProfile
	// labels[rep] is the filter-graph output label carrying representation rep's
	// frames, indexed by repIndex — so labels[i] is still rung i for a
	// single-codec plan, which is what the MPEG-TS packager reads.
	labels []string
	// threads is the job's WHOLE budget, to be divided across the encoders that
	// now run concurrently in one process.
	threads int
	// hasAudio reports whether the source has an audio stream to encode.
	hasAudio bool
	// sourceAudioKbps is the source audio stream's own bitrate (0 = unknown). It
	// is read only by an audio-only plan, which has no rung to take a budget from
	// and would otherwise re-encode every podcast at the ceiling.
	sourceAudioKbps int
}

// profiles is the plan's codec list, resolved.
func (p ladderPlan) profiles() []codecProfile {
	if len(p.codecs) == 0 {
		return defaultVideoCodecs
	}
	return p.codecs
}

// videoReps is the number of encoded video representations: one per rung per
// codec. Zero for an audio-only plan.
func (p ladderPlan) videoReps() int { return len(p.rungs) * len(p.profiles()) }

// repIndex is the representation index of rung `rung` encoded with codec
// `codec`, and it is THE definition of the tree's layout: every codec's whole
// ladder in rung order before the next codec starts, H.264 first.
//
// Grouping by codec rather than interleaving by rung is what lets an adaptation
// set be a contiguous stream range, keeps the H.264 representations at indices
// 0..len(rungs)-1 — which is what every derived asset (downloads, web videos,
// trick-play) addresses — and puts the H.264 variants first in the HLS master
// for players that take the first one they understand.
func (p ladderPlan) repIndex(codec, rung int) int { return codec*len(p.rungs) + rung }

// audioOnly reports that this plan has no video at all: the source carried none,
// so there is one audio representation and nothing else. An empty rung slice is
// the whole signal — a video plan always has at least one rung (the planner
// falls back to a single rung at the source's own size), so there is no third
// state to confuse it with and no second field to keep in sync.
func (p ladderPlan) audioOnly() bool { return len(p.rungs) == 0 }

// audioBitrateKbps is the AAC budget for the ladder's SINGLE shared audio
// representation: the top rung's, or — when there are no rungs to read one from
// — the audio-only budget, which is bounded by what the source actually carries.
func (p ladderPlan) audioBitrateKbps() int {
	if p.audioOnly() {
		return audioOnlyKbpsFor(p.sourceAudioKbps)
	}
	return p.rungs[0].AudioKbps
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
	// audioKbps is the budget the ladder's single shared audio representation was
	// ENCODED at, which is what its manifests must declare. It is carried rather
	// than re-derived because an audio-only tree has no rung to derive it from.
	audioKbps int
	// codecs is the codec plan the ladder was encoded with, H.264 first. Never
	// empty: finalisation checks ffmpeg's own manifest against it, and "no plan"
	// would make that check vacuous rather than trivially true.
	codecs []codecProfile
	tools  packageTools
	// onPackagingFailed and onStoreFailed report a fatal error through the
	// caller's per-rung progress projection before it is returned. Both are
	// optional.
	onPackagingFailed func()
	onStoreFailed     func(rungs []HLSRung)
	// onRungPackaged, when set, is called for each rung with the LOCAL directory
	// holding that rung's freshly-remuxed progressive MP4s — after the remux, and
	// immediately before the directory is uploaded and freed.
	//
	// It exists for one caller and one reason: a job that also wants the
	// standalone progressive "web video" derivatives needs bytes that are
	// identical to the ones sitting in that directory RIGHT NOW and nowhere else
	// afterwards. This is the only moment they can be had without either a second
	// full decode of the source or reading the whole ladder back out of the object
	// store. A failure is fatal to the transcode, exactly as a storeTree failure
	// at the same point already is.
	onRungPackaged func(ctx context.Context, r HLSRung, dir string) error
}

// rungPackaged notifies the optional per-rung derivative hook.
func (req packageRequest) rungPackaged(ctx context.Context, r HLSRung, dir string) error {
	if req.onRungPackaged == nil {
		return nil
	}
	return req.onRungPackaged(ctx, r, dir)
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
// playlist's storage key, the stored size of each rung (keyed by rung height),
// and the format the tree was written in — recorded per video on
// streaming_playlists.format so serving can tell a CMAF tree from a legacy
// MPEG-TS one without probing storage.
type packageResult struct {
	masterKey string
	format    string
	rungSizes map[int]int64
}

// PackagerTS and PackagerCMAF are the selectable packaging formats: the
// TRANSCODING_PACKAGER values an operator writes and what Packager.Name reports.
// Switching between them is config-only — old trees keep serving in whatever
// format they were written, because every video records its own.
const (
	PackagerTS   = "ts"
	PackagerCMAF = "cmaf"
)

// HLSFormatTS and HLSFormatCMAF are the per-video format record
// (streaming_playlists.format) written for a finished transcode.
//
// They are deliberately NOT the TRANSCODING_PACKAGER spellings above: the column
// records what a stored tree IS, and every row that predates CMAF defaults to
// 'hls-ts', so the value names the delivery format ("HLS over MPEG-TS") rather
// than the packager that happened to produce it.
const (
	HLSFormatTS   = "hls-ts"
	HLSFormatCMAF = "cmaf"
)

// --- MPEG-TS packager --------------------------------------------------------

// Stable names inside one rung's packaged output.
const (
	hlsVariantPlaylistFilename = "playlist.m3u8"
	hlsMasterPlaylistFilename  = "master.m3u8"
	tsSegmentPattern           = "seg_%05d.ts"
)

// tsPackager packages a ladder as MPEG-TS segments with HLS playlists: the
// behaviour this package has always had, extracted unchanged. It is stateless;
// everything it needs arrives on the request.
type tsPackager struct{}

// defaultPackager is the packaging format a transcode uses when nothing selects
// one. It stays MPEG-TS even though new installs default to CMAF
// (TRANSCODING_PACKAGER): the deployment-wide default is a configuration
// decision made in cmd/api, while this is the in-process fallback for a
// transcoder built with no packager at all — which must keep behaving exactly as
// it did before CMAF existed.
//
// Deliberately typed as the CONCRETE packager, not as a Packager: the per-rung
// muxer builders below are tsPackager's own API (its Packager methods are
// composed FROM them), and the single-rung oracles in hls.go call them directly.
var defaultPackager = tsPackager{}

// Name implements Packager.
func (tsPackager) Name() string { return PackagerTS }

// SupportsAudioOnly implements Packager: no. See the interface for why this is a
// structural property of the format rather than a gap to be filled in later.
func (tsPackager) SupportsAudioOnly() bool { return false }

// SupportsMultiCodec implements Packager: no. A variant here is a rendition
// directory named for its height, which leaves nowhere to put a second encoding
// of that height — and this format exists as the config-only rollback from CMAF,
// so it stays frozen at what it has always produced.
func (tsPackager) SupportsMultiCodec() bool { return false }

// ScratchDirs implements Packager: one directory per rung, holding that rung's
// segments (unless the ladder streams) and always its progressive MP4s.
func (tsPackager) ScratchDirs(rungs []HLSRung) []string {
	dirs := make([]string, 0, len(rungs))
	for _, r := range rungs {
		dirs = append(dirs, r.Name())
	}
	return dirs
}

// EphemeralOutputs implements Packager: the HLS muxer writes nothing this
// pipeline reads and then throws away — every file it produces is served.
func (tsPackager) EphemeralOutputs() []string { return nil }

// LadderOutputArgs implements Packager: one self-contained -f hls output per
// rung — its own filter branch, its own optional audio map, its own encoder and
// its own muxer — which is the shape this package has always emitted.
//
// It reads plan.labels[i] for rung i, which is the H.264 representation's label
// under ladderPlan.repIndex. That is correct because this format is single-codec
// (SupportsMultiCodec) and the transcoder refuses the combination at boot; a
// multi-codec plan never reaches here.
func (p tsPackager) LadderOutputArgs(out output, plan ladderPlan) []string {
	var args []string
	per := perOutputThreads(plan.threads, len(plan.rungs))
	for i, r := range plan.rungs {
		args = append(args,
			"-map", "["+plan.labels[i]+"]",
			// Audio comes from the input, optionally, so a silent source still
			// encodes; the filter graph only carries video.
			"-map", "0:a:0?",
		)
		if per > 0 {
			args = append(args, "-threads", strconv.Itoa(per))
		}
		args = append(args, hlsRungEncodeArgs(r, "")...)
		args = append(args, p.VariantMuxerArgs(out, rungDest(out, r), r)...)
	}
	return args
}

// TrickPlayOutputArgs implements Packager: one video-only output per rung.
func (p tsPackager) TrickPlayOutputArgs(out output, plan ladderPlan) []string {
	var args []string
	per := perOutputThreads(plan.threads, len(plan.rungs))
	for i, r := range plan.rungs {
		args = append(args, "-map", "["+plan.labels[i]+"]", "-an")
		if per > 0 {
			args = append(args, "-threads", strconv.Itoa(per))
		}
		args = append(args, trickPlayEncodeArgs("")...)
		args = append(args, p.TrickPlayMuxerArgs(out, rungDest(out, r), r)...)
	}
	return args
}

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
		if err := req.rungPackaged(ctx, r, dir); err != nil {
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
		format:    HLSFormatTS,
		rungSizes: rungSizes,
	}, nil
}

// --- finalisation steps ------------------------------------------------------

// treeIO is where ONE directory of a just-encoded tree actually lives, so the
// decode-free post-processing (progressive remux, playlist fixups, trick-play
// finalisation, size accounting) can read and rewrite it without caring whether
// the ladder was written to scratch or streamed into the blob store.
type treeIO struct {
	out output
	// rel is the directory's path under the output prefix: a rung's own name for
	// MPEG-TS ("720p"), the shared segment directory for CMAF ("cmaf").
	rel string
	// scratch is always a local directory: even in streaming mode the
	// progressive MP4s must be written to a file, because +faststart rewinds to
	// move the moov atom to the front and an HTTP PUT body cannot be rewound.
	scratch string
}

// ref is what an external tool (ffmpeg/ffprobe) should open to read name.
func (io treeIO) ref(name string) string {
	if io.out.streaming() {
		return io.out.sink.URL(path.Join(io.rel, name))
	}
	return filepath.Join(io.scratch, name)
}

// read returns the bytes of one of this directory's outputs.
func (io treeIO) read(ctx context.Context, name string) ([]byte, error) {
	if io.out.streaming() {
		return io.out.sink.Get(ctx, path.Join(io.rel, name))
	}
	return os.ReadFile(filepath.Join(io.scratch, name))
}

// write replaces one of this directory's outputs. A streamed tree is already in
// the store by the time finalisation runs, so this is a re-Put there and a plain
// file rewrite (before the tree is uploaded) on scratch — either way the STORED
// bytes are the fixed ones.
func (io treeIO) write(ctx context.Context, name string, body []byte) error {
	if io.out.streaming() {
		return io.out.sink.Replace(ctx, path.Join(io.rel, name), body)
	}
	return os.WriteFile(filepath.Join(io.scratch, name), body, 0o644)
}

// rungIO is treeIO for one ladder rung's own directory.
type rungIO struct {
	out     output
	rung    HLSRung
	scratch string
}

func (io rungIO) tree() treeIO {
	return treeIO{out: io.out, rel: io.rung.Name(), scratch: io.scratch}
}

func (io rungIO) ref(name string) string { return io.tree().ref(name) }

func (io rungIO) read(ctx context.Context, name string) ([]byte, error) {
	return io.tree().read(ctx, name)
}

func (io rungIO) write(ctx context.Context, name string, body []byte) error {
	return io.tree().write(ctx, name, body)
}

// finalizeTrickPlay turns one rung's freshly-encoded trick-play output into the
// playlist clients can use: FFmpeg's `iframes_only` flag emits incorrect
// repeated @0 offsets, so the byte ranges are generated with `single_file` and
// the standards tag is added here after the playlist shape is verified. It also
// measures the declared peak bandwidth and probes the codec string for the
// master playlist. No decoding happens here, so it is cheap to run per rung
// after a single shared encode pass.
func finalizeTrickPlay(ctx context.Context, ffprobeBin string, rio rungIO) (hlsTrickPlayInfo, error) {
	return finalizeTrickPlayIn(ctx, ffprobeBin, rio.tree(), HLSIFramePlaylistFilename, HLSIFrameMediaFilename)
}

// finalizeTrickPlayIn is finalizeTrickPlay with the directory and asset names
// supplied: CMAF keeps every rung's trick-play pair in the shared segment
// directory, so the names carry the rung's representation index instead of
// living in a per-rung directory.
func finalizeTrickPlayIn(ctx context.Context, ffprobeBin string, tio treeIO, playlistName, mediaName string) (hlsTrickPlayInfo, error) {
	playlist, err := tio.read(ctx, playlistName)
	if err != nil {
		return hlsTrickPlayInfo{}, err
	}
	playlist, err = markIFramesOnlyPlaylistFor(playlist, mediaName)
	if err != nil {
		return hlsTrickPlayInfo{}, err
	}
	if err := tio.write(ctx, playlistName, playlist); err != nil {
		return hlsTrickPlayInfo{}, err
	}
	bandwidth, err := trickPlayPeakBandwidth(playlist)
	if err != nil {
		return hlsTrickPlayInfo{}, err
	}
	codec, err := probeH264CodecString(ctx, ffprobeBin, tio.ref(mediaName))
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
	return markIFramesOnlyPlaylistFor(playlist, HLSIFrameMediaFilename)
}

// markIFramesOnlyPlaylistFor is markIFramesOnlyPlaylist with the single-file
// media asset named, because CMAF's trick-play media is an fMP4 whose name
// carries the rung's representation index.
func markIFramesOnlyPlaylistFor(playlist []byte, mediaName string) ([]byte, error) {
	s := string(playlist)
	if !strings.HasPrefix(s, "#EXTM3U\n") ||
		!strings.Contains(s, "#EXT-X-BYTERANGE:") ||
		!strings.Contains(s, mediaName) {
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
