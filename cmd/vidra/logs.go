package main

import (
	"fmt"
	"io"
)

// runLogs is `vidra logs [service…]`: follow the deployment's logs.
//
// It is deploy/compose.sh with a fixed tail, which is what `make prod-logs`
// already is — the same -f --tail=100 — so an operator moving between the meta
// repo's Makefile and this CLI sees the same 100 lines. compose.sh is the
// sanctioned non-gating passthrough: it assembles the SAME -f/--profile/--env
// chain the deploy scripts do (deploy/lib.sh), so what is followed here is the
// project that is actually running, and its own log lines go to stderr so stdout
// stays the logs and nothing else.
//
// Nothing is validated. An unknown name is passed through and compose answers
// "no such service" — that is one clear error from the tool that owns the
// question, and it is also what keeps this working for a service the alias table
// has not heard of (a dev-only minio, a second IPFS node).
func runLogs(s streams, args []string) error {
	f, err := parseWrapperFlags("logs", args)
	if err != nil {
		fmt.Fprintf(s.err, "vidra: %s\n\n", err)
		logsUsage(s.err)
		return errReported
	}
	if f.help {
		logsUsage(s.out)
		return nil
	}
	dep, path, err := resolve("logs", f, "compose.sh")
	if err != nil {
		return err
	}
	tail := []string{"logs", "-f", "--tail=100"}
	for _, name := range f.rest {
		if svc, ok := composeService(name); ok {
			tail = append(tail, svc)
			continue
		}
		tail = append(tail, name)
	}
	return theRunner.Passthrough(dep.bash(path, tail...), s)
}

func logsUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: vidra logs [-C <deployment directory>] [--env <env file>] [service…]

Follows the deployment's logs from the last 100 lines — the same view as `+"`make\nprod-logs`"+`, through deploy/compose.sh, so it addresses the same compose project
the deploy scripts do. Ctrl-C stops it; nothing is changed.

With no service it follows all of them. Service names are the product's own —
api/backend, frontend/web, search, caddy/proxy, postgres/db, redis/cache,
clamav/scan, whisper/captions, rtmp/live, otel/tracing, jaeger, ipfs — and the
raw compose names work too.

flags:
  -C, --repo <dir>   the deployment directory (default ".")
      --env <file>   the deployment's env file, exported as ENV_FILE (default %s)
  -h, --help         this text
`, defaultEnvFile)
}
