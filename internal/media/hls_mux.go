package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

const (
	// HLSMuxTimeout bounds one on-request mux end to end. A stream copy of a
	// feature-length file runs at disk/network speed; this only ends a wedged or
	// trickle-fed process.
	HLSMuxTimeout = 30 * time.Minute
	// hlsMuxWaitDelay is how long Wait lets the pipes drain after the context
	// ends and ffmpeg is killed, so a grandchild holding stdout cannot wedge us.
	hlsMuxWaitDelay = 5 * time.Second
	// hlsMuxStderrCap bounds the stderr ffmpeg may make us hold.
	hlsMuxStderrCap = 16 << 10
)

// hlsMuxInputArgs renders one mux input with its hardening: an allowlist
// matching the kind of input (a local file, or exactly the transport a presigned
// object URL needs), the container forced to mov (which demuxes MP4) so the
// format is never probed from content, and external data references off. The
// inputs are server-resolved storage keys and fragmented MP4 needs nothing
// else, so every other protocol and demuxer stays unreachable.
func hlsMuxInputArgs(src source) []string {
	whitelist := "file"
	if src.remote {
		whitelist = "https,tls,tcp"
		if strings.HasPrefix(src.arg, "http:") {
			whitelist = "http,tcp"
		}
	}
	args := []string{"-protocol_whitelist", whitelist}
	args = append(args, src.opts...)
	return append(args, "-enable_drefs", "0", "-f", "mov", "-i", src.arg)
}

// hlsMuxArgs is the ffmpeg argument vector that stream-copies one video-only and
// one audio-only fragmented MP4 into a single fragmented MP4 on stdout. Pure, so
// it is unit-tested without ffmpeg. -c copy: no re-encode, so the cost is I/O.
// The movflags make the output streamable (no seek-back to write a moov), which
// pipe:1 requires.
func hlsMuxArgs(video, audio source) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	args = append(args, hlsMuxInputArgs(video)...)
	args = append(args, hlsMuxInputArgs(audio)...)
	return append(args,
		"-map", "0:v:0", "-map", "1:a:0",
		"-c", "copy",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4", "pipe:1",
	)
}

// hlsMuxEnv is the environment ffmpeg runs with: PATH, plus the TLS trust-store
// pointers a presigned https input may need. Nothing else of the API's
// environment (JWT secret, database URL, storage keys) is handed to the child.
func hlsMuxEnv() []string {
	env := []string{"PATH=" + os.Getenv("PATH")}
	for _, k := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// hlsMuxPresignTTL is how long a mux input URL must stay valid: the longest the
// process may run, plus a margin. Far shorter than the 6h transcode default.
const hlsMuxPresignTTL = HLSMuxTimeout + 5*time.Minute

// ErrMuxUnsupported means the backend can hand ffmpeg neither a local path nor a
// presigned URL, so the only input would be a full copy of the object spooled to
// the temp directory. A public, anonymous route must not do that.
var ErrMuxUnsupported = errors.New("media: backend cannot supply mux inputs without spooling to disk")

// HLSMuxer joins a split-audio rung's video and audio objects into one MP4 on
// request. Nothing is written to storage.
type HLSMuxer struct {
	blobs storage.Backend
	bin   string
}

// NewHLSMuxer builds a muxer reading from blobs through the "ffmpeg" binary on
// PATH (the same binary the transcoder uses).
func NewHLSMuxer(blobs storage.Backend) *HLSMuxer {
	return &HLSMuxer{blobs: blobs, bin: "ffmpeg"}
}

// Supported reports whether Mux can run against this backend at all: it needs a
// local path or a presigner. Cheap (type assertions), so listings can use it.
func (m *HLSMuxer) Supported() bool {
	if _, ok := m.blobs.(storage.PathProvider); ok {
		return true
	}
	_, ok := m.blobs.(storage.Presigner)
	return ok
}

// openMuxSource is openSourceTTL without the spool-to-temp-file fallback.
func (m *HLSMuxer) openMuxSource(ctx context.Context, key string) (source, error) {
	if pp, ok := m.blobs.(storage.PathProvider); ok {
		p, err := pp.Path(key)
		if err != nil {
			return source{}, err
		}
		return localSource(p), nil
	}
	if pr, ok := m.blobs.(storage.Presigner); ok {
		u, err := pr.PresignGet(ctx, key, hlsMuxPresignTTL)
		if err != nil {
			return source{}, err
		}
		return source{arg: u, opts: httpSourceOpts, remote: true}, nil
	}
	return source{}, ErrMuxUnsupported
}

// newHLSMuxCmd builds the ffmpeg command. Pure (nothing is started), so the
// environment and process limits are assertable.
func newHLSMuxCmd(ctx context.Context, bin string, video, audio source) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, hlsMuxArgs(video, audio)...)
	cmd.WaitDelay = hlsMuxWaitDelay
	cmd.Env = hlsMuxEnv()
	return cmd
}

// Mux starts ffmpeg and returns its stdout. The caller must Close the result.
// Cancelling ctx, closing the reader, or HLSMuxTimeout kills the process. A
// non-zero exit surfaces as the error of the Read that hits end of output, so a
// caller that has not yet received a byte can still tell a failed start from a
// successful one. Errors carry a bounded stderr tail with presigned URLs
// redacted.
func (m *HLSMuxer) Mux(ctx context.Context, videoKey, audioKey string) (io.ReadCloser, error) {
	if !m.Supported() {
		return nil, ErrMuxUnsupported
	}
	vsrc, err := m.openMuxSource(ctx, videoKey)
	if err != nil {
		return nil, fmt.Errorf("media: mux video input: %w", err)
	}
	asrc, err := m.openMuxSource(ctx, audioKey)
	if err != nil {
		return nil, fmt.Errorf("media: mux audio input: %w", err)
	}
	vclean, aclean := func() {}, func() {}
	ctx, cancel := context.WithTimeout(ctx, HLSMuxTimeout)
	cmd := newHLSMuxCmd(ctx, m.bin, vsrc, asrc)
	stderr := &cappedBuffer{limit: hlsMuxStderrCap}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	cleanup := func() { cancel(); vclean(); aclean() }
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, fmt.Errorf("media: start ffmpeg mux: %w", err)
	}
	return &muxStream{cmd: cmd, stdout: stdout, stderr: stderr, srcs: []source{vsrc, asrc}, cleanup: cleanup}, nil
}

type muxStream struct {
	cmd     *exec.Cmd
	stdout  io.Reader
	stderr  *cappedBuffer
	srcs    []source
	cleanup func()

	once    sync.Once
	waitErr error
}

func (s *muxStream) wait() error {
	s.once.Do(func() {
		if err := s.cmd.Wait(); err != nil {
			msg := s.stderr.String()
			for _, src := range s.srcs {
				if src.remote && src.arg != "" {
					msg = strings.ReplaceAll(msg, src.arg, "<presigned source url>")
				}
			}
			s.waitErr = fmt.Errorf("media: ffmpeg mux: %w: %s", err, tailOf(msg))
		}
	})
	return s.waitErr
}

func (s *muxStream) Read(p []byte) (int, error) {
	n, err := s.stdout.Read(p)
	if errors.Is(err, io.EOF) {
		if werr := s.wait(); werr != nil {
			return n, werr
		}
	}
	return n, err
}

func (s *muxStream) Close() error {
	s.cleanup() // cancels the context, which kills a still-running ffmpeg
	_ = s.wait()
	return nil
}

// cappedBuffer keeps the first limit bytes written and silently discards the
// rest, so a chatty process can never grow our memory but is always drained.
type cappedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.limit - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
