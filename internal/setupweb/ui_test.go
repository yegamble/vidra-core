package setupweb

import (
	"regexp"
	"strings"
	"testing"
)

// The page is not exercised by these tests — a browser drives it, and that is a
// separate pass. What IS pinned here are the properties a browser could not tell
// you about until it was too late: that the wizard needs nothing from the
// network, that it carries no credential in its bytes, and that its nine steps
// and the API it calls are the ones this package actually serves.

func TestTheShellFetchesNothingFromAnywhere(t *testing.T) {
	t.Parallel()
	page := string(shell)
	// An operator is configuring a host whose outbound HTTPS may not work yet —
	// it is what this wizard is arranging — and every external reference is
	// another origin the server's Content-Security-Policy would have to allow.
	for _, forbidden := range []string{
		"<script src", "<link ", "@import", "<iframe", "<img ", "srcset=",
		"https://fonts.", "cdn.", "unpkg.", "jsdelivr",
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("the page references something outside itself: %q", forbidden)
		}
	}
	// Every request it makes is same-origin. An absolute URL would be a request
	// the CSP's connect-src 'self' refuses at runtime, which an operator meets as
	// a step that never finishes rather than as an error.
	if regexp.MustCompile("fetch\\(\\s*[\"'`]?[a-z]+://").MatchString(page) {
		t.Error("the page fetches an absolute URL; connect-src 'self' would refuse it")
	}
	// And every path literal it does use is one this server routes — see
	// TestTheShellHasTheNineStepsAndTheEndpointsBehindThem.
	if !strings.Contains(page, `const resp = await fetch(path, init)`) {
		t.Error("the api() helper no longer funnels every request through one place")
	}
	// No inline event handlers: they are the one thing script-src 'unsafe-inline'
	// would still block in a stricter policy, and they are unreadable besides.
	if regexp.MustCompile(`\son(click|change|blur|submit|load)\s*=`).MatchString(page) {
		t.Error("the page uses an inline event handler; bind with addEventListener instead")
	}
}

func TestTheShellCarriesNoCredentialAndNoSecret(t *testing.T) {
	t.Parallel()
	page := string(shell)
	// The token is READ from the one-time link at runtime and put in a header. It
	// is never baked into the document — a page with a credential in its bytes is
	// a credential in every cache, scrollback and screenshot of it.
	if !strings.Contains(page, "URLSearchParams(location.search).get(\"t\")") {
		t.Error("the page does not take its token from the opening link")
	}
	if !strings.Contains(page, `"X-Setup-Token": TOKEN`) {
		t.Error("the page does not send the token in the custom header the server requires")
	}
	// And it does not LEAVE the token in the address bar: it moves it into
	// sessionStorage and strips the query with replaceState, so it is not read off
	// the screen, not written into history, and not carried by a Referer.
	if !strings.Contains(page, `history.replaceState(null, "", "/")`) {
		t.Error("the page does not strip the token from the URL after reading it")
	}
	if !strings.Contains(page, `sessionStorage.setItem("vidra_setup_token"`) || !strings.Contains(page, `sessionStorage.getItem("vidra_setup_token"`) {
		t.Error("the page does not stash/restore the token in sessionStorage, so a reload would lose it")
	}
	// sessionStorage, not localStorage: a one-time install token should die with
	// the tab, not linger on the machine.
	if strings.Contains(page, "localStorage.") {
		t.Error("the page uses localStorage for the token; it must be tab-scoped sessionStorage")
	}
	// EventSource cannot set a header, which is exactly why the install stream is
	// a POST read with fetch. A page that reached for EventSource would need the
	// token in a URL.
	if strings.Contains(page, "new EventSource") {
		t.Error("the page uses EventSource, which cannot carry the token header")
	}
}

