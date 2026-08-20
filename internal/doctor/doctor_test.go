package doctor

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/dbmigrate"
)

// The whole point of the command, in one test: a deployment with nothing wrong
// with it produces no ✗ and exits 0.
func TestHealthyDeploymentPassesEverything(t *testing.T) {
	rep := run(t, newFakeHost(), nil)
	if rep.Failed() {
		for _, r := range rep.Results {
			if r.Status == StatusFail {
				t.Errorf("✗ %s: %s", r.Check, r.Detail)
			}
		}
		t.Fatalf("a healthy deployment failed")
	}
	// Every registered check has to have said SOMETHING: a check that silently
	// produces nothing is a hole in the report that looks like a pass.
	seen := map[string]bool{}
	for _, r := range rep.Results {
		seen[r.Check] = true
		if strings.TrimSpace(r.Detail) == "" {
			t.Errorf("%s produced a finding with no detail", r.Check)
		}
	}
	for _, c := range checks {
		if !seen[c.name] {
			t.Errorf("check %q produced no result at all", c.name)
		}
	}
	if ok, _, fail := rep.Counts(); ok == 0 || fail != 0 {
		t.Errorf("counts = %d ok / %d fail, want passes and no failures", ok, fail)
	}
}

// A raw Go error must never reach the operator. This is the one contract that
// cannot be tested check by check, because it is about all of them at once.
func TestNoFindingLeaksAGoError(t *testing.T) {
	h := newFakeHost()
	// Break as much as possible at once: no env file, no daemon, no systemd.
	delete(h.files, filepath.Join(testRoot, "env/production.env"))
	delete(h.files, filepath.Join(testRoot, "deploy/Caddyfile.local"))
	delete(h.files, filepath.Join(testRoot, "backups/last_success"))
	h.paths = map[string]string{}
	h.respond = func(string, []string) (Output, error) { return Output{}, errNotInstalled }

	rep := run(t, h, nil)
	// These are the shapes Go errors arrive in. None of them belongs in a report
	// an operator is expected to act on.
	for _, r := range rep.Results {
		for _, text := range []string{r.Detail, r.Fix} {
			for _, leak := range []string{"*fs.PathError", "no such file or directory", "exec:", "&errors.errorString", "%!"} {
				if strings.Contains(text, leak) {
					t.Errorf("%s leaked a raw error (%q): %s", r.Check, leak, text)
				}
			}
		}
	}
	// And it must still be a usable report rather than a crash or a blank.
	if len(rep.Results) < len(checks) {
		t.Errorf("got %d results for %d checks", len(rep.Results), len(checks))
	}
}

var errNotInstalled = &notInstalled{}

type notInstalled struct{}

func (*notInstalled) Error() string { return "exec: \"docker\": executable file not found in $PATH" }

func TestComposeVersion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		out       Output
		err       error
		want      Status
		detailHas string
		fixHas    string
	}{
		{name: "current", out: Output{Stdout: "2.29.7\n"}, want: StatusOK, detailHas: "2.29.7"},
		{name: "v5 is newer than 2.24", out: Output{Stdout: "5.1.0\n"}, want: StatusOK, detailHas: "5.1.0"},
		{name: "exactly the floor", out: Output{Stdout: "2.24.0\n"}, want: StatusOK},
		// The failure this check exists for: an older Compose does not ERROR on
		// the prod overlay's merge tags, it ignores them.
		{name: "too old", out: Output{Stdout: "2.23.9\n"}, want: StatusFail,
			detailHas: "older than 2.24", fixHas: "listening on 0.0.0.0"},
		{name: "much too old", out: Output{Stdout: "1.29.2\n"}, want: StatusFail, detailHas: "older than 2.24", fixHas: "!reset"},
		{name: "leading v", out: Output{Stdout: "v2.30.1\n"}, want: StatusOK, detailHas: "2.30.1"},
		{name: "unparseable", out: Output{Stdout: "dev-build\n"}, want: StatusWarn, detailHas: "could not be compared", fixHas: "at least 2.24"},
		{name: "not installed", err: errNotInstalled, want: StatusWarn, detailHas: "skipped:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFakeHost()
			h.respond = func(name string, args []string) (Output, error) {
				if name == "docker" && strings.Join(args, " ") == "compose version --short" {
					return tc.out, tc.err
				}
				return h.healthyRespond(name, args)
			}
			wantFinding(t, one(t, only(t, "compose version", h, nil)), tc.want, tc.detailHas, tc.fixHas)
		})
	}
}

