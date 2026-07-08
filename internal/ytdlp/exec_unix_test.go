//go:build unix

package ytdlp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRunCommandKillsProcessGroupOnTimeout proves the real defense-in-depth
// property of the exec seam: when the context deadline fires, runCommand kills
// the WHOLE process group, so a grandchild the direct child spawned (yt-dlp's
// ffmpeg muxer, modelled here by a `sleep`) is reaped too — never orphaned.
//
// It is hermetic: a tiny /bin/sh fixture spawns a long-lived background sleep,
// records its pid, and blocks. No real yt-dlp binary is involved. The test also
// guards against the sibling failure mode — a grandchild holding the stdout pipe
// wedging Wait open past the deadline — by asserting runCommand returns promptly.
func TestRunCommandKillsProcessGroupOnTimeout(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	script := filepath.Join(dir, "spawn.sh")
	// The direct child spawns a grandchild that outlives the deadline, records its
	// pid, then blocks on it so the whole group is alive when the timeout hits.
	body := "#!/bin/sh\n" +
		"sleep 300 &\n" +
		"echo $! > \"" + pidFile + "\"\n" +
		"wait\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := runCommand(ctx, "sh", script); err == nil {
		t.Fatal("runCommand returned nil error; expected a timeout kill")
	}
	if elapsed := time.Since(start); elapsed > killGrace+2*time.Second {
		t.Fatalf("runCommand blocked %v after the deadline — group-kill/WaitDelay did not release", elapsed)
	}

	pid := readGrandchildPID(t, pidFile)
	// Never leak the grandchild even if the group-kill assertion below fails.
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()

	// The group signal must have reached the grandchild: within a short grace,
	// signal 0 should report it gone (ESRCH) once init has reaped it.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return // ESRCH: the grandchild is gone — group-kill worked.
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild pid %d still alive after the group was killed", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readGrandchildPID polls for the fixture's pid file (written just after the
// grandchild is spawned) and parses it.
func readGrandchildPID(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(raw))); perr == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture never recorded a grandchild pid at %s", pidFile)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
