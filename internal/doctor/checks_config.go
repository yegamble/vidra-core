package doctor

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/vidra/vidra-core/internal/preflight"
	"github.com/vidra/vidra-core/internal/setup"
)

// checkCaddyfile audits the file the caddy container actually bind-mounts.
//
// deploy/Caddyfile is the committed TEMPLATE; docker-compose.prod.yml mounts
// deploy/Caddyfile.local, which `vidra setup` renders from it. Three things go
// wrong with that file and all three are silent: it is missing (a missing
// bind-mount source is created as a DIRECTORY, which crash-loops Caddy), it
// still names the placeholder domain (Caddy orders a certificate for
// example.com and burns a rate limit), or its site address and PUBLIC_BASE_URL
// disagree (Caddy answers on one hostname while the api mints links, OAuth
// callbacks and federation actor ids for the other — every one of which is a
// bug reported somewhere else entirely).
func checkCaddyfile(_ context.Context, s *state) []Finding {
	rel := setup.CaddyOutputPath
	b, err := s.opt.Host.ReadFile(s.path(rel))
	if err != nil {
		if os.IsNotExist(err) {
			return []Finding{failf(
				rel+" is missing, and docker-compose.prod.yml bind-mounts it into the caddy container",
				"generate it with `vidra setup` (it renders from "+setup.CaddyTemplatePath+"). A missing bind-mount source is created as an empty DIRECTORY, which crash-loops Caddy — so this is not a warning about a future deploy, it is the deploy failing")}
		}
		return []Finding{skipf(rel + " could not be read")}
	}

	var findings []Finding
	site := ""
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// setup.PlaceholderDomain is deploy.sh's own anchored test, not a
		// substring search: a real domain that merely CONTAINS one of the
		// placeholders (myexample.com, video.example.company) is not a finding,
		// and a subdomain of one (tube.example.org) is. A second opinion here is
		// a doctor that refuses installs the deploy accepts, or blesses ones it
		// refuses — both of which this check has done.
		if ph, ok := setup.PlaceholderDomain(line); ok {
			findings = append(findings, failf(
				fmt.Sprintf("%s still names the placeholder domain %s on a non-comment line", rel, ph),
				"re-run `vidra setup --domain <your domain>` to regenerate it. Caddy would order a certificate for the placeholder, fail the challenge, and spend this host's Let's Encrypt validation budget doing it. deploy/deploy.sh refuses to deploy this file at all"))
		}
		if site == "" {
			if addr, ok := caddySiteAddress(raw); ok {
				site = addr
			}
		}
	}

	switch {
	case site == "":
		findings = append(findings, warnf(
			"no site address could be found in "+rel+" (expected a line like `video.example.org {` starting at column zero)",
			"re-run `vidra setup` to regenerate it from "+setup.CaddyTemplatePath+"; deploy.sh refuses to deploy a Caddyfile it cannot read a site address out of"))
	case s.envErr != nil:
		findings = append(findings, warnf(
			fmt.Sprintf("%s serves %s, but it could not be compared against PUBLIC_BASE_URL (%s)", rel, site, s.envErr),
			"fix the env file first: the two have to name the same host"))
	default:
		want, err := preflight.Host(s.value("PUBLIC_BASE_URL"))
		switch {
		case err != nil:
			findings = append(findings, failf(
				fmt.Sprintf("%s serves %s, but PUBLIC_BASE_URL in %s is not a usable origin", rel, site, s.envRel),
				"set PUBLIC_BASE_URL to this instance's public origin (https://video.example.org) and re-run `vidra setup`"))
		case !strings.EqualFold(site, want):
			findings = append(findings, failf(
				fmt.Sprintf("%s serves %s but PUBLIC_BASE_URL is https://%s", rel, site, want),
				"fix whichever is wrong and re-run `vidra setup` to regenerate the Caddyfile from PUBLIC_BASE_URL. Caddy answering on one hostname while the api mints links, OAuth callbacks and federation actor ids for another breaks logins and federation in ways that never point back here"))
		default:
			findings = append(findings, okf(fmt.Sprintf("%s serves %s, which matches PUBLIC_BASE_URL", rel, site)))
		}
	}
	return findings
}

// caddySiteAddress reads a site block's address: a line at column zero ending in
// an opening brace. It is deliberately the same shape deploy.sh greps for.
func caddySiteAddress(raw string) (string, bool) {
	if raw == "" || raw[0] == ' ' || raw[0] == '\t' || raw[0] == '#' {
		return "", false
	}
	line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
	if !strings.HasSuffix(line, "{") {
		return "", false
	}
	addr := strings.TrimSpace(strings.TrimSuffix(line, "{"))
	// A global options block opens with a bare `{`, and a snippet with
	// `(name) {`. Neither is a site address.
	if addr == "" || strings.HasPrefix(addr, "(") {
		return "", false
	}
	// Only the first address of a multi-address site block is compared: it is the
	// one PUBLIC_BASE_URL has to be.
	//
	// The emptiness check is not defensive programming, it is a crash that
	// happened: a Caddyfile.local line of `,, {` reduces to no fields at all, and
	// indexing [0] took the WHOLE report down — an operator debugging a broken
	// proxy config lost every other finding to the file they were debugging.
	fields := strings.Fields(strings.ReplaceAll(addr, ",", " "))
	if len(fields) == 0 {
		return "", false
	}
	host, err := preflight.Host(fields[0])
	if err != nil {
		return "", false
	}
	return host, true
}

