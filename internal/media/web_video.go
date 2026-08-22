package media

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// WebVideoResult is one progressive H.264/AAC MP4 derivative.
type WebVideoResult struct {
	Height     int
	Width      int
	StorageKey string
	SizeBytes  int64
	// SHA256 is the lowercase hex digest of the stored object, taken from the
	// upload stream itself (phase-2 storage, work item 2). It rides the result
	// out to the transcode service, which is what writes the video_files row.
	SHA256 string
}

func webVideoArgs(src source, dst string, r HLSRung, threads int) []string {
	vf := fmt.Sprintf("scale=%d:%d", r.Width, r.Height)
	if r.FPS > 0 {
		vf += fmt.Sprintf(",fps=%d", r.FPS)
	}
	args := []string{"-y"}
	args = append(args, src.inputArgs()...)
	args = append(args, "-map", "0:v:0", "-map", "0:a:0?")
	if threads > 0 {
		args = append(args, "-threads", fmt.Sprintf("%d", threads))
	}
	args = append(args, webVideoEncodeArgs(r, vf)...)
	return append(args, dst)
}

// webVideoEncodeArgs is one progressive MP4's encoder configuration, shared by
// the single-rung and ladder forms so they cannot drift. vf is the scale chain
// for the single-rung form; the ladder passes "" because its filter graph has
// already scaled the branch.
func webVideoEncodeArgs(r HLSRung, vf string) []string {
	args := []string{
		"-c:v", "libx264", "-profile:v", "main", "-preset", "veryfast",
		"-pix_fmt", "yuv420p",
	}
	if vf != "" {
		args = append(args, "-vf", vf)
	}
	return append(args,
		"-b:v", fmt.Sprintf("%dk", r.VideoKbps),
		"-maxrate", fmt.Sprintf("%dk", r.VideoKbps),
		"-bufsize", fmt.Sprintf("%dk", 2*r.VideoKbps),
		"-c:a", "aac", "-b:a", fmt.Sprintf("%dk", r.AudioKbps), "-ac", "2",
		"-movflags", "+faststart",
	)
}

// webVideoLadderArgs builds ONE ffmpeg argument vector that decodes the source a
// single time and writes every rung's progressive MP4. Same reasoning as
// hlsLadderArgs: the previous per-rung loop decoded the whole source once per
// resolution to produce different scalings of identical frames.
func webVideoLadderArgs(src source, root string, rungs []HLSRung, threads int) []string {
	args := []string{"-y"}
	args = append(args, src.inputArgs()...)

	chain, labels := splitChain(len(rungs))
	chains := make([]string, 0, len(rungs)+1)
	if chain != "" {
		chains = append(chains, chain)
	}
	outLabels := make([]string, len(rungs))
	for i, r := range rungs {
		vf := fmt.Sprintf("scale=%d:%d", r.Width, r.Height)
		if r.FPS > 0 {
			vf += fmt.Sprintf(",fps=%d", r.FPS)
		}
		outLabels[i] = fmt.Sprintf("w%d", i)
		chains = append(chains, fmt.Sprintf("[%s]%s[%s]", labels[i], vf, outLabels[i]))
	}
	args = append(args, "-filter_complex", strings.Join(chains, ";"))

	per := perOutputThreads(threads, len(rungs))
	for i, r := range rungs {
		args = append(args, "-map", "["+outLabels[i]+"]", "-map", "0:a:0?")
		if per > 0 {
			args = append(args, "-threads", strconv.Itoa(per))
		}
		args = append(args, webVideoEncodeArgs(r, "")...)
		args = append(args, filepath.Join(root, r.Name()+".mp4"))
	}
	return args
}

// WebVideoPrefixForSource keeps manual reruns stable while source replacements
// write a fresh generation. Originals are sibling objects
// (web-videos/<id>.<ext>), so deleting this directory never deletes the source.
func WebVideoPrefixForSource(videoID uuid.UUID, sourceKey string) string {
	prefix := "web-videos/" + videoID.String()
	if gen := HLSGenerationName(OriginalKeyVersion(sourceKey)); gen != "" {
		prefix += "/" + gen
	}
	return prefix
}

