package media

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// WebMContentType is served for the progressive VP9/WebM alternate.
const WebMContentType = "video/webm"

// vp9WebMArgs builds the ffmpeg argument vector encoding a single progressive
// VP9/WebM alternate from src into dst at the given rung's dimensions/bitrates.
// VP9 in HLS needs fMP4/CMAF packaging with patchy client support, so Vidra
// emits VP9 as a progressive WebM download alternate (surfaced via /download)
// rather than an HLS variant — see .ralph/specs/storage-layout.md. Pure (no
// exec) so it is unit-testable. The audio map is optional ("0:a:0?") so silent
// sources still encode.
//
// It encodes from the SOURCE rather than from the ladder's output, so it is the
// one output that has to apply the rung's fps cap itself: everything else
// inherits it from the shared filter graph. Without that the alternate silently
// ignored transcoding_max_fps — a 60fps source under a 30fps cap emitted 60fps
// VP9, and at the standard-rate budget, because planning had already decided the
// output was 30fps.
func vp9WebMArgs(src source, dst string, r HLSRung) []string {
	vf := fmt.Sprintf("scale=%d:%d", r.Width, r.Height)
	if r.FPS > 0 {
		vf += fmt.Sprintf(",fps=%d", r.FPS)
	}
	args := []string{"-y"}
	args = append(args, src.inputArgs()...)
	return append(args,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-c:v", "libvpx-vp9",
		"-b:v", fmt.Sprintf("%dk", r.VideoKbps),
		"-crf", "32",
		"-row-mt", "1",
		"-deadline", "good",
		"-cpu-used", "4",
		"-pix_fmt", "yuv420p",
		"-vf", vf,
		"-c:a", "libopus",
		"-b:a", fmt.Sprintf("%dk", r.AudioKbps),
		"-ac", "2",
		"-f", "webm",
		dst,
	)
}

// VP9WebMKey is the storage key for a video's progressive VP9/WebM alternate,
// kept under the same per-video HLS tree so media GC treats it at the video-id
// level like the rest of the ladder.
func VP9WebMKey(videoID uuid.UUID) string {
	return HLSKeyPrefix(videoID) + "/vp9.webm"
}

// SetVP9 turns on (or off) the progressive VP9/WebM alternate produced alongside
// the H.264 HLS ladder. Off by default; cmd/api enables it from
// TRANSCODING_VP9_ENABLED.
func (t *HLSTranscoder) SetVP9(enabled bool) { t.vp9 = enabled }

// SetStreamOutput makes the HLS ladder write straight into the blob store
// through a loopback blobsink, so segments never land on scratch. Off by
// default; cmd/api enables it from TRANSCODING_STREAM_OUTPUT.
//
// This is a deliberate trade, not a free win. Peak scratch for the ladder drops
// to roughly zero, but the pipeline reads its own output back to build the
// progressive downloads and to probe the trick-play codec, so those reads become
// object-store range requests. Worth it where scratch is scarce and egress is
// cheap or free (a Backblaze B2 bucket fronted by a Bandwidth Alliance CDN);
// not worth it where egress is metered and disk is plentiful.
func (t *HLSTranscoder) SetStreamOutput(enabled bool) { t.streamOutput = enabled }

// encodeVP9 renders the progressive VP9/WebM alternate for src (a local path)
// at the top rung, stores it at <hlsPrefix>/vp9.webm (the same generation
// directory as the HLS tree it accompanies — W14), and returns the stored key,
// byte size and content hash. Best-effort at the call site: a failure must not
// fail the HLS transcode.
func (t *HLSTranscoder) encodeVP9(ctx context.Context, videoID uuid.UUID, hlsPrefix string, src source, top HLSRung) (key string, size int64, sum string, err error) {
	out, err := os.CreateTemp("", "vidra-vp9-*.webm")
	if err != nil {
		return "", 0, "", err
	}
	outPath := out.Name()
	_ = out.Close()
	defer func() { _ = os.Remove(outPath) }()

	cmd := exec.CommandContext(ctx, t.bin, vp9WebMArgs(src, outPath, top)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", 0, "", fmt.Errorf("media: ffmpeg vp9 for %s: %w: %s", videoID, err, tailOf(stderr.String()))
	}
	f, err := os.Open(outPath)
	if err != nil {
		return "", 0, "", err
	}
	defer func() { _ = f.Close() }()
	key = hlsPrefix + "/vp9.webm"
	n, sum, err := storage.PutSizedHashed(ctx, t.blobs, key, f, storage.SizeUnknown)
	if err != nil {
		return "", 0, "", err
	}
	return key, n, sum, nil
}
