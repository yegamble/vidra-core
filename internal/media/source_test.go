package media

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

// presignBackend is a Backend that can mint URLs but exposes no local path — the
// shape of the S3 backend.
type presignBackend struct {
	storage.Backend
	url        string
	err        error
	presignKey string
	ttl        time.Duration
}

func (p *presignBackend) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	p.presignKey = key
	p.ttl = ttl
	if p.err != nil {
		return "", p.err
	}
	return p.url, nil
}

// pathlessBackend exposes no filesystem path, so callers must presign or
// download. It serves one fixed object. When presignErr is set it also
// implements Presigner, but failingly — the "presign configured but broken"
// case.
type pathlessBackend struct {
	storage.Backend
	body       string
	presignErr error
}

func (b *pathlessBackend) Open(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(b.body)), nil
}

// failingPresignBackend is a pathlessBackend that advertises Presigner and
// always fails to mint a URL.
type failingPresignBackend struct{ *pathlessBackend }

func (b *failingPresignBackend) PresignGet(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", b.presignErr
}

func TestSourceInputArgs(t *testing.T) {
	t.Run("local renders a bare -i", func(t *testing.T) {
		got := strings.Join(localSource("/tmp/in.mp4").inputArgs(), " ")
		if got != "-i /tmp/in.mp4" {
			t.Errorf("inputArgs = %q, want %q", got, "-i /tmp/in.mp4")
		}
	})
	t.Run("remote puts protocol options before -i", func(t *testing.T) {
		s := source{arg: "https://store.example/obj?sig=abc", opts: httpSourceOpts, remote: true}
		got := s.inputArgs()
		last := got[len(got)-2:]
		if last[0] != "-i" || last[1] != s.arg {
			t.Fatalf("inputArgs must end with -i <url>, got %v", last)
		}
		joined := strings.Join(got, " ")
		// Every protocol option must precede -i, or ffmpeg applies it to nothing.
		for _, want := range []string{"-seekable 1", "-multiple_requests 1", "-reconnect 1", "-reconnect_on_network_error 1"} {
			if !strings.Contains(joined, want) {
				t.Errorf("inputArgs missing %q; got %q", want, joined)
			}
			if strings.Index(joined, want) > strings.Index(joined, "-i "+s.arg) {
				t.Errorf("%q appears after -i; input options must precede the input", want)
			}
		}
	})
}

// TestOpenSourcePrefersLocalPath proves a backend exposing filesystem paths is
// used in place — no download, no URL, no cleanup work.
func TestOpenSourcePrefersLocalPath(t *testing.T) {
	dir := t.TempDir()
	local, err := storage.NewLocal(dir)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	src, cleanup, err := openSource(context.Background(), local, "a/b.mp4")
	if err != nil {
		t.Fatalf("openSource: %v", err)
	}
	defer cleanup()
	if src.remote {
		t.Error("local backend produced a remote source")
	}
	if len(src.opts) != 0 {
		t.Errorf("local source carries protocol options %v, want none", src.opts)
	}
	if !strings.HasPrefix(src.arg, dir) {
		t.Errorf("source arg = %q, want a path under %q", src.arg, dir)
	}
}

// TestOpenSourcePresignsInsteadOfDownloading is the leanness contract: on a
// backend with no local paths but a presigner, the source is read over HTTP and
// never staged on this instance's disk.
func TestOpenSourcePresignsInsteadOfDownloading(t *testing.T) {
	const url = "https://store.example/media/orig.mp4?X-Amz-Signature=deadbeef"
	b := &presignBackend{url: url}
	src, cleanup, err := openSource(context.Background(), b, "web-videos/orig.mp4")
	if err != nil {
		t.Fatalf("openSource: %v", err)
	}
	defer cleanup()
	if !src.remote {
		t.Fatal("presigning backend did not produce a remote source")
	}
	if src.arg != url {
		t.Errorf("source arg = %q, want the presigned URL", src.arg)
	}
	if b.presignKey != "web-videos/orig.mp4" {
		t.Errorf("presigned key = %q, want the requested key", b.presignKey)
	}
	if b.ttl != remoteSourceTTL {
		t.Errorf("presign ttl = %v, want %v", b.ttl, remoteSourceTTL)
	}
	if len(src.opts) == 0 {
		t.Error("remote source carries no protocol options; a long read would not survive a reconnect")
	}
}