func TestTheShellHasTheNineStepsAndTheEndpointsBehindThem(t *testing.T) {
	t.Parallel()
	page := string(shell)
	for _, id := range []string{
		"s-welcome", "s-check", "s-mode", "s-domain", "s-storage",
		"s-features", "s-review", "s-install", "s-success",
	} {
		if !strings.Contains(page, `id="`+id+`"`) {
			t.Errorf("no section for step %q", id)
		}
	}
	// Every endpoint the server routes is reached, and nothing else is: a path
	// the page calls and the mux does not serve is a 404 an operator meets as a
	// step that never finishes.
	served := map[string]bool{
		"/api/state": true, "/api/validate": true, "/api/check-domain": true,
		"/api/doctor": true, "/api/review": true, "/api/apply": true,
		"/api/install": true, "/api/status": true, "/api/finish": true,
	}
	called := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(/api/[a-z-]+)"`).FindAllStringSubmatch(page, -1) {
		called[m[1]] = true
	}
	for path := range served {
		if !called[path] {
			t.Errorf("the page never calls %s", path)
		}
	}
	for path := range called {
		if !served[path] {
			t.Errorf("the page calls %s, which this server does not route", path)
		}
	}
}

func TestTheShellSpellsTheTLSModesTheEngineAccepts(t *testing.T) {
	t.Parallel()
	page := string(shell)
	// The mode list is presentation — the labels are the wizard's own — but the
	// VALUES have to be the engine's, because they are posted straight through to
	// NormalizeTLSMode. A typo here is a field an operator cannot get past.
	for _, mode := range []string{"acme", "acme-staging", "internal", "external", "plain-http"} {
		if !strings.Contains(page, `v: "`+mode+`"`) {
			t.Errorf("the TLS list is missing %q", mode)
		}
	}
}

// TestTheShellHasTheUXAffordancesTheAuditAskedFor locks the browser-pass fixes
// against a silent regression. It is structural, not behavioural — the behaviour
// is JavaScript a browser drives — but each of these is a hook the fix depends
// on, and losing one is losing the fix.
func TestTheShellHasTheUXAffordancesTheAuditAskedFor(t *testing.T) {
	t.Parallel()
	page := string(shell)
	for name, needle := range map[string]string{
		// #4: the Basic-mode static TLS value and its escape hatch, not a dead
		// one-item dropdown.
		"static TLS value":        `id="tls-static"`,
		"switch-to-advanced link": `id="tls-advanced-link"`,
		// #5: acme email excluded from the posted body when the mode hides it.
		"acme email gated in collect": "acmeMode ? $(\"f-acme\").value.trim() : \"\"",
		// #6: the authoritative mode that survives the Basic/Advanced toggle.
		"authoritative tls mode": "app.tlsMode",
		// #7: stale DNS cleared on domain input.
		"domain input clears DNS": `$("f-domain").addEventListener("input"`,
		// #8: first invalid field is scrolled to and focused.
		"focus the invalid field": "function focusInvalid",
		// #10: the docker/compose blocker lifted above the cosmetic skips.
		"elevated docker blocker": `"compose version" && c.status !== "ok"`,
		// a11y: inputs wired to their alert message.
		"aria-invalid wiring": `input.setAttribute("aria-invalid", "true")`,
	} {
		if !strings.Contains(page, needle) {
			t.Errorf("%s: the page is missing %q", name, needle)
		}
	}
	// #9: the step chips must not advertise themselves as clickable.
	if !strings.Contains(page, "cursor:default") {
		t.Error("the step chips still carry a pointer affordance")
	}
	// a11y: a disabled ghost button dims like the primary one; a blanket
	// button:disabled rule is how both get it.
	if !strings.Contains(page, "button:disabled{opacity:.45;cursor:not-allowed}") {
		t.Error("disabled buttons (ghost included) are not dimmed / not-allowed")
	}
}

func TestTheShellKeepsItsHandSyncCommentsToTheInterview(t *testing.T) {
	t.Parallel()
	page := string(shell)
	// The question TEXT is duplicated from cmd/vidra/setup.go's interview(), and
	// that was accepted deliberately — a form label does not read like a terminal
	// prompt. What makes it survivable is that every block says which interview
	// line it mirrors, so somebody changing one can find the other.
	if strings.Count(page, "Hand-sync:") < 4 {
		t.Error("the question blocks have lost their hand-sync references to cmd/vidra/setup.go's interview()")
	}
	if !strings.Contains(page, "setup.go:") {
		t.Error("no hand-sync comment names a line in the terminal interview")
	}
}
