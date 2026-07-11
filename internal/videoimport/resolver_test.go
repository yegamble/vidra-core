package videoimport

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ytdlp"
)

// stubExtractor is the yt-dlp seam for tests: it "downloads" fixed bytes into the
// caller's private workdir and returns canned metadata — no real binary, no
// network. It records calls so resolver-selection assertions can prove which
// path ran.
type stubExtractor struct {
	meta      ytdlp.Meta
	metaErr   error
	body      []byte
	ext       string // media file extension (default ".mp4")
	dlErr     error
	metaCalls int
	dlCalls   int
	lastURL   string
}

func (s *stubExtractor) Metadata(_ context.Context, url string) (ytdlp.Meta, error) {
	s.metaCalls++
	s.lastURL = url
	return s.meta, s.metaErr
}

func (s *stubExtractor) Download(_ context.Context, url, workdir string) (string, error) {
	s.dlCalls++
	s.lastURL = url
	if s.dlErr != nil {
		return "", s.dlErr
	}
	ext := s.ext
	if ext == "" {
		ext = ".mp4"
	}
	p := filepath.Join(workdir, "media"+ext)
	if err := os.WriteFile(p, s.body, 0o600); err != nil {
		return "", err
	}
	return p, nil
}

// TestYtdlpHappyPath: an explicit resolver=ytdlp import downloads via the stub,
// lands through AttachOriginal → Process (published), prefills the draft from
// metadata, keeps resolver=ytdlp, clears stage on completion, and removes the
// per-job workdir.
func TestYtdlpHappyPath(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	workRoot := t.TempDir()
	ext := &stubExtractor{body: []byte("platform-video-bytes"), meta: ytdlp.Meta{Title: "Imported", Description: "From a platform"}}
	svc := NewService(repo, pipe, 1<<20, WithYtdlp(ext, workRoot))
	vid := seedVideo(pipe, uuid.New())

	if _, err := svc.Enqueue(ctx, vid, "https://platform.example/watch?v=abc", ResolverYtdlp); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if n, err := svc.DrainJobs(ctx, 5); err != nil || n != 1 {
		t.Fatalf("drain = (%d,%v), want (1,nil)", n, err)
	}
	j := repo.latest(vid)
	if j.State != "done" {
		t.Fatalf("state = %q (err=%q), want done", j.State, j.Error)
	}
	if j.Resolver != ResolverYtdlp {
		t.Errorf("resolver = %q, want ytdlp", j.Resolver)
	}
	if j.Stage != "" {
		t.Errorf("stage = %q, want cleared on done", j.Stage)
	}
	if !pipe.published[vid] || pipe.storedSize[vid] != int64(len(ext.body)) {
		t.Errorf("not finalised: published=%v size=%d", pipe.published[vid], pipe.storedSize[vid])
	}
	if got := pipe.prefill[vid]; got[0] != "Imported" || got[1] != "From a platform" {
		t.Errorf("prefill = %v, want the metadata title/description", got)
	}
	if ext.dlCalls != 1 {
		t.Errorf("download calls = %d, want 1", ext.dlCalls)
	}
	// The private per-job workdir must be gone (body.Close removed it).
	if entries, _ := os.ReadDir(workRoot); len(entries) != 0 {
		t.Errorf("workRoot still has %d entries; per-job workdir was not cleaned", len(entries))
	}
	// Honest progress: downloading then processing were recorded.
	assertStageOrder(t, repo.stageLog, StageDownloading, StageProcessing)
}

// TestYtdlpDownloadFailureIsSafe: a stub download error dead-letters/reschedules
// with a SAFE reason (never the URL), nothing is published, and the workdir is
// cleaned.
func TestYtdlpDownloadFailureIsSafe(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	workRoot := t.TempDir()
	ext := &stubExtractor{dlErr: errors.New("HTTP Error 403: Forbidden for https://platform.example/secret")}
	svc := NewService(repo, pipe, 1<<20, WithYtdlp(ext, workRoot))
	vid := seedVideo(pipe, uuid.New())

	url := "https://platform.example/watch?v=zzz"
	if _, err := svc.Enqueue(ctx, vid, url, ResolverYtdlp); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	j := repo.latest(vid)
	if j.State == "done" || pipe.published[vid] {
		t.Fatalf("a failed download must not publish (state=%q published=%v)", j.State, pipe.published[vid])
	}
	if j.Error == "" || strings.Contains(j.Error, "platform.example") || strings.Contains(j.Error, "403") {
		t.Errorf("error = %q, want a safe reason that leaks neither the URL nor raw extractor output", j.Error)
	}
	if entries, _ := os.ReadDir(workRoot); len(entries) != 0 {
		t.Errorf("workRoot not cleaned after a failed download: %d entries", len(entries))
	}
}