// The exposure audit. Its whole value is that it reads the RENDERED model: the
// base compose file publishes Postgres, Redis, ClamAV, Whisper and the Jaeger UI
// on every interface, and whether the prod overlay's `!reset` won is a question
// only the render answers.
func TestPublishedPorts(t *testing.T) {
	model := func(services string) string {
		return `{"name":"vidra","services":{` + services + `}}`
	}
	const caddy = `"caddy":{"ports":[{"target":80,"published":"80","protocol":"tcp"},{"target":443,"published":"443","protocol":"tcp"}]}`

	for _, tc := range []struct {
		name      string
		services  string
		profiles  string
		want      Status
		detailHas string
		fixHas    string
	}{
		{
			name:     "only caddy faces the internet",
			services: caddy + `,"api":{"ports":[{"target":8080,"published":"8080","host_ip":"127.0.0.1"}]}`,
			want:     StatusOK, detailHas: "caddy 80, caddy 443",
		},
		{
			// The exact failure an old Compose produces.
			name:     "postgres published on every interface",
			services: caddy + `,"postgres":{"ports":[{"target":5432,"published":"5432","protocol":"tcp"}]}`,
			want:     StatusFail, detailHas: "postgres publishes 5432 on every interface", fixHas: "docker-compose.prod.yml still resets postgres's `ports:`",
		},
		{
			name:     "the jaeger UI is not a public service",
			services: caddy + `,"jaeger":{"ports":[{"target":16686,"published":"16686"}]}`,
			want:     StatusFail, detailHas: "jaeger publishes 16686",
		},
		{
			// The RTMP edge IS deliberately public — but only when the profile that
			// creates it is on.
			name:     "rtmp with the media profile is expected",
			services: caddy + `,"rtmp":{"ports":[{"target":1935,"published":"1935"}]}`,
			profiles: "core frontend media",
			want:     StatusOK, detailHas: "rtmp 1935",
		},
		{
			name:     "1935 without the media profile is not the RTMP edge",
			services: caddy + `,"rtmp":{"ports":[{"target":1935,"published":"1935"}]}`,
			profiles: "core frontend",
			want:     StatusFail, detailHas: "rtmp publishes 1935",
		},
		{
			// Deliberate, documented, and still worth saying on every run.
			name:     "the ipfs swarm port is a warning",
			services: caddy + `,"ipfs":{"ports":[{"target":4001,"published":"4001"},{"target":4001,"published":"4001","protocol":"udp"}]}`,
			profiles: "core frontend ipfs",
			want:     StatusWarn, detailHas: "ipfs publishes 4001", fixHas: "cloud firewall",
		},
		{
			// A range is a publish too, and a check that only understood single
			// values would wave it straight through.
			name:     "a published range is still an exposure",
			services: caddy + `,"whisper":{"ports":[{"target":8080,"published":"8090-8095"}]}`,
			want:     StatusFail, detailHas: "whisper publishes 8090-8095",
		},
		{
			name:     "a container port with no host publish is not an exposure",
			services: caddy + `,"search":{"ports":[{"target":8080,"published":""}]}`,
			want:     StatusOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFakeHost()
			if tc.profiles != "" {
				h.files[filepath.Join(testRoot, "env/production.env")] = strings.Replace(healthyEnv, "VIDRA_COMPOSE_PROFILES=core frontend", "VIDRA_COMPOSE_PROFILES="+tc.profiles, 1)
			}
			h.respond = func(name string, args []string) (Output, error) {
				if name == "docker" && strings.Contains(strings.Join(args, " "), "config --format json") {
					return Output{Stdout: model(tc.services)}, nil
				}
				return h.healthyRespond(name, args)
			}
			findings := only(t, "published ports", h, nil)
			if tc.want == StatusOK {
				wantFinding(t, one(t, findings), StatusOK, tc.detailHas, "")
				return
			}
			if !hasStatus(findings, tc.want) {
				t.Fatalf("statuses = %v, want a %s among them: %+v", statuses(findings), tc.want, findings)
			}
			if tc.detailHas != "" && !hasAny(findings, tc.detailHas) {
				t.Errorf("findings do not mention %q: %+v", tc.detailHas, findings)
			}
			if tc.fixHas != "" && !hasAny(findings, tc.fixHas) {
				t.Errorf("findings do not suggest %q: %+v", tc.fixHas, findings)
			}
		})
	}
}

// The port audit must run the DEPLOY's chain, not one of its own: an audit
// against a different set of overlays and profiles proves nothing about what
// deploy.sh will bring up.
func TestPublishedPortsRendersTheDeploysOwnChain(t *testing.T) {
	h := newFakeHost()
	h.files[filepath.Join(testRoot, "env/production.env")] = strings.Replace(
		strings.Replace(healthyEnv, "VIDRA_EXTERNAL_REDIS=false", "VIDRA_EXTERNAL_REDIS=true\nREDIS_URL=rediss://:pw@r.example.net:6379/0\nSEARCH_REDIS_URL=rediss://:pw@r.example.net:6379/1", 1),
		"VIDRA_COMPOSE_PROFILES=core frontend", "VIDRA_COMPOSE_PROFILES=core frontend ipfs", 1)
	only(t, "published ports", h, nil)

	var rendered string
	for _, c := range h.commands() {
		if strings.Contains(c, "config --format json") {
			rendered = c
		}
	}
	if rendered == "" {
		t.Fatal("the check never rendered a compose model")
	}
	for _, want := range []string{
		"-f docker-compose.yml", "-f docker-compose.prod.yml",
		"-f docker-compose.external-redis.yml", // the env file says the redis is managed
		"--env-file env/production.env",
		"--profile core", "--profile frontend", "--profile ipfs",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered chain is missing %q: %s", want, rendered)
		}
	}
	if strings.Contains(rendered, "external-postgres") {
		t.Errorf("the chain added the postgres overlay for a bundled database: %s", rendered)
	}
}

func TestStrayCoreEnvFile(t *testing.T) {
	h := newFakeHost()
	wantFinding(t, one(t, only(t, "stray vidra-core/.env", h, nil)), StatusOK, "no vidra-core/.env", "")

	h.files[filepath.Join(testRoot, "vidra-core/.env")] = "DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra\n"
	wantFinding(t, one(t, only(t, "stray vidra-core/.env", h, nil)), StatusFail,
		"resolves the INCLUDED vidra-core model's ${VAR} substitutions against it", "rm vidra-core/.env")
}

