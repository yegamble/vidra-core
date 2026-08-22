package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// --- CMAF packager -----------------------------------------------------------
//
// cmafPackager writes ONE set of fMP4 (CMAF) segments and serves it through BOTH
// manifests: a DASH MPD and HLS playlists that name the very same files. That is
// the whole point — a video is not packaged twice, and adding DASH costs no
// extra bytes in the object store.
//
// HOW (decided 2026-08-22, docs/productionization/cmaf-packaging-decision.md):
// ffmpeg's dash muxer with -hls_playlist 1, FUSED INTO THE ENCODE PASS. There is
// no mezzanine and no second binary; the muxer is an output of the same
// invocation that runs libx264, exactly as the MPEG-TS packager's is.
//
// WHY THE OUTPUT HALF LOOKS NOTHING LIKE THE MPEG-TS ONE. The dash muxer
// consumes the WHOLE ladder into a single output with adaptation sets, so:
//
//   - every rung's maps and encoder options are stream-qualified (-c:v:1,
//     -b:v:1 …); unqualified options would be applied to every rung at once;
//   - audio is encoded ONCE, as the single audio representation every video
//     representation references (the standard demuxed CMAF shape), rather than
//     muxed into each rung as MPEG-TS does;
//   - there is one muxer block for the whole vector, not one per rung.
//
// The rung → representation-index mapping is therefore load-bearing. It is
// ladderPlan.repIndex: each codec's whole ladder in rung order, H.264 first, so
// representation i is ladder rung i for the single-codec ladder every ordinary
// install produces. Finalize re-derives BOTH halves of it from ffmpeg's own
// master playlist — which rung each index holds, and which codec — and refuses
// to continue if either disagrees with the plan.
//
// SEVERAL CODECS AT ONCE (phase-3 item 5) is more representations on that same
// output, not a second tree: with TRANSCODING_HEVC_ENABLED and/or
// TRANSCODING_AV1_ENABLED each rung is additionally encoded with libx265 and/or
// libsvtav1 in the SAME pass, and the manifests gain one video adaptation set
// (DASH) and one variant per rung (HLS) per codec. Audio is still encoded and
// stored once for the whole tree. Everything derived — the progressive
// downloads, the web videos, audio.m4a, trick-play — comes off the H.264
// representations, which is why they hold indices 0..len(rungs)-1.
//
// WHAT FINALISATION FIXES. ffmpeg's manifests are weaker than the ones Vidra
// already authors in Go: no EXT-X-INDEPENDENT-SEGMENTS, no I-frame streams, and
// a wall-clock EXT-X-PROGRAM-DATE-TIME on every segment. So its master playlist
// is read for the CODECS strings it computed (which is where they come from now
// — no second ffprobe, and the path that unblocks HEVC/AV1 later) and then
// discarded, the media playlists are rewritten as VOD without the date-times,
// and the Go writer emits the master at the same key MPEG-TS uses.
//
// WHAT IS DELIBERATELY NOT HERE: back-catalogue re-packaging, CENC (ffmpeg emits
// zero DRM signalling — that is Shaka's job if it ever ships), LL-anything, and
// in-manifest subtitles (WebVTT hard-fails this muxer; captions stay out-of-band).

// The CMAF tree's stable names. Everything the muxer writes lives in ONE flat
// directory under the generation prefix, because HLS and DASH must resolve the
// SAME segment files relative to their own manifests — which they can only do if
// those manifests are siblings of the segments and of each other.
const (
	// cmafDirName is that directory, and is also the pseudo-rendition the file
	// route serves it under: /videos/{id}/hls/cmaf/<file>. The MPD therefore
	// lives at /videos/{id}/hls/cmaf/stream.mpd, where its relative
	// SegmentTemplate URIs resolve against its siblings with no rewriting.
	cmafDirName = "cmaf"

	cmafManifestFilename = "stream.mpd"

	// cmafFFmpegMasterFilename is where the muxer's OWN HLS master is written so
	// it cannot collide with the master Vidra authors. It is read for its CODECS
	// strings and then deleted; it is never served.
	cmafFFmpegMasterFilename = "ffmpeg-master.m3u8"

	// The segment name templates. $RepresentationID$ is expanded by the muxer;
	// the Go side reproduces the same names through cmafInitSegmentName /
	// cmafMediaPlaylistName so the tree shape is pinned on both sides.
	cmafInitSegmentPattern  = "init-$RepresentationID$.mp4"
	cmafMediaSegmentPattern = "chunk-$RepresentationID$-$Number%05d$.m4s"

	// cmafAudioGroupID names the EXT-X-MEDIA rendition group the single shared
	// audio representation is published as.
	cmafAudioGroupID = "audio"
)

// cmafMediaPlaylistName is the HLS media playlist ffmpeg writes for
// representation rep ("media_0.m3u8"). The muxer's own naming; not configurable.
func cmafMediaPlaylistName(rep int) string { return "media_" + strconv.Itoa(rep) + ".m3u8" }

// cmafInitSegmentName is cmafInitSegmentPattern with $RepresentationID$ expanded.
func cmafInitSegmentName(rep int) string { return "init-" + strconv.Itoa(rep) + ".mp4" }

// cmafIFramePlaylistName / cmafIFrameMediaName are one rung's trick-play pair.
// They carry the representation index because, unlike MPEG-TS, every rung's
// trick-play assets share one directory.
func cmafIFramePlaylistName(rep int) string { return "iframe-" + strconv.Itoa(rep) + ".m3u8" }
func cmafIFrameMediaName(rep int) string    { return "iframe-" + strconv.Itoa(rep) + ".mp4" }

// cmafPackager packages a ladder as CMAF/fMP4 with both manifests. Stateless;
// everything it needs arrives on the request.
type cmafPackager struct{}

// Name implements Packager.
func (cmafPackager) Name() string { return PackagerCMAF }

