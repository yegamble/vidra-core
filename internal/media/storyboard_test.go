package media

import (
	"regexp"
	"strconv"
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
	args := storyboardArgs(localSource("/in.mp4"), "/out.jpg", p)
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

// sbCue is one parsed storyboard VTT cue: its [start,end) time span (whole
// seconds) and its sprite-sheet region rectangle.
type sbCue struct{ start, end, x, y, w, h int }

var (
	sbTimingRe = regexp.MustCompile(`^(\d\d):(\d\d):(\d\d)\.\d\d\d --> (\d\d):(\d\d):(\d\d)\.\d\d\d$`)
	sbRegionRe = regexp.MustCompile(`#xywh=(\d+),(\d+),(\d+),(\d+)`)
)

func sbAtoi(s string) int { n, _ := strconv.Atoi(s); return n }

// parseStoryboardCues parses every cue out of a rendered storyboard VTT: each
// cue is a timing line ("HH:MM:SS.000 --> HH:MM:SS.000") immediately followed
// by a region line ("storyboard.jpg#xywh=x,y,w,h").
func parseStoryboardCues(t *testing.T, vtt string) []sbCue {
	t.Helper()
	lines := strings.Split(vtt, "\n")
	var cues []sbCue
	for i := 0; i < len(lines); i++ {
		m := sbTimingRe.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if m == nil {
			continue
		}
		if i+1 >= len(lines) {
			t.Fatalf("timing cue at EOF without a region line:\n%s", vtt)
		}
		rm := sbRegionRe.FindStringSubmatch(lines[i+1])
		if rm == nil {
			t.Fatalf("cue %q missing #xywh region line, got %q", lines[i], lines[i+1])
		}
		cues = append(cues, sbCue{
			start: sbAtoi(m[1])*3600 + sbAtoi(m[2])*60 + sbAtoi(m[3]),
			end:   sbAtoi(m[4])*3600 + sbAtoi(m[5])*60 + sbAtoi(m[6]),
			x:     sbAtoi(rm[1]), y: sbAtoi(rm[2]), w: sbAtoi(rm[3]), h: sbAtoi(rm[4]),
		})
	}
	return cues
}

// TestStoryboardVTTCoversRangeGapFreeWithinBounds pins the invariant the player's
// seek-bar hover preview depends on (W1.C0 checklist item 2): every second of
// [0, duration] maps to exactly one cue (gap-free, no overlap, coverage reaches
// the duration) and every cue's #xywh region lies inside the sprite sheet — a
// region past the sheet edge would sample blank/garbage pixels.
func TestStoryboardVTTCoversRangeGapFreeWithinBounds(t *testing.T) {
	for _, dur := range []int{8, 47, 100, 601, 1000} {
		plan, ok := PlanStoryboard(dur)
		if !ok {
			t.Fatalf("PlanStoryboard(%d) not ok", dur)
		}
		cues := parseStoryboardCues(t, RenderStoryboardVTT(plan))
		if len(cues) != plan.Tiles {
			t.Fatalf("dur=%d: parsed %d cues, want %d (one per tile)", dur, len(cues), plan.Tiles)
		}

		// Coverage of [0, duration]: first cue starts at 0, cues are contiguous
		// (each cue's end is the next cue's start — no gaps, no overlap), and the
		// last cue's end reaches or passes the duration.
		if cues[0].start != 0 {
			t.Errorf("dur=%d: first cue starts at %d, want 0", dur, cues[0].start)
		}
		for i := 1; i < len(cues); i++ {
			if cues[i].start != cues[i-1].end {
				t.Errorf("dur=%d: gap/overlap — cue %d ends at %d but cue %d starts at %d",
					dur, i-1, cues[i-1].end, i, cues[i].start)
			}
		}
		if last := cues[len(cues)-1].end; last < dur {
			t.Errorf("dur=%d: cues cover only [0,%d], do not reach the duration", dur, last)
		}

		// Bounds: every region fits inside the Cols×Rows sprite grid, and each
		// tile is exactly TileW×TileH.
		maxX, maxY := plan.Cols*plan.TileW, plan.Rows*plan.TileH
		for i, c := range cues {
			if c.x < 0 || c.y < 0 || c.x+c.w > maxX || c.y+c.h > maxY {
				t.Errorf("dur=%d cue %d region (x=%d,y=%d,w=%d,h=%d) escapes sprite bounds %dx%d",
					dur, i, c.x, c.y, c.w, c.h, maxX, maxY)
			}
			if c.w != plan.TileW || c.h != plan.TileH {
				t.Errorf("dur=%d cue %d tile size = %dx%d, want %dx%d", dur, i, c.w, c.h, plan.TileW, plan.TileH)
			}
		}
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