func TestCaddyfile(t *testing.T) {
	for _, tc := range []struct {
		name      string
		content   string
		absent    bool
		want      Status
		detailHas string
		fixHas    string
	}{
		{name: "matches PUBLIC_BASE_URL", content: healthyCaddyfile, want: StatusOK, detailHas: "serves video.example.org"},
		{
			name: "missing", absent: true, want: StatusFail,
			detailHas: "is missing", fixHas: "created as an empty DIRECTORY",
		},
		{
			// deploy.sh refuses this too — Caddy would order a certificate for the
			// placeholder and spend the host's validation budget failing.
			name:      "still names the placeholder",
			content:   "example.com {\n\treverse_proxy api:8080\n}\n",
			want:      StatusFail,
			detailHas: "placeholder domain example.com", fixHas: "vidra setup --domain",
		},
		{
			name:      "a different host from PUBLIC_BASE_URL",
			content:   "other.example.org {\n\treverse_proxy api:8080\n}\n",
			want:      StatusFail,
			detailHas: "serves other.example.org but PUBLIC_BASE_URL is https://video.example.org",
			fixHas:    "federation actor ids",
		},
		{
			// A comment about example.com is prose, not configuration: the renderer
			// deliberately leaves it, and deploy.sh greps non-comment lines only.
			name:    "example.com in a comment is fine",
			content: "# the template's site block says example.com\nvideo.example.org {\n\treverse_proxy api:8080\n}\n",
			want:    StatusOK,
		},
		{
			name:      "no site address at all",
			content:   "{\n\temail ops@example.org\n}\n",
			want:      StatusWarn,
			detailHas: "no site address could be found", fixHas: "vidra setup",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFakeHost()
			path := filepath.Join(testRoot, "deploy/Caddyfile.local")
			if tc.absent {
				delete(h.files, path)
			} else {
				h.files[path] = tc.content
			}
			findings := only(t, "reverse proxy", h, nil)
			if !hasStatus(findings, tc.want) {
				t.Fatalf("statuses = %v, want a %s: %+v", statuses(findings), tc.want, findings)
			}
			if tc.detailHas != "" && !hasAny(findings, tc.detailHas) {
				t.Errorf("findings do not mention %q: %+v", tc.detailHas, findings)
			}
			if tc.fixHas != "" && !hasAny(findings, tc.fixHas) {
				t.Errorf("findings do not suggest %q: %+v", tc.fixHas, findings)
			}
		})
	}
}

// The DNS check is gated exactly like deploy.sh's: only for the ACME modes, and
// VIDRA_SKIP_DNS_PREFLIGHT downgrades a failure to a warning rather than hiding
// it. Getting the gate wrong puts a permanent ✗ on a correctly configured
// private instance, or lets a rate-limit lockout through.
func TestDomainDNSGating(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tlsMode   string
		skipEnv   string
		skipFile  bool
		resStatus Status
		want      Status
		detailHas string
	}{
		{name: "acme, resolves here", tlsMode: "acme", resStatus: StatusOK, want: StatusOK, detailHas: "which is this host"},
		{name: "acme, points elsewhere", tlsMode: "acme", resStatus: StatusFail, want: StatusFail},
		{name: "acme-staging is still an order", tlsMode: "acme-staging", resStatus: StatusFail, want: StatusFail},
		// internal issues from Caddy's own CA: no ACME order, nothing for DNS to
		// be wrong about.
		{name: "internal is not checked", tlsMode: "internal", resStatus: StatusFail, want: StatusWarn, detailHas: "issues from Caddy's own CA"},
		{name: "blank means acme", tlsMode: "", resStatus: StatusFail, want: StatusFail},
		{
			name: "the opt-out downgrades, it does not hide", tlsMode: "acme", skipEnv: "1", resStatus: StatusFail,
			want: StatusWarn, detailHas: "VIDRA_SKIP_DNS_PREFLIGHT is set, so this is reported and not enforced",
		},
		{
			name: "the opt-out in the env file counts too", tlsMode: "acme", skipFile: true, resStatus: StatusFail,
			want: StatusWarn, detailHas: "reported and not enforced",
		},
		// A check that could not COMPLETE (no outbound HTTPS to discover this
		// host's own IP) is not a check that failed.
		{name: "an incomplete check stays a warning", tlsMode: "acme", resStatus: StatusWarn, want: StatusWarn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFakeHost()
			env := strings.Replace(healthyEnv, "VIDRA_TLS_MODE=acme", "VIDRA_TLS_MODE="+tc.tlsMode, 1)
			if tc.skipFile {
				env += "VIDRA_SKIP_DNS_PREFLIGHT=1\n"
			}
			h.files[filepath.Join(testRoot, "env/production.env")] = env
			if tc.skipEnv != "" {
				h.env["VIDRA_SKIP_DNS_PREFLIGHT"] = tc.skipEnv
			}
			p := newFakeProber()
			p.domain.Status = tc.resStatus
			switch tc.resStatus {
			case StatusFail:
				p.domain.Message = "video.example.org resolves to 198.51.100.7, which is not this host (203.0.113.10)"
				p.domain.Fix = "point video.example.org at 203.0.113.10"
			case StatusWarn:
				p.domain.Message = "video.example.org resolves to 203.0.113.10, but this host's own public IP could not be determined"
			}
			wantFinding(t, one(t, only(t, "domain DNS", h, p)), tc.want, tc.detailHas, "")
		})
	}
}