// SupportsAudioOnly implements Packager: yes, and almost for free. A CMAF tree
// already carries audio as its OWN representation in its OWN adaptation set,
// referenced by the video ones rather than contained in them. Dropping the video
// representations therefore leaves a manifest of exactly the same shape with one
// fewer adaptation set — and an HLS master whose single variant names the audio
// media playlist directly, which is precisely RFC 8216's audio-only variant.
func (cmafPackager) SupportsAudioOnly() bool { return true }

// SupportsMultiCodec implements Packager: yes, and for the same structural
// reason. Representations here are indices in one shared directory, grouped into
// adaptation sets; a second codec is a second video adaptation set over more
// representation indices, and nothing about the tree's shape, its routes or its
// derived assets changes. See ladderPlan.repIndex for the layout.
func (cmafPackager) SupportsMultiCodec() bool { return true }

// ASPECT RATIO — WHY THERE IS NO FILTER HERE.
//
// The dash muxer refuses to write a manifest whose adaptation set mixes display
// aspect ratios ("Conflicting stream aspect ratios values in Adaptation Set 1"),
// reasonably: representations in one adaptation set are meant to be
// interchangeable mid-playback, so a player switching between two shapes would
// visibly jump. That check looks like it needs handling, because the ladder's
// widths are rounded to even for H.264 4:2:0 and a 720p source's 480p rung is
// 854x480 (1.7792) beside 1280x720 (1.7778).
//
// It does not. scale ALREADY sets each output's sample aspect ratio to exactly
// the value that preserves the input's display aspect through that rounding —
// 854x480 comes out sar 1280:1281, so its display ratio is 16:9 to the bit, the
// same as every other rung. The adaptation set is consistent for free.
//
// Adding setsar=1 is what BREAKS it: it throws that correction away, and the
// rungs then genuinely disagree. Replacing it with setdar pinned to the top
// rung's coded ratio is worse still — silently, and only for the sources that
// matter. Rung dimensions come from ffprobe's CODED width/height, which for a
// rotated phone video or anamorphic SD is not the shape the video is displayed
// at; pinning to it re-renders the entire ladder wrong (a portrait 1920x1080+90°
// clip stretched 3.16x into landscape, unrecoverable without re-encoding).
//
// So the filter graph stays byte-identical to the MPEG-TS one, which is what
// makes CMAF geometry provably equal to MPEG-TS geometry for every source
// shape rather than for the ones we happened to test.

// ScratchDirs implements Packager: the per-rung directories the progressive MP4s
// are written into, plus the shared segment directory the muxer writes into when
// the ladder is not streaming.
func (cmafPackager) ScratchDirs(rungs []HLSRung) []string {
	dirs := make([]string, 0, len(rungs)+1)
	for _, r := range rungs {
		dirs = append(dirs, r.Name())
	}
	return append(dirs, cmafDirName)
}

// EphemeralOutputs implements Packager: ffmpeg's own HLS master playlist, which
// finalisation reads for its CODECS strings and then discards in favour of the
// one Vidra authors. Naming it here is what keeps a streaming transcode from
// storing it and immediately deleting it again.
func (cmafPackager) EphemeralOutputs() []string {
	return []string{cmafDirName + "/" + cmafFFmpegMasterFilename}
}

// LadderOutputArgs implements Packager: ONE dash output carrying every rung as a
// video representation plus, when the source has any, a single shared audio
// representation.
//
// An AUDIO-ONLY plan is the same output with the video half absent: no branches
// to map, no video encoders, one adaptation set. Its audio map is REQUIRED
// rather than optional — for a video ladder a missing audio stream is a silent
// video, but here it is an empty output the muxer would happily write and
// finalisation would happily store.
// MULTI-CODEC is more representations on the SAME output, not a second pass: the
// rungs' scaled frames are already in the filter graph, so a second codec is
// extra encoder blocks reading extra fork outputs and one more adaptation set.
// Every derived asset (progressive downloads, web videos, audio.m4a, trick-play)
// keeps coming off the H.264 representations, which is why they occupy indices
// 0..len(rungs)-1 — see ladderPlan.repIndex.
func (cmafPackager) LadderOutputArgs(out output, plan ladderPlan) []string {
	var args []string
	// Every video branch first, then the audio — the map order IS the
	// representation order, which is the mapping Finalize re-derives and checks.
	for _, label := range plan.labels {
		args = append(args, "-map", "["+label+"]")
	}
	if plan.audioOnly() {
		args = append(args, "-map", "0:a:0")
	} else {
		// Optional even when the probe found audio: the map must not fail the
		// ladder if the stream turns out unusable.
		args = append(args, "-map", "0:a:0?")
	}

	// The thread budget is split across the encoders that run CONCURRENTLY in this
	// one process, and with several codecs that is every codec's every rung — not
	// just the rungs.
	per := perOutputThreads(plan.threads, plan.videoReps())
	for c, prof := range plan.profiles() {
		for i, r := range plan.rungs {
			spec := sharedVideoStream(plan.repIndex(c, i))
			if per > 0 {
				args = append(args, "-threads"+spec.bare, strconv.Itoa(per))
			}
			args = append(args, hlsRungVideoEncodeArgs(r, prof, spec, "")...)
		}
	}
	if plan.hasAudio {
		// One audio encode for the whole ladder, at the TOP rung's audio bitrate:
		// every video representation references this one rendition, so there is no
		// per-rung audio quality to choose between — and no per-CODEC one either,
		// which is why a three-codec tree still stores its audio once.
		args = append(args, hlsAudioEncodeArgs(plan.audioBitrateKbps(), sharedAudioStream(0))...)
	}
	return append(args, cmafMuxerArgs(out, plan)...)
}

