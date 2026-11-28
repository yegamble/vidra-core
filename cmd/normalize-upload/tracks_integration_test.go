//go:build integration

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNormalizeSelectedTracks(t *testing.T) {
	for _, tc := range []struct {
		name, extension string
		streams         []string
		wantVideo       bool
	}{
		{"extra audio", ".mp4", []string{"-f", "lavfi", "-i", "color=s=64x48:r=8:d=1", "-f", "lavfi", "-i", "sine=d=1", "-f", "lavfi", "-i", "sine=d=4", "-map", "0:v", "-map", "1:a", "-map", "2:a", "-c:v", "libx264", "-c:a", "aac"}, true},
		{"packet timestamps", ".mkv", []string{"-f", "lavfi", "-i", "testsrc2=s=64x48:r=8:d=2", "-f", "lavfi", "-i", "sine=d=2", "-c:v", "libx264", "-c:a", "aac"}, true},
		{"audio with cover art", ".mp3", []string{"-f", "lavfi", "-i", "sine=d=2", "-f", "lavfi", "-i", "color=s=32x32:r=1:d=1", "-map", "0:a", "-map", "1:v", "-c:a", "libmp3lame", "-c:v", "mjpeg", "-frames:v", "1", "-disposition:v", "attached_pic"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			in := filepath.Join(dir, "source"+tc.extension)
			out := filepath.Join(dir, "processed.mp4")
			args := append([]string{"-v", "error", "-nostdin"}, tc.streams...)
			args = append(args, "-threads", "1", "-metadata", "title=private-tag-for-test", in)
			if b, err := exec.CommandContext(t.Context(), "ffmpeg", args...).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %v %s", err, b)
			}
			if err := os.WriteFile(out, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := normalize(t.Context(), in, out); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(out)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			got, err := inspectTracks(t.Context(), f)
			if err != nil || !got.audio.present || got.video.present != tc.wantVideo || got.duration() > 3 {
				t.Fatalf("output tracks %+v, %v", got, err)
			}
			bytesOut, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error", "-show_entries", "format_tags:stream_tags", "-of", "json", out).Output()
			if err != nil || bytes.Contains(bytesOut, []byte("private-tag-for-test")) {
				t.Fatalf("output tags: %s, %v", bytesOut, err)
			}
			if b, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-xerror", "-i", out, "-f", "null", "-").CombinedOutput(); err != nil {
				t.Fatalf("decode: %v %s", err, b)
			}
		})
	}
}

func TestOutputMissingEndOfSelectedAudio(t *testing.T) {
	source := tracks{video: track{true, 0, 5}, audio: track{true, 0, 5}}
	if source.matches(tracks{video: source.video, audio: track{true, 0, 2}}) {
		t.Fatal("accepted missing audio")
	}
}
