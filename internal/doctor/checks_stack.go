package doctor

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/vidra/vidra-core/internal/setup"
)

// minComposeMajor/minComposeMinor is the floor docker-compose.prod.yml needs.
// It is not a nicety: that file removes the base compose file's `ports:`
// publishes with the `!reset` merge tag and replaces others with `!override`,
// and a Compose that predates those tags does not error on them — it IGNORES
// them, merges the sequences, and brings Postgres and Redis up published on
// 0.0.0.0 on a public droplet. The failure is silent by construction, which is
// why the version is checked rather than assumed.
const (
	minComposeMajor = 2
	minComposeMinor = 24
)

func checkComposeVersion(ctx context.Context, s *state) []Finding {
	out, err := s.opt.Host.Run(ctx, s.root, "docker", "compose", "version", "--short")
	if err != nil {
		return []Finding{skipf("docker is not on this host's PATH, so its version could not be read")}
	}
	if out.ExitCode != 0 {
		return []Finding{skipf("`docker compose version` failed: " + firstLine(out.Stderr))}
	}
	raw := strings.TrimSpace(strings.TrimPrefix(out.Stdout, "v"))
	major, minor, ok := parseVersion(raw)
	if !ok {
		return []Finding{warnf(
			fmt.Sprintf("docker compose reports version %q, which could not be compared against the %d.%d floor", truncate(raw, 40), minComposeMajor, minComposeMinor),
			fmt.Sprintf("check by hand that it is at least %d.%d: docker-compose.prod.yml uses the !reset/!override merge tags, and an older Compose ignores them silently", minComposeMajor, minComposeMinor))}
	}
	if major < minComposeMajor || (major == minComposeMajor && minor < minComposeMinor) {
		return []Finding{failf(
			fmt.Sprintf("docker compose %s is older than %d.%d", raw, minComposeMajor, minComposeMinor),
			"upgrade the docker-compose-plugin package before deploying: docker-compose.prod.yml removes the base file's port publishes with the `!reset` merge tag, and a Compose this old does not fail on it — it ignores it and leaves Postgres and Redis listening on 0.0.0.0")}
	}
	return []Finding{okf(fmt.Sprintf("docker compose %s (>= %d.%d, so the prod overlay's !reset/!override merge tags are honoured)", raw, minComposeMajor, minComposeMinor))}
}

// parseVersion reads the leading major.minor of a version string.
func parseVersion(v string) (major, minor int, ok bool) {
	fields := strings.SplitN(strings.TrimSpace(v), ".", 3)
	if len(fields) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(strings.TrimFunc(fields[1], func(r rune) bool { return r < '0' || r > '9' }))
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// publicPort is one deliberate hole in the droplet's firewall: a service, a
// port, and the profile that has to be on for it to exist.
//
// Everything not on this list that binds a non-loopback address is a finding.
// The list is short because the design is: Caddy terminates TLS and everything
// else is reached over the compose network, so a published port is either one of
// these three or a mistake.
var publicPorts = []struct {
	service string
	port    int
	profile string // "" means every deployment
	// warn marks a hole that is deliberate but still worth saying out loud on
	// every run, because it is one an operator can forget they opened.
	warn bool
	why  string
}{
	{service: "caddy", port: 80, why: "Let's Encrypt's HTTP-01 challenge needs it, and Caddy redirects the rest to 443"},
	{service: "caddy", port: 443, why: "the site"},
	{service: "rtmp", port: 1935, profile: "media", why: "the RTMP live-ingest edge is deliberately public — streamers connect to it directly"},
	{service: "ipfs", port: 4001, profile: "ipfs", warn: true, why: "the libp2p swarm port is deliberately public; without it the node is outbound-only"},
}

// checkPublishedPorts is the exposure audit, and it is the reason doctor renders
// the compose model rather than reading the compose files. `docker-compose.yml`
// publishes Postgres, Redis, MinIO, ClamAV, Whisper, the OTLP receivers and the
// Jaeger UI on every interface, because it is the DEVELOPMENT definition;
// docker-compose.prod.yml takes some of those publishes away with `!reset` and
// narrows others to 127.0.0.1. Which of the two won for a given service, on a
// given host, with a given profile set, is a question only the RENDERED model
// answers — and it is exactly the question an operator cannot answer by reading.
func checkPublishedPorts(ctx context.Context, s *state) []Finding {
	model, why := s.composeModel(ctx)
	if model == nil {
		return []Finding{skipf(why)}
	}
	profiles := map[string]bool{}
	for _, p := range setup.EnabledProfiles(s.vars) {
		profiles[p] = true
	}

	var findings []Finding
	loopback := 0
	names := make([]string, 0, len(model.Services))
	for name := range model.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, p := range model.Services[name].Ports {
			if _, _, published := publishedRange(p); !published {
				// A container port with no host mapping is reachable on the compose
				// network and nowhere else, which is the shape every internal service
				// is supposed to have.
				continue
			}
			if loopbackIP(p.HostIP) {
				loopback++
				continue
			}
			allowed, warn, why := classifyPort(name, p, profiles)
			switch {
			case allowed && !warn:
			case allowed && warn:
				findings = append(findings, warnf(
					fmt.Sprintf("%s publishes %s on every interface — %s", name, portLabel(p), why),
					fmt.Sprintf("that hole is intentional, so make it deliberate on the host too: allow %s in the cloud firewall from the sources that need it, and nowhere else", portLabel(p))))
			default:
				findings = append(findings, failf(
					fmt.Sprintf("%s publishes %s on every interface, which puts it on the public internet", name, portLabel(p)),
					fmt.Sprintf("nothing but Caddy (80/443) should be reachable from outside this host — the api, the frontend and the search service are reached over the compose network. Check docker-compose.prod.yml still resets %s's `ports:` (that needs Compose >= %d.%d), and close the port in the cloud firewall meanwhile", name, minComposeMajor, minComposeMinor)))
			}
		}
	}
	if len(findings) > 0 {
		return findings
	}
	return []Finding{okf(fmt.Sprintf("only the expected ports face the internet (%s); %d further publish(es) are bound to 127.0.0.1", expectedList(profiles), loopback))}
}