// cmafAdaptationSets is the -adaptation_sets expression: one video adaptation set
// PER CODEC, plus the audio set when there is audio.
//
// A set is what a player switches WITHIN, so mixing codecs in one would tell it
// that an H.264 rung and an AV1 rung are interchangeable mid-playback — which
// they are not for any decoder that only has one of them. One set per codec is
// also what makes each set's `codecs` attribute mean something.
//
// The single-codec expression is the shorthand this package has always emitted,
// character for character. The multi-codec one has to name stream indices
// explicitly because "streams=v" means ALL video.
func cmafAdaptationSets(plan ladderPlan) string {
	// An audio-only tree declares ONE set, and it is the audio one: an empty video
	// adaptation set is just as invalid as an empty audio one, so the sets are
	// exactly the streams that exist.
	if plan.audioOnly() {
		return "id=0,streams=a"
	}
	profiles := plan.profiles()
	if len(profiles) == 1 {
		if plan.hasAudio {
			return "id=0,streams=v id=1,streams=a"
		}
		return "id=0,streams=v"
	}
	sets := make([]string, 0, len(profiles)+1)
	for c := range profiles {
		reps := make([]string, 0, len(plan.rungs))
		for i := range plan.rungs {
			reps = append(reps, strconv.Itoa(plan.repIndex(c, i)))
		}
		sets = append(sets, fmt.Sprintf("id=%d,streams=%s", c, strings.Join(reps, ",")))
	}
	if plan.hasAudio {
		sets = append(sets, fmt.Sprintf("id=%d,streams=a", len(profiles)))
	}
	return strings.Join(sets, " ")
}

// cmafMuxerArgs is the shared dash-muxer block that closes the ladder vector.
func cmafMuxerArgs(out output, plan ladderPlan) []string {
	args := []string{
		"-f", "dash",
		"-seg_duration", strconv.Itoa(hlsSegmentSeconds),
		// fMP4, not WebM: CMAF is an ISOBMFF profile.
		"-dash_segment_type", "mp4",
		// SegmentTemplate + SegmentTimeline keeps the MPD O(1) in segment count
		// and lets HLS and DASH name the very same files.
		"-use_template", "1",
		"-use_timeline", "1",
		// The whole reason this muxer was chosen: HLS playlists over the SAME
		// segments, from the same pass.
		"-hls_playlist", "1",
		"-hls_master_name", cmafFFmpegMasterFilename,
		// The cmfc brand, so the segments are CMAF and not merely fMP4.
		"-format_options", "movflags=+cmaf",
		// Deliberately 0: with io errors ignored the process exit code stops
		// meaning anything, and a half-written tree would be stored as success.
		"-ignore_io_errors", "0",
		"-init_seg_name", cmafInitSegmentPattern,
		"-media_seg_name", cmafMediaSegmentPattern,
	}
	// Video representations grouped into adaptation sets, audio in its own — the
	// standard demuxed CMAF shape. The audio set is declared only when there IS
	// audio: the muxer does not drop an adaptation set that ends up with no
	// streams, it writes an empty <AdaptationSet contentType="audio"/>, and a
	// Representation-less adaptation set is invalid DASH.
	args = append(args, "-adaptation_sets", cmafAdaptationSets(plan))
	args = append(args, out.muxerArgs()...)
	return append(args, out.dest(out.rel(cmafDirName, cmafManifestFilename)))
}

// TrickPlayOutputArgs implements Packager. Trick-play stays a per-rung -f hls
// output as it is for MPEG-TS — the dash muxer has no I-frame-playlist support
// at all — but the media is now an fMP4 single file, and every rung's pair lands
// in the shared segment directory named by representation index.
func (cmafPackager) TrickPlayOutputArgs(out output, plan ladderPlan) []string {
	var args []string
	per := perOutputThreads(plan.threads, len(plan.rungs))
	dest := func(name string) string { return out.dest(out.rel(cmafDirName, name)) }
	for i := range plan.rungs {
		args = append(args, "-map", "["+plan.labels[i]+"]", "-an")
		if per > 0 {
			args = append(args, "-threads", strconv.Itoa(per))
		}
		args = append(args, trickPlayEncodeArgs("")...)
		args = append(args, cmafTrickPlayMuxerArgs(out, dest, i)...)
	}
	return args
}

// cmafTrickPlayMuxerArgs is one rung's fMP4 byte-range trick-play output.
//
// As on the MPEG-TS path, `iframes_only` is not used (ffmpeg emits incorrect
// repeated @0 offsets for it); the byte ranges come from `single_file` and the
// standards tag is added during finalisation. In single_file mode the muxer
// writes the init segment into the head of that same file and IGNORES
// hls_fmp4_init_filename — which is why the playlist's EXT-X-MAP carries a
// BYTERANGE and there is no separate init object to store.
func cmafTrickPlayMuxerArgs(out output, dest rungRef, rep int) []string {
	args := []string{
		"-f", "hls",
		"-hls_time", "1",
		"-hls_playlist_type", "vod",
		"-hls_list_size", "0",
		"-hls_segment_type", "fmp4",
		"-hls_flags", out.hlsFlags("single_file"),
	}
	args = append(args, out.muxerArgs()...)
	return append(args,
		"-hls_segment_filename", dest(cmafIFrameMediaName(rep)),
		dest(cmafIFramePlaylistName(rep)),
	)
}

