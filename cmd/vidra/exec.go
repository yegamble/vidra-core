package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runner is the CLI's one seam onto process execution. Everything `vidra` runs
// on the host — the five wrapped deploy scripts, deploy/compose.sh — goes
// through it, so the tests can assert on the exact command line, environment and
// working directory a command would have used without a docker daemon, a bash
// or a deployment anywhere near them.
//
// It is deliberately two methods rather than one. PASSTHROUGH and CAPTURE are
// not the same act: the first hands the operator's own terminal to a script that
// prompts and streams progress for minutes, the second reads a subprocess's
// output as data. Collapsing them into a flag would mean one call site could ask
// for the wrong half of the behaviour.
type runner interface {
	// Environ is the process environment the children inherit. It is here, and
	// not read from os.Environ() at the call sites, because two rules depend on
	// it — ENV_FILE precedence and deploy/lib.sh's env_get — and a test that
	// could not set it would be testing the shell it happened to run under.
	Environ() []string
	// Passthrough runs a command with the caller's streams attached and waits.
	// A non-zero exit is returned as *exitError and nothing else: the scripts
	// print their own diagnostics, and a wrapper that added a line of its own
	// would be the second opinion nobody asked for.
	Passthrough(spec execSpec, s streams) error
	// Capture runs a command to completion and returns its output. A non-zero
	// exit is a RESULT, not an error (compose says "not running" that way); the
	// error return is for a command that could not be started at all.
	Capture(ctx context.Context, spec execSpec) (execResult, error)
}

// execSpec is one child process.
type execSpec struct {
	// Path is the program. It is "bash" for every script this CLI runs, with
	// the script itself as Args[0] — the deployment's scripts are bash and are
	// run as bash, rather than relying on an executable bit that a tarball, a
	// checkout on a noexec mount or a Windows-formatted clone can lose.
	Path string
	Args []string
	// Dir is the working directory. The scripts all cd to their own REPO_ROOT
	// first, so this is consistency rather than a requirement — but a relative
	// path in an argument (a dump file, an env file) resolves against it, and
	// that IS load-bearing.
	Dir string
	// Env is the COMPLETE child environment, not an overlay.
	Env []string
}

// execResult is a finished command.
type execResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// exitError is a child process's non-zero exit, carried up to main so the
// process exits with the SAME code and prints nothing extra.
//
// Exit-code fidelity is the whole contract of the passthrough commands: an
// operator's `vidra deploy && vidra logs`, a cron entry, a CI step and the
// runbook's `|| ./deploy/rollback.sh` all read the number, and deploy.sh's
// refusals are already one clear sentence each on stderr. A wrapper that
// collapsed every failure to 1, or that appended "vidra: exit status 3" under a
// script's own die message, would break both.
type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// newExitError normalises a wait status into an exit code that means "failed".
// A process killed by a signal reports -1, which is not a code a shell can
// return; it becomes 1, because the one thing the caller must not read is
// success.
func newExitError(code int) *exitError {
	if code <= 0 {
		code = 1
	}
	return &exitError{code: code}
}

// osRunner is the production runner: real processes on this host.
type osRunner struct{}

func (osRunner) Environ() []string { return os.Environ() }

func (osRunner) Passthrough(spec execSpec, s streams) error {
	cmd := exec.Command(spec.Path, spec.Args...) //nolint:gosec // the path is deploy/<name>.sh under the operator's own --repo.
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	// Assigned as-is, deliberately. os/exec dups an *os.File straight onto the
	// child's descriptor and only starts a copying goroutine — a PIPE — for
	// anything else. That distinction is load-bearing rather than an
	// optimisation: release.sh tests `[ -t 0 ]` before prompting and restore.sh
	// reads a typed confirmation, and both see a pipe as "not a terminal". So
	// the process's own files pass through untouched, and the buffers a test
	// supplies take the pipe path.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = s.in, s.out, s.err
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return newExitError(exitErr.ExitCode())
	}
	if err != nil {
		return fmt.Errorf("%s could not be run: %w", spec.display(), err)
	}
	return nil
}

func (osRunner) Capture(ctx context.Context, spec execSpec) (execResult, error) {
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...) //nolint:gosec // as above.
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := execResult{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("%s could not be run: %w", spec.display(), err)
	}
	return res, nil
}

// display is the spec as something worth naming in an error: the script, not
// the interpreter, because "bash could not be run" tells an operator nothing
// about which of their commands failed.
func (c execSpec) display() string {
	if len(c.Args) > 0 {
		return c.Args[0]
	}
	return c.Path
}

// theRunner is the exec seam in use. It is a package variable rather than a
// parameter because the dispatch table's signature is (streams, args) — adding a
// runner to it would change setup and doctor, which do not exec anything, for
// the sake of six commands that do. Tests swap it through swapRunner.
var theRunner runner = osRunner{}

var _ runner = osRunner{}