func classifyPort(service string, p composePort, profiles map[string]bool) (allowed, warn bool, why string) {
	for _, e := range publicPorts {
		if e.service != service || !publishesPort(p, e.port) {
			continue
		}
		if e.profile != "" && !profiles[e.profile] {
			// The port is on the list, but the profile that justifies it is off —
			// so this publish is not the one the list is about.
			return false, false, ""
		}
		return true, e.warn, e.why
	}
	return false, false, ""
}

// publishesPort reports whether a rendered publish covers port. `published` is a
// string because compose allows a RANGE ("8000-8010"), and a check that only
// understood single values would wave a range straight through.
func publishesPort(p composePort, port int) bool {
	lo, hi, ok := publishedRange(p)
	if !ok {
		return false
	}
	return port >= lo && port <= hi
}

func publishedRange(p composePort) (lo, hi int, ok bool) {
	raw := strings.TrimSpace(p.Published)
	if raw == "" {
		// No host port at all: the container port is exposed to the compose
		// network only, which is not a publish.
		return 0, 0, false
	}
	first, last, isRange := strings.Cut(raw, "-")
	lo, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil {
		return 0, 0, false
	}
	if !isRange {
		return lo, lo, true
	}
	hi, err = strconv.Atoi(strings.TrimSpace(last))
	if err != nil {
		return lo, lo, true
	}
	return lo, hi, true
}

func portLabel(p composePort) string {
	label := strings.TrimSpace(p.Published)
	if label == "" {
		label = strconv.Itoa(p.Target)
	}
	if proto := strings.TrimSpace(p.Protocol); proto != "" && proto != "tcp" {
		label += "/" + proto
	}
	return label
}

func expectedList(profiles map[string]bool) string {
	var out []string
	for _, e := range publicPorts {
		if e.profile != "" && !profiles[e.profile] {
			continue
		}
		out = append(out, fmt.Sprintf("%s %d", e.service, e.port))
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ", ")
}

// checkLogCaps looks for a service whose json-file log can grow until it fills
// the disk. The daemon's default is UNBOUNDED, and an api container logging one
// line per request fills a 25 GB droplet in weeks — at which point Postgres
// stops being able to write and the failure looks like a database problem.
func checkLogCaps(ctx context.Context, s *state) []Finding {
	model, why := s.composeModel(ctx)
	if model == nil {
		return []Finding{skipf(why)}
	}
	daemonDefault, _ := s.dockerInfo(ctx)

	var uncapped []string
	names := make([]string, 0, len(model.Services))
	for name := range model.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		lg := model.Services[name].Logging
		driver := "json-file"
		if lg != nil && strings.TrimSpace(lg.Driver) != "" {
			driver = strings.TrimSpace(lg.Driver)
		} else if d := strings.TrimSpace(daemonDefault.LoggingDriver); d != "" {
			driver = d
		}
		// Only the file-based drivers grow on this disk. A service shipping its
		// logs elsewhere (journald, fluentd, awslogs) is somebody else's rotation
		// problem and not a finding here.
		if driver != "json-file" && driver != "local" {
			continue
		}
		if lg.option("max-size") == "" {
			uncapped = append(uncapped, name)
		}
	}
	if len(uncapped) == 0 {
		return []Finding{okf(fmt.Sprintf("every service caps its container log (%d service(s), driver json-file with max-size)", len(names)))}
	}
	return []Finding{warnf(
		fmt.Sprintf("%s log to an uncapped json-file, which grows until the disk is full", strings.Join(uncapped, ", ")),
		"give them the prod overlay's `logging` anchor (`driver: json-file` with `max-size` and `max-file`), or set the same defaults daemon-wide in /etc/docker/daemon.json — an unrotated api log fills a small droplet in weeks, and the first symptom is Postgres failing to write")}
}