// Finalize implements Packager. Like the MPEG-TS path everything here is
// decode-free — stream-copy remuxes and manifest bookkeeping — and it ends with
// the tree stored.
//
// The ORDER differs from MPEG-TS for one reason: the segments are shared, so
// there is no per-rung directory to upload and free as the ladder is walked.
// What is per-rung (the two progressive MP4s) is still uploaded and freed one
// rung at a time, and the shared directory is stored once at the end.
func (p cmafPackager) Finalize(ctx context.Context, req packageRequest) (packageResult, error) {
	tree := treeIO{
		out:     req.out,
		rel:     cmafDirName,
		scratch: filepath.Join(req.scratch, cmafDirName),
	}

	// ffmpeg's own master playlist is the source of truth for how the ladder
	// actually landed: which representation each rung is, and the CODECS string
	// the muxer computed for it (mandatory on every EXT-X-STREAM-INF for fMP4 in
	// Safari, and read here rather than probed so HEVC/AV1 need no new code).
	rawMaster, err := tree.read(ctx, cmafFFmpegMasterFilename)
	if err != nil {
		req.packagingFailed()
		return packageResult{}, fmt.Errorf("media: cmaf master playlist for %q: %w", req.sourceKey, err)
	}
	profiles := req.codecs
	if len(profiles) == 0 {
		profiles = defaultVideoCodecs
	}
	layout, err := parseCMAFMasterPlaylist(rawMaster, len(req.rungs)*len(profiles))
	if err != nil {
		req.packagingFailed()
		return packageResult{}, fmt.Errorf("media: cmaf layout for %q: %w", req.sourceKey, err)
	}
	layout.codecs = profiles
	// The resolution mapping is verified above; this is the codec half of it. Both
	// are read out of ffmpeg's own manifest rather than assumed, because a tree
	// whose representations are mislabelled plays — it just plays the wrong thing.
	if verr := layout.verifyCodecs(len(req.rungs)); verr != nil {
		req.packagingFailed()
		return packageResult{}, fmt.Errorf("media: cmaf layout for %q: %w", req.sourceKey, verr)
	}

	// Media playlists: strip the wall-clock date-times and declare VOD. The
	// rewrite goes through treeIO, so the STORED bytes are the fixed ones whether
	// the ladder was streamed or is still on scratch.
	for _, rep := range layout.representations() {
		name := cmafMediaPlaylistName(rep)
		body, rerr := tree.read(ctx, name)
		if rerr != nil {
			req.packagingFailed()
			return packageResult{}, fmt.Errorf("media: cmaf media playlist %s for %q: %w", name, req.sourceKey, rerr)
		}
		fixed, ferr := fixCMAFMediaPlaylist(body)
		if ferr != nil {
			req.packagingFailed()
			return packageResult{}, fmt.Errorf("media: cmaf media playlist %s for %q: %w", name, req.sourceKey, ferr)
		}
		if werr := tree.write(ctx, name, fixed); werr != nil {
			req.packagingFailed()
			return packageResult{}, werr
		}
	}

	// Trick-play, per rung, in the shared directory — and H.264 only, so its
	// representation index is the rung index whatever else the tree carries.
	// Scrub thumbnails are decoded by every client that has them, including the
	// ones that cannot decode HEVC or AV1, so the compatibility floor is the only
	// correct codec for them and a per-codec set would be pure waste.
	trickPlay := make(map[int]hlsTrickPlayInfo, len(req.rungs))
	for i, r := range req.rungs {
		info, terr := finalizeTrickPlayIn(ctx, req.tools.ffprobe, tree,
			cmafIFramePlaylistName(i), cmafIFrameMediaName(i))
		if terr != nil {
			req.packagingFailed()
			return packageResult{}, fmt.Errorf("media: trick-play %s for %q: %w", r.Name(), req.sourceKey, terr)
		}
		trickPlay[r.Height] = info
	}

	// Progressive downloads. A CMAF video representation carries no audio, so the
	// muxed asset is remuxed from TWO inputs — the rung's media playlist and the
	// shared audio one. Both remain plain `-c copy` passes.
	//
	// The rung's playlist is its H.264 representation (index i), and that is not
	// an oversight to fix by offering HEVC downloads. A downloaded file leaves the
	// platform and is opened by whatever the person has — an old phone, a TV
	// stick, a video editor — so it has to be the codec everything decodes. The
	// same reasoning gives the derived web videos and audio.m4a their codec.
	audioPlaylist := ""
	if layout.hasAudio {
		audioPlaylist = tree.ref(cmafMediaPlaylistName(layout.audioRep))
	}
	if audioPlaylist != "" {
		// The audio-only download, written at the TOP level beside the master. It
		// is extracted once for the whole tree because there is only one audio
		// representation to extract from — which is why it sits here rather than
		// inside the rung loop, and why an audio-only tree (no rungs at all) still
		// gets one.
		//
		// Best effort on a VIDEO tree: it is a convenience asset there, and a
		// failure must not fail the canonical transcode. On an audio-only tree it
		// is the whole point, so a failure is fatal — a "successful" transcode of a
		// podcast that produced no downloadable audio is not a success.
		dst := filepath.Join(req.scratch, HLSAudioDownloadFilename)
		cmd := exec.CommandContext(ctx, req.tools.ffmpeg, hlsAudioM4AArgs(audioPlaylist, dst)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if aerr := cmd.Run(); aerr != nil {
			_ = os.Remove(dst)
			if layout.audioOnly() {
				req.packagingFailed()
				return packageResult{}, fmt.Errorf("media: cmaf audio download for %q: %w: %s",
					req.sourceKey, aerr, tailOf(stderr.String()))
			}
		}
	}
	rungSizes := make(map[int]int64, len(req.rungs))
	for i, r := range req.rungs {
		dir := filepath.Join(req.scratch, r.Name())
		videoPlaylist := tree.ref(cmafMediaPlaylistName(i))
		if derr := remuxCMAFDownloads(ctx, req.tools.ffmpeg, req.sourceKey, videoPlaylist, audioPlaylist, dir, r); derr != nil {
			req.packagingFailed()
			return packageResult{}, derr
		}
		if herr := req.rungPackaged(ctx, r, dir); herr != nil {
			req.packagingFailed()
			return packageResult{}, herr
		}
		// A rung directory holds only its two progressive MP4s: the segments are
		// shared and accounted for below.
		local, serr := directorySize(dir)
		if serr != nil {
			return packageResult{}, serr
		}
		rungSizes[r.Height] = local
		if serr := storeTree(ctx, req.tools.blobs, dir, req.prefix+"/"+r.Name()); serr != nil {
			req.storeFailed(r)
			return packageResult{}, serr
		}
		if rerr := os.RemoveAll(dir); rerr != nil {
			return packageResult{}, rerr
		}
	}

	master, err := renderCMAFMasterPlaylist(req.rungs, req.audioKbps, layout, trickPlay)
	if err != nil {
		req.packagingFailed()
		return packageResult{}, fmt.Errorf("media: cmaf master playlist for %q: %w", req.sourceKey, err)
	}
	if werr := os.WriteFile(filepath.Join(req.scratch, hlsMasterPlaylistFilename), []byte(master), 0o644); werr != nil {
		return packageResult{}, werr
	}

	// ffmpeg's master is not part of the tree. A streamed ladder never stored it
	// (EphemeralOutputs kept it out of the flush); on scratch it is removed here,
	// before the upload walk can see it.
	if !req.out.streaming() {
		if rerr := os.Remove(filepath.Join(tree.scratch, cmafFFmpegMasterFilename)); rerr != nil {
			return packageResult{}, rerr
		}
	}

	// The shared segment directory is attributed to the TOP rung, together with
	// the top-level extras, exactly as the MPEG-TS packager attributes its own
	// top-level files. Per-rung attribution of a deliberately SHARED segment set
	// would be a fiction; what the sum has to equal — and does — is the stored
	// size of the whole tree.
	//
	// An AUDIO-ONLY tree has no rung to attribute anything to, and deliberately
	// gets no video_renditions row to carry a size (a rendition row describes a
	// resolution). Its bytes are therefore not in the per-video size ledger —
	// a known gap, and the honest one: the alternative is inventing a 0p rung the
	// whole ladder universe was explicitly built without.
	shared, err := cmafSharedTreeSize(req.out, tree.scratch)
	if err != nil {
		return packageResult{}, err
	}
	if len(req.rungs) > 0 {
		rungSizes[req.rungs[0].Height] += shared
	}

	if !req.out.streaming() {
		if serr := storeTree(ctx, req.tools.blobs, tree.scratch, req.prefix+"/"+cmafDirName); serr != nil {
			req.storeFailed(req.rungs...)
			return packageResult{}, serr
		}
		if rerr := os.RemoveAll(tree.scratch); rerr != nil {
			return packageResult{}, rerr
		}
	}

	// Only the master playlist and the optional audio-only download are left in
	// the scratch root now, so what it measures IS the top-level extra.
	extra, err := directorySize(req.scratch)
	if err != nil {
		return packageResult{}, err
	}
	if len(req.rungs) > 0 {
		rungSizes[req.rungs[0].Height] += extra
	}

	// A replacement source may be silent when the previous generation had audio.
	// storeTree only overwrites files present in the new tree, so remove the old
	// optional derivative before storing a generation that omits it.
	if _, statErr := os.Stat(filepath.Join(req.scratch, HLSAudioDownloadFilename)); os.IsNotExist(statErr) {
		if derr := req.tools.blobs.Delete(ctx, req.prefix+"/"+HLSAudioDownloadFilename); derr != nil {
			return packageResult{}, derr
		}
	} else if statErr != nil {
		return packageResult{}, statErr
	}
	if serr := storeTree(ctx, req.tools.blobs, req.scratch, req.prefix); serr != nil {
		req.storeFailed(req.rungs...)
		return packageResult{}, serr
	}
	return packageResult{
		masterKey: req.prefix + "/" + hlsMasterPlaylistFilename,
		format:    HLSFormatCMAF,
		rungSizes: rungSizes,
	}, nil
}

// cmafSharedTreeSize totals the stored bytes of the shared segment directory.
// The sink excludes discarded objects from its own accounting, so ffmpeg's
// never-stored master playlist is already out of this on both paths.
func cmafSharedTreeSize(out output, scratch string) (int64, error) {
	if out.streaming() {
		return out.sink.BytesUnder(cmafDirName), nil
	}
	return directorySize(scratch)
}

// remuxCMAFDownloads emits the two required progressive MP4 assets for a
// rendition from its CMAF representation. Both are canonical download outputs,
// so either command failing fails the transcode rather than storing a partial
// tree.
func remuxCMAFDownloads(ctx context.Context, ffmpegBin, sourceKey, videoPlaylist, audioPlaylist, dir string, r HLSRung) error {
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
		args := cmafProgressiveMP4Args(videoPlaylist, audioPlaylist, dst, output.includeAudio)
		cmd := exec.CommandContext(ctx, ffmpegBin, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("media: ffmpeg cmaf %s %s download for %q: %w: %s",
				r.Name(), output.label, sourceKey, err, tailOf(stderr.String()))
		}
	}
	return nil
}