// skipDNSPreflightVar is the escape hatch deploy.sh honours. doctor reads it
// from the PROCESS environment like deploy.sh does, and ALSO from the env file:
// an operator who wrote it into the deployment's own configuration has said what
// they mean at least as clearly as one who exported it.
const skipDNSPreflightVar = "VIDRA_SKIP_DNS_PREFLIGHT"

// checkDomainDNS is preflight's DNS check, gated exactly the way deploy.sh gates
// it: only for the ACME modes, and downgraded to a warning when the operator has
// deliberately turned it off.
//
// The gate is the point, and each mode outside it is skipped for its OWN reason
// — a permanent ✗ on a correctly configured instance is a diagnostic nobody
// reads twice. internal issues from Caddy's own CA and places no ACME order, so
// there is nothing for DNS to be wrong ABOUT; plain-http is a LAN/lab instance
// whose name may resolve only on an internal resolver, or be an IP; external
// points at the operator's own terminator, which is a DIFFERENT host from this
// one by definition, so "does this A record point here" is the wrong question.
// And when an order IS coming, a wrong A record does not fail politely — Let's
// Encrypt counts the failed authorisation against a per-hostname limit that
// locks this host out of the CA for the rest of the hour.
func checkDomainDNS(ctx context.Context, s *state) []Finding {
	if s.envErr != nil {
		return []Finding{skipf(fmt.Sprintf("the env file could not be read (%s), so there is no domain or TLS mode to check", s.envErr))}
	}
	mode := s.value("VIDRA_TLS_MODE")
	if mode == "" {
		mode = setup.TLSModeACME
	}
	if mode != setup.TLSModeACME && mode != setup.TLSModeACMEStaging {
		return []Finding{skipf(fmt.Sprintf("VIDRA_TLS_MODE=%s: %s, so no ACME order depends on this host's DNS", mode, tlsModeDNSReason(mode)))}
	}
	skipped := dnsPreflightSkipped(s.opt.Host.Getenv(skipDNSPreflightVar)) || dnsPreflightSkipped(s.value(skipDNSPreflightVar))

	res := s.opt.Prober.CheckDomain(ctx, preflight.DomainRequest{
		Domain:   s.value("PUBLIC_BASE_URL"),
		Expected: s.value("VIDRA_PUBLIC_IP"),
	})
	status := res.Status
	detail, fix := res.Message, res.Fix
	if skipped && status == StatusFail {
		// The operator asked deploy.sh not to enforce this, so doctor does not
		// enforce it either — it still SAYS it, because the consequence
		// (a rate-limit lockout on the next certificate order) has not gone away.
		status = StatusWarn
		detail = fmt.Sprintf("%s (%s is set, so this is reported and not enforced)", detail, skipDNSPreflightVar)
	}
	if detail == "" {
		detail = fmt.Sprintf("the DNS check returned no finding for %s", res.Domain)
		status = StatusWarn
	}
	return []Finding{{Status: status, Detail: detail, Fix: fix}}
}

// tlsModeDNSReason is the half-sentence checkDomainDNS's skip line is built
// from: WHY this mode places no ACME order. An unrecognised mode gets the
// conservative wording rather than a claim about a mode this binary predates —
// setup's own tlsIssues is what reports the value as unusable.
func tlsModeDNSReason(mode string) string {
	switch mode {
	case setup.TLSModeInternal:
		return "certificates are issued by Caddy's own local CA"
	case setup.TLSModePlainHTTP:
		return "this instance is served over plain HTTP with no certificate at all"
	case setup.TLSModeExternal:
		return "your own proxy terminates TLS, and the domain resolves to it rather than to this host"
	default:
		return "that mode orders no certificate from a public CA"
	}
}

// maxDriftNames caps how many variable names a drift line prints. The point of
// the check is "your file is out of step with the template"; the full list
// belongs in a diff, not in a one-line finding.
const maxDriftNames = 8

