package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/vidra/vidra-core/internal/doctor"
	"github.com/vidra/vidra-core/internal/preflight"
	"github.com/vidra/vidra-core/internal/setup"
	"github.com/vidra/vidra-core/internal/setupweb"
)

// runSetupWeb is `vidra setup --web`: the same setup, driven from a browser
// instead of a terminal (docs/productionization phase-1 item 9).
//
// THIS FILE IS WIRING AND A BANNER. The wizard's behaviour is internal/setupweb;
// its answers go through internal/setup, the same engine the interview above
// uses. What lives here is the four things setupweb deliberately does not know
// how to do for itself, injected as narrow closures so its own tests need no
// docker, no nameserver and no deployment:
//
//   - RENDERING THE PROXY CONFIGS, because which of the two files a deployment
//     gets (and whether --no-caddy suppressed the first) is a decision
//     renderCaddyfile/renderNginxExample already make for the terminal path. A
//     second copy in setupweb would be the drift that leaves a deployment with a
//     Caddyfile nothing mounts.
//   - THE DNS REPORT, through the same `checkDomain` seam the interview uses.
//   - THE HOST CHECKS, internal/doctor's whole registry.
//   - THE DEPLOY AND THE STATUS PROBE, through theRunner and collectStatus — the
//     same exec seam and the same probes `vidra deploy` and `vidra status` use,
//     so the wizard cannot deploy something the CLI would have refused.
func runSetupWeb(s streams, opt *setupOptions, outPath string) error {
	// The flags that belong to the TERMINAL path are refused rather than
	// ignored. Every one of them has an answer inside the wizard (the overwrite
	// acknowledgement is a checkbox in the Review step; the answers file is the
	// form), and a flag that silently did nothing would be an operator believing
	// they had pre-authorised something.
	for _, bad := range []struct {
		name string
		set  bool
		why  string
	}{
		{"--non-interactive", opt.nonInteractive, "it means the opposite: --web IS the interactive path, served to a browser instead of a terminal"},
		{"--answers", opt.answersPath != "", "the wizard's form is the answers file — seed it by pointing --template at the deployment and pressing through"},
		{"--from", opt.from != "", "the wizard merges one source, the file it is about to write, which it always preserves in full"},
		{"--rotate", len(opt.rotate) > 0, "the wizard does not rotate secrets; run `vidra setup --rotate " + strings.Join(opt.rotate, " --rotate ") + "` in a terminal for that"},
		{"--yes", opt.yes, "the wizard asks for the in-place rewrite in its Review step, where you can see what it would change"},
		{"--yes-i-know", opt.yesIKnow, "it authorises a destructive KEK rotation, which the wizard cannot do at all"},
	} {
		if bad.set {
			return fmt.Errorf("setup: --web and %s do not go together — %s", bad.name, bad.why)
		}
	}

	listen, err := setupweb.ParseListen(opt.listen)
	if err != nil {
		return err
	}
	// The env file this run is about, spelled as the scripts want it: relative
	// stays relative, because that is what ENV_FILE hands to deploy.sh and what
	// its messages quote back.
	wrapper := wrapperFlags{repo: ".", envFile: outPath, explicit: true}

	srv, err := setupweb.New(setupweb.Options{
		Listen:       listen,
		TemplatePath: opt.template,
		OutputPath:   outPath,
		CaddyOutPath: opt.caddyOut,
		NginxOutPath: opt.nginxOut,

		RenderProxy: func(values map[string]string) ([]byte, []byte, error) {
			caddy, cerr := renderCaddyfile(opt.caddyTemplate, opt.noCaddy, values)
			if cerr != nil {
				return nil, nil, cerr
			}
			nginx, nerr := renderNginxExample(values)
			if nerr != nil {
				return nil, nil, nerr
			}
			return caddy, nginx, nil
		},

		CheckDomain: func(ctx context.Context, domain string) setupweb.DomainReport {
			res := checkDomain(ctx, preflight.DomainRequest{
				Domain: domain,
				// Half the budget per endpoint, so both can be asked inside the
				// caller's deadline — the same split reportDomainDNS uses.
				PublicIP: preflight.PublicIPOptions{Timeout: domainCheckTimeout / 2},
			})
			return setupweb.DomainReport{Status: res.Status.String(), Message: res.Message, Fix: res.Fix}
		},

		Doctor: func(ctx context.Context) (setupweb.DoctorReport, error) {
			// Root "." and the env file this run is writing: `vidra setup` is
			// documented to run from the deployment directory, which is the same
			// assumption `vidra doctor` makes with no -C.
			report, rerr := doctor.Run(ctx, doctor.Options{Root: ".", EnvFile: outPath})
			if rerr != nil {
				return setupweb.DoctorReport{}, rerr
			}
			return doctorReport(report), nil
		},

		Install: func(ctx context.Context, w io.Writer) (int, error) {
			return runDeployForWizard(ctx, wrapper, w)
		},

		Status: func(ctx context.Context) []setupweb.StatusLine {
			return statusLines(collectStatus(ctx, wrapper))
		},

		DeployCommand: deployCommand(outPath),
	})
	if err != nil {
		return err
	}
	// Bound BEFORE the banner: "that port is taken" belongs on screen while the
	// operator is still looking at the command they typed, not after a URL they
	// have already started copying.
	if err := srv.Listen(); err != nil {
		return err
	}
	printWizardBanner(s, srv, outPath)

	// SIGINT is one of the three ways this ends, and the ordinary one: the
	// operator is watching this terminal because the banner told them to.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.Run(ctx); err != nil {
		return err
	}
	fmt.Fprintf(s.out, "\n✓ the setup wizard has stopped (%s).\n", srv.Reason())
	return nil
}