// cmafProgressiveMP4Args builds an ffmpeg argument vector that remuxes one CMAF
// representation into a progressive MP4 without re-encoding.
//
// It takes TWO playlists where the MPEG-TS builder takes one: a CMAF video
// representation carries no audio, so the muxed asset is assembled from the
// rung's own representation plus the ladder's single shared audio one. A silent
// source has no audio representation at all, and the muxed asset then contains
// video alone — the same outcome the MPEG-TS builder's optional audio map gives.
func cmafProgressiveMP4Args(videoPlaylist, audioPlaylist, dst string, includeAudio bool) []string {
	args := []string{"-y", "-i", videoPlaylist}
	if includeAudio && audioPlaylist != "" {
		return append(args,
			"-i", audioPlaylist,
			"-map", "0:v:0",
			"-map", "1:a:0",
			"-c", "copy",
			"-movflags", "+faststart",
			dst,
		)
	}
	return append(args,
		"-map", "0:v:0",
		"-c:v", "copy",
		"-an",
		"-movflags", "+faststart",
		dst,
	)
}

// --- ffmpeg manifest parsing -------------------------------------------------

// cmafLayout is what ffmpeg's own HLS master playlist says about the tree it just
// wrote: the CODECS string it computed for each rung's representation, and
// whether a shared audio representation exists.
//
// It is parsed rather than assumed because the rung → representation mapping is
// the one thing that would corrupt the whole tree if it silently changed: a
// master playlist pointing every variant at the wrong media playlist plays, it
// just plays the wrong resolutions.
type cmafLayout struct {
	// videoCodecs[rep] is the CODECS attribute ffmpeg wrote for video
	// representation rep (ladderPlan.repIndex: every codec's whole ladder in rung
	// order, H.264 first).
	videoCodecs []string
	// codecs is the codec plan the ladder was ENCODED with — the one field here
	// that is not read out of ffmpeg's manifest. It is carried on the layout
	// because both consumers need it beside what ffmpeg reported: verifyCodecs
	// checks one against the other, and the master renderer needs it to know which
	// rung and which budget each representation is. Empty means the single-codec
	// H.264 ladder, which is what a layout parsed on its own resolves to.
	codecs   []codecProfile
	hasAudio bool
	// audioRep is the representation index of the shared audio rendition (the
	// index after the last video one).
	audioRep      int
	audioChannels string
	// audioCodecs is the CODECS string of the audio representation, and is read
	// only for an AUDIO-ONLY tree — where the single variant IS the audio, so its
	// codec list is what the master must declare. In a video tree the audio codec
	// already appears inside each variant's own CODECS.
	audioCodecs string
}