// VIDRA_PUBLIC_IP is how an operator behind NAT tells the check what this host's
// address really is; deploy.sh reads it from the env file, and so must this.
func TestDomainDNSHonoursVidraPublicIP(t *testing.T) {
	h := newFakeHost()
	h.files[filepath.Join(testRoot, "env/production.env")] = healthyEnv + "VIDRA_PUBLIC_IP=203.0.113.10\n"
	p := newFakeProber()
	only(t, "domain DNS", h, p)
	if p.sawDomain.Expected != "203.0.113.10" {
		t.Errorf("Expected = %q, want the env file's VIDRA_PUBLIC_IP passed through", p.sawDomain.Expected)
	}
	if p.sawDomain.Domain != "https://video.example.org" {
		t.Errorf("Domain = %q, want PUBLIC_BASE_URL", p.sawDomain.Domain)
	}
}

// The check that catches an UPGRADE: a release adding a required variable leaves
// every existing deployment's file silently short of it.
func TestEnvTemplateDrift(t *testing.T) {
	h := newFakeHost()
	wantFinding(t, one(t, only(t, "env file vs template", h, nil)), StatusOK, "exactly the", "")

	h.files[filepath.Join(testRoot, "env/production.env.example")] = healthyEnv + "OWNER_CLAIM_TTL=24h\nNEW_REQUIRED_KEY=\n"
	findings := only(t, "env file vs template", h, nil)
	wantFinding(t, findings[0], StatusWarn, "OWNER_CLAIM_TTL", "preserves every value the file already sets")

	// The other direction: a key the template does not define. A deliberate extra
	// and a typo look identical from here, which is what the fix text says.
	h = newFakeHost()
	h.files[filepath.Join(testRoot, "env/production.env")] = healthyEnv + "JWT_SECRE=typo\n"
	findings = only(t, "env file vs template", h, nil)
	if !hasAny(findings, "JWT_SECRE") || !hasAny(findings, "MISSPELLED") {
		t.Errorf("findings do not flag the unknown key: %+v", findings)
	}

	h = newFakeHost()
	delete(h.files, filepath.Join(testRoot, "env/production.env.example"))
	wantFinding(t, one(t, only(t, "env file vs template", h, nil)), StatusWarn, "skipped:", "")
}

// The configuration check is the api's own boot validation, reported per
// variable and before the deploy rather than after it.
func TestConfigValues(t *testing.T) {
	h := newFakeHost()
	wantFinding(t, one(t, only(t, "configuration values", h, nil)), StatusOK, "pass the api's own boot validation", "")

	// A blank JWT_SECRET in production is the failure the api refuses to boot on.
	h.files[filepath.Join(testRoot, "env/production.env")] = strings.Replace(healthyEnv, "JWT_SECRET=0123456789abcdef0123456789abcdef0123456789abcdef", "JWT_SECRET=", 1)
	findings := only(t, "configuration values", h, nil)
	if !hasStatus(findings, StatusFail) || !hasAny(findings, "JWT_SECRET") {
		t.Errorf("findings do not name JWT_SECRET: %+v", findings)
	}
	if !hasAny(findings, "fix JWT_SECRET in env/production.env") {
		t.Errorf("findings do not say where to fix it: %+v", findings)
	}

	// An unreadable env file is a failure of its own, not a cascade of eighteen.
	h = newFakeHost()
	delete(h.files, filepath.Join(testRoot, "env/production.env"))
	wantFinding(t, one(t, only(t, "configuration values", h, nil)), StatusFail,
		"env/production.env does not exist", "vidra setup --template")
}

func TestSchemaLedger(t *testing.T) {
	// The bundled Postgres publishes no host port, so the ledger is read from
	// inside the api container.
	h := newFakeHost()
	wantFinding(t, one(t, only(t, "schema ledger", h, nil)), StatusOK, "version 42 and clean", "")

	// A dirty ledger is the one that must never be deployed over.
	h = newFakeHost()
	h.respond = func(name string, args []string) (Output, error) {
		if strings.Contains(strings.Join(args, " "), "exec -T api migrate version") {
			return Output{Stdout: "version=42 dirty=true\n", ExitCode: 1}, nil
		}
		return h.healthyRespond(name, args)
	}
	wantFinding(t, one(t, only(t, "schema ledger", h, nil)), StatusFail, "DIRTY at version 42", "migrate force")

	// A managed database is dialled directly — this is also the database
	// reachability check, which is why unreachable is ✗ rather than a skip.
	h = newFakeHost()
	h.files[filepath.Join(testRoot, "env/production.env")] = healthyEnv + "DATABASE_URL=postgres://u:p@db.example.net:25060/defaultdb?sslmode=require\n"
	p := newFakeProber()
	p.ledgerErr = &notInstalled{}
	wantFinding(t, one(t, only(t, "schema ledger", h, p)), StatusFail, "the database is unreachable", "DATABASE_URL")

	// Never migrated: a warning, because the next deploy fixes it.
	h = newFakeHost()
	h.respond = func(name string, args []string) (Output, error) {
		if strings.Contains(strings.Join(args, " "), "exec -T api migrate version") {
			return Output{Stdout: "version=none dirty=false\n"}, nil
		}
		return h.healthyRespond(name, args)
	}
	wantFinding(t, one(t, only(t, "schema ledger", h, nil)), StatusWarn, "no migration has ever run", "deploy once")

	// The stack is down and the database has no host port: a skip, not a failure.
	h = newFakeHost()
	h.respond = func(name string, args []string) (Output, error) {
		if name == "docker" && strings.HasPrefix(strings.Join(args, " "), "ps ") {
			return Output{Stdout: ""}, nil
		}
		return h.healthyRespond(name, args)
	}
	wantFinding(t, one(t, only(t, "schema ledger", h, nil)), StatusWarn, "publishes no host port", "")
}

