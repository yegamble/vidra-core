package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The discovery tests never leave the machine. githubAPIBase is a variable for
// exactly this: an httptest server answers the two endpoints, so the ordering
// rule, the draft/prerelease filter and every refusal are proved here rather
// than against GitHub's uptime and this project's rate-limit budget.

// fakeGitHub serves /repos/<owner>/<repo>/releases and
// /repos/<owner>/<repo>/releases/tags/<tag> from a table, and points
// githubAPIBase at itself for the duration of one test.
type fakeGitHub struct {
	// releases is repo name -> that repository's releases.
	releases map[string][]release
	// status, when non-zero, is returned for every request instead of a document
	// — the rate-limit and outage cases.
	status int
	// mu guards the two recordings below. The handler runs on the server's
	// goroutine and the assertions on the test's, and `go test -race` is part of
	// the gate.
	mu sync.Mutex
	// requests records the paths asked for, in order.
	requests []string
	// tokens records the Authorization header of every request.
	tokens []string
}

// asked is the path of the n-th request, or "" if there was none.
func (g *fakeGitHub) asked(n int) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if n >= len(g.requests) {
		return ""
	}
	return g.requests[n]
}

// token is the Authorization header of the n-th request.
func (g *fakeGitHub) token(n int) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if n >= len(g.tokens) {
		return ""
	}
	return g.tokens[n]
}

