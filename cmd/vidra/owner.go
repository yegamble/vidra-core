package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// runOwner is `vidra owner reset`: print a one-time link that sets the owner's
// password when the owner has lost it and no reset mail can be delivered.
//
// It runs `api owner-recovery` as a one-shot of the api service: that needs the
// api's whole environment (config.Load), which the migrate one-shot lacks. The
// image ENTRYPOINT is ["/app/api"] and no compose file overrides it on api, so
// `run … api owner-recovery` is `/app/api owner-recovery`. --no-deps: postgres
// is up, and the default would re-run the migrate and prep-volumes one-shots.
// -T: a pty would merge stderr into the captured one-line stdout.
//
// Like `vidra claim`, the link is printed only to a terminal, and that check
// runs BEFORE the one-shot: minting is the side effect, and a refusal must not
// leave a live, audited token nobody saw.
func runOwner(s streams, args []string) error {
	if len(args) == 0 || args[0] != "reset" {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			ownerUsage(s.out)
			return nil
		}
		ownerUsage(s.err)
		return errReported
	}
	fs := flag.NewFlagSet("owner reset", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var removeMFA, yes bool
	var repo, envFile string
	fs.BoolVar(&removeMFA, "remove-mfa", false, "")
	fs.BoolVar(&yes, "yes", false, "")
	fs.StringVar(&repo, "C", ".", "")
	fs.StringVar(&repo, "repo", ".", "")
	fs.StringVar(&envFile, "env", defaultEnvFile, "")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			ownerUsage(s.out)
			return nil
		}
		fmt.Fprintf(s.err, "vidra: owner reset: %s\n\n", err)
		ownerUsage(s.err)
		return errReported
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(s.err, "vidra: owner reset: unexpected argument %q\n\n", fs.Arg(0))
		ownerUsage(s.err)
		return errReported
	}
	flags := wrapperFlags{repo: repo, envFile: envFile}
	fs.Visit(func(f *flag.Flag) { flags.explicit = flags.explicit || f.Name == "env" })
	if !stdoutIsTerminal(s.out) {
		return errors.New("owner reset: stdout is not a terminal, so the reset link is not minted — it is a one-time credential for the owner account and a pipe, CI log or file would keep it. Run `vidra owner reset` interactively")
	}
	dep, path, err := resolve("owner reset", flags, "compose.sh")
	if err != nil {
		return err
	}
	if removeMFA && !yes {
		// Typed word, not y/N: removing a second factor is the step that turns
		// "someone saw the link" into "someone owns the instance".
		fmt.Fprint(s.out, "--remove-mfa deletes the owner's authenticator and recovery codes and signs the owner out everywhere.\n"+
			"Anyone who gets the link can then take the account with a password alone.\n"+
			"Type 'remove' to continue: ")
		line, _ := bufio.NewReader(s.in).ReadString('\n')
		fmt.Fprintln(s.out)
		if strings.TrimSpace(line) != "remove" {
			return errors.New("owner reset: aborted, nothing was run")
		}
	}

	argv := []string{"run", "--rm", "--no-deps", "-T", "api", "owner-recovery"}
	if removeMFA {
		argv = append(argv, "--remove-mfa")
	}
	res, err := theRunner.Capture(context.Background(), dep.bash(path, argv...))
	if err != nil {
		return fmt.Errorf("owner reset: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("owner reset: %s", ownerFailureLine(res))
	}
	link := strings.TrimSpace(res.Stdout)
	// Exactly one line is the contract; anything else is not shown.
	if strings.Contains(link, "\n") || !strings.Contains(link, "/reset-password/confirm?token=") {
		return errors.New("owner reset: the api printed something other than one reset link, so nothing is shown — check `vidra logs api` and re-run")
	}
	fmt.Fprintf(s.out, "Set the owner's password at:\n\n  %s\n\n", link)
	if ctx := strings.TrimSpace(res.Stderr); ctx != "" {
		fmt.Fprintln(s.out, ctx)
	}
	return nil
}

// ownerFailureLine: the subcommand's own `owner-recovery: …` line (compose
// prints container chatter around it), else the last stderr line, else the exit.
func ownerFailureLine(res execResult) string {
	last := ""
	for _, line := range strings.Split(res.Stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "owner-recovery: "); ok {
			return truncate(rest, 400)
		}
		last = line
	}
	if last == "" {
		return fmt.Sprintf("exit status %d — is the stack up? try `vidra status`", res.ExitCode)
	}
	return truncate(last, 400)
}

func ownerUsage(w io.Writer) {
	fmt.Fprint(w, `usage: vidra owner reset [--remove-mfa] [--yes] [-C <deployment directory>] [--env <env file>]

Prints a one-time link that sets the owner's password, for an owner who lost it
when no reset mail can be delivered. Anyone with the link can take the owner
account, so it prints only to a terminal, never a pipe or a CI log.

flags:
      --remove-mfa   also remove the owner's authenticator and recovery codes and
                     sign them out everywhere; asks you to type 'remove' first
      --yes          skip that confirmation (still terminal only)
  -C, --repo <dir>   the deployment directory (default ".")
      --env <file>   the deployment's env file, exported as ENV_FILE (default `+defaultEnvFile+`)
  -h, --help         this text
`)
}
