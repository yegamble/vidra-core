package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/vidra/vidra-core/internal/preflight"
)

// runStatus is `vidra status`: what is running, and does it answer.
//
// It aggregates four sources an operator otherwise checks by hand — the compose
// project's containers, the api's own /readyz and /schemaz, the search service
// (which publishes no host port in production, so it is probed from inside the
// network) and the frontend — into one screen with doctor's three glyphs.
//
// It is UNAUTHENTICATED and stays that way in this wave. Everything below is
// either a local process the operator already controls or an operations endpoint
// deliberately root-mounted for host-local tooling; the admin API's richer view
// needs a JWT, and a CLI that stores an admin token to render a status screen is
// a credential on disk in exchange for a nicer table.
//
// The division of labour with `vidra doctor` is deliberate and worth keeping:
// doctor answers "is this deployment CONFIGURED correctly" from files, the
// rendered compose model and the database, and it is the pre-deploy gate. This
// answers "is it UP right now", from processes and HTTP. They share the glyphs
// and the exit-code rule (1 if and only if something is ✗) and nothing else.
func runStatus(s streams, args []string) error {
	f, err := parseWrapperFlags("status", args)
	if err != nil {
		fmt.Fprintf(s.err, "vidra: %s\n\n", err)
		statusUsage(s.err)
		return errReported
	}
	if f.help {
		statusUsage(s.out)
		return nil
	}
	if len(f.rest) > 0 {
		fmt.Fprintf(s.err, "vidra: status: unexpected argument %q\n\n", f.rest[0])
		statusUsage(s.err)
		return errReported
	}
	report := collectStatus(context.Background(), f)
	renderStatus(s.out, report)
	if report.failed() {
		// Already printed, in the shape the operator needs it. main must not add
		// a second error line under a report that ends with its own summary.
		return errReported
	}
	return nil
}

// The two budgets. HTTP is short because every target is on this host's loopback
// — a probe that takes longer than this has already answered the question. The
// exec budget is longer: it is a `docker compose` invocation, which on a cold
// small droplet spends most of it in the daemon.
const (
	statusHTTPTimeout = 5 * time.Second
	statusExecTimeout = 20 * time.Second
)

// statusLine is one printed line: doctor's Finding with the source it came from.
type statusLine struct {
	source string
	check  string
	status preflight.Status
	detail string
	fix    string
	// block is verbatim output printed under the line, indented. Only `compose
	// ps` uses it: the table IS the finding, and summarising it into a sentence
	// would throw away the one thing an operator wants to read.
	block string
}

type statusReport struct {
	root    string
	envFile string
	lines   []statusLine
}

func (r statusReport) failed() bool {
	for _, l := range r.lines {
		if l.status == preflight.StatusFail {
			return true
		}
	}
	return false
}

// The sources, in print order, which is the order of causation: anything wrong
// with the deployment directory or its env file explains the containers, and the
// containers explain everything that does or does not answer below them.
const (
	sourceDeployment = "deployment"
	sourceContainers = "containers"
	sourceAPI        = "api"
	sourceSearch     = "search"
	sourceFrontend   = "frontend"
)

var sourceOrder = []string{sourceDeployment, sourceContainers, sourceAPI, sourceSearch, sourceFrontend}

