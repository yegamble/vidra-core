package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

// hlsMuxProtocols is the ffmpeg input protocol allowlist: a local file, or the
// plain/TLS HTTP a presigned object URL needs. The inputs are server-resolved
// storage keys and fragmented MP4 needs nothing else, so every other protocol
// (concat:, subfile:, tcp/udp to arbitrary hosts via a crafted container) stays
// off.
const hlsMuxProtocols = "file,http,https,tcp,tls"

// hlsMuxArgs is the ffmpeg argument vector that stream-copies one video-only and
// one audio-only fragmented MP4 into a single fragmented MP4 on stdout. Pure, so
// it is unit-tested without ffmpeg. -c copy: no re-encode, so the cost is I/O.
// The movflags make the output streamable (no seek-back to write a moov), which
// pipe:1 requires.
func hlsMuxArgs(video, audio source) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	for _, src := range []source{video, audio} {
		args = append(args, "-protocol_whitelist", hlsMuxProtocols)
		args = append(args, src.inputArgs()...)
	}
	return append(args,
		"-map", "0:v:0", "-map", "1:a:0",
		"-c", "copy",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4", "pipe:1",
	)
}

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

// Mux starts ffmpeg and returns its stdout. The caller must Close the result.
// Cancelling ctx, closing the reader, or HLSMuxTimeout kills the process. A
// non-zero exit surfaces as the error of the Read that hits end of output, so a
// caller that has not yet received a byte can still tell a failed start from a
// successful one. Errors carry a bounded stderr tail with presigned URLs
// redacted.
func (m *HLSMuxer) Mux(ctx context.Context, videoKey, audioKey string) (io.ReadCloser, error) {
	vsrc, vclean, err := openSource(ctx, m.blobs, videoKey)
	if err != nil {
		return nil, fmt.Errorf("media: mux video input: %w", err)
	}
	asrc, aclean, err := openSource(ctx, m.blobs, audioKey)
	if err != nil {
		vclean()
		return nil, fmt.Errorf("media: mux audio input: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, HLSMuxTimeout)
	cmd := exec.CommandContext(ctx, m.bin, hlsMuxArgs(vsrc, asrc)...)
	cmd.WaitDelay = hlsMuxWaitDelay
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