// audioOnly reports that the tree ffmpeg wrote has no video representations.
func (l cmafLayout) audioOnly() bool { return len(l.videoCodecs) == 0 }

// profiles is the layout's codec plan, resolved.
func (l cmafLayout) profiles() []codecProfile {
	if len(l.codecs) == 0 {
		return defaultVideoCodecs
	}
	return l.codecs
}

// verifyCodecs checks that ffmpeg encoded each representation with the codec the
// ladder assigned to it, by reading the CODECS string the muxer computed for it.
//
// It is the codec half of the same guarantee parseCMAFMasterPlaylist gives for
// resolution, and it exists because the failure it catches is silent in exactly
// the same way. A tree whose "HEVC" representations are really H.264 plays
// perfectly — it just delivers H.264 to every client that was told to expect
// H.265, at the H.265 rung's smaller budget, which looks like a quality
// regression and nothing else. And the specific fault this pipeline has already
// measured is worse still: an HEVC stream written without the hvc1 tag makes
// ffmpeg compute an EMPTY codec string, producing a master no client will start.
//
// rungs is the ladder height, so representation rep belongs to codec rep/rungs.
func (l cmafLayout) verifyCodecs(rungs int) error {
	if rungs == 0 {
		return nil // audio-only: no video representations to attribute
	}
	profiles := l.profiles()
	if want := rungs * len(profiles); want != len(l.videoCodecs) {
		return fmt.Errorf("ffmpeg wrote %d video representations, want %d (%d rungs × %d codecs)",
			len(l.videoCodecs), want, rungs, len(profiles))
	}
	for c, prof := range profiles {
		for i := 0; i < rungs; i++ {
			rep := c*rungs + i
			if !strings.HasPrefix(l.videoCodecs[rep], prof.CodecsPrefix) {
				return fmt.Errorf(
					"representation %d is rung %d encoded as %s, but ffmpeg declared CODECS %q, which does not start with %q",
					rep, i, prof.Name, l.videoCodecs[rep], prof.CodecsPrefix)
			}
		}
	}
	return nil
}

// representations lists every representation index in the tree, video first.
func (l cmafLayout) representations() []int {
	reps := make([]int, 0, len(l.videoCodecs)+1)
	for i := range l.videoCodecs {
		reps = append(reps, i)
	}
	if l.hasAudio {
		reps = append(reps, l.audioRep)
	}
	return reps
}

// parseCMAFMasterPlaylist reads ffmpeg's generated HLS master playlist and
// verifies the representation layout is exactly the one the ladder asked for:
// videoReps video representations in the order they were mapped, then at most
// one audio rendition.
//
// videoReps is rungs × codecs, not rungs: with several codecs enabled the same
// resolution appears once per codec, and the whole point of reading this file is
// that the index-to-content mapping is checked rather than assumed. WHICH codec
// each index holds is checked separately, by cmafLayout.verifyCodecs, because
// only that check needs to know the codec plan.
func parseCMAFMasterPlaylist(playlist []byte, videoReps int) (cmafLayout, error) {
	if videoReps == 0 {
		return parseCMAFAudioOnlyMasterPlaylist(playlist)
	}
	layout := cmafLayout{videoCodecs: make([]string, videoReps)}
	seen := make([]bool, videoReps)

	lines := strings.Split(string(playlist), "\n")
	pendingCodecs, pending := "", false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA:"):
			if attr, _ := m3u8Attr(line, "TYPE"); attr != "AUDIO" {
				continue
			}
			uri, ok := m3u8Attr(line, "URI")
			if !ok {
				return cmafLayout{}, errors.New("audio rendition has no URI")
			}
			rep, ok := cmafRepOfMediaPlaylist(uri)
			if !ok {
				return cmafLayout{}, fmt.Errorf("audio rendition URI %q is not a media playlist", uri)
			}
			if rep != videoReps {
				return cmafLayout{}, fmt.Errorf("audio is representation %d, want %d (every video representation, then audio)", rep, videoReps)
			}
			layout.hasAudio, layout.audioRep = true, rep
			layout.audioChannels, _ = m3u8Attr(line, "CHANNELS")
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			pendingCodecs, _ = m3u8Attr(line, "CODECS")
			pending = true
		case strings.HasPrefix(line, "#"):
			// Any other tag; a URI must follow its own STREAM-INF immediately.
		default:
			if !pending {
				continue
			}
			pending = false
			rep, ok := cmafRepOfMediaPlaylist(line)
			if !ok {
				return cmafLayout{}, fmt.Errorf("variant URI %q is not a media playlist", line)
			}
			if rep < 0 || rep >= videoReps {
				return cmafLayout{}, fmt.Errorf("variant representation %d is outside the ladder's %d video representations", rep, videoReps)
			}
			if seen[rep] {
				return cmafLayout{}, fmt.Errorf("representation %d appears twice", rep)
			}
			seen[rep], layout.videoCodecs[rep] = true, pendingCodecs
		}
	}
	for i, ok := range seen {
		if !ok {
			return cmafLayout{}, fmt.Errorf("representation %d has no variant", i)
		}
		if layout.videoCodecs[i] == "" {
			return cmafLayout{}, fmt.Errorf("representation %d has no CODECS", i)
		}
	}
	return layout, nil
}

