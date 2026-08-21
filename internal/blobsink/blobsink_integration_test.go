//go:build integration

// Excluded from `make ci`; run with: go test -tags integration ./internal/blobsink/
package blobsink

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// TestFFmpegWritesHLSThroughSink is the proof the whole component exists for:
// real ffmpeg, pushing a real HLS ladder over -method PUT, lands every segment
// and playlist in the backend without a single output file touching local disk.
//
// If this passes, "ffmpeg cannot write to S3" is solved for MPEG-TS output — the
// sink signs on ffmpeg's behalf.
func TestFFmpegWritesHLSThroughSink(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	b := newMemBackend()
	s, err := New(b, "streaming-playlists/vid1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = s.Close() }()

	// -hls_flags -temp_file: the muxer's write-then-rename dance is meaningless
	// over HTTP and makes it PUT to a .tmp name the sink would store verbatim.
	cmd := exec.Command("ffmpeg",
		"-y",
		"-f", "lavfi", "-i", "testsrc=duration=8:size=320x240:rate=24",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		// Segments can only start on IDR frames; without forcing them x264's
		// default GOP would yield a single segment and prove nothing.
		"-force_key_frames", "expr:gte(t,n_forced*2)",
		"-f", "hls",
		"-hls_time", "2",
		"-hls_playlist_type", "vod",
		"-hls_list_size", "0",
		"-hls_flags", "-temp_file",
		"-method", "PUT",
		"-http_persistent", "1",
		"-hls_segment_filename", s.URL("240p/seg_%05d.ts"),
		s.URL("240p/playlist.m3u8"),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg through sink: %v\n%s", err, out)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("sink recorded a storage failure: %v", err)
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	keys := b.keys()
	var segments int
	for _, k := range keys {
		if !strings.HasPrefix(k, "streaming-playlists/vid1/240p/") {
			t.Errorf("stored %q outside the sink prefix", k)
		}
		if strings.HasSuffix(k, ".ts") {
			segments++
			if len(b.object(k)) == 0 {
				t.Errorf("segment %q is empty", k)
			}
		}
	}
	// 8 seconds at 2s segments.
	if segments < 3 {
		t.Errorf("stored %d segments, want at least 3: %v", segments, keys)
	}

	playlist := string(b.object("streaming-playlists/vid1/240p/playlist.m3u8"))
	if !strings.HasPrefix(playlist, "#EXTM3U") {
		t.Fatalf("playlist not stored or malformed: %q", playlist)
	}
	if !strings.Contains(playlist, "#EXT-X-ENDLIST") {
		t.Error("playlist has no ENDLIST; the coalesced copy is not the final rewrite")
	}
	// Segment URIs must stay relative so the stored tree serves from any base.
	if strings.Contains(playlist, "http://") {
		t.Errorf("playlist embeds absolute loopback URIs, which would be stored and served: %q", playlist)
	}
	if n := b.putCount("streaming-playlists/vid1/240p/playlist.m3u8"); n != 1 {
		t.Errorf("playlist written %d times, want 1 despite one rewrite per segment", n)
	}

	// And the pipeline can read its own output back through the sink, which is
	// what the progressive-download remux needs.
	rc, err := s.backend.Open(context.Background(), "streaming-playlists/vid1/240p/seg_00000.ts")
	if err != nil {
		t.Fatalf("Open first segment: %v", err)
	}
	defer func() { _ = rc.Close() }()
	if n, _ := io.Copy(io.Discard, rc); n == 0 {
		t.Error("first segment reads back empty")
	}
}

// TestFFmpegRemuxesFromSinkURL proves the read-back path: ffmpeg can consume a
// playlist and its segments straight out of the sink over HTTP, which is how the
// progressive-download remux works when nothing is on local disk.
func TestFFmpegRemuxesFromSinkURL(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	b := newMemBackend()
	s, err := New(b, "streaming-playlists/vid2")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = s.Close() }()

	write := exec.Command("ffmpeg",
		"-y", "-f", "lavfi", "-i", "testsrc=duration=4:size=320x240:rate=24",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-f", "hls", "-hls_time", "2", "-hls_playlist_type", "vod",
		"-hls_list_size", "0", "-hls_flags", "-temp_file",
		"-method", "PUT", "-http_persistent", "1",
		"-hls_segment_filename", s.URL("240p/seg_%05d.ts"),
		s.URL("240p/playlist.m3u8"),
	)
	if out, err := write.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg write: %v\n%s", err, out)
	}

	// Deliberately BEFORE Flush: the remux runs mid-job, when the playlist is
	// still only in the sink's buffer.
	dst := t.TempDir() + "/video.mp4"
	remux := exec.Command("ffmpeg",
		"-y", "-i", s.URL("240p/playlist.m3u8"),
		"-map", "0:v:0", "-c", "copy", "-movflags", "+faststart", dst,
	)
	if out, err := remux.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg remux from sink: %v\n%s", err, out)
	}
	probe, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "stream=codec_name", "-of", "csv=p=0", dst).Output()
	if err != nil {
		t.Fatalf("ffprobe remuxed file: %v", err)
	}
	if !strings.Contains(string(probe), "h264") {
		t.Errorf("remuxed file streams = %q, want h264", probe)
	}
}
