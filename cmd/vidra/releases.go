package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file is `vidra update`'s discovery half: which releases exist, which of
// them is the newest, and does the tag being asked for exist in all three
// component repositories.
//
// IT IS STDLIB net/http AND NOT `gh`. The command runs on a droplet that was
// installed with a shell one-liner; the GitHub CLI is not there, is not a
// dependency this project is willing to add to a host it does not control, and
// would have to be authenticated to be useful. The requests below need no
// credentials at all — public releases are public — and a GITHUB_TOKEN, when the
// operator has one exported, only buys a bigger rate-limit budget.

// githubAPIBase is the API root. It is a package variable for exactly one
// reason: the tests point it at an httptest server, so the suite proves the
// parsing, the ordering, the refusals and the rate-limit message without ever
// leaving the machine. A test that reached api.github.com would be testing
// GitHub's uptime and this project's rate-limit budget.
var githubAPIBase = "https://api.github.com"

// The three repositories a release spans. deploy/release.sh cuts all three with
// ONE tag, which is what makes "vidra v0.3.0" a meaningful thing to say — and
// what makes a tag that exists in only some of them a broken release rather than
// a partial update.
const (
	coreRepo   = "vidra-core"
	userRepo   = "vidra-user"
	searchRepo = "vidra-search"
)

// releaseHTTPTimeout is the budget for one GitHub request. Longer than
// status.go's loopback probes because this one crosses the internet from a
// droplet, and short enough that an update on a box with no route out fails in
// seconds rather than hanging at the first thing it prints.
const releaseHTTPTimeout = 15 * time.Second

// release is the subset of a GitHub release this reads. Everything else in that
// document — the body, the author, the assets — is someone else's business.
type release struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// semver is a vMAJOR.MINOR.PATCH release tag, parsed.
//
// The grammar is deliberately STRICTER than deploy.sh's semver_ge, which
// tolerates a -rc1 suffix: this code CHOOSES what to deploy, and a prerelease is
// not something to pick by accident. deploy.sh is comparing a tag an operator
// already chose against a floor, which is a different question.
type semver struct{ major, minor, patch int }

// parseReleaseTag accepts exactly ^v[0-9]+\.[0-9]+\.[0-9]+$ — no suffix, no
// "latest", no bare "1.2.3". Anything else is not a tag this command will offer.
func parseReleaseTag(tag string) (semver, bool) {
	rest, ok := strings.CutPrefix(tag, "v")
	if !ok {
		return semver{}, false
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var v semver
	for i, p := range parts {
		if p == "" || strings.ContainsFunc(p, func(r rune) bool { return r < '0' || r > '9' }) {
			return semver{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return semver{}, false
		}
		switch i {
		case 0:
			v.major = n
		case 1:
			v.minor = n
		case 2:
			v.patch = n
		}
	}
	return v, true
}

func (v semver) less(o semver) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	if v.minor != o.minor {
		return v.minor < o.minor
	}
	return v.patch < o.patch
}

func (v semver) String() string { return fmt.Sprintf("v%d.%d.%d", v.major, v.minor, v.patch) }

// releaseTags reduces a repository's releases to the tags this command will
// consider, OLDEST FIRST.
//
// Drafts and prereleases are dropped rather than ranked below the rest: a draft
// has no image behind it (publish-container.yml triggers on `release:
// published`), and a prerelease is a tag whose whole point is that somebody
// chose it deliberately. Anything not vMAJOR.MINOR.PATCH is dropped for the same
// reason.
//
// The ORDER is computed here rather than taken from the API's, which is by
// creation date. Those two agree until the day someone back-cuts a patch release
// for an older minor, and on that day "the newest release" and "the highest
// version" are different tags — the second is the one an update moves to, and
// the first would silently roll the deployment backwards.
func releaseTags(rs []release) []string {
	versions := make([]semver, 0, len(rs))
	seen := map[semver]bool{}
	for _, r := range rs {
		if r.Draft || r.Prerelease {
			continue
		}
		v, ok := parseReleaseTag(strings.TrimSpace(r.TagName))
		if !ok || seen[v] {
			continue
		}
		seen[v] = true
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].less(versions[j]) })
	out := make([]string, 0, len(versions))
	for _, v := range versions {
		out = append(out, v.String())
	}
	return out
}