// collectStatus runs every probe. It returns a report, never an error: a
// deployment that is down is a FINDING, and the one thing this command must
// never print is a raw Go error at the moment an operator is trying to find out
// why their site is off.
func collectStatus(ctx context.Context, f wrapperFlags) statusReport {
	// The header names the file the ports below were actually read from, which
	// is not always the --env flag's value: an exported ENV_FILE beats its
	// default (effectiveEnvFile). It is computed here rather than taken from the
	// deployment because resolve can fail, and a report about the wrong env file
	// is worse than one about a directory that is not a deployment.
	r := statusReport{root: f.repo, envFile: effectiveEnvFile(theRunner.Environ(), f)}

	dep, composeScript, err := resolve("status", f, "compose.sh")
	if err != nil {
		// A --repo that is not a deployment. The passthrough commands treat this
		// as the usage error it is and refuse; status reports instead, because
		// its job is to describe what it finds and half of what follows (the
		// loopback probes) still runs against process-environment defaults.
		r.add(statusLine{
			source: sourceDeployment, check: "deploy/compose.sh", status: preflight.StatusWarn,
			detail: "this directory is not a Vidra deployment, so the containers could not be listed",
			fix:    "point -C/--repo at the deployment directory — the checkout that holds docker-compose.yml, deploy/ and env/",
		})
	}
	r.root = dep.root
	if dep.root == "" {
		r.root = f.repo
	}

	values, envErr := dep.values()
	if err == nil && envErr != nil {
		r.add(statusLine{
			source: sourceDeployment, check: "env file", status: preflight.StatusWarn,
			detail: fmt.Sprintf("%s — the ports below fall back to the defaults", envErr),
			fix:    "run `vidra setup` to generate it, or pass --env if this deployment keeps it elsewhere",
		})
	}
	processEnv := environMap(theRunner.Environ())
	apiPort := envGet(processEnv, values, "HTTP_PORT", "8080")
	frontendPort := envGet(processEnv, values, "FRONTEND_PORT", "3000")

	if err == nil {
		r.add(containerLines(ctx, dep, composeScript)...)
	}
	client := &http.Client{Timeout: statusHTTPTimeout}
	r.add(readyLine(ctx, client, apiPort))
	r.add(schemaLine(ctx, client, apiPort))
	if err == nil {
		r.add(searchLine(ctx, dep, composeScript))
	}
	r.add(frontendLine(ctx, client, frontendPort))
	return r
}

func (r *statusReport) add(lines ...statusLine) { r.lines = append(r.lines, lines...) }

// ---------------------------------------------------------------------------
// The containers.

func containerLines(ctx context.Context, dep deployment, composeScript string) []statusLine {
	cctx, cancel := context.WithTimeout(ctx, statusExecTimeout)
	defer cancel()
	out, err := theRunner.Capture(cctx, dep.bash(composeScript, "ps"))
	if err != nil {
		return []statusLine{{
			source: sourceContainers, check: "compose ps", status: preflight.StatusWarn,
			detail: "the deployment's compose wrapper could not be run on this host",
			fix:    "deploy/compose.sh is a bash script that shells out to docker — check that both are installed and on PATH",
		}}
	}
	if out.ExitCode != 0 {
		return []statusLine{{
			source: sourceContainers, check: "compose ps", status: preflight.StatusWarn,
			detail: "the containers could not be listed: " + orUnknown(composeDiagnostic(out.Stderr)),
			fix:    "run `" + composeCommandLine(dep, "ps") + "` to see it in full",
		}}
	}
	rows := psRows(out.Stdout)
	if len(rows) == 0 {
		return []statusLine{{
			source: sourceContainers, check: "compose ps", status: preflight.StatusWarn,
			detail: "nothing is running: this deployment's compose project has no containers",
			fix:    "`vidra deploy` brings the stack up. If it was up, `vidra logs` still holds the last 100 lines of why it stopped",
		}}
	}
	return []statusLine{{
		source: sourceContainers, check: "compose ps", status: preflight.StatusOK,
		detail: fmt.Sprintf("%d container(s) in this deployment's compose project", len(rows)),
		block:  strings.TrimRight(out.Stdout, "\n"),
	}}
}

// psRows counts the container rows in `docker compose ps` output, skipping its
// header. The header is recognised by its first column rather than by position,
// so an empty project (header only, or nothing at all) counts as zero either
// way.
func psRows(stdout string) []string {
	var rows []string
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "NAME") {
			continue
		}
		rows = append(rows, line)
	}
	return rows
}

// ---------------------------------------------------------------------------
// The api.

