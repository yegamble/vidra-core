package hlsmanifest

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestReferencesVisitEveryMasterAndMediaDependency(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []Reference
	}{
		{"master", "#EXTM3U\r\n#EXT-X-MEDIA:TYPE=AUDIO,NAME=\"Main, English\",URI=\"audio.m3u8\"\r\n#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=40,URI=\"iframe.m3u8\"\r\n#EXT-X-SESSION-KEY:METHOD=AES-128,URI=\"key.mp4\"\r\n#EXT-X-STREAM-INF:BANDWIDTH=100,CODECS=\"avc1,mp4a\"\r\n720p/playlist.m3u8\r\n",
			[]Reference{{URI: "audio.m3u8", Kind: PlaylistReference, Line: 2}, {URI: "iframe.m3u8", Kind: PlaylistReference, Line: 3}, {URI: "key.mp4", Kind: KeyReference, Line: 4}, {URI: "720p/playlist.m3u8", Kind: PlaylistReference, Line: 6}}},
		{"media", "#EXTM3U\n#EXT-X-MAP:URI=\"video.mp4\",BYTERANGE=\"12@0\"\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.mp4\"\n#EXTINF:4,\n#EXT-X-BYTERANGE:16@12\nvideo.mp4\n#EXT-X-KEY:METHOD=NONE\n#EXTINF:4,\nvideo.mp4\n#EXT-X-ENDLIST\n",
			[]Reference{{URI: "video.mp4", Kind: MapReference, Line: 2}, {URI: "key.mp4", Kind: KeyReference, Line: 3}, {URI: "video.mp4", Kind: MediaReference, Line: 6}, {URI: "video.mp4", Kind: MediaReference, Line: 9}}},
		{"muxed audio", "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\"\n", nil},
		{"metadata URI", "#EXTM3U\n#EXT-X-SESSION-DATA:DATA-ID=\"metadata\",URI=\"object.mp4\"\n",
			[]Reference{{URI: "object.mp4", Kind: AuxiliaryReference, Line: 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []Reference
			err := VisitReferences([]byte(tc.body), func(ref Reference) error { got = append(got, ref); return nil })
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("references = %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
}

func TestReferencesRejectInvalidOrIncompletePlaylists(t *testing.T) {
	for _, body := range []string{
		"", "not HLS", "#EXTM3U\n\x00", "#EXTM3U\n\xff",
		"#EXTM3U\n#EXT-X-MAP:BYTERANGE=\"12@0\"\n",
		"#EXTM3U\n#EXT-X-KEY:METHOD=AES-128\n", "#EXTM3U\n#EXT-X-KEY:URI=\"key.mp4\"\n",
		"#EXTM3U\n#EXT-X-SESSION-KEY:METHOD=NONE\n",
		"#EXTM3U\n#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subs\"\n",
		"#EXTM3U\n#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=40\n",
		"#EXTM3U\n#EXT-X-MAP:URI=init.mp4\n", "#EXTM3U\n#EXT-X-MAP:URI=\"a\",URI=\"b\"\n",
		"#EXTM3U\n#EXT-X-MAP:X-URI=\"other.mp4\"\n",
		"#EXTM3U\n#EXT-X-KEY:METHOD=NONE,IV=0x123\n",
		"#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=100\n#EXT-X-ENDLIST\n",
	} {
		if err := VisitReferences([]byte(body), func(Reference) error { return nil }); err == nil {
			t.Fatalf("invalid playlist accepted: %q", body)
		}
	}
	oversized := "#EXTM3U\n" + strings.Repeat("#\n", MaxPlaylistBytes/2)
	if err := VisitReferences([]byte(oversized), func(Reference) error { return nil }); err == nil {
		t.Fatal("oversized playlist accepted")
	}
}

func TestReferenceVisitorStopsBeforeRetainingMoreReferences(t *testing.T) {
	stop := errors.New("budget exhausted")
	calls := 0
	err := VisitReferences([]byte("#EXTM3U\nfirst.ts\nsecond.ts\n"), func(Reference) error { calls++; return stop })
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("visitor continued after refusal: calls=%d err=%v", calls, err)
	}
	if err := VisitReferences([]byte("#EXTM3U\nfirst.ts\n"), nil); err == nil {
		t.Fatal("nil visitor accepted")
	}
}