// parseCMAFAudioOnlyMasterPlaylist is parseCMAFMasterPlaylist for a tree with no
// video representations at all.
//
// It is a separate reader rather than a special case inside the other one
// because ffmpeg writes a genuinely DIFFERENT master here: with no video to
// group renditions against, it emits no EXT-X-MEDIA line at all and publishes
// the audio as an ordinary EXT-X-STREAM-INF variant. That is the correct HLS
// shape — RFC 8216's audio-only variant is a variant, not a rendition group —
// but it means the audio arrives through the branch that means "video rung" in
// the other reader.
func parseCMAFAudioOnlyMasterPlaylist(playlist []byte) (cmafLayout, error) {
	layout := cmafLayout{hasAudio: true}
	found := false
	pendingCodecs, pending := "", false
	for _, raw := range strings.Split(string(playlist), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			pendingCodecs, _ = m3u8Attr(line, "CODECS")
			pending = true
		case strings.HasPrefix(line, "#"):
			// Any other tag; a URI must follow its own STREAM-INF immediately.
		default:
			if !pending {
				continue
			}
			pending = false
			if found {
				return cmafLayout{}, errors.New("audio-only tree has more than one variant")
			}
			rep, ok := cmafRepOfMediaPlaylist(line)
			if !ok {
				return cmafLayout{}, fmt.Errorf("variant URI %q is not a media playlist", line)
			}
			if rep != 0 {
				return cmafLayout{}, fmt.Errorf("audio is representation %d, want 0 (it is the only stream)", rep)
			}
			found, layout.audioRep, layout.audioCodecs = true, rep, pendingCodecs
		}
	}
	if !found {
		return cmafLayout{}, errors.New("audio-only tree has no variant")
	}
	// Guarded because the whole point of the master is telling a player what it
	// is about to decode, and an audio-only variant's codec list is the ONLY
	// place the audio codec appears — a video tree's variants each carry it
	// inside their own CODECS.
	if !strings.Contains(layout.audioCodecs, "mp4a.") {
		return cmafLayout{}, fmt.Errorf("audio-only variant CODECS %q names no audio codec", layout.audioCodecs)
	}
	return layout, nil
}

// cmafRepOfMediaPlaylist extracts N from "media_N.m3u8".
func cmafRepOfMediaPlaylist(uri string) (int, bool) {
	name := strings.TrimSpace(uri)
	rest, ok := strings.CutPrefix(name, "media_")
	if !ok {
		return 0, false
	}
	digits, ok := strings.CutSuffix(rest, ".m3u8")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// m3u8Attr reads one attribute of an m3u8 tag line, unquoting a quoted-string
// value. Attribute lists are comma-separated NAME=VALUE pairs where a value may
// itself contain commas inside quotes (CODECS="avc1.4d4015,mp4a.40.2").
func m3u8Attr(line, name string) (string, bool) {
	_, list, ok := strings.Cut(line, ":")
	if !ok {
		return "", false
	}
	for len(list) > 0 {
		var pair string
		if i := attrSeparator(list); i >= 0 {
			pair, list = list[:i], list[i+1:]
		} else {
			pair, list = list, ""
		}
		key, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || key != name {
			continue
		}
		if unquoted, ok := strings.CutPrefix(value, `"`); ok {
			value, _ = strings.CutSuffix(unquoted, `"`)
		}
		return value, true
	}
	return "", false
}

// attrSeparator is the index of the comma ending the first attribute in list,
// ignoring commas inside a quoted value; -1 when there is no further attribute.
func attrSeparator(list string) int {
	quoted := false
	for i, c := range list {
		switch c {
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				return i
			}
		}
	}
	return -1
}

// --- Go-authored manifests ---------------------------------------------------

// fixCMAFMediaPlaylist repairs one of ffmpeg's media playlists for VOD delivery:
// the per-segment EXT-X-PROGRAM-DATE-TIME lines are dropped (they pin a finished
// recording to the wall-clock instant it happened to be packaged, which players
// surface as a bogus live-edge date) and EXT-X-PLAYLIST-TYPE:VOD is declared so
// clients know the playlist is final and fully seekable.
func fixCMAFMediaPlaylist(playlist []byte) ([]byte, error) {
	lines := strings.Split(string(playlist), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "#EXTM3U" {
		return nil, errors.New("not an m3u8")
	}
	out := make([]string, 0, len(lines)+1)
	// Declared right after the header block ffmpeg writes, before the first
	// segment: EXT-X-PLAYLIST-TYPE is a playlist-wide tag.
	declared := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#EXT-X-PROGRAM-DATE-TIME") {
			continue
		}
		if trimmed == "#EXT-X-PLAYLIST-TYPE:VOD" {
			declared = true
		}
		if !declared && strings.HasPrefix(trimmed, "#EXT-X-MAP") {
			out = append(out, "#EXT-X-PLAYLIST-TYPE:VOD")
			declared = true
		}
		out = append(out, line)
	}
	if !declared {
		return nil, errors.New("no EXT-X-MAP to anchor the VOD declaration")
	}
	return []byte(strings.Join(out, "\n")), nil
}

