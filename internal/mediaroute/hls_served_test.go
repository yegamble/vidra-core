package mediaroute

import "testing"

func TestHLSServedRel(t *testing.T) {
	const native = "streaming-playlists/v/r1/master.m3u8"
	cases := []struct {
		rel, master string
		peertube    bool
		want        bool
	}{
		// What the HLS routes serve.
		{"master.m3u8", native, false, true},
		{"master.m3u8", "", false, true},
		{"720p/playlist.m3u8", native, false, true},
		{"720p/seg_00001.ts", native, false, true},
		{"720p/iframe.m3u8", native, false, true},
		{"720p/iframe.ts", native, false, true},
		{"cmaf/stream.mpd", native, false, true},
		{"cmaf/media_0.m3u8", native, false, true},
		{"cmaf/init-3.mp4", native, false, true},
		{"cmaf/chunk-0-00012.m4s", native, false, true},
		{"cmaf/iframe-1.m3u8", native, false, true},
		{"cmaf/iframe-1.mp4", native, false, true},
		// Same family, never served: the progressive downloads, the audio asset, the
		// VP9 alternate, and the muxer's scratch master.
		{"720p/video.mp4", native, false, false},
		{"720p/video-only.mp4", native, false, false},
		{"cmaf/video.mp4", native, false, false},
		{"audio.m4a", native, false, false},
		{"vp9.webm", native, false, false},
		{"cmaf/ffmpeg-master.m3u8", native, false, false},
		// Shape and traversal.
		{"playlist.m3u8", native, false, false},
		{"720p/sub/seg_00001.ts", native, false, false},
		{"../master.m3u8", native, false, false},
		{"/master.m3u8", native, false, false},
		{"", native, false, false},
		{"other.m3u8", native, false, false},
		// An imported PeerTube tree: flat, loose names, its own master name.
		{"source-master.m3u8", "streaming-playlists/hls/u/source-master.m3u8", true, true},
		{"u-720-fragmented.mp4", "streaming-playlists/hls/u/source-master.m3u8", true, true},
		{"u-720.m3u8", "streaming-playlists/hls/u/source-master.m3u8", true, true},
		{"nested/u-720.m3u8", "streaming-playlists/hls/u/source-master.m3u8", true, false},
		{"vp9.webm", "streaming-playlists/hls/u/source-master.m3u8", true, false},
		{"720p/playlist.m3u8", "streaming-playlists/hls/u/source-master.m3u8", true, false},
	}
	for _, c := range cases {
		if got := HLSServedRel(c.rel, c.master, c.peertube); got != c.want {
			t.Errorf("HLSServedRel(%q, %q, peertube=%v) = %v, want %v", c.rel, c.master, c.peertube, got, c.want)
		}
	}
}

func TestHLSChildShapeIsExclusive(t *testing.T) {
	for _, c := range [][2]string{{"720p", "playlist.m3u8"}, {"cmaf", "stream.mpd"}, {"peertube", "a.m3u8"}} {
		a, b, p := HLSChildShape(c[0], c[1])
		n := 0
		for _, v := range []bool{a, b, p} {
			if v {
				n++
			}
		}
		if n != 1 {
			t.Errorf("HLSChildShape(%q, %q) = %v %v %v, want exactly one", c[0], c[1], a, b, p)
		}
	}
}
