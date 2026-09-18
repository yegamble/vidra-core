package media

import (
	"context"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

const storyboardTestMaster = "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,URI=\"audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=9000,RESOLUTION=1280x720\nhigh.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=426x240\nlow.m3u8\n"
const storyboardTestVariant = "#EXTM3U\n#EXT-X-MAP:URI=\"video.mp4\",BYTERANGE=\"100@0\"\n#EXTINF:2,\n#EXT-X-BYTERANGE:200@100\nvideo.mp4\n#EXT-X-ENDLIST\n"

func TestStoryboardHLSInput(t *testing.T) {
	for _, tc := range []struct {
		name, master, variant string
		fail                  bool
	}{
		{"lowest video, ignoring separate audio", storyboardTestMaster, storyboardTestVariant, false},
		{"external variant", strings.ReplaceAll(storyboardTestMaster, "low.m3u8", "https://example.test/low.m3u8"), storyboardTestVariant, true},
		{"traversal", storyboardTestMaster, strings.ReplaceAll(storyboardTestVariant, "video.mp4", "../video.mp4"), true},
		{"different init", storyboardTestMaster, strings.Replace(storyboardTestVariant, "video.mp4", "init.mp4", 1), true},
		{"segmented media", storyboardTestMaster, storyboardTestVariant + "other.mp4\n", true},
		{"live", storyboardTestMaster, strings.ReplaceAll(storyboardTestVariant, "#EXT-X-ENDLIST", ""), true},
		{"encrypted", storyboardTestMaster, storyboardTestVariant + "#EXT-X-KEY:METHOD=AES-128,URI=\"key\"\n", true},
		{"oversized", strings.Repeat("x", (1<<20)+1), storyboardTestVariant, true},
		{"oversized variant", storyboardTestMaster, strings.Repeat("x", (1<<20)+1), true},
		{"missing variant", strings.ReplaceAll(storyboardTestMaster, "low.m3u8", "gone.m3u8"), storyboardTestVariant, true},
		{"audio only", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=100\naudio.m3u8\n", storyboardTestVariant, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := storage.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for key, body := range map[string]string{"hls/master.m3u8": tc.master, "hls/low.m3u8": tc.variant} {
				if _, err := b.Put(context.Background(), key, strings.NewReader(body)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := storyboardInputKey(context.Background(), b, "hls/master.m3u8")
			if (err != nil) != tc.fail {
				t.Fatalf("key=%q err=%v", got, err)
			}
			if !tc.fail && got != "hls/video.mp4" {
				t.Fatalf("key=%q", got)
			}
		})
	}
}
