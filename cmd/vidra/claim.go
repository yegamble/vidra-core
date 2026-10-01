package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

// runClaim is `vidra claim`: print the link that claims the owner account.
//
// The owner-claim token exists in exactly one place an operator can read it —
// the api's own startup log line — and that line is a trap in three ways: it
// scrolls away under later output, the api re-mints the token on EVERY boot
// while the owner is unclaimed (so every older line in the log is already dead
// and only the NEWEST is valid), and an operator who greps for it and copies the
// first hit pastes a revoked token and gets a 403 that looks like a bug. This
// command reads the logs, takes the LAST such line, and prints the one URL the
// setup wizard consumes.
//
// The token is a one-time credential for the admin account, so it is printed
// ONLY to a terminal. A pipe, a CI step, `| tee` or a redirect to a file would
// carry it into a log that outlives the claim window. The check is positive:
// stdout must be provably a terminal; "cannot tell" refuses.
func runClaim(s streams, args []string) error {
	f, err := parseWrapperFlags("claim", args)
	if err != nil {
		fmt.Fprintf(s.err, "vidra: %s\n\n", err)
		claimUsage(s.err)
		return errReported
	}
	if f.help {
		claimUsage(s.out)
		return nil
	}
	if len(f.rest) > 0 {
		fmt.Fprintf(s.err, "vidra: claim: unexpected argument %q\n\n", f.rest[0])
		claimUsage(s.err)
		return errReported
	}
	if !stdoutIsTerminal(s.out) {
		return errors.New("claim: stdout is not a terminal, so the setup token is not printed — it is a one-time credential for the admin account and a pipe, CI log or file would keep it. Run `vidra claim` interactively")
	}
	dep, path, err := resolve("claim", f, "compose.sh")
	if err != nil {
		return err
	}
	values, err := dep.values()
	if err != nil {
		return fmt.Errorf("claim: %w", err)
	}
	origin := strings.TrimRight(envGet(environMap(theRunner.Environ()), values, "PUBLIC_BASE_URL", ""), "/")
	if origin == "" {
		return fmt.Errorf("claim: PUBLIC_BASE_URL is not set in %s, so there is no address to build the link on", dep.envFile)
	}

	// No --tail: the line was written at the api's last boot, which on a
	// long-running unclaimed instance is further back than any fixed window.
	res, err := theRunner.Capture(context.Background(), dep.bash(path, "logs", "--no-color", "--no-log-prefix", "api"))
	if err != nil {
		return fmt.Errorf("claim: %w", err)
	}
	if res.ExitCode != 0 {
		detail := firstLine(res.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("exit status %d", res.ExitCode)
		}
		return fmt.Errorf("claim: could not read the api logs (%s) — is the stack up? try `vidra status`", detail)
	}
	token, ok := lastClaimToken(res.Stdout)
	if !ok {
		fmt.Fprintf(s.out, "No setup token in the api logs. The owner is probably already claimed, or the api has not started yet.\n"+
			"Check with `vidra status`, and look at the api's boot with `vidra logs api`.\n")
		return errReported
	}
	fmt.Fprintf(s.out, "Claim the owner account at:\n\n  %s/setup/claim#token=%s\n\n", origin, url.QueryEscape(token))
	fmt.Fprintf(s.out, "This token is valid until the api restarts: every restart mints a new one and invalidates this link — run `vidra claim` again if you restart it.\n")
	return nil
}

// claimLogMarkers are the two api log lines that carry a freshly minted token
// (cmd/api/main.go): first run, and a restart on an instance that has users but
// never claimed its owner. Both rotate the token, so the newest of EITHER wins.
var claimLogMarkers = []string{"FIRST-RUN SETUP REQUIRED", "OWNER STILL UNCLAIMED"}

// lastClaimToken scans compose log output for the newest claim line and returns
// its token. Lines are JSON (the api's production format) or slog text, with or
// without compose's `api-1  | ` prefix; the message ends with ": <token>".
func lastClaimToken(logs string) (string, bool) {
	token := ""
	for _, line := range strings.Split(logs, "\n") {
		marked := false
		for _, m := range claimLogMarkers {
			if strings.Contains(line, m) {
				marked = true
			}
		}
		if !marked {
			continue
		}
		if t := tokenFromLogLine(line); t != "" {
			token = t // later lines overwrite: only the newest mint is live
		}
	}
	return token, token != ""
}

func tokenFromLogLine(line string) string {
	msg := line
	if i := strings.IndexByte(line, '{'); i >= 0 {
		var rec struct {
			Msg string `json:"msg"`
		}
		if json.Unmarshal([]byte(line[i:]), &rec) == nil && rec.Msg != "" {
			msg = rec.Msg
		}
	}
	i := strings.LastIndex(msg, ": ")
	if i < 0 {
		return ""
	}
	rest := msg[i+2:]
	// The token is base64url; stopping at the first other byte drops a text-format
	// line's closing quote and any attributes after it.
	end := 0
	for end < len(rest) && isTokenByte(rest[end]) {
		end++
	}
	return rest[:end]
}

func isTokenByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_'
}

// stdoutIsTerminal is a variable so tests can stand in a terminal; the real
// check is stdlib-only and fails CLOSED (anything not provably a terminal is not
// one), the opposite of setup's one-sided stdin rule, because here the wrong
// guess leaks a credential rather than blocking an install.
var stdoutIsTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isTerminal(f)
}

func claimUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: vidra claim [-C <deployment directory>] [--env <env file>]

Prints the link that claims the owner (admin) account on a fresh instance:
<PUBLIC_BASE_URL>/setup/claim#token=<setup token>. The token is read from the
api's startup log (through deploy/compose.sh); the NEWEST such line is the only
valid one, because every api restart mints a new token and revokes the old.

It prints only to a terminal — the token is a one-time admin credential and
must not land in a pipe or a CI log. If the logs hold no token, the owner is
probably already claimed or the api has not started: see `+"`vidra status`"+`.

flags:
  -C, --repo <dir>   the deployment directory (default ".")
      --env <file>   the deployment's env file, exported as ENV_FILE (default %s)
  -h, --help         this text
`, defaultEnvFile)
}