func TestSearchLedger(t *testing.T) {
	// With a managed database the search ledger is a second table read on a
	// connection already proven.
	h := newFakeHost()
	h.files[filepath.Join(testRoot, "env/production.env")] = healthyEnv + "DATABASE_URL=postgres://u:p@db.example.net:25060/defaultdb?sslmode=require\n"
	wantFinding(t, one(t, only(t, "search ledger", h, nil)), StatusOK, "vidra_search_migrations) is at version 7", "")

	// A dirty search ledger blocks the search-migrate one-shot exactly the way a
	// dirty core ledger blocks migrate.
	p := newFakeProber()
	p.ledgers[searchLedgerTable] = dbmigrate.Status{Version: 7, Dirty: true, Applied: true}
	wantFinding(t, one(t, only(t, "search ledger", h, p)), StatusFail, "is DIRTY at version 7", "migrate force")

	// One connection failure is reported ONCE, by the core check; repeating it
	// per table is noise an operator has to read past.
	p = newFakeProber()
	p.ledgerErr = &notInstalled{}
	wantFinding(t, one(t, only(t, "search ledger", h, p)), StatusWarn, "not reachable for the core ledger either", "")

	// With the bundled Postgres there is no host port and the api image's
	// `migrate version` only knows core's ledger, so this one stands down.
	h = newFakeHost()
	wantFinding(t, one(t, only(t, "search ledger", h, nil)), StatusWarn, "only reports core's ledger", "")
}

func TestBackups(t *testing.T) {
	for _, tc := range []struct {
		name      string
		marker    string
		absent    bool
		want      Status
		detailHas string
		fixHas    string
	}{
		{name: "fresh", marker: testNow.Add(-3*time.Hour).Format(time.RFC3339) + " vidra.dump.gz", want: StatusOK, detailHas: "3h0m0s ago"},
		{name: "within the daily window", marker: testNow.Add(-25*time.Hour).Format(time.RFC3339) + " vidra.dump.gz", want: StatusOK},
		{name: "a run has been missed", marker: testNow.Add(-30*time.Hour).Format(time.RFC3339) + " vidra.dump.gz", want: StatusFail,
			detailHas: "more than the 26h0m0s", fixHas: "journalctl -u vidra-backup.service"},
		{name: "never", absent: true, want: StatusWarn, detailHas: "backups never succeeded here", fixHas: "deploy/backup.sh"},
		{name: "a clock in the future", marker: testNow.Add(2*time.Hour).Format(time.RFC3339) + " vidra.dump.gz", want: StatusWarn, detailHas: "dated in the future", fixHas: "timedatectl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFakeHost()
			path := filepath.Join(testRoot, "backups/last_success")
			if tc.absent {
				delete(h.files, path)
			} else {
				h.files[path] = tc.marker + "\n"
			}
			wantFinding(t, one(t, only(t, "backups", h, nil)), tc.want, tc.detailHas, tc.fixHas)
		})
	}

	// A marker with no readable timestamp falls back to the file's own mtime —
	// backup.sh rewrites it on every success, so it carries the same fact.
	h := newFakeHost()
	h.files[filepath.Join(testRoot, "backups/last_success")] = "ok\n"
	wantFinding(t, one(t, only(t, "backups", h, nil)), StatusOK, "0s ago", "")

	// Against a managed database the local backup scripts refuse to run, so a ✗
	// here would be permanent and wrong.
	h = newFakeHost()
	h.files[filepath.Join(testRoot, "env/production.env")] = strings.Replace(healthyEnv, "VIDRA_EXTERNAL_POSTGRES=false", "VIDRA_EXTERNAL_POSTGRES=true", 1) +
		"DATABASE_URL=postgres://u:p@db.example.net:25060/defaultdb?sslmode=require\n"
	delete(h.files, filepath.Join(testRoot, "backups/last_success"))
	wantFinding(t, one(t, only(t, "backups", h, nil)), StatusWarn, "provider's automated ones", "")
}

func TestBackupTimer(t *testing.T) {
	h := newFakeHost()
	wantFinding(t, one(t, only(t, "backup timer", h, nil)), StatusOK, "enabled and active", "")

	h = newFakeHost()
	h.respond = func(name string, args []string) (Output, error) {
		if name == "systemctl" {
			return Output{Stdout: strings.Replace(strings.Replace(args[0], "is-enabled", "disabled", 1), "is-active", "inactive", 1) + "\n", ExitCode: 1}, nil
		}
		return h.healthyRespond(name, args)
	}
	wantFinding(t, one(t, only(t, "backup timer", h, nil)), StatusFail, "no backup is scheduled", "systemctl enable --now vidra-backup.timer")

	// No systemd (a macOS or container dev box) is a skip, not a failure.
	h = newFakeHost()
	delete(h.paths, "systemctl")
	wantFinding(t, one(t, only(t, "backup timer", h, nil)), StatusWarn, "no systemctl", "")
}

