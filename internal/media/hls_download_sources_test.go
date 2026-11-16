package media

import (
	"context"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

func putHLS(t *testing.T, b storage.Backend, files map[string]string) {
	t.Helper()
	for key, body := range files {
		if _, err := b.Put(context.Background(), key, strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
}

func byteRangePlaylist(object string) string {
	return "#EXTM3U\n#EXT-X-MAP:URI=\"" + object + "\",BYTERANGE=\"100@0\"\n#EXTINF:2,\n#EXT-X-BYTERANGE:200@100\n" + object + "\n#EXTINF:2,\n#EXT-X-BYTERANGE:200@300\n" + object + "\n#EXT-X-ENDLIST\n"
}

const splitAudioMaster = "#EXTM3U\n" +
	"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"Audio\",DEFAULT=YES,URI=\"u-0.m3u8\"\n" +
	"#EXT-X-STREAM-INF:BANDWIDTH=2000,RESOLUTION=720x1280,CODECS=\"avc1.4d401f,mp4a.40.2\",AUDIO=\"audio\"\nu-1280.m3u8\n" +
	"#EXT-X-STREAM-INF:BANDWIDTH=900,RESOLUTION=240x426,AUDIO=\"audio\"\nu-426.m3u8\n"

func TestResolveHLSDownloadSources(t *testing.T) {
	ctx := context.Background()
	newBackend := func(t *testing.T) storage.Backend {
		b, err := storage.NewLocal(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	t.Run("split audio, portrait", func(t *testing.T) {
		b := newBackend(t)
		putHLS(t, b, map[string]string{
			"p/u-master.m3u8": splitAudioMaster,
			"p/u-0.m3u8":      byteRangePlaylist("u-0-fragmented.mp4"),
			"p/u-1280.m3u8":   byteRangePlaylist("u-1280-fragmented.mp4"),
			"p/u-426.m3u8":    byteRangePlaylist("u-426-fragmented.mp4"),
		})
		got, err := ResolveHLSDownloadSources(ctx, b, "p/u-master.m3u8")
		if err != nil {
			t.Fatal(err)
		}
		if got.AudioKey != "p/u-0-fragmented.mp4" || len(got.Rungs) != 2 {
			t.Fatalf("got %+v", got)
		}
		r := got.Rungs[0]
		if r.Width != 720 || r.Height != 1280 || r.VideoKey != "p/u-1280-fragmented.mp4" || r.AudioKey != "p/u-0-fragmented.mp4" {
			t.Errorf("rung0 = %+v", r)
		}
		if r := got.Rungs[1]; r.Width != 240 || r.Height != 426 || r.VideoKey != "p/u-426-fragmented.mp4" {
			t.Errorf("rung1 = %+v", r)
		}
	})

	t.Run("muxed rungs without an audio group, landscape", func(t *testing.T) {
		b := newBackend(t)
		putHLS(t, b, map[string]string{
			"p/m.m3u8":     "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=1280x720\nm-720.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=500,RESOLUTION=640x360\nm-360.m3u8\n",
			"p/m-720.m3u8": byteRangePlaylist("m-720.mp4"),
			"p/m-360.m3u8": byteRangePlaylist("m-360.mp4"),
		})
		got, err := ResolveHLSDownloadSources(ctx, b, "p/m.m3u8")
		if err != nil {
			t.Fatal(err)
		}
		if got.AudioKey != "" || len(got.Rungs) != 2 || got.Rungs[0].AudioKey != "" ||
			got.Rungs[0].Width != 1280 || got.Rungs[0].Height != 720 || got.Rungs[0].VideoKey != "p/m-720.mp4" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("audio group without URI means audio is inside the rung", func(t *testing.T) {
		b := newBackend(t)
		putHLS(t, b, map[string]string{
			"p/m.m3u8":     "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"a\",NAME=\"x\"\n#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=1280x720,AUDIO=\"a\"\nm-720.m3u8\n",
			"p/m-720.m3u8": byteRangePlaylist("m-720.mp4"),
		})
		got, err := ResolveHLSDownloadSources(ctx, b, "p/m.m3u8")
		if err != nil || len(got.Rungs) != 1 || got.Rungs[0].AudioKey != "" || got.AudioKey != "" {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("unsafe variants are skipped, the rest survive", func(t *testing.T) {
		b := newBackend(t)
		master := "#EXTM3U\n"
		files := map[string]string{}
		add := func(name, body string, res string) {
			master += "#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=" + res + "\n" + name + "\n"
			if body != "" {
				files["p/"+name] = body
			}
		}
		good := byteRangePlaylist("good.mp4")
		add("good.m3u8", good, "1280x720")
		add("two.m3u8", good+"", "1x1") // replaced below
		files["p/two.m3u8"] = strings.Replace(good, "#EXT-X-ENDLIST", "other.mp4\n#EXT-X-ENDLIST", 1)
		add("enc.m3u8", strings.Replace(good, "#EXTINF:2,", "#EXT-X-KEY:METHOD=AES-128,URI=\"k\"\n#EXTINF:2,", 1), "2x2")
		add("live.m3u8", strings.Replace(good, "#EXT-X-ENDLIST\n", "", 1), "3x3")
		add("disc.m3u8", strings.Replace(good, "#EXTINF:2,", "#EXT-X-DISCONTINUITY\n#EXTINF:2,", 1), "4x4")
		add("../up.m3u8", "", "5x5")
		add("https://example.test/x.m3u8", "", "6x6")
		add("/abs.m3u8", "", "7x7")
		add("trav.m3u8", byteRangePlaylist("../escape.mp4"), "8x8")
		add("url.m3u8", byteRangePlaylist("https://example.test/v.mp4"), "9x9")
		add("noarea.m3u8", good, "")
		files["p/master.m3u8"] = master
		putHLS(t, b, files)
		got, err := ResolveHLSDownloadSources(ctx, b, "p/master.m3u8")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Rungs) != 1 || got.Rungs[0].VideoKey != "p/good.mp4" {
			t.Fatalf("got %+v, want only the good rung", got)
		}
	})

	t.Run("variant that references a missing audio group is skipped", func(t *testing.T) {
		b := newBackend(t)
		putHLS(t, b, map[string]string{
			"p/m.m3u8":     "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=1280x720,AUDIO=\"nope\"\nm-720.m3u8\n",
			"p/m-720.m3u8": byteRangePlaylist("m-720.mp4"),
		})
		got, err := ResolveHLSDownloadSources(ctx, b, "p/m.m3u8")
		if err != nil || len(got.Rungs) != 0 {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("two audio objects in one group is ambiguous and skipped", func(t *testing.T) {
		b := newBackend(t)
		putHLS(t, b, map[string]string{
			"p/m.m3u8": "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"a\",URI=\"a1.m3u8\"\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"a\",URI=\"a2.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=1280x720,AUDIO=\"a\"\nm-720.m3u8\n",
		})
		putHLS(t, b, map[string]string{"p/m-720.m3u8": byteRangePlaylist("m-720.mp4"), "p/a1.m3u8": byteRangePlaylist("a1.mp4"), "p/a2.m3u8": byteRangePlaylist("a2.mp4")})
		got, err := ResolveHLSDownloadSources(ctx, b, "p/m.m3u8")
		if err != nil || len(got.Rungs) != 0 {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("oversized or invalid master is an error", func(t *testing.T) {
		b := newBackend(t)
		putHLS(t, b, map[string]string{"p/big.m3u8": strings.Repeat("x", (1<<20)+1), "p/bad.m3u8": "nope"})
		for _, k := range []string{"p/big.m3u8", "p/bad.m3u8", "p/missing.m3u8", "p/notaplaylist.mp4"} {
			if _, err := ResolveHLSDownloadSources(ctx, b, k); err == nil {
				t.Errorf("%s: want error", k)
			}
		}
	})

	t.Run("oversized variant is skipped", func(t *testing.T) {
		b := newBackend(t)
		putHLS(t, b, map[string]string{
			"p/m.m3u8":     "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=1280x720\nm-720.m3u8\n",
			"p/m-720.m3u8": strings.Repeat("x", (1<<20)+1),
		})
		got, err := ResolveHLSDownloadSources(ctx, b, "p/m.m3u8")
		if err != nil || len(got.Rungs) != 0 {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})
}
