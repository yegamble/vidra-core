package peertubeimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

type singleReadSource struct {
	storage.Backend
	opened map[string]int
}

func (s *singleReadSource) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	s.opened[key]++
	if s.opened[key] > 1 {
		return nil, fmt.Errorf("source object reopened: %s", key)
	}
	return s.Backend.Open(ctx, key)
}

func TestCopyHLSTreeReadsEachObjectOnce(t *testing.T) {
	ctx := context.Background()
	src, _ := storage.NewLocal(t.TempDir())
	dst, _ := storage.NewLocal(t.TempDir())
	prefix := "streaming-playlists/hls/once"
	for name, body := range map[string]string{"master.m3u8": "#EXTM3U\nvideo.mp4\n", "video.mp4": "media bytes"} {
		if _, err := src.Put(ctx, prefix+"/"+name, strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	im := &Importer{srcMedia: &singleReadSource{Backend: src, opened: map[string]int{}}, destMedia: dst}
	if err := im.copyHLSTree(ctx, prefix, "master.m3u8"); err != nil {
		t.Fatal(err)
	}
}

type copyFunc func(context.Context, storage.Backend, string, string, int64) (int64, error)

func (f copyFunc) Copy(ctx context.Context, src storage.Backend, from, to string, limit int64) (int64, error) {
	return f(ctx, src, from, to, limit)
}

func TestCopyHLSAccelerationAndFallback(t *testing.T) {
	ctx := context.Background()
	for _, outcome := range []string{"copy", "unavailable", "changed", "private", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			src, _ := storage.NewLocal(t.TempDir())
			dst, _ := storage.NewLocal(t.TempDir())
			prefix := "streaming-playlists/hls/fast"
			for name, body := range map[string]string{"master.m3u8": "#EXTM3U\nvideo.mp4\n", "video.mp4": "media bytes"} {
				if _, err := src.Put(ctx, prefix+"/"+name, strings.NewReader(body)); err != nil {
					t.Fatal(err)
				}
			}
			readSource := &singleReadSource{Backend: src, opened: map[string]int{}}
			changed := errors.New("source precondition failed")
			calls := 0
			copier := copyFunc(func(ctx context.Context, _ storage.Backend, from, to string, limit int64) (int64, error) {
				calls++
				if strings.HasSuffix(from, ".m3u8") || limit != maxSourceFileBytes {
					t.Fatal("manifest accelerated or source cap lost")
				}
				switch outcome {
				case "unavailable":
					return 0, storage.ErrCopyUnavailable
				case "changed":
					return 0, changed
				case "cancelled":
					return 0, context.Canceled
				case "private":
					if !strings.Contains(from, "/private/") {
						return 0, storage.ErrNotFound
					}
				}
				return dst.Put(ctx, to, strings.NewReader("media bytes"))
			})
			im := &Importer{srcMedia: readSource, destMedia: dst, copyMediaServer: copier}
			err := im.copyHLSTree(ctx, prefix, "master.m3u8")
			if outcome == "changed" || outcome == "cancelled" {
				want := changed
				if outcome == "cancelled" {
					want = context.Canceled
				}
				if !errors.Is(err, want) || readSource.opened[prefix+"/video.mp4"] != 0 {
					t.Fatal("source change silently streamed instead of failing")
				}
				return
			}
			if err != nil || calls == 0 || (outcome == "private" && calls != 2) {
				t.Fatalf("copy: calls=%d err=%v", calls, err)
			}
			wantReads := 0
			if outcome == "unavailable" {
				wantReads = 1
			}
			if readSource.opened[prefix+"/video.mp4"] != wantReads {
				t.Fatal("accelerated payload read by host, or fallback did not stream")
			}
			r, err := dst.Open(ctx, prefix+"/video.mp4")
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			body, err := io.ReadAll(r)
			if err != nil || string(body) != "media bytes" {
				t.Fatal("destination bytes differ")
			}
		})
	}
}