// TestAutoPrefersDirectOnExtension: resolver=auto with a recognised video
// extension resolves to direct WITHOUT probing or touching yt-dlp.
func TestAutoPrefersDirectOnExtension(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	ext := &stubExtractor{body: []byte("unused")}
	svc := NewService(repo, pipe, 1<<20, WithHTTPClient(&http.Client{}), WithYtdlp(ext, t.TempDir()))
	origin := originServer(t)
	vid := seedVideo(pipe, uuid.New())

	if _, err := svc.Enqueue(ctx, vid, loopbackHost(origin.URL)+"/clip.mp4", ResolverAuto); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	j := repo.latest(vid)
	if j.State != "done" || j.Resolver != ResolverDirect {
		t.Fatalf("auto→direct: state=%q resolver=%q, want done/direct", j.State, j.Resolver)
	}
	if ext.dlCalls != 0 {
		t.Errorf("yt-dlp was invoked for a direct .mp4 (dlCalls=%d)", ext.dlCalls)
	}
}

// TestAutoDirectViaContentType: resolver=auto with no extension but a video
// content-type (guarded HEAD) resolves to direct.
func TestAutoDirectViaContentType(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	ext := &stubExtractor{body: []byte("unused")}
	svc := NewService(repo, pipe, 1<<20, WithHTTPClient(&http.Client{}), WithYtdlp(ext, t.TempDir()))
	origin := originServer(t)
	vid := seedVideo(pipe, uuid.New())

	if _, err := svc.Enqueue(ctx, vid, loopbackHost(origin.URL)+"/stream", ResolverAuto); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	j := repo.latest(vid)
	if j.State != "done" || j.Resolver != ResolverDirect {
		t.Fatalf("auto→direct(ct): state=%q resolver=%q, want done/direct", j.State, j.Resolver)
	}
	if ext.dlCalls != 0 {
		t.Errorf("yt-dlp invoked for a video content-type (dlCalls=%d)", ext.dlCalls)
	}
}

// TestAutoFallsBackToYtdlp: resolver=auto with no extension and a non-media
// content-type routes to the yt-dlp extractor when enabled, rewriting the stored
// resolver to the concrete choice and recording the resolving stage.
func TestAutoFallsBackToYtdlp(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	ext := &stubExtractor{body: []byte("platform-bytes"), ext: ".mp4"}
	svc := NewService(repo, pipe, 1<<20, WithHTTPClient(&http.Client{}), WithYtdlp(ext, t.TempDir()))
	origin := originServer(t)
	vid := seedVideo(pipe, uuid.New())

	if _, err := svc.Enqueue(ctx, vid, loopbackHost(origin.URL)+"/page", ResolverAuto); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	j := repo.latest(vid)
	if j.State != "done" || j.Resolver != ResolverYtdlp {
		t.Fatalf("auto→ytdlp: state=%q resolver=%q, want done/ytdlp", j.State, j.Resolver)
	}
	if ext.dlCalls != 1 {
		t.Errorf("yt-dlp download calls = %d, want 1", ext.dlCalls)
	}
	assertStageOrder(t, repo.stageLog, StageResolving, StageDownloading, StageProcessing)
}

// TestAutoWithoutYtdlpStaysDirect: resolver=auto with yt-dlp DISABLED never
// routes to the extractor — it attempts a direct fetch, which fails safely when
// the body is not an accepted container.
func TestAutoWithoutYtdlpStaysDirect(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	svc := NewService(repo, pipe, 1<<20, WithHTTPClient(&http.Client{})) // no ytdlp
	origin := originServer(t)
	vid := seedVideo(pipe, uuid.New())

	if _, err := svc.Enqueue(ctx, vid, loopbackHost(origin.URL)+"/page", ResolverAuto); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	j := repo.latest(vid)
	if pipe.published[vid] {
		t.Fatalf("an HTML page must not import as a video")
	}
	if j.Resolver != ResolverDirect {
		t.Errorf("resolver = %q, want direct (no yt-dlp fallback when disabled)", j.Resolver)
	}
	if j.Error == "" {
		t.Errorf("want a safe failure reason for a non-video body")
	}
}

// TestEnqueueResolverPolicy covers resolver validation + the disabled policy at
// enqueue: unknown → ErrInvalidResolver; ytdlp while disabled → ErrResolverDisabled
// (the handler maps it to 503); default/empty → auto; explicit ytdlp while
// enabled → stored ytdlp.
func TestEnqueueResolverPolicy(t *testing.T) {
	ctx := context.Background()

	// yt-dlp disabled.
	repo, pipe := newFakeRepo(), newFakePipeline()
	svc := NewService(repo, pipe, 1<<20)
	vid := seedVideo(pipe, uuid.New())
	if _, err := svc.Enqueue(ctx, vid, "https://x.example/a", "bogus"); !errors.Is(err, ErrInvalidResolver) {
		t.Errorf("bogus resolver err = %v, want ErrInvalidResolver", err)
	}
	if _, err := svc.Enqueue(ctx, vid, "https://x.example/a", ResolverYtdlp); !errors.Is(err, ErrResolverDisabled) {
		t.Errorf("ytdlp-while-disabled err = %v, want ErrResolverDisabled", err)
	}
	if svc.YtdlpEnabled() {
		t.Errorf("YtdlpEnabled() = true with no extractor wired")
	}
	job, err := svc.Enqueue(ctx, vid, "https://x.example/a", "")
	if err != nil {
		t.Fatalf("default enqueue: %v", err)
	}
	if job.Resolver != ResolverAuto {
		t.Errorf("default resolver = %q, want auto", job.Resolver)
	}

	// yt-dlp enabled.
	repo2, pipe2 := newFakeRepo(), newFakePipeline()
	svc2 := NewService(repo2, pipe2, 1<<20, WithYtdlp(&stubExtractor{}, t.TempDir()))
	vid2 := seedVideo(pipe2, uuid.New())
	if !svc2.YtdlpEnabled() {
		t.Errorf("YtdlpEnabled() = false with an extractor wired")
	}
	job2, err := svc2.Enqueue(ctx, vid2, "https://x.example/b", ResolverYtdlp)
	if err != nil {
		t.Fatalf("ytdlp enqueue: %v", err)
	}
	if job2.Resolver != ResolverYtdlp {
		t.Errorf("stored resolver = %q, want ytdlp", job2.Resolver)
	}
}

