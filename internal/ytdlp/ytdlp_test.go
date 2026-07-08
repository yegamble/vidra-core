package ytdlp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func baseCfg() Config {
	return Config{Path: "yt-dlp", Timeout: time.Minute, MaxHeight: 1080, MaxBytes: 100 << 20}
}

// TestMetadataArgsAreSandboxed proves the metadata argv is the fixed allowlist,
// carries the hardening flags, and puts the URL as the single positional after
// "--" (never a flag value, never shell-interpolated).
func TestMetadataArgsAreSandboxed(t *testing.T) {
	url := "https://example.com/watch?v=abc"
	args := metadataArgs(baseCfg(), url)

	mustContainInOrder(t, args, "--", url) // URL is last, after the option terminator
	if args[len(args)-1] != url {
		t.Fatalf("URL is not the final positional: %v", args)
	}
	mustHave(t, args, "--ignore-config", "--no-playlist", "--no-download", "-J")
	mustNotHave(t, args, "--exec", "--update", "-U", "--load-info-json", "--config-location")
	if countArg(args, "--") != 1 {
		t.Errorf("want exactly one option terminator, got %v", args)
	}
}

// TestDownloadArgsAreSandboxed proves the download argv shape + caps.
func TestDownloadArgsAreSandboxed(t *testing.T) {
	url := "https://example.com/v/xyz"
	args := downloadArgs(baseCfg(), url, "/tmp/job/media.%(ext)s")

	if args[len(args)-1] != url {
		t.Fatalf("URL is not the final positional: %v", args)
	}
	mustContainInOrder(t, args, "--", url)
	mustHave(t, args, "--ignore-config", "--no-playlist", "--restrict-filenames")
	mustHavePair(t, args, "--max-filesize", "104857600") // 100 MiB

	mustHavePair(t, args, "-o", "/tmp/job/media.%(ext)s")
	mustNotHave(t, args, "--exec", "--update", "-U")
	// The height cap must be reflected in the -f selector.
	if !strings.Contains(strings.Join(args, " "), "height<=1080") {
		t.Errorf("format selector missing the height cap: %v", args)
	}
}

// TestDownloadArgsNoMaxWhenZero: MaxBytes/MaxHeight of 0 omit their flags but
// the run is still sandboxed.
func TestDownloadArgsNoMaxWhenZero(t *testing.T) {
	cfg := Config{Path: "yt-dlp", Timeout: time.Minute}
	args := downloadArgs(cfg, "https://example.com/x", "/tmp/j/media.%(ext)s")
	mustNotHave(t, args, "--max-filesize")
	if strings.Contains(strings.Join(args, " "), "height<=") {
		t.Errorf("no height cap expected when MaxHeight=0: %v", args)
	}
	mustHave(t, args, "--ignore-config", "--no-playlist")
}

// TestProxyThreadedWhenSet: the operator egress proxy is passed as --proxy on
// both phases when configured, and omitted otherwise.
func TestProxyThreadedWhenSet(t *testing.T) {
	cfg := baseCfg()
	cfg.Proxy = "http://egress.internal:3128"
	mustHavePair(t, metadataArgs(cfg, "https://x/y"), "--proxy", "http://egress.internal:3128")
	mustHavePair(t, downloadArgs(cfg, "https://x/y", "/t/media.%(ext)s"), "--proxy", "http://egress.internal:3128")

	cfg.Proxy = ""
	mustNotHave(t, metadataArgs(cfg, "https://x/y"), "--proxy")
}

// TestMetadataParsesAndMapsErrors: a good -J document parses; a run failure maps
// to ErrRun without leaking output.
func TestMetadata(t *testing.T) {
	good := `{"title":"  Hello  ","description":"A clip","duration":42.9,"thumbnail":"https://img/x.jpg","_type":"video"}`
	c := &Client{cfg: baseCfg(), run: fakeRun([]byte(good), nil)}
	m, err := c.Metadata(context.Background(), "https://example.com/v")
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if m.Title != "Hello" || m.Description != "A clip" || m.DurationSeconds != 42 || m.Thumbnail != "https://img/x.jpg" {
		t.Fatalf("parsed meta = %+v", m)
	}

	c = &Client{cfg: baseCfg(), run: fakeRun(nil, errors.New("exit status 1"))}
	if _, err := c.Metadata(context.Background(), "https://example.com/v"); !errors.Is(err, ErrRun) {
		t.Fatalf("run failure err = %v, want ErrRun", err)
	}

	c = &Client{cfg: baseCfg(), run: fakeRun([]byte("not json"), nil)}
	if _, err := c.Metadata(context.Background(), "https://example.com/v"); !errors.Is(err, ErrRun) {
		t.Fatalf("bad-json err = %v, want ErrRun", err)
	}
}

// TestMetadataPassesArgvToRunner proves the client hands the exact sandboxed
// argv (URL last) to the exec seam.
func TestMetadataPassesArgvToRunner(t *testing.T) {
	var gotName string
	var gotArgs []string
	c := &Client{cfg: baseCfg(), run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		return []byte(`{"title":"t"}`), nil
	}}
	if _, err := c.Metadata(context.Background(), "https://example.com/v"); err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if gotName != "yt-dlp" {
		t.Errorf("exec name = %q, want yt-dlp", gotName)
	}
	if len(gotArgs) == 0 || gotArgs[len(gotArgs)-1] != "https://example.com/v" {
		t.Errorf("URL not passed as final positional: %v", gotArgs)
	}
}

// ---- helpers --------------------------------------------------------------

func fakeRun(out []byte, err error) runFunc {
	return func(_ context.Context, _ string, _ ...string) ([]byte, error) { return out, err }
}

// mustHave asserts each wanted arg appears somewhere in args (membership, not
// adjacency — flags may interleave).
func mustHave(t *testing.T, args []string, want ...string) {
	t.Helper()
	for _, w := range want {
		if countArg(args, w) == 0 {
			t.Errorf("args %v missing %q", args, w)
		}
	}
}

// mustHavePair asserts flag is immediately followed by value (so the value can
// never be mis-read as a positional/URL).
func mustHavePair(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			if args[i+1] == value {
				return
			}
			t.Errorf("args %v: %q followed by %q, want %q", args, flag, args[i+1], value)
			return
		}
	}
	t.Errorf("args %v missing flag %q", args, flag)
}

func countArg(args []string, target string) int {
	n := 0
	for _, a := range args {
		if a == target {
			n++
		}
	}
	return n
}

func mustNotHave(t *testing.T, args []string, forbidden ...string) {
	t.Helper()
	for _, f := range forbidden {
		for _, a := range args {
			if a == f {
				t.Errorf("args %v must NOT contain %q", args, f)
			}
		}
	}
}

func mustContainInOrder(t *testing.T, args []string, a, b string) {
	t.Helper()
	ai, bi := -1, -1
	for i, v := range args {
		if v == a && ai == -1 {
			ai = i
		}
		if v == b {
			bi = i
		}
	}
	if ai == -1 || bi == -1 || ai >= bi {
		t.Errorf("args %v: want %q before %q", args, a, b)
	}
}