// readinessBody is the subset of GET /readyz this reads. It is the shape
// internal/httpapi.readinessResponse writes.
type readinessBody struct {
	Status     string `json:"status"`
	Components map[string]struct {
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	} `json:"components"`
}

func readyLine(ctx context.Context, client *http.Client, port string) statusLine {
	line := statusLine{source: sourceAPI, check: "/readyz"}
	status, body, err := get(ctx, client, loopback(port)+"/readyz")
	if err != nil {
		line.status, line.detail = preflight.StatusWarn, "the api is not answering on "+loopback(port)+": "+dialSummary(err)
		line.fix = "`vidra logs api` says why. If nothing is running at all, `vidra deploy` brings the stack up"
		return line
	}
	var parsed readinessBody
	_ = json.Unmarshal(body, &parsed)
	switch {
	case status == http.StatusOK && readyHasDownComponent(parsed):
		// 200 with something down is the api saying "I am still serving, but
		// not on all cylinders" — a Redis outage, for instance, which leaves
		// every rate limiter failing open rather than taking the instance out
		// of rotation. Green would hide it; red would claim an outage there
		// is not.
		line.status = preflight.StatusWarn
		line.detail = "the api is serving but DEGRADED" + componentSummary(parsed)
		line.fix = "a non-critical dependency is down — the api stays in rotation without it, but something is worse than it should be (with Redis down, every rate limit fails open). `vidra logs redis` is the usual next line"
	case status == http.StatusOK:
		line.status = preflight.StatusOK
		line.detail = "the api is ready" + componentSummary(parsed)
	case status == http.StatusServiceUnavailable && parsed.Status == "draining":
		// A replica that has been told to stop is not broken. Saying so keeps
		// `vidra status` usable as a gate during a rolling restart.
		line.status = preflight.StatusWarn
		line.detail = "the api is DRAINING: it has been told to shut down and is refusing readiness so a load balancer stops routing to it"
		line.fix = "this is what a graceful shutdown looks like (HTTP_DRAIN_DELAY). Run it again once the restart has finished; if it stays this way, the process is stuck between SIGTERM and exit and `vidra logs api` says where"
	case status == http.StatusServiceUnavailable:
		line.status = preflight.StatusFail
		line.detail = "the api is up but NOT ready" + componentSummary(parsed)
		line.fix = "a dependency it needs is down. `vidra status` names which above; `vidra logs postgres` / `vidra logs redis` is the next line to read"
	default:
		line.status = preflight.StatusWarn
		line.detail = fmt.Sprintf("something answered %s with HTTP %d, which is not the api's /readyz", loopback(port), status)
		line.fix = "check HTTP_PORT — another process may hold that port on this host"
	}
	return line
}

// readyHasDownComponent reports whether any dependency in a /readyz body says
// "down". "not_configured" is not one: an install with no search service or no
// mail is a supported deployment, not a fault.
func readyHasDownComponent(b readinessBody) bool {
	for _, c := range b.Components {
		if c.Status == "down" {
			return true
		}
	}
	return false
}

// componentSummary renders /readyz's per-dependency block as "(postgres ok,
// redis down)". Sorted, so two runs of the same command print the same line.
func componentSummary(b readinessBody) string {
	if len(b.Components) == 0 {
		return ""
	}
	names := make([]string, 0, len(b.Components))
	for name := range b.Components {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		c := b.Components[name]
		part := name + " " + orUnknown(c.Status)
		if c.Error != "" {
			part += " (" + truncate(firstLine(c.Error), 100) + ")"
		}
		parts = append(parts, part)
	}
	return " — " + strings.Join(parts, ", ")
}

// schemaBody is the subset of GET /schemaz this reads
// (internal/httpapi.schemaResponse).
type schemaBody struct {
	Software struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	} `json:"software"`
	Schema struct {
		Version int64  `json:"version"`
		Dirty   bool   `json:"dirty"`
		Applied bool   `json:"applied"`
		Error   string `json:"error,omitempty"`
	} `json:"schema"`
}

