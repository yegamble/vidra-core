package media

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/vidra/vidra-core/internal/storage"
)

// thumbnailWidth is the poster width; height is derived to keep aspect ratio
// (the "-2" keeps it even, required by some encoders).
const thumbnailWidth = 640

// Thumbnailer extracts a single poster frame from a video as JPEG bytes by
// shelling out to ffmpeg. It satisfies video.Thumbnailer.
type Thumbnailer struct {
	blobs storage.Backend
	bin   string
}

// NewThumbnailer builds a Thumbnailer reading objects from blobs via the
// "ffmpeg" binary on PATH.
func NewThumbnailer(blobs storage.Backend) *Thumbnailer {
	return &Thumbnailer{blobs: blobs, bin: "ffmpeg"}
}

// DetectThumbnailer returns a Thumbnailer when the ffmpeg binary is on PATH,
// else (nil, false) so callers can publish without a poster.
func DetectThumbnailer(blobs storage.Backend) (*Thumbnailer, bool) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, false
	}
	return NewThumbnailer(blobs), true
}

// Thumbnail produces a JPEG poster for the media at key. durationSeconds (0 if
// unknown) hints which frame to grab.
func (t *Thumbnailer) Thumbnail(ctx context.Context, key string, durationSeconds int) ([]byte, error) {
	src, cleanup, err := objectPath(ctx, t.blobs, key)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	out, err := os.CreateTemp("", "vidra-thumb-*.jpg")
	if err != nil {
		return nil, err
	}
	outPath := out.Name()
	_ = out.Close()
	defer func() { _ = os.Remove(outPath) }()

	cmd := exec.CommandContext(ctx, t.bin, thumbnailArgs(src, outPath, thumbnailSeekSeconds(durationSeconds))...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("media: ffmpeg thumbnail %q: %w", key, err)
	}
	b, err := os.ReadFile(outPath)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("media: ffmpeg produced an empty thumbnail for %q", key)
	}
	return b, nil
}

// ThumbnailAt produces a JPEG poster for the media at key by extracting the exact
// frame at atSeconds (fractional seconds allowed). Unlike Thumbnail, which picks a
// representative frame automatically, this honours a caller-chosen timestamp — the
// frame-pick poster path (UPLOAD-04 / W2.C5). The caller is responsible for
// bounding atSeconds against the media duration; a timestamp at/after the last
// frame yields an empty output, reported as an error.
func (t *Thumbnailer) ThumbnailAt(ctx context.Context, key string, atSeconds float64) ([]byte, error) {
	src, cleanup, err := objectPath(ctx, t.blobs, key)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	out, err := os.CreateTemp("", "vidra-frame-*.jpg")
	if err != nil {
		return nil, err
	}
	outPath := out.Name()
	_ = out.Close()
	defer func() { _ = os.Remove(outPath) }()

	cmd := exec.CommandContext(ctx, t.bin, thumbnailAtArgs(src, outPath, atSeconds)...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("media: ffmpeg frame %q: %w", key, err)
	}
	b, err := os.ReadFile(outPath)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("media: ffmpeg produced an empty frame for %q", key)
	}
	return b, nil
}

// thumbnailSeekSeconds picks a representative frame: 1s in for anything at least
// 2s long (avoids a black opening frame), else the very first frame.
func thumbnailSeekSeconds(durationSeconds int) int {
	if durationSeconds >= 2 {
		return 1
	}
	return 0
}

// thumbnailArgs builds the ffmpeg argument vector to extract one scaled JPEG
// frame. Pure (no exec) so it is unit-testable.
func thumbnailArgs(src, dst string, seekSeconds int) []string {
	return []string{
		"-y",
		"-ss", strconv.Itoa(seekSeconds),
		"-i", src,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:-2", thumbnailWidth),
		"-q:v", "3",
		dst,
	}
}

// thumbnailAtArgs builds the ffmpeg argument vector to extract one scaled JPEG
// frame at an exact timestamp (fractional seconds). Pure (no exec) so it is
// unit-testable; mirrors thumbnailArgs but seeks to a caller-chosen time. The
// timestamp is formatted with the shortest exact decimal (e.g. 5, 5.5), never in
// scientific notation, so ffmpeg always reads a plain seconds value.
func thumbnailAtArgs(src, dst string, atSeconds float64) []string {
	return []string{
		"-y",
		"-ss", strconv.FormatFloat(atSeconds, 'f', -1, 64),
		"-i", src,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:-2", thumbnailWidth),
		"-q:v", "3",
		dst,
	}
}
