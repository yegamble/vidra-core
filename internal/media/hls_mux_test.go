package media

import (
	"reflect"
	"strings"
	"testing"
)

func TestHLSMuxArgs(t *testing.T) {
	got := hlsMuxArgs(localSource("/m/v.mp4"), source{arg: "https://s3.test/a.mp4?sig=x", opts: []string{"-seekable", "1"}, remote: true})
	want := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", "file,http,https,tcp,tls", "-i", "/m/v.mp4",
		"-protocol_whitelist", "file,http,https,tcp,tls", "-seekable", "1", "-i", "https://s3.test/a.mp4?sig=x",
		"-map", "0:v:0", "-map", "1:a:0", "-c", "copy",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4", "pipe:1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv =\n%v\nwant\n%v", got, want)
	}
	for _, a := range got {
		if a == "-y" || strings.Contains(a, "libx") {
			t.Errorf("argv must stream-copy and never write files: %q", a)
		}
	}
}
