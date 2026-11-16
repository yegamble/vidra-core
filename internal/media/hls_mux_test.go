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
		"-protocol_whitelist", "file", "-enable_drefs", "0", "-f", "mov", "-i", "/m/v.mp4",
		"-protocol_whitelist", "https,tls,tcp", "-seekable", "1", "-enable_drefs", "0", "-f", "mov", "-i", "https://s3.test/a.mp4?sig=x",
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

// Every -i is directly preceded by "-f mov" (the format is never probed), and
// carries its own allowlist matching the input kind.
func TestHLSMuxArgsInputHardening(t *testing.T) {
	for name, tc := range map[string]struct {
		src       source
		whitelist string
	}{
		"local path": {localSource("/m/v.mp4"), "file"},
		"https":      {source{arg: "https://s3.test/v.mp4?sig=x", remote: true}, "https,tls,tcp"},
		"http":       {source{arg: "http://127.0.0.1:9/v.mp4", remote: true}, "http,tcp"},
	} {
		t.Run(name, func(t *testing.T) {
			args := hlsMuxArgs(tc.src, tc.src)
			inputs := 0
			for i, a := range args {
				if a != "-i" {
					continue
				}
				inputs++
				if i < 2 || args[i-2] != "-f" || args[i-1] != "mov" {
					t.Errorf("-i at %d is not preceded by -f mov: %v", i, args)
				}
			}
			if inputs != 2 {
				t.Fatalf("inputs = %d", inputs)
			}
			count := 0
			for i, a := range args {
				if a == "-protocol_whitelist" {
					count++
					if args[i+1] != tc.whitelist {
						t.Errorf("whitelist = %q, want %q", args[i+1], tc.whitelist)
					}
				}
			}
			if count != 2 {
				t.Errorf("whitelists = %d, want one per input", count)
			}
		})
	}
}

func TestHLSMuxEnvIsMinimal(t *testing.T) {
	t.Setenv("JWT_SECRET", "must-not-leak")
	t.Setenv("DATABASE_URL", "postgres://u:p@h/db")
	t.Setenv("PATH", "/usr/bin")
	for _, kv := range hlsMuxEnv() {
		if strings.HasPrefix(kv, "JWT_SECRET") || strings.HasPrefix(kv, "DATABASE_URL") {
			t.Errorf("env leaks %q", kv)
		}
	}
	if got := hlsMuxEnv(); len(got) == 0 || got[0] != "PATH=/usr/bin" {
		t.Errorf("env = %v", got)
	}
}

func TestHLSMuxPresignTTLIsShort(t *testing.T) {
	pb := &presignBackend{url: "https://s3.test/x?sig=1"}
	if _, _, err := openSourceTTL(t.Context(), pb, "k.mp4", hlsMuxPresignTTL); err != nil {
		t.Fatal(err)
	}
	if pb.ttl != hlsMuxPresignTTL || pb.ttl >= remoteSourceTTL {
		t.Errorf("ttl = %v, want %v (< %v)", pb.ttl, hlsMuxPresignTTL, remoteSourceTTL)
	}
}