// listReleases reads one repository's releases.
//
// per_page=100 and no pagination: this project cuts one release per component
// per version, so a hundred of them is years of history and the newest is always
// on the first page. A deployment old enough to fall off the end of it has a
// longer conversation to have than an unattended update.
func listReleases(ctx context.Context, environ map[string]string, owner, repo string) ([]release, error) {
	body, err := githubGet(ctx, environ, fmt.Sprintf("/repos/%s/%s/releases?per_page=100", owner, repo))
	var missing *notFoundError
	if errors.As(err, &missing) {
		return nil, fmt.Errorf("there is no repository %s/%s on GitHub. The owner comes from VIDRA_IMAGE_OWNER in the env file (GITHUB_OWNER is the other spelling); a fork has to set it to its own account", owner, repo)
	}
	if err != nil {
		return nil, err
	}
	var out []release
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("the release list for %s/%s was not the document this expects — if %s is a proxy or a mirror, it is answering with something else", owner, repo, githubAPIBase)
	}
	return out, nil
}

// hasRelease reports whether one repository has a PUBLISHED release for a tag.
//
// A draft is deliberately "no": /releases/tags returns drafts only to a token
// that can see them, and a draft release has no image behind it either way, so
// treating one as present would arm an update whose `docker compose pull` cannot
// succeed.
func hasRelease(ctx context.Context, environ map[string]string, owner, repo, tag string) (bool, error) {
	_, err := githubGet(ctx, environ, fmt.Sprintf("/repos/%s/%s/releases/tags/%s", owner, repo, tag))
	var missing *notFoundError
	if errors.As(err, &missing) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// notFoundError is a 404 kept apart from the other failures, because "that tag
// has no release" is an ANSWER to hasRelease's question while every other HTTP
// status is a failure to ask it.
type notFoundError struct{ path string }

func (e *notFoundError) Error() string { return "GitHub has nothing at " + e.path }

// githubGet is the one request this file makes, and the one place a GitHub
// failure is turned into a sentence an operator can act on.
//
// EVERY error return is operator-facing text. A raw Go transport error —
// `Get "https://api.github.com/...": dial tcp: lookup api.github.com: no such
// host` — is a stack trace with a URL in it, printed at the moment somebody is
// trying to find out whether they can update, and it answers a question they did
// not ask.
func githubGet(ctx context.Context, environ map[string]string, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIBase+path, nil)
	if err != nil {
		return nil, fmt.Errorf("%s is not a usable release URL — check VIDRA_IMAGE_OWNER in the env file", githubAPIBase+path)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	// GitHub rejects a request with no User-Agent outright, with a message about
	// user agents that reads as a bug in this tool.
	req.Header.Set("User-Agent", "vidra-cli")
	// The token is read from the environment the RUNNER reports, not os.Getenv,
	// for the same reason ENV_FILE is: that is the one seam this CLI has onto the
	// process environment, and a test that could not set it would be testing
	// whichever shell `go test` happened to run under.
	if token := strings.TrimSpace(environ["GITHUB_TOKEN"]); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: releaseHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub could not be reached (%s) — this host needs outbound HTTPS to look up releases. `vidra deploy` still ships whatever the env file already pins, and a tag can be written there by hand", githubAPIBase)
	}
	defer func() { _ = resp.Body.Close() }()
	// Bounded: a release list is tens of kilobytes, and whatever is on the other
	// end when it is NOT GitHub need not be able to make this command allocate.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusNotFound:
		return nil, &notFoundError{path: path}
	case http.StatusUnauthorized:
		return nil, errors.New("GitHub rejected the GITHUB_TOKEN in this environment (HTTP 401). Public releases need no token at all — unset it, or replace it with one that has not expired")
	case http.StatusForbidden, http.StatusTooManyRequests:
		// Unauthenticated requests are capped at 60 per hour PER IP ADDRESS, and
		// this command spends up to three of them. A droplet behind a shared NAT,
		// or an operator running `--check` from a cron entry, reaches it — and the
		// fix is one environment variable, so it is named.
		return nil, fmt.Errorf("GitHub is rate-limiting this host (HTTP %d). Unauthenticated requests are capped per IP address and the window resets within the hour; export GITHUB_TOKEN with any personal access token (it needs no scopes to read public releases) to raise the cap, or wait", resp.StatusCode)
	default:
		return nil, fmt.Errorf("GitHub answered HTTP %d for %s, which is neither a release document nor a refusal this knows how to read", resp.StatusCode, path)
	}
}
