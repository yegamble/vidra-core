package media

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// WebVideoResult is one progressive H.264/AAC MP4 derivative.
type WebVideoResult struct {
	Height     int
	Width      int
	StorageKey string
	SizeBytes  int64
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
	return append(args,
		"-c:v", "libx264", "-profile:v", "main", "-preset", "veryfast",
		"-pix_fmt", "yuv420p", "-vf", vf,
		"-b:v", fmt.Sprintf("%dk", r.VideoKbps),
		"-maxrate", fmt.Sprintf("%dk", r.VideoKbps),
		"-bufsize", fmt.Sprintf("%dk", 2*r.VideoKbps),
		"-c:a", "aac", "-b:a", fmt.Sprintf("%dk", r.AudioKbps), "-ac", "2",
		"-movflags", "+faststart", dst,
	)
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

	for _, r := range rungs {
		reportProgress(progress, TranscodeProgress{
			Format: TranscodeFormatWebVideo, Height: r.Height, Width: r.Width,
			State: ProgressRunning, Stage: "encoding", Percent: 1,
		})
		dst := filepath.Join(tmp, r.Name()+".mp4")
		stderr, runErr := runFFmpegWithProgress(ctx, t.bin, webVideoArgs(src, dst, r, settings.Threads), md.DurationSeconds, func(percent int) {
			reportProgress(progress, TranscodeProgress{
				Format: TranscodeFormatWebVideo, Height: r.Height, Width: r.Width,
				State: ProgressRunning, Stage: "encoding", Percent: percent * 95 / 100,
			})
		})
		if runErr != nil {
			reportProgress(progress, TranscodeProgress{
				Format: TranscodeFormatWebVideo, Height: r.Height, Width: r.Width,
				State: ProgressFailed, Stage: "encoding", Percent: 0,
			})
			return nil, fmt.Errorf("media: ffmpeg web video %s for %q: %w: %s", r.Name(), sourceKey, runErr, tailOf(stderr))
		}
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
		f, err := os.Open(filepath.Join(tmp, filename))
		if err != nil {
			return nil, err
		}
		key := path.Join(prefix, filename)
		size, putErr := t.blobs.Put(ctx, key, f)
		_ = f.Close()
		if putErr != nil {
			return nil, putErr
		}
		results = append(results, WebVideoResult{
			Height: r.Height, Width: r.Width, StorageKey: key, SizeBytes: size,
		})
		reportProgress(progress, TranscodeProgress{
			Format: TranscodeFormatWebVideo, Height: r.Height, Width: r.Width,
			State: ProgressSucceeded, Stage: "complete", Percent: 100,
		})
	}
	return results, nil
}
