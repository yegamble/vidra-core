//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestFillVideoDurationIfUnknownOnlyFillsAGap (G6): the transcode's measured
// duration is written when there is no metadata row or the probe stored NULL,
// and never over a stored duration — so a rerun is a no-op and a probed value
// stays authoritative. Width and height are not touched.
func TestFillVideoDurationIfUnknownOnlyFillsAGap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	_, channelID, cleanup := seedUserChannel(t, st)
	defer cleanup()
	newVideo := func(title string) sqlcgen.Video {
		v, err := q.CreateVideo(ctx, sqlcgen.CreateVideoParams{ChannelID: channelID, Title: title, Privacy: "public", CommentsPolicy: "enabled"})
		if err != nil {
			t.Fatalf("CreateVideo: %v", err)
		}
		return v
	}
	i32 := func(n int32) *int32 { return &n }
	fill := func(v sqlcgen.Video, seconds int32) int64 {
		n, err := q.FillVideoDurationIfUnknown(ctx, sqlcgen.FillVideoDurationIfUnknownParams{VideoID: v.ID, DurationSeconds: i32(seconds)})
		if err != nil {
			t.Fatalf("FillVideoDurationIfUnknown: %v", err)
		}
		return n
	}
	metadata := func(v sqlcgen.Video) sqlcgen.VideoMetadatum {
		m, err := q.GetVideoMetadata(ctx, v.ID)
		if err != nil {
			t.Fatalf("GetVideoMetadata: %v", err)
		}
		return m
	}

	// The probe read dimensions but no duration (a WebM written to a pipe).
	unknown := newVideo("no stated duration")
	if _, err := q.UpsertVideoMetadata(ctx, sqlcgen.UpsertVideoMetadataParams{VideoID: unknown.ID, Width: i32(320), Height: i32(240)}); err != nil {
		t.Fatalf("UpsertVideoMetadata: %v", err)
	}
	if n := fill(unknown, 4); n != 1 {
		t.Fatalf("fill over NULL affected %d rows, want 1", n)
	}
	if m := metadata(unknown); m.DurationSeconds == nil || *m.DurationSeconds != 4 || m.Width == nil || *m.Width != 320 || m.Height == nil || *m.Height != 240 {
		t.Errorf("after fill: %+v, want duration 4 and the probed 320x240 kept", m)
	}
	// A rerun measuring something slightly different changes nothing.
	if n := fill(unknown, 5); n != 0 {
		t.Errorf("second fill affected %d rows, want 0", n)
	}
	if m := metadata(unknown); *m.DurationSeconds != 4 {
		t.Errorf("rerun overwrote the duration with %d, want 4", *m.DurationSeconds)
	}

	// A probed duration is never overwritten.
	probed := newVideo("probed")
	if _, err := q.UpsertVideoMetadata(ctx, sqlcgen.UpsertVideoMetadataParams{VideoID: probed.ID, DurationSeconds: i32(7)}); err != nil {
		t.Fatalf("UpsertVideoMetadata: %v", err)
	}
	if n := fill(probed, 4); n != 0 {
		t.Errorf("fill over a probed duration affected %d rows, want 0", n)
	}
	if m := metadata(probed); *m.DurationSeconds != 7 {
		t.Errorf("probed duration = %d after fill, want 7", *m.DurationSeconds)
	}

	// No metadata row at all (no probe ran): the fill inserts one.
	bare := newVideo("never probed")
	if n := fill(bare, 4); n != 1 {
		t.Errorf("fill with no row affected %d rows, want 1", n)
	}
	if m := metadata(bare); m.DurationSeconds == nil || *m.DurationSeconds != 4 || m.Width != nil || m.Height != nil {
		t.Errorf("inserted row = %+v, want duration 4 and NULL dimensions", m)
	}
}
