package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/vidra/vidra-core/internal/setup"
)

// runRestart is `vidra restart <service>`: bounce one container.
//
// It is deploy/compose.sh restart with THREE refusals in front of it, and each
// one exists because the compose error it replaces sends the operator to the
// wrong place:
//
//   - a one-shot (migrate, search-migrate, prep-volumes) restarts happily and
//     does nothing, because it already ran to completion during the deploy. The
//     operator watching for a fixed schema learns nothing and waits.
//
//   - postgres or redis on a deployment whose env file says they are EXTERNAL:
//     the overlay parks the bundled service on a profile nothing enables, so
//     compose answers "no such service" — which reads as "your deployment is
//     broken" rather than "that database is managed elsewhere, restart it
//     there".
//
//   - a service outside the enabled profiles: same "no such service", same wrong
//     conclusion, when the answer is one key in the env file.
//
// It restarts exactly one service. `vidra restart` with no argument would be a
// whole-stack bounce, which is a deploy without any of a deploy's gates — that
// is `vidra deploy`, and this command deliberately cannot do it.
func runRestart(s streams, args []string) error {
	f, err := parseWrapperFlags("restart", args)
	if err != nil {
		fmt.Fprintf(s.err, "vidra: %s\n\n", err)
		restartUsage(s.err)
		return errReported
	}
	if f.help {
		restartUsage(s.out)
		return nil
	}
	switch len(f.rest) {
	case 1:
	case 0:
		fmt.Fprint(s.err, "vidra: restart: name one service to restart\n\n")
		restartUsage(s.err)
		return errReported
	default:
		return fmt.Errorf("restart: one service at a time (%s were given) — restarting several at once is a deploy without a deploy's gates, which is `vidra deploy`", strings.Join(f.rest, ", "))
	}

	svc, ok := composeService(f.rest[0])
	if !ok {
		return fmt.Errorf("restart: %q is not a service in this deployment. The names it knows are: %s",
			f.rest[0], strings.Join(serviceNames(), ", "))
	}
	if why, isOneShot := oneShots[svc]; isOneShot {
		return fmt.Errorf("restart: %s is a one-shot, not a service — %s, so there is nothing running to restart. `vidra deploy` runs it again as part of a deploy", svc, why)
	}

	dep, path, err := resolve("restart", f, "compose.sh")
	if err != nil {
		return err
	}
	// The env file is read BEFORE the restart rather than after a compose error,
	// because the two refusals below are the whole reason this command is not
	// just an alias.
	values, err := dep.values()
	if err != nil {
		return fmt.Errorf("restart: %s — it is read to tell which services this deployment actually runs", err)
	}
	if external, key := externalDatastore(svc, values); external {
		return fmt.Errorf("restart: %s is not part of this deployment — %s is true in %s, so it is managed outside the stack and the bundled container is disabled by its compose overlay. Restart it wherever it is hosted", svc, key, dep.envFile)
	}
	profiles := setup.EnabledProfiles(values)
	if !enabledIn(svc, profiles) {
		return fmt.Errorf("restart: %s is not enabled in this deployment — it lives behind the %s compose profile, and VIDRA_COMPOSE_PROFILES (plus EXTRA_COMPOSE_PROFILES) enables %s. Add the profile there and run `vidra deploy` to start it",
			svc, quoteList(serviceProfiles[svc], " or "), quoteList(profiles, ", "))
	}
	if svc == "caddy" {
		// Said BEFORE the restart, not after, because after is too late to
		// choose the other command. A Caddyfile.local edit does not need this:
		// deploy.sh follows `up -d` with a graceful `caddy reload`, which swaps
		// the config with no dropped connection.
		fmt.Fprintln(s.out, "note: an edit to deploy/Caddyfile.local is picked up by `vidra deploy` too, through a graceful `caddy reload` that drops nothing. A restart takes the TLS edge — the whole site — down for the second or two the container takes to come back.")
	}
	return theRunner.Passthrough(dep.bash(path, "restart", svc), s)
}

// externalDatastore reports whether this service is the bundled container for a
// datastore the operator has moved off the box, and which key says so.
func externalDatastore(svc string, values map[string]string) (bool, string) {
	key := ""
	switch svc {
	case "postgres":
		key = "VIDRA_EXTERNAL_POSTGRES"
	case "redis":
		key = "VIDRA_EXTERNAL_REDIS"
	default:
		return false, ""
	}
	// setup.IsTrue, not `== "true"`: the env file is operator-edited and
	// yes/1/on all mean yes to deploy/lib.sh's is_true. The two must agree, or
	// vidra restarts a container the deploy scripts have disabled.
	return setup.IsTrue(strings.TrimSpace(values[key])), key
}

func quoteList(items []string, sep string) string {
	if len(items) == 0 {
		return "nothing"
	}
	quoted := make([]string, 0, len(items))
	for _, s := range items {
		quoted = append(quoted, fmt.Sprintf("%q", s))
	}
	return strings.Join(quoted, sep)
}

func restartUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: vidra restart [-C <deployment directory>] [--env <env file>] <service>

Restarts ONE container, through deploy/compose.sh, in the same compose project
the deploy scripts address. It pulls nothing, migrates nothing and changes no
configuration: this is for a service that has wedged, not for shipping a change.
Use `+"`vidra deploy`"+` for that.

Service names are the product's own — api/backend, frontend/web, search,
caddy/proxy, postgres/db, redis/cache, clamav/scan, whisper/captions, rtmp/live,
otel/tracing, jaeger, ipfs — and the raw compose names work too.

It refuses, with the reason, for a one-shot (migrate, search-migrate,
prep-volumes), for postgres/redis on a deployment that has moved them off the
box, and for a service the deployment's compose profiles do not enable.

flags:
  -C, --repo <dir>   the deployment directory (default ".")
      --env <file>   the deployment's env file, exported as ENV_FILE (default %s)
  -h, --help         this text
`, defaultEnvFile)
}