func TestDiskSpace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage DiskUsage
		want  Status
	}{
		{name: "roomy", usage: DiskUsage{TotalBytes: 100 * gib, FreeBytes: 60 * gib}, want: StatusOK},
		{name: "under 10 percent", usage: DiskUsage{TotalBytes: 100 * gib, FreeBytes: 9 * gib}, want: StatusWarn},
		{name: "under 5 percent", usage: DiskUsage{TotalBytes: 100 * gib, FreeBytes: 4 * gib}, want: StatusFail},
		// A big disk can be percentage-healthy and still have nowhere to put a
		// transcode: the absolute floor is what catches that.
		{name: "plenty of percent, no gigabytes", usage: DiskUsage{TotalBytes: 40 * gib, FreeBytes: 4 * gib}, want: StatusWarn},
		{name: "almost nothing left", usage: DiskUsage{TotalBytes: 200 * gib, FreeBytes: 1 * gib}, want: StatusFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFakeHost()
			h.usage[testRoot] = tc.usage
			h.usage["/var/lib/docker"] = DiskUsage{TotalBytes: 500 * gib, FreeBytes: 400 * gib}
			findings := only(t, "disk space", h, nil)
			if !hasStatus(findings, tc.want) {
				t.Fatalf("statuses = %v, want a %s: %+v", statuses(findings), tc.want, findings)
			}
			if tc.want == StatusFail && !hasAny(findings, "docker system prune") {
				t.Errorf("no suggested fix for a full disk: %+v", findings)
			}
		})
	}

	// One filesystem under two names is one line, not two identical ones.
	h := newFakeHost()
	same := DiskUsage{TotalBytes: 100 * gib, FreeBytes: 60 * gib}
	h.usage[testRoot], h.usage["/var/lib/docker"] = same, same
	if got := only(t, "disk space", h, nil); len(got) != 1 {
		t.Errorf("got %d findings for one filesystem under two names: %+v", len(got), got)
	}
}

func TestObjectStorage(t *testing.T) {
	// Local storage is not a skip: there is no object store in this deployment to
	// reach, and a permanent ⚠ would train an operator to stop reading.
	h := newFakeHost()
	wantFinding(t, one(t, only(t, "object storage", h, nil)), StatusOK, "media lives in the media_data volume", "")

	s3Env := healthyEnv + strings.Join([]string{
		"STORAGE_S3_ENDPOINT=nyc3.digitaloceanspaces.com",
		"STORAGE_S3_REGION=nyc3",
		"STORAGE_S3_BUCKET=vidra-media",
		"STORAGE_S3_ACCESS_KEY=AKIAEXAMPLE",
		"STORAGE_S3_SECRET_KEY=secret",
		"",
	}, "\n")
	s3Env = strings.Replace(s3Env, "STORAGE_BACKEND=local", "STORAGE_BACKEND=s3", 1)

	h = newFakeHost()
	h.files[filepath.Join(testRoot, "env/production.env")] = s3Env
	p := newFakeProber()
	wantFinding(t, one(t, only(t, "object storage", h, p)), StatusOK, `"vidra-media"`, "")
	if p.sawBucket.Endpoint != "nyc3.digitaloceanspaces.com" || p.sawBucket.Bucket != "vidra-media" || !p.sawBucket.UseSSL {
		t.Errorf("the probe got %+v, want the env file's endpoint/bucket and TLS on by default", p.sawBucket)
	}

	// The credentials work and the bucket is not there: a typo reads as an empty
	// library rather than an error, which is why it is called out separately.
	p = newFakeProber()
	p.bucket = false
	wantFinding(t, one(t, only(t, "object storage", h, p)), StatusFail, "does not exist", "create the bucket")

	p = newFakeProber()
	p.bucketErr = &notInstalled{}
	wantFinding(t, one(t, only(t, "object storage", h, p)), StatusFail, "could not be reached or would not authenticate", "STORAGE_S3_ENDPOINT")
}

func TestSMTP(t *testing.T) {
	h := newFakeHost()
	wantFinding(t, one(t, only(t, "smtp", h, nil)), StatusOK, "mail is off", "")

	mailEnv := strings.Replace(healthyEnv, "MAIL_ENABLED=false", "MAIL_ENABLED=true\nSMTP_HOST=smtp.example.net\nSMTP_PORT=587\nSMTP_FROM=noreply@example.org", 1)
	h.files[filepath.Join(testRoot, "env/production.env")] = mailEnv
	p := newFakeProber()
	wantFinding(t, one(t, only(t, "smtp", h, p)), StatusOK, "220 smtp.example.net", "")
	if p.sawSMTP != "smtp.example.net:587" {
		t.Errorf("dialled %q, want smtp.example.net:587", p.sawSMTP)
	}

	p = newFakeProber()
	p.smtpErr = &notInstalled{}
	wantFinding(t, one(t, only(t, "smtp", h, p)), StatusFail, "no SMTP relay answered", "outbound 25")

	// Something answered, but it is not a mail relay.
	p = newFakeProber()
	p.banner = "HTTP/1.1 400 Bad Request"
	wantFinding(t, one(t, only(t, "smtp", h, p)), StatusWarn, "not an SMTP 220", "captive portal")
}