func newFakeGitHub(t *testing.T, releases map[string][]release) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{releases: releases}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	prev := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = prev })
	return g
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.requests = append(g.requests, r.URL.Path)
	g.tokens = append(g.tokens, r.Header.Get("Authorization"))
	g.mu.Unlock()
	if g.status != 0 {
		w.WriteHeader(g.status)
		_, _ = w.Write([]byte(`{"message":"nope"}`))
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// /repos/<owner>/<repo>/releases[/tags/<tag>]
	if len(parts) < 4 || parts[0] != "repos" || parts[3] != "releases" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	repo := parts[2]
	rs, ok := g.releases[repo]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if len(parts) == 6 && parts[4] == "tags" {
		for _, rel := range rs {
			if rel.TagName == parts[5] && !rel.Draft {
				_ = json.NewEncoder(w).Encode(rel)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(rs)
}

// published is the ordinary case: a release anyone can see and an image behind.
func published(tags ...string) []release {
	out := make([]release, 0, len(tags))
	for _, t := range tags {
		out = append(out, release{TagName: t})
	}
	return out
}

func TestParseReleaseTagIsStrict(t *testing.T) {
	for _, tag := range []string{"v0.2.0", "v1.20.3", "v10.0.0"} {
		if _, ok := parseReleaseTag(tag); !ok {
			t.Errorf("%q is a release tag and was rejected", tag)
		}
	}
	// A prerelease suffix is deliberately NOT accepted here, unlike deploy.sh's
	// semver_ge: that function compares a tag somebody already chose, this one
	// decides what to deploy.
	for _, tag := range []string{"v0.2.0-rc1", "0.2.0", "v0.2", "v0.2.0.1", "latest", "vX.Y.Z", "v0.2.a", "", "v"} {
		if _, ok := parseReleaseTag(tag); ok {
			t.Errorf("%q is not a release tag and was accepted", tag)
		}
	}
}

// The order is by VERSION, not by the order GitHub returned. The day someone
// back-cuts a patch for an older minor, "newest by date" and "highest version"
// are different tags, and picking the first would roll a deployment backwards.
func TestReleaseTagsAreOrderedByVersion(t *testing.T) {
	got := releaseTags([]release{
		{TagName: "v0.1.9"},
		{TagName: "v0.10.0"},
		{TagName: "v0.2.0"},
		{TagName: "v0.2.0"}, // a duplicate, from a re-cut release
		{TagName: "v1.0.0"},
	})
	want := []string{"v0.1.9", "v0.2.0", "v0.10.0", "v1.0.0"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("releaseTags = %v, want %v", got, want)
	}
}

// A draft has no image behind it (publish-container.yml triggers on `release:
// published`), and a prerelease is a tag whose whole point is that somebody
// chose it deliberately. Neither may be picked by an unattended update.
func TestReleaseTagsSkipDraftsPrereleasesAndJunk(t *testing.T) {
	got := releaseTags([]release{
		{TagName: "v0.2.0"},
		{TagName: "v0.3.0", Draft: true},
		{TagName: "v0.4.0", Prerelease: true},
		{TagName: "v0.5.0-rc1"},
		{TagName: "nightly"},
	})
	if len(got) != 1 || got[0] != "v0.2.0" {
		t.Errorf("releaseTags = %v, want only [v0.2.0]", got)
	}
}

func TestListReleasesReadsTheDocument(t *testing.T) {
	g := newFakeGitHub(t, map[string][]release{coreRepo: published("v0.1.0", "v0.2.0")})
	rs, err := listReleases(context.Background(), nil, "someone", coreRepo)
	if err != nil {
		t.Fatalf("listReleases: %v", err)
	}
	if tags := releaseTags(rs); strings.Join(tags, ",") != "v0.1.0,v0.2.0" {
		t.Errorf("tags = %v", tags)
	}
	if got := g.asked(0); got != "/repos/someone/vidra-core/releases" {
		t.Errorf("asked for %q", got)
	}
}

// The rate limit is per IP address for unauthenticated requests, which a droplet
// behind a shared NAT reaches on its own. The fix is one environment variable, so
// the message names it rather than printing an HTTP status.
func TestListReleasesExplainsTheRateLimit(t *testing.T) {
	g := newFakeGitHub(t, map[string][]release{coreRepo: published("v0.2.0")})
	g.status = http.StatusForbidden
	_, err := listReleases(context.Background(), nil, "someone", coreRepo)
	if err == nil {
		t.Fatal("a 403 was not reported")
	}
	for _, want := range []string{"GITHUB_TOKEN", "rate-limit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the 403 message does not mention %q: %v", want, err)
		}
	}
}

func TestGitHubTokenIsSentWhenExported(t *testing.T) {
	g := newFakeGitHub(t, map[string][]release{coreRepo: published("v0.2.0")})
	if _, err := listReleases(context.Background(), map[string]string{"GITHUB_TOKEN": "  ghp_x  "}, "someone", coreRepo); err != nil {
		t.Fatalf("listReleases: %v", err)
	}
	if got := g.token(0); got != "Bearer ghp_x" {
		t.Errorf("Authorization = %q, want the trimmed bearer token", got)
	}

	// And nothing is sent when nothing is exported: these are public releases.
	g2 := newFakeGitHub(t, map[string][]release{coreRepo: published("v0.2.0")})
	if _, err := listReleases(context.Background(), nil, "someone", coreRepo); err != nil {
		t.Fatalf("listReleases: %v", err)
	}
	if got := g2.token(0); got != "" {
		t.Errorf("Authorization = %q on an unauthenticated request", got)
	}
}

// A 404 on the LIST is a repository that does not exist, which is an owner
// problem — and the owner is a value in the env file, so the message names the
// key rather than the URL.
func TestListReleasesNamesTheOwnerKey(t *testing.T) {
	newFakeGitHub(t, map[string][]release{coreRepo: published("v0.2.0")})
	_, err := listReleases(context.Background(), nil, "someone", "vidra-nope")
	if err == nil {
		t.Fatal("a missing repository was not reported")
	}
	if !strings.Contains(err.Error(), "VIDRA_IMAGE_OWNER") {
		t.Errorf("the message does not name the env-file key: %v", err)
	}
}

// A 404 on ONE TAG is an answer, not a failure: that repository has no release
// for it. Every other status is a failure to ask the question.
func TestHasReleaseSeparates404FromFailure(t *testing.T) {
	g := newFakeGitHub(t, map[string][]release{
		userRepo: append(published("v0.2.0"), release{TagName: "v0.3.0", Draft: true}),
	})
	for _, tc := range []struct {
		tag  string
		want bool
	}{
		{"v0.2.0", true},
		{"v0.9.9", false},
		// A draft is "no": it has no image behind it either way.
		{"v0.3.0", false},
	} {
		got, err := hasRelease(context.Background(), nil, "someone", userRepo, tc.tag)
		if err != nil {
			t.Fatalf("hasRelease(%s): %v", tc.tag, err)
		}
		if got != tc.want {
			t.Errorf("hasRelease(%s) = %v, want %v", tc.tag, got, tc.want)
		}
	}

	g.status = http.StatusInternalServerError
	if _, err := hasRelease(context.Background(), nil, "someone", userRepo, "v0.2.0"); err == nil {
		t.Error("a 500 was read as `that tag has no release`")
	}
}
