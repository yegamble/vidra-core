package peertubeimport

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

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
