package media

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/vidra/vidra-core/internal/storage"
)

// FFProbe extracts media metadata by shelling out to ffprobe. It satisfies
// video.Prober. A probe failure (non-media file, unreadable, ffprobe error)
// surfaces as an error so the caller can mark the video failed.
type FFProbe struct {
	blobs storage.Backend
	bin   string
}

// NewFFProbe builds an FFProbe reading objects from blobs via the "ffprobe"
// binary on PATH.
func NewFFProbe(blobs storage.Backend) *FFProbe {
	return &FFProbe{blobs: blobs, bin: "ffprobe"}
}

// DetectFFProbe returns an FFProbe backed by blobs when the ffprobe binary is on
// PATH, else (nil, false) so callers can fall back to publishing unprobed
// instead of failing every upload on a host without ffmpeg installed.
func DetectFFProbe(blobs storage.Backend) (*FFProbe, bool) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return nil, false
	}
	return NewFFProbe(blobs), true
}

// Probe runs ffprobe against the object at key and returns its metadata.
func (f *FFProbe) Probe(ctx context.Context, key string) (Metadata, error) {
	path, cleanup, err := objectPath(ctx, f.blobs, key)
	if err != nil {
		return Metadata{}, err
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, f.bin,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return Metadata{}, fmt.Errorf("media: ffprobe %q: %w", key, err)
	}
	return parseFFProbe(out)
}

// objectPath returns a filesystem path an external tool can read for key. When
// the backend exposes paths directly (local) it is used in place; otherwise the
// object is streamed to a temp file. The returned cleanup removes any temp file.
func objectPath(ctx context.Context, blobs storage.Backend, key string) (string, func(), error) {
	if pp, ok := blobs.(storage.PathProvider); ok {
		p, err := pp.Path(key)
		if err != nil {
			return "", func() {}, err
		}
		return p, func() {}, nil
	}

	rc, err := blobs.Open(ctx, key)
	if err != nil {
		return "", func() {}, err
	}
	defer func() { _ = rc.Close() }()
	tmp, err := os.CreateTemp("", "vidra-media-*")
	if err != nil {
		return "", func() {}, err
	}
	if _, err := io.Copy(tmp, rc); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", func() {}, err
	}
	_ = tmp.Close()
	return tmp.Name(), func() { _ = os.Remove(tmp.Name()) }, nil
}

// ffprobeOutput is the subset of `ffprobe -print_format json` we read.
type ffprobeOutput struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// parseFFProbe maps ffprobe JSON to Metadata. It is pure (no exec) so it can be
// unit-tested with canned fixtures. Missing/blank fields are left zero.
func parseFFProbe(b []byte) (Metadata, error) {
	var out ffprobeOutput
	if err := json.Unmarshal(b, &out); err != nil {
		return Metadata{}, fmt.Errorf("media: parse ffprobe output: %w", err)
	}
	var m Metadata
	if d, err := strconv.ParseFloat(strings.TrimSpace(out.Format.Duration), 64); err == nil && d > 0 {
		m.DurationSeconds = int(d + 0.5) // round to nearest second
	}
	for _, s := range out.Streams {
		if s.CodecType == "video" && s.Width > 0 && s.Height > 0 {
			m.Width = s.Width
			m.Height = s.Height
			break
		}
	}
	return m, nil
}