// TestOpenSourceFallsBackToDownloadOnPresignFailure proves a presign error
// degrades to the download path rather than failing the job: a misconfigured
// store should transcode less leanly, not refuse to transcode.
func TestOpenSourceFallsBackToDownloadOnPresignFailure(t *testing.T) {
	b := &failingPresignBackend{pathlessBackend: &pathlessBackend{
		body:       "video-bytes",
		presignErr: errors.New("presign unavailable"),
	}}
	src, cleanup, err := openSource(context.Background(), b, "web-videos/orig.mp4")
	if err != nil {
		t.Fatalf("openSource: %v", err)
	}
	defer cleanup()
	if src.remote {
		t.Fatal("a failed presign still produced a remote source")
	}
	got, err := os.ReadFile(src.arg)
	if err != nil {
		t.Fatalf("reading downloaded temp file: %v", err)
	}
	if string(got) != "video-bytes" {
		t.Errorf("downloaded %q, want the object body", got)
	}
	cleanup()
	if _, err := os.Stat(src.arg); !os.IsNotExist(err) {
		t.Error("cleanup left the temp download behind")
	}
}

// TestOpenSourceDownloadsWhenBackendCanDoNeither covers a backend with no path
// and no presign at all.
func TestOpenSourceDownloadsWhenBackendCanDoNeither(t *testing.T) {
	src, cleanup, err := openSource(context.Background(), &pathlessBackend{body: "abc"}, "k")
	if err != nil {
		t.Fatalf("openSource: %v", err)
	}
	defer cleanup()
	if src.remote {
		t.Error("plain backend produced a remote source")
	}
	if filepath.Base(src.arg) == "k" {
		t.Error("source arg looks like the key, want a temp file path")
	}
}

// TestRedactSourceStripsPresignedURL pins the credential-handling rule: a
// presigned URL is a bearer credential for the object, and ffmpeg/ffprobe echo
// their input in diagnostics, so it must never reach a log or an error.
func TestRedactSourceStripsPresignedURL(t *testing.T) {
	const url = "https://store.example/o.mp4?X-Amz-Signature=secret"
	remote := source{arg: url, remote: true}
	err := redactSource(remote, errors.New("ffprobe: "+url+": Invalid data found"))
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), url) {
		t.Fatalf("redactSource leaked the signed URL: %v", err)
	}
	if !strings.Contains(err.Error(), "Invalid data found") {
		t.Errorf("redactSource dropped the diagnostic: %v", err)
	}

	// A local path is not a credential and must survive untouched, so operators
	// keep the actionable detail.
	local := localSource("/scratch/in.mp4")
	lerr := redactSource(local, errors.New("ffprobe: /scratch/in.mp4: Invalid data"))
	if !strings.Contains(lerr.Error(), "/scratch/in.mp4") {
		t.Errorf("redactSource redacted a local path: %v", lerr)
	}
	if redactSource(remote, nil) != nil {
		t.Error("redactSource(nil) must stay nil")
	}
}

// TestFFProbeArgsPlaceOptionsBeforePositionalInput guards ffprobe's syntax:
// unlike ffmpeg it takes the input positionally, so protocol options must come
// before the bare value and there must be no -i.
func TestFFProbeArgsPlaceOptionsBeforePositionalInput(t *testing.T) {
	remote := source{arg: "https://store.example/o.mp4?sig=x", opts: httpSourceOpts, remote: true}
	args := ffprobeArgs(remote)
	if args[len(args)-1] != remote.arg {
		t.Errorf("last arg = %q, want the input value", args[len(args)-1])
	}
	for _, a := range args {
		if a == "-i" {
			t.Error("ffprobe args contain -i; ffprobe takes its input positionally")
		}
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-seekable 1") {
		t.Errorf("ffprobe args dropped the protocol options: %q", joined)
	}
}