func schemaLine(ctx context.Context, client *http.Client, port string) statusLine {
	line := statusLine{source: sourceAPI, check: "/schemaz"}
	status, body, err := get(ctx, client, loopback(port)+"/schemaz")
	if err != nil {
		line.status, line.detail = preflight.StatusWarn, "the running build and schema version could not be read: "+dialSummary(err)
		return line
	}
	if status == http.StatusNotFound {
		// Not a failure, and never one: /schemaz is newer than the images some
		// deployments are still on, and a status command that went red for an
		// older-but-working api would be a status command people stop running.
		line.status = preflight.StatusWarn
		line.detail = "this api image predates /schemaz, so its build and schema version cannot be read from here"
		line.fix = "`vidra doctor` reads the migration ledger out of the database directly. Deploying a newer image adds this line"
		return line
	}
	if status != http.StatusOK {
		line.status = preflight.StatusWarn
		line.detail = fmt.Sprintf("/schemaz answered HTTP %d", status)
		return line
	}
	var parsed schemaBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		line.status = preflight.StatusWarn
		line.detail = "/schemaz answered with something that is not the expected document"
		return line
	}
	build := "vidra " + orUnknown(parsed.Software.Version)
	if parsed.Software.Commit != "" {
		build += " (" + parsed.Software.Commit + ")"
	}
	switch {
	case parsed.Schema.Error != "":
		line.status = preflight.StatusWarn
		line.detail = build + " — its database was reachable but the migration ledger could not be read (" + truncate(firstLine(parsed.Schema.Error), 120) + ")"
		line.fix = "the api is running; this is about its database. `vidra doctor` checks the connection itself"
	case parsed.Schema.Dirty:
		line.status = preflight.StatusFail
		line.detail = fmt.Sprintf("%s — the migration ledger is DIRTY at version %d: a migration died half-applied", build, parsed.Schema.Version)
		line.fix = "the schema is in an unknown state. Fix the failed migration by hand, then re-stamp the ledger with `api migrate force <version> --yes-i-know` inside the api container. Restoring the pre-deploy dump is the other way back"
	case !parsed.Schema.Applied:
		line.status = preflight.StatusWarn
		line.detail = build + " — no migration has ever run against this database"
		line.fix = "`vidra deploy` runs the migrators as a gated step. A fresh install shows this until the first deploy finishes"
	default:
		line.status = preflight.StatusOK
		line.detail = fmt.Sprintf("%s — schema version %d, clean", build, parsed.Schema.Version)
	}
	return line
}

// ---------------------------------------------------------------------------
// The search service.

func searchLine(ctx context.Context, dep deployment, composeScript string) statusLine {
	line := statusLine{source: sourceSearch, check: "/readyz"}
	cctx, cancel := context.WithTimeout(ctx, statusExecTimeout)
	defer cancel()
	// From INSIDE the network: docker-compose.prod.yml resets the search
	// service's ports outright, deliberately — it is HMAC-authenticated and
	// called only by the api, never by a browser — so there is no host port to
	// dial. `vidra doctor` probes it the same way.
	//
	// /readyz rather than doctor's /healthz: liveness only says the process is
	// up, and what an operator wants from a status screen is whether it can
	// serve, which means its own dependencies.
	out, err := theRunner.Capture(cctx, dep.bash(composeScript,
		"exec", "-T", "search", "wget", "-qO-", "http://127.0.0.1:8080/readyz"))
	if err != nil {
		line.status = preflight.StatusWarn
		line.detail = "the search service could not be probed: it publishes no host port in production, and running docker compose from here failed"
		return line
	}
	if out.ExitCode == 0 {
		line.status = preflight.StatusOK
		line.detail = "the search service answers /readyz inside the compose network"
		if body := firstLine(out.Stdout); body != "" {
			line.detail += ": " + truncate(body, 100)
		}
		return line
	}
	// wget was given -q, so it prints nothing at all. Anything on stderr
	// therefore came from docker or compose, and that is the distinction this
	// branch turns on: their message means the probe never ran, silence means it
	// ran and the service did not answer.
	diag := composeDiagnostic(out.Stderr)
	switch {
	case notRunning(diag):
		line.status = preflight.StatusWarn
		line.detail = "the search container is not running on this host, so there is no way to reach it (it publishes no host port)"
		line.fix = "`vidra logs search` holds why it stopped; `vidra deploy` brings it back"
	case diag != "":
		line.status = preflight.StatusWarn
		line.detail = "the search service could not be probed: " + diag
	default:
		line.status = preflight.StatusFail
		line.detail = "the search container is running but does not answer /readyz on its own loopback"
		line.fix = "read `vidra logs search`. Search being down does not take the site down — the api falls back to database queries — but discovery and suggestions are wrong until it is back"
	}
	return line
}

