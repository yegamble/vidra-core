// Package doctor is the one command an operator runs when a Vidra deployment is
// wrong and they do not yet know why (docs/productionization phase-1 item 14).
//
// It is the runbook, executed. Every check below exists because a real
// deployment failed in that exact way and the meta repo grew a paragraph about
// it: a Compose older than 2.24 that ignores the prod overlay's `!reset` and
// leaves Postgres published on 0.0.0.0; a stray vidra-core/.env poisoning the
// included model's substitutions; a bare `docker compose up` quietly loading
// docker-compose.override.yml and turning rate limiting off in production; a
// backup timer nobody enabled. Reading fifteen paragraphs of runbook and
// checking each one by hand is a thing operators do once; this is what they run
// instead.
//
// FOUR RULES SHAPE THE PACKAGE.
//
//  1. FACTS, NOT PROSE. Every finding is derived from a file, the rendered
//     compose model, the database, or the daemon. Nothing here restates what a
//     document says should be true. That is why the port check runs the deploy's
//     OWN compose chain (setup.RenderCheckArgs) and reads the ports out of the
//     rendered model, rather than grepping the compose files for the mapping it
//     hopes is there.
//
//  2. AN OPERATOR-FACING LINE OR NOTHING. A check reports ✓/⚠/✗ with one line of
//     finding and, when it failed, one line of what to do about it. A raw Go
//     error never reaches the output: an operator staring at
//     `dial tcp: i/o timeout` at 3am learns less than "the database is
//     unreachable" plus the command that proves it. Errors are still the input —
//     they are just translated, once, here.
//
//  3. A CHECK THAT CANNOT RUN IS NOT A CHECK THAT FAILED. No systemd on a
//     laptop, no daemon, a stack that is not up: those are ⚠ WITH THE REASON, and
//     the exit code stays 0. Only a check that ran and found a problem is ✗. The
//     distinction is what keeps doctor honest enough to be worth running
//     anywhere; see preflight, whose Status type this package deliberately
//     reuses rather than inventing a second vocabulary for the same three
//     outcomes.
//
//     A check whose SUBJECT is switched off is a third case, and it is ✓: an
//     instance on local storage has no object store to reach, and reporting that
//     as a permanent ⚠ would train operators to ignore the warnings that matter.
//     ⚠ is reserved for "I could not tell".
//
//  4. EVERY OUTSIDE DEPENDENCY IS INJECTED. Exec, filesystem, DNS, HTTP, SMTP,
//     the object store and the database all arrive through Host and Prober, so
//     the tests are hermetic — a doctor whose own test suite needs a working
//     Docker daemon, a nameserver and an S3 bucket is a doctor nobody can change.
//     Every network and exec call is bounded by Options.Timeout, because a check
//     that hangs makes the whole command useless at exactly the moment it is
//     needed.
package doctor

import (
	"context"
	"sort"
	"time"

	"github.com/vidra/vidra-core/internal/preflight"
)

// Status is a check's outcome. It is preflight's, unchanged: the two packages
// answer the same question about the same host, and a caller printing both must
// not have to translate between two spellings of "warn".
type Status = preflight.Status

// The three outcomes, re-exported so callers of this package need not also
// import preflight to switch on a result.
const (
	StatusOK   = preflight.StatusOK
	StatusWarn = preflight.StatusWarn
	StatusFail = preflight.StatusFail
)

// Section groups related checks in the output. It is presentation, not
// behaviour: nothing branches on it.
type Section string

// The sections, in the order they print. They are ordered by what an operator
// does about them — a broken stack shape makes every reachability finding below
// it meaningless, so it is reported first.
const (
	SectionStack   Section = "docker & compose"
	SectionConfig  Section = "configuration"
	SectionState   Section = "data & state"
	SectionReach   Section = "reachability"
	sectionUnknown Section = "other"
)

var sectionOrder = []Section{SectionStack, SectionConfig, SectionState, SectionReach, sectionUnknown}

// Finding is one line of the report: what a check found, and — when there is
// something to do — what to do. Fix is empty for ✓ and for the ⚠ that only says
// a check could not run.
//
// Detail and Fix are ALWAYS operator-facing sentences. A check that has caught a
// Go error translates it here; it never passes err.Error() through.
type Finding struct {
	Status Status
	Detail string
	Fix    string
}

// Result is a Finding with the check it came from attached.
type Result struct {
	Check   string
	Section Section
	Finding
}

// Report is a whole run.
type Report struct {
	Results []Result
	// Root and EnvFile are what the run was pointed at, echoed so a report
	// pasted into an issue says which deployment it describes.
	Root    string
	EnvFile string
	Elapsed time.Duration
}

// Failed reports whether any check found a problem. It is the exit-code rule:
// ⚠ never fails a run, ✗ always does.
func (r Report) Failed() bool {
	for _, res := range r.Results {
		if res.Status == StatusFail {
			return true
		}
	}
	return false
}