// The search service publishes no host port in production, deliberately, so the
// probe goes through the container and a stack that is down is a skip.
func TestSearchService(t *testing.T) {
	h := newFakeHost()
	wantFinding(t, one(t, only(t, "search service", h, nil)), StatusOK, "answers /healthz inside the compose network", "")

	h = newFakeHost()
	h.respond = func(name string, args []string) (Output, error) {
		if strings.Contains(strings.Join(args, " "), "exec -T search wget") {
			return Output{ExitCode: 1, Stderr: "wget: server returned error: HTTP/1.1 503\n"}, nil
		}
		return h.healthyRespond(name, args)
	}
	wantFinding(t, one(t, only(t, "search service", h, nil)), StatusFail, "does not answer /healthz", "logs --tail=50 search")

	h = newFakeHost()
	h.respond = func(name string, args []string) (Output, error) {
		if name == "docker" && strings.HasPrefix(strings.Join(args, " "), "ps ") {
			return Output{Stdout: ""}, nil
		}
		return h.healthyRespond(name, args)
	}
	wantFinding(t, one(t, only(t, "search service", h, nil)), StatusWarn, "is not running on this host", "")
}

func TestFFmpeg(t *testing.T) {
	h := newFakeHost()
	wantFinding(t, one(t, only(t, "ffmpeg", h, nil)), StatusOK, "present in the api container", "")

	// The binary is baked into the image, so its absence means the wrong image.
	h = newFakeHost()
	h.respond = func(name string, args []string) (Output, error) {
		if strings.Contains(strings.Join(args, " "), "exec -T api ffmpeg") {
			return Output{ExitCode: 126, Stderr: "exec: \"ffmpeg\": not found"}, nil
		}
		return h.healthyRespond(name, args)
	}
	wantFinding(t, one(t, only(t, "ffmpeg", h, nil)), StatusWarn, "every transcode will fail", "wrong image is deployed")

	// A missing host ffmpeg with no container to check is a ⚠, never a ✗: the
	// binary that matters lives in the image.
	h = newFakeHost()
	delete(h.paths, "ffmpeg")
	h.respond = func(name string, args []string) (Output, error) {
		if name == "docker" && strings.HasPrefix(strings.Join(args, " "), "ps ") {
			return Output{Stdout: ""}, nil
		}
		return h.healthyRespond(name, args)
	}
	wantFinding(t, one(t, only(t, "ffmpeg", h, nil)), StatusWarn, "not on this host's PATH", "")
}

// The trap this check exists for: a bare `docker compose up` auto-loads
// docker-compose.override.yml, which turns rate limiting off. Nothing about the
// running stack looks wrong, so the evidence has to come from the labels.
func TestDevOverride(t *testing.T) {
	h := newFakeHost()
	wantFinding(t, one(t, only(t, "dev override", h, nil)), StatusOK, "does not load docker-compose.override.yml", "")

	h = newFakeHost()
	h.respond = func(name string, args []string) (Output, error) {
		if name == "docker" && strings.HasPrefix(strings.Join(args, " "), "ps ") {
			return Output{Stdout: "vidra-api-1\tvidra\tapi\t/srv/vidra/docker-compose.yml,/srv/vidra/docker-compose.override.yml\trunning\tUp 2 hours\n"}, nil
		}
		return h.healthyRespond(name, args)
	}
	wantFinding(t, one(t, only(t, "dev override", h, nil)), StatusFail, "rate limiting is off", "./deploy/deploy.sh")

	// Nothing running, but the trap is present next to a production env file.
	h = newFakeHost()
	h.files[filepath.Join(testRoot, "docker-compose.override.yml")] = "services: {}\n"
	h.respond = func(name string, args []string) (Output, error) {
		if name == "docker" && strings.HasPrefix(strings.Join(args, " "), "ps ") {
			return Output{Stdout: ""}, nil
		}
		return h.healthyRespond(name, args)
	}
	wantFinding(t, one(t, only(t, "dev override", h, nil)), StatusWarn, "would load it and turn rate limiting off", "deploy.sh")
}

func TestLogCaps(t *testing.T) {
	h := newFakeHost()
	wantFinding(t, one(t, only(t, "log caps", h, nil)), StatusOK, "caps its container log", "")

	h = newFakeHost()
	h.respond = func(name string, args []string) (Output, error) {
		if name == "docker" && strings.Contains(strings.Join(args, " "), "config --format json") {
			return Output{Stdout: `{"name":"vidra","services":{"api":{},"caddy":{"logging":{"driver":"json-file","options":{"max-size":"10m"}}}}}`}, nil
		}
		return h.healthyRespond(name, args)
	}
	wantFinding(t, one(t, only(t, "log caps", h, nil)), StatusWarn, "api log to an uncapped json-file", "max-size")

	// A service shipping its logs elsewhere is somebody else's rotation problem.
	h = newFakeHost()
	h.respond = func(name string, args []string) (Output, error) {
		if name == "docker" && strings.Contains(strings.Join(args, " "), "config --format json") {
			return Output{Stdout: `{"name":"vidra","services":{"api":{"logging":{"driver":"journald"}}}}`}, nil
		}
		return h.healthyRespond(name, args)
	}
	wantFinding(t, one(t, only(t, "log caps", h, nil)), StatusOK, "", "")
}

// Every docker-dependent check degrades to ⚠ when there is no daemon, and the
// run still exits 0: a laptop is not a broken deployment.
func TestNoDockerDegradesToWarnings(t *testing.T) {
	h := newFakeHost()
	h.respond = func(name string, args []string) (Output, error) {
		if name == "docker" {
			return Output{}, errNotInstalled
		}
		return h.healthyRespond(name, args)
	}
	rep := run(t, h, nil)
	if rep.Failed() {
		for _, r := range rep.Results {
			if r.Status == StatusFail {
				t.Errorf("✗ %s: %s", r.Check, r.Detail)
			}
		}
		t.Fatal("a host without docker was reported as a broken deployment")
	}
	if _, warn, _ := rep.Counts(); warn == 0 {
		t.Error("no warnings at all on a host without docker")
	}
}

