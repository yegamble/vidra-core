package storage

import "testing"

func TestContentTypeForKey(t *testing.T) {
	cases := map[string]string{
		// The whole-file media a viewer can be redirected to.
		"web-videos/2b1c.mp4":                                       "video/mp4",
		"web-videos/2b1c.r3.mp4":                                    "video/mp4",
		"web-videos/2b1c/240p.mp4":                                  "video/mp4",
		"web-videos/2b1c/video-vp9.webm":                            "video/webm",
		"streaming-playlists/2b1c/audio.m4a":                        "audio/mp4",
		// The CMAF tree.
		"streaming-playlists/2b1c/cmaf/init-0.mp4":                  "video/mp4",
		"streaming-playlists/2b1c/cmaf/chunk-0-00001.m4s":           "video/mp4",
		"streaming-playlists/2b1c/240p/seg_00000.ts":                "video/mp2t",
		"streaming-playlists/2b1c/master.m3u8":                      "application/vnd.apple.mpegurl",
		"streaming-playlists/2b1c/cmaf/stream.mpd":                  "application/dash+xml",
		// Images and text.
		"thumbnails/2b1c.jpg":                                       "image/jpeg",
		"thumbnails/2b1c.JPEG":                                      "image/jpeg",
		"storyboards/2b1c.png":                                      "image/png",
		"avatars/users/2b1c.webp":                                   "image/webp",
		"storyboards/2b1c.vtt":                                      "text/vtt; charset=utf-8",
		"captions/2b1c/en.vtt":                                      "text/vtt; charset=utf-8",
		// Unknown, and the shapes that must NOT be guessed at.
		"uploads/2b1c/0":                                            "",
		".vidra/owner":                                              "",
		"web-videos/no-extension":                                   "",
		"web-videos/2b1c.exe":                                       "",
		"":                                                          "",
	}
	for key, want := range cases {
		if got := ContentTypeForKey(key); got != want {
			t.Errorf("ContentTypeForKey(%q) = %q, want %q", key, got, want)
		}
	}
}
