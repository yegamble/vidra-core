//go:build integration

package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// Each successful whole-file scan permits one write of exactly those bytes.
// This also checks rewritten playlists and identical files stored at two keys.
type auditedPlaybackStore struct {
	*storage.Local
	mu       sync.Mutex
	accepted map[[32]byte]int
	reject   func([]byte) bool
}

func (s *auditedPlaybackStore) ScanReader(_ context.Context, body io.Reader) (bool, error) {
	b, err := io.ReadAll(body)
	if err != nil {
		return false, err
	}
	if s.reject != nil && s.reject(b) {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accepted[sha256.Sum256(b)]++
	return true, nil
}

func (s *auditedPlaybackStore) Put(ctx context.Context, key string, body io.Reader) (int64, error) {
	b, err := io.ReadAll(body)
	if err != nil {
		return 0, err
	}
	sum := sha256.Sum256(b)
	s.mu.Lock()
	count := s.accepted[sum]
	s.accepted[sum]--
	s.mu.Unlock()
	if count <= 0 {
		return 0, fmt.Errorf("write before whole-file scan: %s", key)
	}
	return s.Local.Put(ctx, key, bytes.NewReader(b))
}

func TestPlaybackScanCoversPackagedAndProgressiveOutputs(t *testing.T) {
	requireEncoders(t, "libx264", "libvpx-vp9", "libopus")
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const src = "source.mp4"
	source, err := blobs.Path(src)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=12",
		"-f", "lavfi", "-i", "sine=frequency=440", "-t", "1", "-c:v", "libx264", "-c:a", "aac", source).CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v: %s", err, b)
	}
	for _, modes := range []struct {
		name     string
		packager string
		stream   bool
		reject   func([]byte) bool
	}{
		{name: "CMAF on disk", packager: PackagerCMAF},
		{name: "CMAF streamed", packager: PackagerCMAF, stream: true},
		{name: "TS on disk", packager: PackagerTS},
		{name: "TS streamed", packager: PackagerTS, stream: true},
		{name: "rejected streamed TS segment", packager: PackagerTS, stream: true, reject: func(b []byte) bool {
			return len(b) >= 188 && b[0] == 0x47
		}},
		{name: "rejected optional WebM", packager: PackagerCMAF, reject: func(b []byte) bool {
			return bytes.HasPrefix(b, []byte{0x1a, 0x45, 0xdf, 0xa3})
		}},
	} {
		t.Run(modes.name, func(t *testing.T) {
			store := &auditedPlaybackStore{Local: blobs, accepted: make(map[[32]byte]int), reject: modes.reject}
			tc := NewHLSTranscoder(store)
			tc.SetOutputScanner(store)
			tc.SetVP9(true)
			tc.SetStreamOutput(modes.stream)
			if err := tc.SetPackager(modes.packager); err != nil {
				t.Fatal(err)
			}
			md, err := tc.Probe(ctx, src)
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.New()
			hls, web, err := tc.TranscodeAll(ctx, id, src, 1, md, nil)
			if modes.reject != nil {
				if !IsPermanent(err) || hls.MasterKey != "" || len(web) != 0 {
					t.Fatalf("rejected output returned master %q, %d web videos, error %v", hls.MasterKey, len(web), err)
				}
				return
			}
			if err != nil || hls.MasterKey == "" || hls.WebMKey == "" || len(web) != 1 {
				t.Fatalf("master %q, WebM %q, %d web videos, error %v", hls.MasterKey, hls.WebMKey, len(web), err)
			}
			if modes.packager == PackagerCMAF && !modes.stream {
				web, err := tc.TranscodeWebVideos(ctx, id, src, 2, md, nil)
				if err != nil || len(web) != 1 {
					t.Fatalf("standalone web-video scan: %v, %v", web, err)
				}
			}
		})
	}
}