// A compose chain that does not render is a ⚠ carrying compose's own message,
// not a Go error and not a silent pass.
func TestUnrenderableComposeChain(t *testing.T) {
	h := newFakeHost()
	h.respond = func(name string, args []string) (Output, error) {
		if name == "docker" && strings.Contains(strings.Join(args, " "), "config --format json") {
			return Output{ExitCode: 1, Stderr: "required variable JWT_SECRET is missing a value\n"}, nil
		}
		return h.healthyRespond(name, args)
	}
	wantFinding(t, one(t, only(t, "published ports", h, nil)), StatusWarn, "required variable JWT_SECRET is missing a value", "")
}

func TestRenderAndExitCode(t *testing.T) {
	var sb strings.Builder
	rep := run(t, newFakeHost(), nil)
	Render(&sb, rep)
	out := sb.String()
	for _, want := range []string{"vidra doctor", "docker & compose", "configuration", "data & state", "reachability", "✓"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered report is missing %q:\n%s", want, out)
		}
	}
	if rep.Failed() {
		t.Error("a healthy deployment reported a failure")
	}

	// A failing run says what the exit code means.
	h := newFakeHost()
	h.files[filepath.Join(testRoot, "vidra-core/.env")] = "JWT_SECRET=dev\n"
	sb.Reset()
	rep = run(t, h, nil)
	Render(&sb, rep)
	if !rep.Failed() {
		t.Error("a stray vidra-core/.env did not fail the run")
	}
	if !strings.Contains(sb.String(), "needs attention before it is deployed to again") {
		t.Errorf("the summary does not explain the exit code:\n%s", sb.String())
	}
	// A fix line is printed under the failure, and nowhere under the passes.
	if !strings.Contains(sb.String(), "      → ") {
		t.Errorf("no suggested fix was rendered:\n%s", sb.String())
	}
}

// The three outcomes are the exit-code rule, and it has to hold for every
// possible report rather than the two above.
func TestExitCodeRule(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statuses   []Status
		wantFailed bool
	}{
		{"all clear", []Status{StatusOK, StatusOK}, false},
		{"warnings do not fail a run", []Status{StatusOK, StatusWarn, StatusWarn}, false},
		{"one failure fails it", []Status{StatusOK, StatusWarn, StatusFail}, true},
		{"empty", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rep Report
			for _, s := range tc.statuses {
				rep.Results = append(rep.Results, Result{Finding: Finding{Status: s}})
			}
			if rep.Failed() != tc.wantFailed {
				t.Errorf("Failed() = %v, want %v", rep.Failed(), tc.wantFailed)
			}
		})
	}
}

func TestParseMigrateVersion(t *testing.T) {
	for _, tc := range []struct {
		in          string
		wantVersion uint
		wantDirty   bool
		wantApplied bool
		ok          bool
	}{
		{in: "version=42 dirty=false", wantVersion: 42, wantApplied: true, ok: true},
		{in: "version=42 dirty=true", wantVersion: 42, wantDirty: true, wantApplied: true, ok: true},
		{in: "version=none dirty=false", ok: true},
		{in: "time=... level=INFO msg=connected\nversion=7 dirty=false\n", wantVersion: 7, wantApplied: true, ok: true},
		{in: "no such command", ok: false},
		{in: "", ok: false},
	} {
		t.Run(tc.in, func(t *testing.T) {
			st, ok := parseMigrateVersion(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if st.Version != tc.wantVersion || st.Dirty != tc.wantDirty || st.Applied != tc.wantApplied {
				t.Errorf("= %+v, want version=%d dirty=%v applied=%v", st, tc.wantVersion, tc.wantDirty, tc.wantApplied)
			}
		})
	}
}

func TestLoopbackIP(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.1.1", true},
		{"::1", true},
		{"[::1]", true},
		// The one that matters: compose renders "publish on every interface" as an
		// EMPTY host IP, and reading that as loopback would disable the whole audit.
		{"", false},
		{"0.0.0.0", false},
		{"::", false},
		{"203.0.113.10", false},
		{"nonsense", false},
	} {
		t.Run(tc.in, func(t *testing.T) {
			if got := loopbackIP(tc.in); got != tc.want {
				t.Errorf("loopbackIP(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestAddDSNParams(t *testing.T) {
	got, err := addDSNParams("postgres://u:p@h:5432/db?sslmode=require", map[string]string{"connect_timeout": "5", "x-migrations-table": "vidra_search_migrations"})
	if err != nil {
		t.Fatalf("addDSNParams: %v", err)
	}
	for _, want := range []string{"sslmode=require", "connect_timeout=5", "x-migrations-table=vidra_search_migrations"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is missing %q", got, want)
		}
	}
	// An operator's own value wins: doctor adds parameters, it does not rewrite
	// the connection string.
	got, err = addDSNParams("postgres://h/db?connect_timeout=30", map[string]string{"connect_timeout": "5"})
	if err != nil || !strings.Contains(got, "connect_timeout=30") {
		t.Errorf("= %q (%v), want the existing connect_timeout kept", got, err)
	}
}