// TranscodeWebVideos creates one independently tracked progressive MP4 per
// planned resolution, always from the retained original sourceKey.
// md is the caller's already-obtained probe of sourceKey; the worker probes once
// per job and shares it across targets.
func (t *HLSTranscoder) TranscodeWebVideos(ctx context.Context, videoID uuid.UUID, sourceKey string, md Metadata, progress ProgressFunc) ([]WebVideoResult, error) {
	settings := t.encodeSettings()
	rungs := PlanHLSLadderWith(settings, md.Width, md.Height, md.FPS)
	if len(rungs) == 0 {
		// A source with no video has no progressive VIDEO derivatives to make, and
		// that is a complete answer rather than a failure: its audio deliverable is
		// the audio.m4a the streaming tree already produces. Returning nothing lets
		// the caller record nothing, which is exactly right — anything else would
		// fail a job whose work is genuinely done.
		if md.AudioOnly() {
			return nil, nil
		}
		return nil, fmt.Errorf("media: source %q has no probeable video dimensions", sourceKey)
	}
	for _, r := range rungs {
		reportProgress(progress, TranscodeProgress{
			Format: TranscodeFormatWebVideo, Height: r.Height, Width: r.Width,
			State: ProgressQueued, Stage: "queued", Percent: 0,
		})
	}

	src, cleanup, err := openSource(ctx, t.blobs, sourceKey)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	tmp, err := os.MkdirTemp("", "vidra-web-video-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	// One decode for the whole set (see webVideoLadderArgs). The rungs advance
	// together instead of one after another, so each reports the shared progress
	// of the single pass.
	reportAll := func(stage, state string, percent int) {
		for _, r := range rungs {
			reportProgress(progress, TranscodeProgress{
				Format: TranscodeFormatWebVideo, Height: r.Height, Width: r.Width,
				State: state, Stage: stage, Percent: percent,
			})
		}
	}
	reportAll("encoding", ProgressRunning, 1)
	stderr, runErr := runFFmpegWithProgress(ctx, t.bin, webVideoLadderArgs(src, tmp, rungs, settings.Threads), md.DurationSeconds, func(percent int) {
		reportAll("encoding", ProgressRunning, percent*95/100)
	})
	if runErr != nil {
		reportAll("encoding", ProgressFailed, 0)
		return nil, fmt.Errorf("media: ffmpeg web video ladder for %q: %w: %s", sourceKey, redactSource(src, runErr), tailOf(stderr))
	}

	prefix := WebVideoPrefixForSource(videoID, sourceKey)
	if deleter, ok := t.blobs.(storage.PrefixDeleter); ok {
		if err := deleter.DeletePrefix(ctx, prefix); err != nil {
			return nil, err
		}
	}
	results := make([]WebVideoResult, 0, len(rungs))
	for _, r := range rungs {
		reportProgress(progress, TranscodeProgress{
			Format: TranscodeFormatWebVideo, Height: r.Height, Width: r.Width,
			State: ProgressRunning, Stage: "storing", Percent: 97,
		})
		filename := r.Name() + ".mp4"
		local := filepath.Join(tmp, filename)
		f, err := os.Open(local)
		if err != nil {
			return nil, err
		}
		key := path.Join(prefix, filename)
		size, sum, putErr := storage.PutSizedHashed(ctx, t.blobs, key, f, storage.SizeUnknown)
		_ = f.Close()
		if putErr != nil {
			return nil, putErr
		}
		// Free each rendition as soon as it is stored. The single decode-once
		// pass necessarily produces every rung before any can be uploaded, so
		// this does not lower the peak — it shortens how long the peak is held,
		// which is what matters when several jobs share one scratch volume.
		if err := os.Remove(local); err != nil {
			return nil, err
		}
		results = append(results, WebVideoResult{
			Height: r.Height, Width: r.Width, StorageKey: key, SizeBytes: size, SHA256: sum,
		})
		reportProgress(progress, TranscodeProgress{
			Format: TranscodeFormatWebVideo, Height: r.Height, Width: r.Width,
			State: ProgressSucceeded, Stage: "complete", Percent: 100,
		})
	}
	return results, nil
}
