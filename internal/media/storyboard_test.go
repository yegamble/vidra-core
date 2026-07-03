package media

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPlanStoryboard(t *testing.T) {
	// A short clip: one tile per second, single row.
	p, ok := PlanStoryboard(8)
	if !ok {
		t.Fatalf("PlanStoryboard(8) not ok")
	}
	if p.IntervalSeconds != 1 || p.Tiles != 8 {
		t.Fatalf("8s: got interval=%d tiles=%d, want 1/8", p.IntervalSeconds, p.Tiles)
	}
	if p.Cols != 8 || p.Rows != 1 {
		t.Fatalf("8s grid: got %dx%d, want 8x1", p.Cols, p.Rows)
	}

	// A long clip is capped at storyboardMaxTiles with a coarser interval.
	p, ok = PlanStoryboard(1000)
	if !ok {
		t.Fatalf("PlanStoryboard(1000) not ok")
	}
	if p.Tiles > storyboardMaxTiles {
		t.Fatalf("1000s: tiles=%d exceeds cap %d", p.Tiles, storyboardMaxTiles)
	}
	if p.IntervalSeconds != 10 || p.Tiles != 100 {
		t.Fatalf("1000s: got interval=%d tiles=%d, want 10/100", p.IntervalSeconds, p.Tiles)
	}
	if p.Cols != storyboardCols || p.Rows != 10 {
		t.Fatalf("1000s grid: got %dx%d, want %dx10", p.Cols, p.Rows, storyboardCols)
	}

	// Unmeasurable duration returns ok=false (single fallback tile).
	if _, ok := PlanStoryboard(0); ok {
		t.Fatalf("PlanStoryboard(0) should not be ok")
	}
	if _, ok := PlanStoryboard(-5); ok {
		t.Fatalf("PlanStoryboard(-5) should not be ok")
	}
}

func TestStoryboardArgs(t *testing.T) {
	p, _ := PlanStoryboard(20)
	args := storyboardArgs("/in.mp4", "/out.jpg", p)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-i /in.mp4") {
		t.Errorf("args missing input: %q", joined)
	}
	if args[len(args)-1] != "/out.jpg" {
		t.Errorf("args should end with output path, got %q", args[len(args)-1])
	}
	if !strings.Contains(joined, "fps=1/") || !strings.Contains(joined, "tile=") {
		t.Errorf("args missing fps/tile filter: %q", joined)
	}
	if !strings.Contains(joined, "160:90") {
		t.Errorf("args missing tile scale 160:90: %q", joined)
	}
}

func TestRenderStoryboardVTT(t *testing.T) {
	// 3 tiles at 5s each, 10-col grid: all on row 0, cols 0..2.
	p := StoryboardPlan{Tiles: 3, IntervalSeconds: 5, Cols: 10, Rows: 1, TileW: 160, TileH: 90}
	vtt := RenderStoryboardVTT(p)
	if !strings.HasPrefix(vtt, "WEBVTT") {
		t.Fatalf("VTT must start with WEBVTT header")
	}
	// First cue: 00:00:00.000 --> 00:00:05.000, region x=0.
	if !strings.Contains(vtt, "00:00:00.000 --> 00:00:05.000") {
		t.Errorf("missing first cue timing:\n%s", vtt)
	}
	if !strings.Contains(vtt, "storyboard.jpg#xywh=0,0,160,90") {
		t.Errorf("missing first cue region:\n%s", vtt)
	}
	// Third cue region is at col 2 → x = 320.
	if !strings.Contains(vtt, "00:00:10.000 --> 00:00:15.000") {
		t.Errorf("missing third cue timing:\n%s", vtt)
	}
	if !strings.Contains(vtt, "storyboard.jpg#xywh=320,0,160,90") {
		t.Errorf("missing third cue region:\n%s", vtt)
	}
	// The sprite is referenced relatively (no leading slash or /api path).
	if strings.Contains(vtt, "/api/") || strings.Contains(vtt, "/storyboard.jpg") {
		t.Errorf("sprite reference should be relative:\n%s", vtt)
	}
}

func TestStoryboardVTTRowWrap(t *testing.T) {
	// 12 tiles, 10 cols → tile 10 wraps to row 1 (y = 90), col 0 (x = 0).
	p := StoryboardPlan{Tiles: 12, IntervalSeconds: 1, Cols: 10, Rows: 2, TileW: 160, TileH: 90}
	vtt := RenderStoryboardVTT(p)
	if !strings.Contains(vtt, "00:00:10.000 --> 00:00:11.000\nstoryboard.jpg#xywh=0,90,160,90") {
		t.Errorf("tile 10 should wrap to row 1 col 0 (x=0,y=90):\n%s", vtt)
	}
}

func TestStoryboardKeys(t *testing.T) {
	id := uuid.New()
	if got, want := StoryboardKeyJPG(id), "storyboards/"+id.String()+".jpg"; got != want {
		t.Errorf("StoryboardKeyJPG = %q, want %q", got, want)
	}
	if got, want := StoryboardKeyVTT(id), "storyboards/"+id.String()+".vtt"; got != want {
		t.Errorf("StoryboardKeyVTT = %q, want %q", got, want)
	}
}