// assertStageOrder checks want appears as an ordered (not necessarily
// contiguous) subsequence of got.
func assertStageOrder(t *testing.T, got []string, want ...string) {
	t.Helper()
	i := 0
	for _, s := range got {
		if i < len(want) && s == want[i] {
			i++
		}
	}
	if i != len(want) {
		t.Errorf("stage log %v does not contain the ordered stages %v", got, want)
	}
}

// TestYtdlpGate covers the import_http_enabled runtime gate (config-parity W8)
// over a WIRED extractor: gate off → the resolver is effectively disabled
// (explicit enqueue refused, auto never falls back, a queued job fails safely);
// gate on → behaviour is identical to no gate at all. The gate can only narrow:
// without an extractor it changes nothing.
func TestYtdlpGate(t *testing.T) {
	ctx := context.Background()

	t.Run("gate off disables a wired extractor", func(t *testing.T) {
		repo, pipe := newFakeRepo(), newFakePipeline()
		ext := &stubExtractor{body: []byte("bytes")}
		svc := NewService(repo, pipe, 1<<20,
			WithHTTPClient(&http.Client{}),
			WithYtdlp(ext, t.TempDir()),
			WithYtdlpGate(func() bool { return false }))
		if svc.YtdlpEnabled() {
			t.Error("YtdlpEnabled() = true with the gate off")
		}
		vid := seedVideo(pipe, uuid.New())
		if _, err := svc.Enqueue(ctx, vid, "https://x.example/a", ResolverYtdlp); !errors.Is(err, ErrResolverDisabled) {
			t.Errorf("explicit ytdlp enqueue err = %v, want ErrResolverDisabled", err)
		}
		// resolver=auto never falls back to the extractor while gated off: the
		// direct attempt runs (and fails safely on a non-container body).
		origin := originServer(t)
		if _, err := svc.Enqueue(ctx, vid, loopbackHost(origin.URL)+"/page", ResolverAuto); err != nil {
			t.Fatalf("auto enqueue: %v", err)
		}
		if _, err := svc.DrainJobs(ctx, 5); err != nil {
			t.Fatalf("drain: %v", err)
		}
		if j := repo.latest(vid); j.Resolver == ResolverYtdlp || ext.dlCalls != 0 {
			t.Errorf("auto routed to ytdlp while gated off (resolver=%q dlCalls=%d)", j.Resolver, ext.dlCalls)
		}
	})

	t.Run("gate flips at runtime per call", func(t *testing.T) {
		repo, pipe := newFakeRepo(), newFakePipeline()
		enabled := false
		svc := NewService(repo, pipe, 1<<20,
			WithYtdlp(&stubExtractor{body: []byte("bytes")}, t.TempDir()),
			WithYtdlpGate(func() bool { return enabled }))
		vid := seedVideo(pipe, uuid.New())
		if _, err := svc.Enqueue(ctx, vid, "https://x.example/a", ResolverYtdlp); !errors.Is(err, ErrResolverDisabled) {
			t.Fatalf("gated-off enqueue err = %v, want ErrResolverDisabled", err)
		}
		enabled = true // admin re-enables — no restart, next call sees it
		if !svc.YtdlpEnabled() {
			t.Error("YtdlpEnabled() = false after the gate re-opened")
		}
		if _, err := svc.Enqueue(ctx, vid, "https://x.example/a", ResolverYtdlp); err != nil {
			t.Errorf("gated-on enqueue err = %v, want nil", err)
		}
	})

	t.Run("gate on cannot enable an unwired extractor", func(t *testing.T) {
		repo, pipe := newFakeRepo(), newFakePipeline()
		svc := NewService(repo, pipe, 1<<20, WithYtdlpGate(func() bool { return true })) // no WithYtdlp
		if svc.YtdlpEnabled() {
			t.Error("YtdlpEnabled() = true without an extractor")
		}
		vid := seedVideo(pipe, uuid.New())
		if _, err := svc.Enqueue(ctx, vid, "https://x.example/a", ResolverYtdlp); !errors.Is(err, ErrResolverDisabled) {
			t.Errorf("unwired enqueue err = %v, want ErrResolverDisabled", err)
		}
	})
}
