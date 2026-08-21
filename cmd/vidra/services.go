package main

import (
	"sort"
	"strings"
)

// The one place a name an operator says becomes the name compose knows.
//
// `vidra logs`, `vidra restart` and `vidra status` all share it, deliberately:
// three commands that each accepted a slightly different vocabulary would be
// three things to remember, and the one an operator reaches for at 3am is
// whichever they typed last. The compose names are accepted too — an operator
// reading `docker compose ps` output must be able to paste what they see.

// serviceAliases maps every accepted name to its compose service. The canonical
// names map to themselves so one lookup answers both questions.
//
// The aliases are the words the product uses (the README, the admin UI, the
// runbook) rather than the words the compose file uses: "backend", "web",
// "database", "antivirus", "captions", "tracing" are what an operator calls
// these things before they have read docker-compose.prod.yml.
var serviceAliases = map[string]string{
	"api":     "api",
	"backend": "api",

	"frontend": "frontend",
	"web":      "frontend",
	"ui":       "frontend",

	"search": "search",

	"caddy": "caddy",
	"proxy": "caddy",
	"edge":  "caddy",

	"postgres": "postgres",
	"db":       "postgres",
	"database": "postgres",

	"redis": "redis",
	"cache": "redis",

	"clamav":    "clamav",
	"scan":      "clamav",
	"antivirus": "clamav",

	"whisper":  "whisper",
	"captions": "whisper",

	"rtmp": "rtmp",
	"live": "rtmp",

	"otel":           "otel-collector",
	"otel-collector": "otel-collector",
	"tracing":        "otel-collector",

	"jaeger": "jaeger",

	"ipfs": "ipfs",

	// The one-shots. They are NAMED here rather than left unknown so `vidra
	// restart migrate` can answer "that is a one-shot" instead of "no such
	// service" — the second is true of the sentence and false of the deployment,
	// and it sends the operator looking for a typo they did not make.
	"migrate":        "migrate",
	"search-migrate": "search-migrate",
	"prep-volumes":   "prep-volumes",
}

// oneShots are the services that run to completion during a deploy and exit,
// with the sentence explaining why restarting one is meaningless.
var oneShots = map[string]string{
	"migrate":        "it runs the core migrations once during a deploy and exits",
	"search-migrate": "it runs the search migrations once during a deploy and exits",
	"prep-volumes":   "it fixes volume ownership once during a deploy and exits",
}

// serviceProfiles is the compose profile (or profiles) each service is gated
// behind, as docker-compose.yml and docker-compose.prod.yml declare it. A
// service with none is always in the project.
//
// caddy USED to be that service — "the TLS terminator comes up with every
// production deploy" — and it no longer is: it sits on the `edge` profile, which
// the ENGINE adds unless VIDRA_TLS_MODE=external (setup.EnabledProfiles, the Go
// half of deploy/lib.sh's edge_profile). Leaving it mapped to "no profile" made
// `vidra restart caddy` on an external deployment die at compose with "no such
// service" instead of the clean refusal every other profiled service gets.
//
// This is a transcription, so it can drift from the compose files. It is only
// ever used to REFUSE an action that compose would refuse anyway ("no such
// service" from a --profile it was not given), so the worst a stale entry costs
// is a wrong sentence rather than a wrong deployment. Reading it out of a
// rendered `compose config` instead would cost a subprocess on every restart of
// a service that is obviously running.
var serviceProfiles = map[string][]string{
	"api":            {"core"},
	"postgres":       {"core"},
	"redis":          {"core"},
	"search":         {"core"},
	"migrate":        {"core"},
	"search-migrate": {"core"},
	"prep-volumes":   {"core"},
	"frontend":       {"frontend"},
	"clamav":         {"scan"},
	"whisper":        {"captions"},
	"otel-collector": {"otel"},
	"jaeger":         {"otel"},
	"rtmp":           {"media"},
	"ipfs":           {"ipfs", "full"},
	"caddy":          {"edge"},
}

// composeService resolves what an operator typed to a compose service name.
func composeService(name string) (string, bool) {
	svc, ok := serviceAliases[strings.ToLower(strings.TrimSpace(name))]
	return svc, ok
}

// serviceNames is every accepted name, sorted — the list an unknown one is
// answered with.
func serviceNames() []string {
	out := make([]string, 0, len(serviceAliases))
	for name := range serviceAliases {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// enabledIn reports whether a service is in a deployment that enables these
// compose profiles.
func enabledIn(service string, profiles []string) bool {
	want := serviceProfiles[service]
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		for _, p := range profiles {
			if w == p {
				return true
			}
		}
	}
	return false
}