// renderCMAFMasterPlaylist renders the master playlist for a CMAF tree. Pure (no
// exec/IO) so it is unit-testable.
//
// It is version 7 rather than the MPEG-TS path's 4 (fMP4 media requires it) and
// carries CODECS on every variant — advisory in the specification, in practice
// mandatory: Safari will not start an fMP4 variant it has no codec string for.
// Those strings come from ffmpeg's own manifest, so they stay correct for codecs
// this package has no bespoke parser for.
//
// Every URI is RELATIVE and prefixed with the shared segment directory, so the
// playlist works from any base path and the file route resolves it as the "cmaf"
// pseudo-rendition.
//
// It returns an error rather than a best-effort playlist when ffmpeg's CODECS
// strings do not describe what the variant actually plays: an fMP4 variant whose
// codec list is wrong or incomplete is exactly what Safari refuses, and a master
// that is silently unplayable in one browser is far worse than a failed
// transcode an operator can see.
//
// VARIANT ORDER IS COMPAT-FIRST when several codecs are enabled: every H.264
// variant, in ladder order, then every HEVC one, then every AV1 one. RFC 8216
// gives a master no ranking semantics, and a good client reads CODECS and picks
// what it can decode at the bandwidth it has — but clients that simply take the
// first variant they can play exist, and for them the first thing in the file
// must be the one everything can play. Trick-play is advertised for the H.264
// variants only, because that is the only codec it is encoded in.
func renderCMAFMasterPlaylist(rungs []HLSRung, audioKbps int, layout cmafLayout, trickPlay map[int]hlsTrickPlayInfo) (string, error) {
	if len(rungs) == 0 {
		return renderCMAFAudioOnlyMasterPlaylist(audioKbps, layout)
	}
	// Audio is encoded ONCE for the whole ladder, so every variant's peak rate
	// includes the same audio bitrate — the one the shared representation was
	// encoded at. Using each rung's own AudioKbps here (as the MPEG-TS master
	// legitimately does, because MPEG-TS really does encode audio per rung) would
	// under-declare BANDWIDTH on every variant below the top, and RFC 8216
	// requires it to be the peak.
	if !layout.hasAudio {
		audioKbps = 0
	}

	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	audioAttr := ""
	if layout.hasAudio {
		fmt.Fprintf(&b, "#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=%q,NAME=%q,DEFAULT=YES,AUTOSELECT=YES",
			cmafAudioGroupID, cmafAudioGroupID)
		if layout.audioChannels != "" {
			fmt.Fprintf(&b, ",CHANNELS=%q", layout.audioChannels)
		}
		fmt.Fprintf(&b, ",URI=%q\n", cmafDirName+"/"+cmafMediaPlaylistName(layout.audioRep))
		audioAttr = fmt.Sprintf(",AUDIO=%q", cmafAudioGroupID)
	}
	for c, prof := range layout.profiles() {
		for i, r := range rungs {
			rep := c*len(rungs) + i
			if rep >= len(layout.videoCodecs) {
				return "", fmt.Errorf("representation %d (%s rung %d) is missing from the layout", rep, prof.Name, i)
			}
			codecs := layout.videoCodecs[rep]
			// A variant that references the audio rendition group must say so in its
			// codec list; a player picks a variant on CODECS before it fetches
			// anything, and one that omits the audio codec is chosen and then fails.
			if layout.hasAudio && !strings.Contains(codecs, "mp4a.") {
				return "", fmt.Errorf("variant %d references the audio rendition but its CODECS %q names no audio codec", rep, codecs)
			}
			// The declared peak is THIS codec's budget for this rung — an HEVC rung
			// is encoded at its own multiplier of the H.264 one, and declaring the
			// H.264 number would tell an ABR player the cheap variant costs as much
			// as the expensive one.
			fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=%q%s\n",
				cmafVariantBandwidth(prof.videoKbps(r), audioKbps), r.Width, r.Height, codecs, audioAttr)
			b.WriteString(cmafDirName + "/" + cmafMediaPlaylistName(rep) + "\n")
			// Trick-play is H.264-only (the compatibility floor), so it is
			// advertised beside the H.264 variants and nowhere else.
			if c != 0 {
				continue
			}
			if tp, ok := trickPlay[r.Height]; ok && tp.Bandwidth > 0 && tp.Codec != "" {
				fmt.Fprintf(&b,
					"#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=%q,URI=%q\n",
					tp.Bandwidth, r.Width, r.Height, tp.Codec, cmafDirName+"/"+cmafIFramePlaylistName(i))
			}
		}
	}
	return b.String(), nil
}

// cmafVariantBandwidth is one CMAF variant's declared peak rate: its own video
// bitrate — its CODEC's budget for its rung — plus the ladder's SHARED audio
// bitrate, with the same ~10% container allowance HLSRung.Bandwidth applies.
func cmafVariantBandwidth(videoKbps, sharedAudioKbps int) int {
	return (videoKbps + sharedAudioKbps) * 1000 * 11 / 10
}

// renderCMAFAudioOnlyMasterPlaylist renders the master for a tree with no video.
//
// It is RFC 8216 §4.3.4.2's audio-only presentation: a single EXT-X-STREAM-INF
// naming the audio media playlist directly, with the audio codec in CODECS and
// NO RESOLUTION attribute (there is nothing to resolve). Deliberately NOT an
// EXT-X-MEDIA rendition group — a group describes alternates OF a video
// presentation, and referencing one from no variant at all is not a playlist any
// client will start.
func renderCMAFAudioOnlyMasterPlaylist(audioKbps int, layout cmafLayout) (string, error) {
	if !layout.hasAudio || layout.audioCodecs == "" {
		return "", errors.New("audio-only master needs an audio representation")
	}
	if audioKbps <= 0 {
		return "", errors.New("audio-only master needs the encoded audio budget")
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,CODECS=%q\n",
		cmafAudioOnlyBandwidth(audioKbps), layout.audioCodecs)
	b.WriteString(cmafDirName + "/" + cmafMediaPlaylistName(layout.audioRep) + "\n")
	return b.String(), nil
}

// cmafAudioOnlyBandwidth is the audio-only variant's declared peak rate: the
// encoder's own budget with the same ~10% container allowance every other
// BANDWIDTH here carries.
func cmafAudioOnlyBandwidth(audioKbps int) int { return audioKbps * 1000 * 11 / 10 }
