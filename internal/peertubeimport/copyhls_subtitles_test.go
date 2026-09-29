package peertubeimport

import (
	"context"
	"crypto/sha256"
	"io"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

func TestCopyHLSSeparatesPeerTubeSubtitlePlaylists(t *testing.T) {
	ctx := context.Background()
	src, _ := storage.NewLocal(t.TempDir())
	dst, _ := storage.NewLocal(t.TempDir())
	prefix := "streaming-playlists/hls/fixture"
	english := `#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English, descriptive",DEFAULT=NO,AUTOSELECT=YES,LANGUAGE="en",URI="fixture-en.m3u8"`
	french := `#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs-fr",NAME="Français",LANGUAGE="fr",URI="fixture-fr.m3u8"`
	master := strings.Join([]string{
		"#EXTM3U", "#EXT-X-VERSION:7", english,
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Main, URI=label",DEFAULT=YES,URI="audio.m3u8"`,
		`#EXT-X-MEDIA:TYPE=CLOSED-CAPTIONS,GROUP-ID="cc",NAME="CC",INSTREAM-ID="CC1"`,
		`#EXT-X-STREAM-INF:SUBTITLES="subs",BANDWIDTH=100000,CODECS="avc1.64001f,mp4a.40.2",AUDIO="audio",CLOSED-CAPTIONS="cc"`, "video.m3u8",
		`#EXT-X-STREAM-INF:BANDWIDTH=200000,SUBTITLES="subs-fr",AUDIO="audio"`, "video.m3u8",
		`#EXT-X-STREAM-INF:BANDWIDTH=300000,AUDIO="audio",SUBTITLES="subs"`, "video.m3u8", french,
		`#EXT-X-VIDRA-UNKNOWN:NAME="untouched, value"`, "",
	}, "\r\n")
	wantMaster := strings.ReplaceAll(strings.ReplaceAll(master, english+"\r\n", ""), french+"\r\n", "")
	wantMaster = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(wantMaster, `SUBTITLES="subs",`, ""), `,SUBTITLES="subs-fr"`, ""), `,SUBTITLES="subs"`, "")
	files := map[string]string{
		"master.m3u8": master,
		"video.m3u8":  "#EXTM3U\r\n#EXT-X-MAP:URI=\"video.mp4\",BYTERANGE=\"12@0\"\r\n#EXTINF:4,\r\n#EXT-X-BYTERANGE:16@12\r\nvideo.mp4\r\n#EXT-X-ENDLIST\r\n",
		"audio.m3u8":  "#EXTM3U\n#EXTINF:4,\naudio.mp4\n#EXT-X-ENDLIST\n",
		"video.mp4":   "\x00\x00\x00\x18ftypfixture-video-bytes\x00\xff",
		"audio.mp4":   "\x00\x00\x00\x18ftypfixture-audio-bytes\x00\xfe",
		// PeerTube 8.2.4 emits a local subtitle playlist whose VTT URI is
		// absolute. Neither that optional playlist nor its URI may be opened.
		"fixture-en.m3u8": "#EXTM3U\n#EXTINF:4,\nhttps://source.invalid/captions/fixture-en.vtt\n",
		"fixture-fr.m3u8": "#EXTM3U\n#EXTINF:4,\nhttps://source.invalid/captions/fixture-fr.vtt\n",
	}
	for name, body := range files {
		if _, err := src.Put(ctx, prefix+"/"+name, strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	observed := &singleReadSource{Backend: src, opened: map[string]int{}}
	im := &Importer{srcMedia: observed, destMedia: dst}
	if err := im.copyHLSTree(ctx, prefix, "master.m3u8"); err != nil {
		t.Fatal(err)
	}
	for name, original := range files {
		if strings.HasPrefix(name, "fixture-") {
			present, err := dst.Exists(ctx, prefix+"/"+name)
			if err != nil || present || observed.opened[prefix+"/"+name] != 0 {
				t.Fatal("removed subtitle playlist was read or copied")
			}
			continue
		}
		reader, err := dst.Open(ctx, prefix+"/"+name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(reader)
		_ = reader.Close()
		want := original
		if name == "master.m3u8" {
			want = wantMaster
		}
		if err != nil || sha256.Sum256(got) != sha256.Sum256([]byte(want)) {
			t.Fatalf("unexpected byte changes in %s: %s", name, got)
		}
	}
}

func TestCopyHLSRejectsMalformedSubtitleAttributesAndExternalMedia(t *testing.T) {
	for name, line := range map[string]string{
		"unterminated":       `#EXT-X-MEDIA:TYPE=SUBTITLES,URI="video.m3u8`,
		"duplicate type":     `#EXT-X-MEDIA:TYPE=SUBTITLES,TYPE=AUDIO,URI="video.m3u8"`,
		"missing type":       `#EXT-X-MEDIA:URI="video.m3u8"`,
		"trailing comma":     `#EXT-X-MEDIA:TYPE=SUBTITLES,URI="video.m3u8",`,
		"empty unquoted":     `#EXT-X-MEDIA:TYPE=SUBTITLES,NAME=,URI="video.m3u8"`,
		"unquoted uri":       `#EXT-X-MEDIA:TYPE=SUBTITLES,URI=video.m3u8`,
		"quote junk":         `#EXT-X-MEDIA:TYPE=SUBTITLES,NAME="valid"junk,URI="video.m3u8"`,
		"unquoted group":     `#EXT-X-STREAM-INF:BANDWIDTH=100000,SUBTITLES=subs`,
		"duplicate group":    `#EXT-X-STREAM-INF:BANDWIDTH=100000,SUBTITLES="subs",SUBTITLES="other"`,
		"empty stream":       `#EXT-X-STREAM-INF:SUBTITLES="subs"`,
		"external audio":     `#EXT-X-MEDIA:TYPE=AUDIO,URI="https://source.invalid/audio.m3u8"`,
		"external video":     `#EXT-X-MEDIA:TYPE=VIDEO,URI="https://source.invalid/video.m3u8"`,
		"external plain uri": "#EXT-X-STREAM-INF:BANDWIDTH=100000\nhttps://source.invalid/video.m3u8",
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			src, _ := storage.NewLocal(t.TempDir())
			dst, _ := storage.NewLocal(t.TempDir())
			prefix := "streaming-playlists/hls/fixture"
			for key, body := range map[string]string{"master.m3u8": "#EXTM3U\n" + line + "\nvideo.m3u8\n", "video.m3u8": "#EXTM3U\nclip.mp4\n", "clip.mp4": "video"} {
				if _, err := src.Put(ctx, prefix+"/"+key, strings.NewReader(body)); err != nil {
					t.Fatal(err)
				}
			}
			if err := (&Importer{srcMedia: src, destMedia: dst}).copyHLSTree(ctx, prefix, "master.m3u8"); err == nil {
				t.Fatal("malformed subtitle attributes or external media accepted")
			}
		})
	}
}