// Counts totals the three outcomes, for the summary line.
func (r Report) Counts() (ok, warn, fail int) {
	for _, res := range r.Results {
		switch res.Status {
		case StatusOK:
			ok++
		case StatusWarn:
			warn++
		case StatusFail:
			fail++
		}
	}
	return ok, warn, fail
}

// check is one registry entry. run returns AT LEAST ONE finding — a check that
// found nothing wrong still says so, because a diagnostic that stays silent when
// it passes cannot be told apart from one that never ran.
//
// Several findings are returned when a check is naturally per-item: the
// configuration validator reports one line per bad variable, and the port
// audit one line per exposed service, which is what makes them actionable.
type check struct {
	name    string
	section Section
	run     func(context.Context, *state) []Finding
}

// checks is the registry, in run order. Adding a check is one entry here and
// one function; nothing else in this package knows how many there are.
//
// The order is deliberate and it is the order of causation. The compose version
// comes first because an old one invalidates the rendered model every check
// after it reads. The env file's own validity comes before anything that dials
// what the env file names. Reachability is last, because it is the slowest and
// the least useful when the four above it are red.
var checks = []check{
	{"compose version", SectionStack, checkComposeVersion},
	{"published ports", SectionStack, checkPublishedPorts},
	{"log caps", SectionStack, checkLogCaps},
	{"dev override", SectionStack, checkDevOverride},
	{"stray vidra-core/.env", SectionStack, checkStrayEnv},

	{"reverse proxy", SectionConfig, checkCaddyfile},
	{"domain DNS", SectionConfig, checkDomainDNS},
	{"env file vs template", SectionConfig, checkEnvTemplateDrift},
	{"configuration values", SectionConfig, checkConfigValues},

	{"schema ledger", SectionState, checkSchemaLedger},
	{"search ledger", SectionState, checkSearchLedger},
	{"backups", SectionState, checkBackupAge},
	{"backup timer", SectionState, checkBackupTimer},
	{"disk space", SectionState, checkDiskSpace},

	{"object storage", SectionReach, checkObjectStorage},
	{"smtp", SectionReach, checkSMTP},
	{"search service", SectionReach, checkSearchService},
	{"ffmpeg", SectionReach, checkFFmpeg},
}

// Run executes every check against one deployment and returns the report. It
// never returns an error for a finding — a finding IS the result — and reserves
// its error return for a run that could not be set up at all (a --repo that is
// not a directory), which is a usage problem rather than a diagnosis.
func Run(ctx context.Context, opt Options) (Report, error) {
	st, err := newState(opt)
	if err != nil {
		return Report{}, err
	}
	started := st.opt.Host.Now()
	report := Report{Root: st.root, EnvFile: st.envPath}
	for _, c := range checks {
		// Per-check deadline: one hung probe must not cost the whole report. The
		// budget is per check rather than per run so a slow-but-working object
		// store cannot starve the checks queued behind it.
		cctx, cancel := context.WithTimeout(ctx, st.opt.Timeout)
		findings := c.run(cctx, st)
		cancel()
		if len(findings) == 0 {
			// A check that returns nothing is a bug in that check, not a pass.
			// Saying so is better than an invisible gap in the report.
			findings = []Finding{{Status: StatusWarn, Detail: "the check returned no result"}}
		}
		for _, f := range findings {
			report.Results = append(report.Results, Result{Check: c.name, Section: c.section, Finding: f})
		}
	}
	report.Elapsed = st.opt.Host.Now().Sub(started)
	return report, nil
}

// sortedSections returns the sections present in a report, in print order.
func sortedSections(results []Result) []Section {
	seen := map[Section]bool{}
	for _, r := range results {
		seen[r.Section] = true
	}
	out := make([]Section, 0, len(seen))
	for _, s := range sectionOrder {
		if seen[s] {
			out = append(out, s)
			delete(seen, s)
		}
	}
	// Anything the order list does not mention still prints, after the rest.
	rest := make([]Section, 0, len(seen))
	for s := range seen {
		rest = append(rest, s)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i] < rest[j] })
	return append(out, rest...)
}

// The finding constructors. They exist so a check reads as a sentence and so the
// three outcomes cannot be spelled inconsistently across eighteen of them.

func okf(detail string) Finding { return Finding{Status: StatusOK, Detail: detail} }

func warnf(detail, fix string) Finding {
	return Finding{Status: StatusWarn, Detail: detail, Fix: fix}
}

// skipf is the ⚠ that means "this check could not run", with the reason. It
// carries no fix: there is nothing wrong to fix, and inventing one would make a
// gap in the report look like a finding.
func skipf(reason string) Finding {
	return Finding{Status: StatusWarn, Detail: "skipped: " + reason}
}

func failf(detail, fix string) Finding {
	return Finding{Status: StatusFail, Detail: detail, Fix: fix}
}