// printWizardBanner is the whole terminal half of `vidra setup --web`: the
// one-time link, how to reach it from somewhere else, and why this window has to
// stay open.
//
// The TUNNEL INSTRUCTION is not optional decoration. The wizard binds to
// loopback and refuses to do otherwise, so an operator on a droplet who is not
// told about `ssh -L` has been handed a URL they cannot open — and the next
// thing they reach for is a firewall rule and a flag that does not exist.
func printWizardBanner(s streams, srv *setupweb.Server, outPath string) {
	host, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		host, port = "127.0.0.1", "8321"
	}
	fmt.Fprintf(s.out, `
The setup wizard is running. Open this link — it carries a ONE-TIME token, it is
printed only here, and it is the only thing that lets anything talk to it:

  %s

If you are on the server over SSH, forward the port to your own machine first
(from your laptop, in another terminal):

  ssh -L %s:%s:%s <user>@<this server>

then open the same link there.

Keep this terminal open: the wizard lives in this process. It writes %s and can
run the deploy for you, so it shuts itself down when you press Finish, when you
press Ctrl-C here, and after %s with no browser talking to it.
`, srv.URL(), port, host, port, outPath, setupweb.DefaultIdleTimeout)
}

// runDeployForWizard execs the deployment's own deploy.sh with STDIN CLOSED and
// both output streams going to the wizard.
//
// The stdin decision is the one worth stating: there is nobody at a terminal, so
// a script that blocked on a prompt would hang the wizard behind a spinner with
// nothing to read. /dev/null is handed over as a real *os.File, which os/exec
// dups straight onto the child's descriptor 0 — so a `read` fails at once and
// the script dies with its own message, in the stream the operator is already
// watching. (deploy.sh does not prompt. The two scripts that do, restore.sh and
// release.sh, are not reachable from here.)
//
// A non-zero exit is a RESULT, not an error: the script printed its own reason
// and the wizard shows the code. The error return is for a deploy that could not
// be STARTED, which has no log to read and is worth saying differently.
func runDeployForWizard(_ context.Context, f wrapperFlags, w io.Writer) (int, error) {
	dep, path, err := resolve("setup", f, "deploy.sh")
	if err != nil {
		return 0, err
	}
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return 0, fmt.Errorf("the deploy could not be started: %s could not be opened to close the script's stdin with", os.DevNull)
	}
	defer func() { _ = devnull.Close() }()

	// Stdout and Stderr are the SAME writer, deliberately: deploy.sh logs its
	// progress to one and its refusals to the other, and an operator watching an
	// install needs them interleaved in the order they happened rather than in
	// two panes they have to zip together mentally.
	err = theRunner.Passthrough(dep.bash(path), streams{in: devnull, out: w, err: w})
	var exit *exitError
	if errors.As(err, &exit) {
		return exit.code, nil
	}
	if err != nil {
		return 0, err
	}
	return 0, nil
}

// deployCommand is what to type to run the install by hand, for the wizard's
// "I'll run it myself" path. The --env is shown only when it is not the default,
// so the ordinary case stays a two-word command.
func deployCommand(envFile string) string {
	if envFile == defaultEnvFile {
		return "vidra deploy"
	}
	return "vidra deploy --env " + envFile
}

// doctorReport flattens internal/doctor's report into the wizard's wire shape.
// The Status is rendered as its WORD rather than its integer: preflight.Status
// is an iota, and a page switching on 0/1/2 would break silently the day a
// fourth outcome is added.
func doctorReport(r doctor.Report) setupweb.DoctorReport {
	out := setupweb.DoctorReport{Root: r.Root, EnvFile: r.EnvFile}
	for _, res := range r.Results {
		out.Results = append(out.Results, setupweb.DoctorResult{
			Check:   res.Check,
			Section: string(res.Section),
			Status:  res.Status.String(),
			Detail:  res.Detail,
			Fix:     res.Fix,
		})
	}
	out.OK, out.Warn, out.Fail = r.Counts()
	return out
}

// statusLines flattens `vidra status`'s report the same way. The report's own
// `block` (the verbatim `compose ps` table) is deliberately dropped: the wizard's
// Success step is a summary, and the table belongs to the terminal command whose
// whole job is to print it.
func statusLines(r statusReport) []setupweb.StatusLine {
	out := make([]setupweb.StatusLine, 0, len(r.lines))
	for _, l := range r.lines {
		out = append(out, setupweb.StatusLine{
			Source: l.source,
			Check:  l.check,
			Status: l.status.String(),
			Detail: l.detail,
			Fix:    l.fix,
		})
	}
	return out
}

// Compile-time proof that the setup engine's TLS mode constants are the ones the
// wizard's page offers. The page's list is presentation, but its VALUES are
// posted straight to NormalizeTLSMode; this is the reminder that the two are a
// contract rather than a coincidence.
var _ = [...]string{
	setup.TLSModeACME, setup.TLSModeACMEStaging, setup.TLSModeInternal,
	setup.TLSModeExternal, setup.TLSModePlainHTTP,
}
