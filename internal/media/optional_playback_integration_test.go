//go:build integration

package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"os"
	"os/exec"
	"path"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

func TestSegmentedPlaybackSurvivesOversizedProgressiveDownloads(t *testing.T) {
	requireEncoders(t, "libx264")
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const original = "processed.mp4"
	source, err := blobs.Path(original)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=256x144:rate=8",
		"-f", "lavfi", "-i", "sine=sample_rate=22050", "-t", "32",
		"-c:v", "libx264", "-threads", "1", "-crf", "45", "-c:a", "aac", "-b:a", "16k", source).CombinedOutput()
	if err != nil {
		t.Fatalf("generate compressed source: %v: %s", err, out)
	}
	originalBytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	// The reduced ceiling keeps this test small. The source fits, each normal
	// six-second segment fits, and the complete progressive copy exceeds it.
	limit := max(int64(768<<10), int64(len(originalBytes))+4096)
	for _, spec := range []struct {
		name   string
		format string
		stream bool
	}{{"CMAF disk", PackagerCMAF, false}, {"CMAF stream", PackagerCMAF, true}, {"TS disk", PackagerTS, false}, {"TS stream", PackagerTS, true}} {
		t.Run(spec.name, func(t *testing.T) {
			store := &auditedPlaybackStore{Local: blobs, accepted: make(map[[32]byte]int)}
			tc := NewHLSTranscoder(store)
			tc.SetOutputScanner(store)
			tc.scannedOutputs.maxBytes = limit
			tc.SetStreamOutput(spec.stream)
			if err := tc.SetPackager(spec.format); err != nil {
				t.Fatal(err)
			}
			md, err := tc.Probe(ctx, original)
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.New()
			hls, web, err := tc.TranscodeAll(ctx, id, original, 1, md, nil)
			if err != nil || hls.MasterKey == "" || len(hls.Renditions) != 1 || len(web) != 0 {
				t.Fatalf("HLS %q, renditions %d, progressive %d, error %v", hls.MasterKey, len(hls.Renditions), len(web), err)
			}
			rung := hls.Renditions[0]
			for _, key := range []string{HLSDownloadKey(rung.KeyPrefix, true), path.Join(WebVideoPrefixForGeneration(id, 1), path.Base(rung.KeyPrefix)+".mp4")} {
				if exists, err := blobs.Exists(ctx, key); err != nil || exists {
					t.Fatalf("oversized progressive output %s exists = %t, error %v", key, exists, err)
				}
			}
			master, err := blobs.Path(hls.MasterKey)
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(master)
			if err != nil || bytes.Contains(body, []byte("I-FRAME-STREAM-INF")) {
				t.Fatalf("master advertises omitted trick-play, error %v", err)
			}
			keys, err := blobs.ListKeys(ctx, HLSPrefixForGeneration(id, 1))
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range keys {
				local, err := blobs.Path(key)
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(local)
				if err != nil || info.Size() > limit {
					t.Fatalf("size check failed for %s at limit %d: %v", key, limit, err)
				}
			}
			if b, err := exec.Command("ffmpeg", "-v", "error", "-xerror", "-i", master, "-map", "0:v:0", "-map", "0:a:0", "-f", "null", "-").CombinedOutput(); err != nil {
				t.Fatalf("decode complete HLS: %v: %s", err, b)
			}
			rc, err := blobs.Open(ctx, original)
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			h := sha256.New()
			n, err := io.Copy(h, rc)
			digest := sha256.Sum256(originalBytes)
			if err != nil || n >= limit || !bytes.Equal(h.Sum(nil), digest[:]) {
				t.Fatalf("processed download changed: bytes %d, error %v", n, err)
			}
			t.Logf("source %d bytes, scan ceiling %d, %d HLS objects, decoded %d seconds", n, limit, len(keys), md.DurationSeconds)
		})
	}
}

func TestOversizedProgressiveStopsOnScratch(t *testing.T) {
	requireEncoders(t, "libx264")
	dir := t.TempDir()
	source := path.Join(dir, "source.mp4")
	if b, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24",
		"-t", "24", "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", "-crf", "20", source).CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v %s", err, b)
	}
	input, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	const limit = int64(64 << 10)
	out := path.Join(dir, "truncated.mp4")
	args := limitSingleFile([]string{"-v", "error", "-i", source, "-c", "copy", "-movflags", "+faststart", out}, limit)
	if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("capped remux: %v %s", err, b)
	}
	file, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if file.Size() <= limit || file.Size() >= input.Size()/2 {
		t.Fatalf("capped remux %d bytes, expected larger than policy %d and substantially below full %d", file.Size(), limit, input.Size())
	}
}