// notRunning recognises docker/compose's several spellings of "that service has
// no container here".
func notRunning(diag string) bool {
	d := strings.ToLower(diag)
	for _, phrase := range []string{"not running", "no container", "no such service", "is not running"} {
		if strings.Contains(d, phrase) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The frontend.

func frontendLine(ctx context.Context, client *http.Client, port string) statusLine {
	line := statusLine{source: sourceFrontend, check: "/healthz"}
	status, _, err := get(ctx, client, loopback(port)+"/healthz")
	if err != nil {
		line.status = preflight.StatusWarn
		line.detail = "the frontend is not answering on " + loopback(port) + ": " + dialSummary(err)
		line.fix = "`vidra logs frontend` says why. If nothing is running at all, `vidra deploy` brings the stack up"
		return line
	}
	if status == http.StatusNotFound {
		// Images older than this wave have no /healthz, and their 404 is the
		// SERVER answering — which is the thing being asked about. So the fall
		// back to / is not a second chance, it is the older way of asking the
		// same question.
		line.check = "/"
		rootStatus, _, rootErr := get(ctx, client, loopback(port)+"/")
		switch {
		case rootErr != nil:
			line.status = preflight.StatusWarn
			line.detail = "the frontend answered /healthz with 404 and then stopped answering: " + dialSummary(rootErr)
		case rootStatus >= 200 && rootStatus < 400:
			line.status = preflight.StatusOK
			line.detail = fmt.Sprintf("the frontend serves / (HTTP %d). This image predates /healthz, so that is the whole check", rootStatus)
		default:
			line.status = preflight.StatusFail
			line.detail = fmt.Sprintf("the frontend is up but serves / with HTTP %d", rootStatus)
			line.fix = "read `vidra logs frontend`. A frontend that answers with an error is usually a bad PUBLIC_API_BASE_URL or an api it cannot reach"
		}
		return line
	}
	if status >= 200 && status < 400 {
		line.status = preflight.StatusOK
		line.detail = fmt.Sprintf("the frontend answers /healthz (HTTP %d)", status)
		return line
	}
	line.status = preflight.StatusFail
	line.detail = fmt.Sprintf("the frontend is up but answers /healthz with HTTP %d", status)
	line.fix = "read `vidra logs frontend`"
	return line
}

// ---------------------------------------------------------------------------
// HTTP, and the translation of its failures.

// loopback is where every probe dials. 127.0.0.1 and never the public origin:
// this command answers "is the stack up on this host", and going out through
// DNS and the TLS edge would answer a different question and fail for reasons
// (a certificate, a firewall) that belong to `vidra doctor`.
func loopback(port string) string { return "http://127.0.0.1:" + port }

func get(ctx context.Context, client *http.Client, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	// Bounded: these documents are hundreds of bytes, and whatever is on that
	// port when it is NOT the api need not be able to make this command allocate.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, body, nil
}

// dialSummary turns a transport error into a sentence. The Go error itself
// never reaches the operator: "dial tcp 127.0.0.1:8080: connect: connection
// refused" and "nothing is listening there" are the same fact, and only one of
// them is an answer.
func dialSummary(err error) string {
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "nothing is listening on that port"
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("it accepted the connection but did not answer within %s", statusHTTPTimeout)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Sprintf("it did not answer within %s", statusHTTPTimeout)
	}
	return "the connection failed"
}

// composeDiagnostic is the first line of a compose invocation's stderr that is
// worth showing.
//
// deploy/compose.sh logs to stderr on purpose (stdout belongs to docker compose,
// so `compose.sh config | yq` works), which means its own "[compose] external
// datastores: …" note shares the stream with real errors. Its plain log lines
// are dropped here and its ERROR lines are kept, so a missing env file still
// reaches the operator and a routine note never masquerades as one.
func composeDiagnostic(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[compose] ") && !strings.Contains(line, "ERROR") {
			continue
		}
		return truncate(strings.TrimPrefix(line, "[compose] "), 200)
	}
	return ""
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