// checkDevOverride catches the deployment brought up with a bare
// `docker compose up`.
//
// docker-compose.override.yml is AUTO-LOADED by every plain compose invocation
// in the repo, and it exists to make local development pleasant: among other
// things it sets RATE_LIMIT_ENABLED=false, because browser HMR blows through the
// per-IP budget. On a production host that is an unmetered public API, and
// absolutely nothing about the running stack looks wrong — which is why the
// evidence has to come from the containers' own labels rather than from what
// anyone remembers typing.
func checkDevOverride(ctx context.Context, s *state) []Finding {
	running, why := s.containers(ctx)
	const overrideFile = "docker-compose.override.yml"
	if why != "" {
		// Fall back to the static half of the check: an override file next to a
		// production env file is at least worth knowing about.
		return []Finding{s.overridePresenceFinding(overrideFile, "the running containers could not be inspected ("+why+")")}
	}
	var offenders []string
	for _, c := range running {
		for _, f := range c.ConfigFiles {
			if strings.HasSuffix(f, overrideFile) {
				offenders = append(offenders, c.Service)
				break
			}
		}
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		return []Finding{failf(
			fmt.Sprintf("the running stack was brought up with %s loaded (%s), so it is running with development overrides — rate limiting is off", overrideFile, strings.Join(dedupe(offenders), ", ")),
			"bring the stack up through the deploy script, which names its files explicitly and never auto-loads the override: `./deploy/deploy.sh`. The chain it uses is `"+s.composeCommand("up", "-d")+"`")}
	}
	if len(running) == 0 {
		return []Finding{s.overridePresenceFinding(overrideFile, "no compose containers are running on this host")}
	}
	return []Finding{okf(fmt.Sprintf("the running stack does not load %s (%d container(s) checked)", overrideFile, len(running)))}
}

// overridePresenceFinding is the static half of the dev-override check, for when
// there is nothing running to inspect. It is a ⚠ note rather than a skip when
// the trap is actually present: an override file beside a production env file is
// a bare `docker compose up` away from being loaded.
func (s *state) overridePresenceFinding(overrideFile, reason string) Finding {
	if _, err := s.opt.Host.Stat(s.path(overrideFile)); err != nil {
		return skipf(reason)
	}
	if s.envErr != nil {
		return skipf(reason)
	}
	return warnf(
		fmt.Sprintf("%s (nothing is running to check), but %s is present next to the production env file %s — a bare `docker compose up` on this host would load it and turn rate limiting off", reason, overrideFile, s.envRel),
		"always bring this stack up with `./deploy/deploy.sh`, which names its compose files explicitly: `"+s.composeCommand("up", "-d")+"`")
}

// checkStrayEnv looks for vidra-core/.env.
//
// The meta compose file INCLUDES vidra-core/docker-compose.yml, and Compose
// resolves an included model's ${VAR} substitutions against the .env file in the
// INCLUDED file's own directory — a file vidra-core's own README tells its
// developers to create. It sets a localhost DATABASE_URL, POSTGRES_PASSWORD=vidra
// and the dev JWT constant, and it takes precedence over `--env-file
// env/production.env`.
func checkStrayEnv(_ context.Context, s *state) []Finding {
	const rel = "vidra-core/.env"
	path := s.path("vidra-core", ".env")
	if _, err := s.opt.Host.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return []Finding{okf("no " + rel + " to poison the included model's variable substitutions")}
		}
		return []Finding{skipf(rel + " could not be stat'd")}
	}
	return []Finding{failf(
		rel+" exists on a deployment host: Compose resolves the INCLUDED vidra-core model's ${VAR} substitutions against it, ahead of --env-file "+s.envRel,
		"remove it (`rm "+rel+"`) — it is a development file, and here it hands the api a localhost DSN, the dev POSTGRES_PASSWORD and the built-in dev JWT_SECRET. Confirm the fix with `"+s.composeCommand("config")+" | grep -E 'DATABASE_URL|REDIS_URL'`: every DSN must name the compose services")}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