func TestCopyHLSTreeRejectsIncompleteOrExternalMedia(t *testing.T) {
	for name, body := range map[string]string{
		"missing":   "#EXTM3U\nmissing.mp4\n",
		"external":  "#EXTM3U\nhttps://source.invalid/segment.mp4\n",
		"traversal": "#EXTM3U\n../segment.mp4\n",
		"unquoted":  "#EXTM3U\n#EXT-X-MAP:URI=missing.mp4\n",
		"oversize":  "#EXTM3U\n#" + strings.Repeat("x", 1<<20),
	} {
		t.Run(name, func(t *testing.T) {
			src, _ := storage.NewLocal(t.TempDir())
			dest, _ := storage.NewLocal(t.TempDir())
			_, err := src.Put(context.Background(), "streaming-playlists/hls/fixture/master.m3u8", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			im := &Importer{srcMedia: src, destMedia: dest}
			if err := im.copyHLSTree(context.Background(), "streaming-playlists/hls/fixture", "master.m3u8"); err == nil {
				t.Fatal("accepted incomplete or external HLS")
			}
		})
	}
}

func TestCopyHLSTreeRetriesPrivateSplitAudio(t *testing.T) {
	ctx := context.Background()
	src, _ := storage.NewLocal(t.TempDir())
	dest, _ := storage.NewLocal(t.TempDir())
	files := map[string]string{
		"master.m3u8": "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,URI=\"audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=100000\nvideo.m3u8\n",
		"audio.m3u8":  "#EXTM3U\n#EXTINF:4,\naudio.mp4\n#EXT-X-ENDLIST\n",
		"video.m3u8":  "#EXTM3U\n#EXT-X-MAP:URI=\"video.mp4\",BYTERANGE=\"12@0\"\n#EXTINF:4,\n#EXT-X-BYTERANGE:16@12\nvideo.mp4\n#EXT-X-ENDLIST\n",
		"video.mp4":   "synthetic video bytes",
	}
	for name, body := range files {
		if _, err := src.Put(ctx, "streaming-playlists/hls/private/fixture/"+name, strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	im := &Importer{srcMedia: src, destMedia: dest}
	if err := im.copyHLSTree(ctx, "streaming-playlists/hls/fixture", "master.m3u8"); err == nil {
		t.Fatal("missing audio accepted")
	}
	files["audio.mp4"] = "synthetic audio bytes"
	if _, err := src.Put(ctx, "streaming-playlists/hls/private/fixture/audio.mp4", strings.NewReader(files["audio.mp4"])); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := im.copyHLSTree(ctx, "streaming-playlists/hls/fixture", "master.m3u8"); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		reader, err := dest.Open(ctx, "streaming-playlists/hls/fixture/"+name)
		if err != nil {
			t.Fatal(err)
		}
		var got strings.Builder
		_, err = io.Copy(&got, reader)
		_ = reader.Close()
		if err != nil || got.String() != body {
			t.Fatalf("copied dependency %s differs", name)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := im.copyHLSTree(cancelled, "streaming-playlists/hls/fixture", "master.m3u8"); err == nil {
		t.Fatal("cancel ignored")
	}
}

// A source tree may span many objects; the server-copy shortcut must still
// apply the aggregate 64 GiB budget, including the bytes in its manifest.
func TestCopyHLSTreeServerCopyAggregateCap(t *testing.T) {
	ctx := context.Background()
	src, _ := storage.NewLocal(t.TempDir())
	dst, _ := storage.NewLocal(t.TempDir())
	prefix := "streaming-playlists/hls/budget"
	body := "#EXTM3U\na.mp4\nb.mp4\nc.mp4\nd.mp4\n"
	if _, err := src.Put(ctx, prefix+"/master.m3u8", strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	calls := 0
	capError := errors.New("source exceeds remaining tree budget")
	im := &Importer{srcMedia: src, destMedia: dst, copyMediaServer: copyFunc(func(_ context.Context, _ storage.Backend, _, _ string, limit int64) (int64, error) {
		calls++
		if calls <= 3 {
			if limit != 16<<30 {
				t.Fatalf("per-object budget = %d", limit)
			}
			return 16 << 30, nil
		}
		if limit != (16<<30)-int64(len(body)) {
			t.Fatalf("remaining tree budget = %d", limit)
		}
		return 0, capError
	})}
	if err := im.copyHLSTree(ctx, prefix, "master.m3u8"); !errors.Is(err, capError) || calls != 4 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}

func TestCopyHLSUnavailableDisablesRepeatedAttempts(t *testing.T) {
	ctx := context.Background()
	src, _ := storage.NewLocal(t.TempDir())
	dst, _ := storage.NewLocal(t.TempDir())
	prefix := "streaming-playlists/hls/unavailable"
	for name, body := range map[string]string{"master.m3u8": "#EXTM3U\na.mp4\nb.mp4\n", "a.mp4": "a", "b.mp4": "b"} {
		if _, err := src.Put(ctx, prefix+"/"+name, strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	im := &Importer{srcMedia: src, destMedia: dst, copyMediaServer: copyFunc(func(context.Context, storage.Backend, string, string, int64) (int64, error) {
		calls++
		return 0, storage.ErrCopyUnavailable
	})}
	if err := im.copyHLSTree(ctx, prefix, "master.m3u8"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("unavailable copier called %d times", calls)
	}
}