// ---------------------------------------------------------------------------
// Rendering.

// The glyphs, and they are the same three internal/doctor prints. An operator
// reads both commands in the same session and must not have to learn two
// alphabets; the constants are duplicated rather than exported from doctor
// because a rendering choice is not an API.
const (
	glyphOK   = "✓"
	glyphWarn = "⚠"
	glyphFail = "✗"
)

func glyph(s preflight.Status) string {
	switch s {
	case preflight.StatusOK:
		return glyphOK
	case preflight.StatusFail:
		return glyphFail
	default:
		return glyphWarn
	}
}

func renderStatus(w io.Writer, r statusReport) {
	fmt.Fprintf(w, "vidra status — %s (%s)\n", r.root, r.envFile)
	for _, source := range sourceOrder {
		printed := false
		for _, l := range r.lines {
			if l.source != source {
				continue
			}
			if !printed {
				fmt.Fprintf(w, "\n%s\n", source)
				printed = true
			}
			fmt.Fprintf(w, "  %s %s: %s\n", glyph(l.status), l.check, l.detail)
			if l.status != preflight.StatusOK && strings.TrimSpace(l.fix) != "" {
				fmt.Fprintf(w, "      → %s\n", l.fix)
			}
			for _, bl := range strings.Split(l.block, "\n") {
				if strings.TrimSpace(bl) != "" {
					fmt.Fprintf(w, "      %s\n", bl)
				}
			}
		}
	}
	var ok, warn, fail int
	for _, l := range r.lines {
		switch l.status {
		case preflight.StatusOK:
			ok++
		case preflight.StatusFail:
			fail++
		default:
			warn++
		}
	}
	fmt.Fprintf(w, "\n%d %s  %d %s  %d %s\n", ok, glyphOK, warn, glyphWarn, fail, glyphFail)
	switch {
	case fail > 0:
		fmt.Fprintln(w, "Something that is running is not working. `vidra doctor` checks the deployment's configuration; `vidra logs <service>` holds the reason.")
	case warn > 0:
		fmt.Fprintln(w, "Nothing is broken. The warnings above are things that are not running here, or that could not be asked.")
	default:
		fmt.Fprintln(w, "Everything this command can reach is up and answering.")
	}
}

func statusUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: vidra status [-C <deployment directory>] [--env <env file>]

What is running, and does it answer. One ✓/⚠/✗ line per source: the compose
project's containers, the api's /readyz and /schemaz, the search service (probed
from inside the compose network, where in production it is reachable at all) and
the frontend. Everything is dialled on 127.0.0.1, so this describes THIS HOST and
not what the internet can see.

It reads; it changes nothing. It exits 0 unless something is ✗, so it is usable
as a gate — a service that is simply not running here is ⚠ with the reason, not a
failure.

For "is this deployment configured correctly" rather than "is it up", run
`+"`vidra doctor`"+`.

flags:
  -C, --repo <dir>   the deployment directory (default ".")
      --env <file>   the deployment's env file; HTTP_PORT and FRONTEND_PORT are
                     read from it, unless they are set in your environment
                     (default %s)
  -h, --help         this text
`, defaultEnvFile)
}
