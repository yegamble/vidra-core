package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/vidra/vidra-core/internal/doctor"
)

// runDoctor is `vidra doctor`: run every host-level check against a deployment
// and print one ✓/⚠/✗ line each. The checks live in internal/doctor — this file
// is argument plumbing and an exit code, so the web console (phase-1 item 15) can
// render the same Report without going through a terminal.
//
// The exit code is the contract: 0 when nothing FAILED, 1 when something did. A
// warning never fails a run, because a warning is either a check that could not
// run here or a finding that does not stop a deployment — and a command that
// exits non-zero for those is one every wrapper script learns to ignore.
func runDoctor(s streams, args []string) error {
	fs := flag.NewFlagSet("vidra doctor", flag.ContinueOnError)
	fs.SetOutput(s.err)

	var (
		repo    = fs.String("repo", ".", "`path` to the deployment directory — the checkout holding docker-compose.yml, deploy/ and env/")
		envFile = fs.String("env", doctor.DefaultEnvFile, "`path` to the deployment's env file, absolute or relative to --repo")
		timeout = fs.Duration("timeout", doctor.DefaultTimeout, "how long any single check may take before it gives up and reports that it could not complete")
	)
	// -C is the short spelling, matching make and git: `vidra doctor -C ..` from
	// inside a component checkout is the common invocation.
	fs.StringVar(repo, "C", ".", "shorthand for --repo")

	usage := func(w io.Writer) {
		fmt.Fprint(w, `usage: vidra doctor [-C <deployment directory>] [--env <env file>]

Checks a Vidra deployment the way the runbook says to, and prints one line per
check: ✓ passed, ⚠ could not run (or a finding that does not stop a deploy), ✗ a
problem to fix, each with one line of what to do about it. It exits 0 unless
something is ✗, so it is usable as a pre-deploy gate.

Every finding is derived from a file, the rendered compose model, the database or
the docker daemon — never from what a document says should be true. It reads;
it changes nothing, creates no bucket and sends no mail.

Run it from the deployment directory (the one deploy.sh lives in), or point --repo
at it. Checks whose dependency is missing here — no systemd on a laptop, no
daemon, a stack that is not up — report ⚠ with the reason rather than failing.

flags:
`)
		fs.SetOutput(w)
		fs.PrintDefaults()
		fs.SetOutput(s.err)
	}
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(s.out)
			return nil
		}
		usage(s.err)
		return errReported
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(s.err, "vidra doctor: unexpected argument %q\n\n", fs.Arg(0))
		usage(s.err)
		return errReported
	}
	if *timeout <= 0 {
		return errors.New("doctor: --timeout must be positive (it is what stops one hung probe from costing the whole report)")
	}

	report, err := doctor.Run(context.Background(), doctor.Options{
		Root:    *repo,
		EnvFile: *envFile,
		Timeout: *timeout,
	})
	if err != nil {
		return err
	}
	doctor.Render(s.out, report)
	if report.Failed() {
		// Already printed, in the shape the operator needs it: main must not add a
		// second, redundant error line under a report that ends with a summary.
		return errReported
	}
	return nil
}
