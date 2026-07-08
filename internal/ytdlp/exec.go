package ytdlp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// killGrace bounds how long Wait may block after the deadline fires before the
// child's I/O pipes are force-closed. A grandchild (e.g. an ffmpeg muxer) that
// survives the group signal — or races the kill — must not be able to wedge the
// caller open past its own wall-clock budget.
const killGrace = 5 * time.Second

// runCommand is the production runFunc: it executes name+args with NO shell (so
// the URL positional can never be word-split or command-substituted) and returns
// stdout. Stderr is captured only to distinguish a run failure; it is NOT
// surfaced to the caller (which maps everything to the sentinel ErrRun so no raw
// extractor output — potentially echoing the URL — reaches a client-visible
// error). The context carries the hard wall-clock deadline.
//
// The child is started in its OWN process group (Setpgid) and, on ctx expiry, the
// whole group is signalled (SIGKILL to -pid) rather than just the direct child.
// yt-dlp spawns an ffmpeg grandchild to mux separate video/audio streams; a
// child-only kill would orphan that ffmpeg and — because it inherits the stdout
// pipe — could also keep this call blocked in Wait indefinitely. WaitDelay caps
// that residual window as a belt-and-braces bound.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// Never inherit the parent environment beyond an empty, predictable set: no
	// HTTP(S)_PROXY leakage, no ambient credentials. yt-dlp reads only its argv.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	// Put the child in a new process group so a timeout kill can reach the entire
	// tree (yt-dlp + any ffmpeg grandchild), not just the direct child.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Override CommandContext's default (which kills only cmd.Process): signal the
	// negated pid, i.e. the whole process group led by the child.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// If a grandchild outlives the group signal while still holding the output
	// pipe, don't let Wait block on it forever.
	cmd.WaitDelay = killGrace
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// findMedia returns the single downloaded media file in workdir. yt-dlp writes
// exactly one file named media.<ext> (--no-playlist, media.%(ext)s template);
// finding none is ErrNoMedia. If more than one matches (belt and braces), the
// first lexical match wins — all live in the private per-job workdir.
func findMedia(workdir string) (string, error) {
	entries, err := os.ReadDir(workdir)
	if err != nil {
		return "", ErrNoMedia
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) >= len("media.") && name[:len("media.")] == "media." {
			return filepath.Join(workdir, name), nil
		}
	}
	return "", ErrNoMedia
}

// filepathJoin is a thin alias so ytdlp.go can build the output template without
// importing path/filepath directly (keeps that file to the pure argv shape).
func filepathJoin(dir, name string) string { return filepath.Join(dir, name) }
