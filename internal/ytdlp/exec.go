package ytdlp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// runCommand is the production runFunc: it executes name+args with NO shell (so
// the URL positional can never be word-split or command-substituted) and returns
// stdout. Stderr is captured only to distinguish a run failure; it is NOT
// surfaced to the caller (which maps everything to the sentinel ErrRun so no raw
// extractor output — potentially echoing the URL — reaches a client-visible
// error). The context carries the hard wall-clock deadline; on expiry the child
// is killed.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// Never inherit the parent environment beyond an empty, predictable set: no
	// HTTP(S)_PROXY leakage, no ambient credentials. yt-dlp reads only its argv.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
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
