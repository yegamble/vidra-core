package media

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// Storyboard sprite geometry: a grid of small poster tiles the player shows as a
// seek-bar hover preview. Tiles are 160x90 (16:9 scaled with padding), packed
// storyboardCols per row, at most storyboardMaxTiles total.
const (
	storyboardTileW      = 160
	storyboardTileH      = 90
	storyboardCols       = 10
	storyboardMaxTiles   = 100
	storyboardSpriteName = "storyboard.jpg"
)

// StoryboardPlan is the pure layout for a storyboard sprite sheet: how many
// tiles, the seconds each tile spans, and the grid geometry. It is computed from
// the source duration alone so it can be unit-tested without ffmpeg.
type StoryboardPlan struct {
	Tiles           int // number of poster tiles (>=1)
	IntervalSeconds int // seconds of video each tile represents (>=1)
	Cols            int
	Rows            int
	TileW           int
	TileH           int
}

// PlanStoryboard lays out a sprite sheet for a source of the given duration.
// It targets at most storyboardMaxTiles tiles, one every IntervalSeconds; a
// non-positive duration yields a single-tile plan (ok=false so callers can skip
// generation for unmeasurable media). Interval is whole seconds so the ffmpeg
// fps filter is exact.
func PlanStoryboard(durationSeconds int) (StoryboardPlan, bool) {
	if durationSeconds <= 0 {
		return StoryboardPlan{Tiles: 1, IntervalSeconds: 1, Cols: 1, Rows: 1, TileW: storyboardTileW, TileH: storyboardTileH}, false
	}
	// One tile per second, coarsening the interval so the count never exceeds the
	// cap: interval = ceil(duration / maxTiles).
	interval := (durationSeconds + storyboardMaxTiles - 1) / storyboardMaxTiles
	if interval < 1 {
		interval = 1
	}
	tiles := (durationSeconds + interval - 1) / interval
	if tiles < 1 {
		tiles = 1
	}
	if tiles > storyboardMaxTiles {
		tiles = storyboardMaxTiles
	}
	cols := storyboardCols
	if tiles < cols {
		cols = tiles
	}
	rows := (tiles + cols - 1) / cols
	return StoryboardPlan{
		Tiles:           tiles,
		IntervalSeconds: interval,
		Cols:            cols,
		Rows:            rows,
		TileW:           storyboardTileW,
		TileH:           storyboardTileH,
	}, true
}

// storyboardArgs builds the ffmpeg argument vector that renders the sprite sheet
// for src into dst: sample one frame every IntervalSeconds, scale each into a
// TileW x TileH box (aspect-preserving with padding), and tile them ColsxRows
// into a single JPEG. Pure (no exec) so it is unit-testable.
func storyboardArgs(src, dst string, p StoryboardPlan) []string {
	vf := fmt.Sprintf(
		"fps=1/%d,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,tile=%dx%d",
		p.IntervalSeconds, p.TileW, p.TileH, p.TileW, p.TileH, p.Cols, p.Rows,
	)
	return []string{
		"-y",
		"-i", src,
		"-frames:v", "1",
		"-vf", vf,
		"-q:v", "4",
		dst,
	}
}

// RenderStoryboardVTT renders the WebVTT sprite map for a plan: one cue per tile,
// each spanning IntervalSeconds and pointing at the tile's region in the sprite
// via a #xywh media fragment. The sprite is referenced by a RELATIVE filename so
// the VTT resolves it against its own served URL (…/storyboard.vtt →
// …/storyboard.jpg). Pure (no IO) so it is unit-testable.
func RenderStoryboardVTT(p StoryboardPlan) string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for i := 0; i < p.Tiles; i++ {
		start := i * p.IntervalSeconds
		end := (i + 1) * p.IntervalSeconds
		col := i % p.Cols
		row := i / p.Cols
		x := col * p.TileW
		y := row * p.TileH
		fmt.Fprintf(&b, "%s --> %s\n", vttTimestamp(start), vttTimestamp(end))
		fmt.Fprintf(&b, "%s#xywh=%d,%d,%d,%d\n\n", storyboardSpriteName, x, y, p.TileW, p.TileH)
	}
	return b.String()
}

// vttTimestamp formats whole seconds as a WebVTT HH:MM:SS.000 timestamp.
func vttTimestamp(sec int) string {
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	return fmt.Sprintf("%02d:%02d:%02d.000", h, m, s)
}

// StoryboardKeyJPG / StoryboardKeyVTT are the storage keys for a video's sprite
// sheet and its WebVTT map (PeerTube-aligned one-dir-per-kind layout, see
// .ralph/specs/storage-layout.md).
func StoryboardKeyJPG(videoID uuid.UUID) string { return "storyboards/" + videoID.String() + ".jpg" }
func StoryboardKeyVTT(videoID uuid.UUID) string { return "storyboards/" + videoID.String() + ".vtt" }

// Storyboarder renders a seek-preview sprite sheet + WebVTT map for a stored
// original by shelling out to ffmpeg. It satisfies video.Storyboarder. The
// plan/args/VTT builders above are pure and unit-tested; the exec path is
// covered by a -tags=integration test.
type Storyboarder struct {
	blobs storage.Backend
	probe *FFProbe
	bin   string
}

// NewStoryboarder builds a Storyboarder reading objects from blobs via the
// "ffmpeg" (and, for duration, "ffprobe") binaries on PATH.
func NewStoryboarder(blobs storage.Backend) *Storyboarder {
	return &Storyboarder{blobs: blobs, probe: NewFFProbe(blobs), bin: "ffmpeg"}
}

// DetectStoryboarder returns a Storyboarder when both ffmpeg and ffprobe are on
// PATH, else (nil, false) so callers can publish without a storyboard.
func DetectStoryboarder(blobs storage.Backend) (*Storyboarder, bool) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, false
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return nil, false
	}
	return NewStoryboarder(blobs), true
}

// Storyboard produces the sprite-sheet JPEG bytes and the WebVTT sprite-map
// bytes for the media at key. durationSeconds (0 if unknown) hints the layout;
// when unknown the source is probed. Returns an error when it cannot render a
// usable sheet (empty output, ffmpeg failure).
func (t *Storyboarder) Storyboard(ctx context.Context, key string, durationSeconds int) (sprite, vtt []byte, err error) {
	if durationSeconds <= 0 {
		if md, perr := t.probe.Probe(ctx, key); perr == nil {
			durationSeconds = md.DurationSeconds
		}
	}
	plan, ok := PlanStoryboard(durationSeconds)
	if !ok {
		return nil, nil, fmt.Errorf("media: storyboard: source %q has no measurable duration", key)
	}

	src, cleanup, err := objectPath(ctx, t.blobs, key)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()

	out, err := os.CreateTemp("", "vidra-storyboard-*.jpg")
	if err != nil {
		return nil, nil, err
	}
	outPath := out.Name()
	_ = out.Close()
	defer func() { _ = os.Remove(outPath) }()

	cmd := exec.CommandContext(ctx, t.bin, storyboardArgs(src, outPath, plan)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("media: ffmpeg storyboard %q: %w: %s", key, err, tailOf(stderr.String()))
	}
	sprite, err = os.ReadFile(outPath)
	if err != nil {
		return nil, nil, err
	}
	if len(sprite) == 0 {
		return nil, nil, fmt.Errorf("media: ffmpeg produced an empty storyboard for %q", key)
	}
	return sprite, []byte(RenderStoryboardVTT(plan)), nil
}