// checkEnvTemplateDrift compares the live env file against the template it came
// from. It is the check that catches an UPGRADE: a release that adds a required
// variable leaves every existing deployment's file silently short of it, and the
// symptom is an api that boots and then refuses one feature.
//
// Both directions are reported and both are ⚠, because neither is knowably
// wrong on its own — a key the template does not define may be an operator's
// deliberate extra (EXTRA_COMPOSE_PROFILES, a commented-out KEK they turned on),
// and a key the file lacks may be one whose default is fine. What turns either
// into a real problem is the configuration check below, which validates values
// rather than names.
func checkEnvTemplateDrift(_ context.Context, s *state) []Finding {
	if s.envErr != nil {
		return []Finding{skipf(fmt.Sprintf("the env file could not be read (%s)", s.envErr))}
	}
	rel := "env/production.env.example"
	b, err := s.opt.Host.ReadFile(s.path("env", "production.env.example"))
	if err != nil {
		return []Finding{skipf(rel + " is not in this checkout, so there is nothing to compare against")}
	}
	tmpl, err := setup.ParseEnvFile(b)
	if err != nil {
		return []Finding{skipf(rel + " could not be parsed")}
	}

	// The engine's OWN keys are not drift. Several of them appear in the template
	// only as commented-out examples (DATABASE_URL, REDIS_URL, SEARCH_REDIS_URL
	// are documented there, never assigned), so every external-service deployment
	// used to carry a permanent ⚠ naming the three variables `vidra setup` had
	// just written for it — a warning that is wrong on the deployments that need
	// this report most, and the fastest way to teach an operator to stop reading
	// the ⚠ column.
	managed := map[string]bool{}
	for _, k := range setup.ManagedKeys() {
		managed[k] = true
	}

	var missing, extra []string
	for _, k := range tmpl.Keys() {
		if _, ok := s.vars[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range s.vars {
		if !tmpl.Has(k) && !managed[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)

	var findings []Finding
	if len(missing) > 0 {
		findings = append(findings, warnf(
			fmt.Sprintf("%s defines %d key(s) %s does not assign: %s", rel, len(missing), s.envRel, nameList(missing)),
			"re-run `vidra setup --template "+rel+" --output "+s.envRel+" --yes` to fill them in — it preserves every value the file already sets, including the KEKs, and adds the ones a newer template introduced"))
	}
	if len(extra) > 0 {
		findings = append(findings, warnf(
			fmt.Sprintf("%s assigns %d key(s) %s does not define: %s", s.envRel, len(extra), rel, nameList(extra)),
			"check each one is still read by something. A key the template dropped is usually harmless (it is simply unused), but a MISSPELLED one looks exactly the same from here and takes its setting with it"))
	}
	if len(findings) == 0 {
		findings = append(findings, okf(fmt.Sprintf("%s assigns exactly the %d key(s) %s defines", s.envRel, len(tmpl.Keys()), rel)))
	}
	return findings
}

func nameList(names []string) string {
	if len(names) <= maxDriftNames {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:maxDriftNames], ", "), len(names)-maxDriftNames)
}

// checkConfigValues runs the api's OWN boot validation over the deployment's env
// file, plus the setup engine's placeholder and component rules. There is no
// second validation library here on purpose: what doctor reports is exactly what
// the api would refuse to boot on, reported before the deploy rather than after.
func checkConfigValues(_ context.Context, s *state) []Finding {
	if s.envErr != nil {
		return []Finding{failf(
			fmt.Sprintf("the deployment's env file cannot be read: %s", s.envErr),
			"every check below that reads configuration is blind without it. Generate one with `vidra setup --template env/production.env.example`, or point --env at the file this deployment really uses")}
	}
	issues := setup.Check(s.vars)
	warnings := setup.Warnings(s.vars)

	findings := make([]Finding, 0, len(issues)+len(warnings)+1)
	for _, is := range issues {
		detail := is.Msg
		fix := ""
		if is.Var != "" {
			detail = is.Var + ": " + is.Msg
			fix = "fix " + is.Var + " in " + s.envRel
		}
		findings = append(findings, failf(detail, fix))
	}
	for _, w := range warnings {
		findings = append(findings, warnf(w, ""))
	}
	if len(findings) == 0 {
		return []Finding{okf(fmt.Sprintf("%d variable(s) in %s pass the api's own boot validation", len(s.vars), s.envRel))}
	}
	return findings
}

// dnsPreflightSkipped reads VIDRA_SKIP_DNS_PREFLIGHT exactly as deploy.sh's
// require_dns_points_here does, which is NOT setup.IsTrue:
//
//	case "${VIDRA_SKIP_DNS_PREFLIGHT:-0}" in ""|0|false|no) ;; *) warn; return 0 ;; esac
//
// An opt-out is deliberately generous — anything that is not plainly "off" turns
// it off — because an operator who wrote something into that variable meant to
// disable the check, and a doctor that enforced what the deploy skips would
// report a ✗ nobody can act on. The keys the two READ AND WRITE together
// (VIDRA_EXTERNAL_*, MAIL_ENABLED) go through setup.IsTrue instead: there,
// agreeing with the shell means the same list, spelling for spelling.
func dnsPreflightSkipped(v string) bool {
	switch strings.TrimSpace(v) {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}
