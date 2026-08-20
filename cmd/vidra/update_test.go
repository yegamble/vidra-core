package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `vidra update` is tested end to end without a network, a docker daemon or a
// deployment: an httptest server is GitHub, a second one is the running api's
// /schemaz, the fake runner answers for git and for the two deploy scripts, and
// a temp directory is the deployment. Everything this command decides — which
// release, whether the schema allows it, whether a rollback is armed, what argv
// the scripts get — is observable in those four places.

// updateEnv is a deployment pinned to v0.2.0, with a comment above the tags so
// the rewrite has something to preserve.
const updateEnv = `# The released image tags. Bump these, then deploy.
VIDRA_CORE_TAG=v0.2.0
VIDRA_USER_TAG=v0.2.0
VIDRA_SEARCH_TAG=v0.2.0

VIDRA_IMAGE_OWNER=someone
JWT_SECRET=notasecret
HTTP_PORT=%s
FRONTEND_PORT=3000
VIDRA_COMPOSE_PROFILES=core frontend
`

type updateStage struct {
	t      *testing.T
	dir    string
	runner *fakeRunner
	github *fakeGitHub
	h      *harness

	// The running api's /schemaz answer.
	schemaStatus  int
	schemaVersion int64
	schemaApplied bool

	// What `git ls-tree <tag> -- migrations` prints, and whether git works at all.
	migrations string
	gitExit    int
}

func newUpdateStage(t *testing.T) *updateStage {
	t.Helper()
	st := &updateStage{
		t:             t,
		schemaStatus:  http.StatusOK,
		schemaVersion: 104,
		schemaApplied: true,
		// The target release carries migrations up to 110 — ahead of the database,
		// which is what an ordinary update looks like.
		migrations: "migrations/000109_a.up.sql\nmigrations/000109_a.down.sql\nmigrations/000110_b.up.sql\nmigrations/000110_b.down.sql\n",
	}
	api := httptest.NewServer(http.HandlerFunc(st.serveSchemaz))
	t.Cleanup(api.Close)
	st.dir = fakeDeployment(t, fmt.Sprintf(updateEnv, port(t, api.URL)))
	// deploy.sh reads the migration version out of this checkout; the pre-flight
	// reads it out of the same one with `git ls-tree`, which moves nothing.
	if err := os.MkdirAll(filepath.Join(st.dir, coreRepo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir core checkout: %v", err)
	}
	st.github = newFakeGitHub(t, map[string][]release{
		coreRepo:   published("v0.1.0", "v0.2.0", "v0.3.0"),
		userRepo:   published("v0.1.0", "v0.2.0", "v0.3.0"),
		searchRepo: published("v0.1.0", "v0.2.0", "v0.3.0"),
	})
	st.runner = swapRunner(t, &fakeRunner{})
	st.runner.onCapture = st.capture
	st.h = newHarness(t)
	return st
}

func (st *updateStage) serveSchemaz(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/schemaz" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if st.schemaStatus != http.StatusOK {
		w.WriteHeader(st.schemaStatus)
		return
	}
	writeJSON(w, http.StatusOK, fmt.Sprintf(
		`{"software":{"version":"v0.2.0","commit":"abc1234"},"schema":{"version":%d,"dirty":false,"applied":%t}}`,
		st.schemaVersion, st.schemaApplied))
}

// capture answers the two git commands the schema pre-flight runs. Nothing else
// is captured by this command.
func (st *updateStage) capture(spec execSpec) (execResult, error) {
	if spec.Path != "git" {
		st.t.Fatalf("something other than git was captured: %v", spec)
	}
	if st.gitExit != 0 {
		return execResult{ExitCode: st.gitExit, Stderr: "fatal: not a git repository"}, nil
	}
	for _, a := range spec.Args {
		if a == "ls-tree" {
			return execResult{Stdout: st.migrations}, nil
		}
	}
	return execResult{}, nil
}

func (st *updateStage) run(args ...string) error {
	return st.h.run(append([]string{"update", "-C", st.dir}, args...)...)
}

func (st *updateStage) out() string { return st.h.out.String() }

// pin rewrites all three tags in the deployment's env file, for the tests that
// are about where an update comes FROM.
func (st *updateStage) pin(tag string) {
	st.t.Helper()
	write(st.t, filepath.Join(st.dir, "env", "production.env"),
		strings.ReplaceAll(st.envFile(), "_TAG=v0.2.0", "_TAG="+tag))
}

// rollbackLine is the confirm screen's one-line verdict on the automatic
// rollback. The reason has to be readable THERE — on the screen an operator
// decides against — which is a different claim from "the words appear somewhere
// in the output".
func (st *updateStage) rollbackLine() string {
	for _, line := range strings.Split(st.out(), "\n") {
		if strings.HasPrefix(line, "rollback:") {
			return line
		}
	}
	return ""
}

// envFile is the deployment's env file as it stands now.
func (st *updateStage) envFile() string {
	st.t.Helper()
	b, err := os.ReadFile(filepath.Join(st.dir, "env", "production.env"))
	if err != nil {
		st.t.Fatalf("read env file: %v", err)
	}
	return string(b)
}

// scriptCalls is every deploy script this run executed, as "<script> <args…>"
// with the deployment root stripped off.
func (st *updateStage) scriptCalls() []string {
	var out []string
	for _, c := range st.runner.calls {
		if c.Path != "bash" {
			continue
		}
		rel, err := filepath.Rel(st.dir, c.script())
		if err != nil {
			rel = c.script()
		}
		out = append(out, strings.TrimSpace(rel+" "+strings.Join(c.tail(), " ")))
	}
	return out
}

func (st *updateStage) wantCalls(want ...string) {
	st.t.Helper()
	got := st.scriptCalls()
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		st.t.Errorf("scripts run:\n  got  %v\n  want %v", got, want)
	}
}

func contains(t *testing.T, haystack string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(haystack, want) {
			t.Errorf("missing %q in:\n%s", want, haystack)
		}
	}
}

// ---------------------------------------------------------------------------
// Discovery.

func TestUpdateCheckReportsCurrentAndLatest(t *testing.T) {
	st := newUpdateStage(t)
	if err := st.run("--check"); err != nil {
		t.Fatalf("--check = %v", err)
	}
	contains(t, st.out(), "core", "user", "search", "v0.2.0", "v0.3.0",
		"An update is available: v0.2.0 → v0.3.0.",
		"Nothing has been changed by this check.")
	// It changes nothing and runs nothing: not the deploy, not even a git fetch.
	if len(st.runner.calls) != 0 {
		t.Errorf("--check ran something: %v", st.runner.calls)
	}
	if !strings.Contains(st.envFile(), "VIDRA_CORE_TAG=v0.2.0") {
		t.Error("--check rewrote the env file")
	}
}

// The newest release is the highest VERSION, and a draft or a prerelease is not
// a candidate at all — a draft has no image behind it, and a prerelease is a tag
// somebody chose deliberately.
func TestUpdateSkipsDraftsAndPrereleases(t *testing.T) {
	st := newUpdateStage(t)
	st.github.releases[coreRepo] = append(published("v0.2.0", "v0.3.0"),
		release{TagName: "v0.4.0", Draft: true},
		release{TagName: "v0.5.0", Prerelease: true})
	if err := st.run("--check"); err != nil {
		t.Fatalf("--check = %v", err)
	}
	contains(t, st.out(), "v0.3.0")
	for _, unwanted := range []string{"v0.4.0", "v0.5.0"} {
		if strings.Contains(st.out(), unwanted) {
			t.Errorf("a draft or prerelease was offered:\n%s", st.out())
		}
	}
}

func TestUpdateRefusesATagThatIsNotAReleasedOne(t *testing.T) {
	st := newUpdateStage(t)
	err := st.run("--tag", "v9.9.9", "--yes")
	if err == nil {
		t.Fatal("an unreleased tag was accepted")
	}
	contains(t, err.Error(), "v9.9.9", "someone/vidra-core", "v0.3.0")
	if len(st.runner.calls) != 0 {
		t.Errorf("the refused run executed something: %v", st.runner.calls)
	}

	// And a tag that is not even semver-shaped is refused before a single request
	// is made.
	if err := st.run("--tag", "latest"); err == nil {
		t.Fatal("`--tag latest` was accepted")
	}
}

// deploy/release.sh cuts all three components with ONE tag. A tag that exists in
// only some of them is a broken release, and pinning it would fail the deploy's
// `pull` step AFTER the pre-deploy dump had already been taken.
func TestUpdateRefusesAHalfCutRelease(t *testing.T) {
	st := newUpdateStage(t)
	st.github.releases[searchRepo] = published("v0.1.0", "v0.2.0")
	err := st.run("--yes")
	if err == nil {
		t.Fatal("a release that only two of the three repositories carry was accepted")
	}
	// The refusal names WHICH repository is missing it: that is the difference
	// between "wait for the release" and "go and look at three tabs".
	contains(t, err.Error(), "someone/vidra-search", "v0.3.0")
	if strings.Contains(err.Error(), "vidra-user") {
		t.Errorf("the refusal blames a repository that has the release: %v", err)
	}
	if len(st.runner.calls) != 0 {
		t.Errorf("the refused run executed something: %v", st.runner.calls)
	}
}

func TestUpdateReportsARateLimitWithTheWayOut(t *testing.T) {
	st := newUpdateStage(t)
	st.github.status = http.StatusForbidden
	err := st.run("--check")
	if err == nil {
		t.Fatal("a rate-limited lookup reported success")
	}
	contains(t, err.Error(), "GITHUB_TOKEN")
}

func TestUpdateSaysWhenThereIsNothingToDo(t *testing.T) {
	st := newUpdateStage(t)
	st.github.releases[coreRepo] = published("v0.1.0", "v0.2.0")
	if err := st.run("--yes"); err != nil {
		t.Fatalf("an up-to-date deployment reported an error: %v", err)
	}
	contains(t, st.out(), "Already on v0.2.0")
	if len(st.runner.calls) != 0 {
		t.Errorf("an up-to-date deployment deployed anyway: %v", st.runner.calls)
	}
}

// Going backwards is `vidra rollback`, which re-probes and whose header carries
// the restore-first sequence. Doing it under the name "update" would also skip
// the question of whether the newer release migrated the database.
func TestUpdateRefusesToGoBackwards(t *testing.T) {
	st := newUpdateStage(t)
	err := st.run("--tag", "v0.1.0", "--yes")
	if err == nil {
		t.Fatal("an update to an older release was accepted")
	}
	contains(t, err.Error(), "vidra rollback v0.1.0", "OLDER")
}

// ---------------------------------------------------------------------------
// The schema floor.

// THE REFUSAL /schemaz's numeric version exists for. An image whose embedded
// migrations are behind the database comes up green — its own `migrate up` is a
// no-op — and then serves code that predates columns it reads. deploy.sh cannot
// catch it: it compares the ledger against the checkout it just moved to the
// same tag, so it compares the image with itself.
func TestUpdateRefusesAnImageBehindTheDatabase(t *testing.T) {
	st := newUpdateStage(t)
	st.schemaVersion = 200
	st.migrations = "migrations/000110_b.up.sql\n"
	err := st.run("--yes")
	if err == nil {
		t.Fatal("an image whose migrations are older than the database was deployed")
	}
	contains(t, err.Error(), "110", "200", "v0.3.0")
	st.wantCalls()
	if !strings.Contains(st.envFile(), "VIDRA_CORE_TAG=v0.2.0") {
		t.Error("the refused update rewrote the env file")
	}
}

// "Not provably wrong" must never become "refuse". Every way of failing to READ
// the two numbers is a note and a warning, because a command that refused
// whenever it could not prove an update safe would refuse on every host whose
// stack is currently down — which is most of the hosts anybody updates.
func TestUpdateWarnsAndContinuesWhenTheFloorCannotBeRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*updateStage)
		want string
	}{
		{"an api image older than /schemaz", func(st *updateStage) { st.schemaStatus = http.StatusNotFound }, "predates /schemaz"},
		{"a database nothing has migrated yet", func(st *updateStage) { st.schemaApplied = false }, "no migration has ever run"},
		{"a git that cannot list the tag", func(st *updateStage) { st.gitExit = 128 }, "could not be read"},
		{"a release with no migrations directory", func(st *updateStage) { st.migrations = "" }, "no migrations/*.up.sql"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newUpdateStage(t)
			tc.set(st)
			if err := st.run("--yes"); err != nil {
				t.Fatalf("the update was refused for something unprovable: %v", err)
			}
			contains(t, st.out(), "note:", tc.want)
			st.wantCalls("deploy/deploy.sh")
		})
	}
}

// A deployment with no vidra-core checkout cannot be asked what the target's
// migrations are. deploy.sh tolerates the same thing (`[ -e "$repo" ] || continue`).
func TestUpdateWarnsWhenThereIsNoCoreCheckout(t *testing.T) {
	st := newUpdateStage(t)
	if err := os.RemoveAll(filepath.Join(st.dir, coreRepo)); err != nil {
		t.Fatalf("rm checkout: %v", err)
	}
	if err := st.run("--yes"); err != nil {
		t.Fatalf("update = %v", err)
	}
	contains(t, st.out(), "note:", "no vidra-core checkout")
}

// The pre-flight reads the tag's tree; it must not move the working directory,
// which deploy.sh is about to do for itself.
func TestUpdateReadsTheTargetsMigrationsWithoutCheckingOut(t *testing.T) {
	st := newUpdateStage(t)
	if err := st.run("--yes"); err != nil {
		t.Fatalf("update = %v", err)
	}
	var sawFetch, sawList bool
	for _, c := range st.runner.calls {
		if c.Path != "git" {
			continue
		}
		line := strings.Join(c.Args, " ")
		switch {
		case strings.Contains(line, "fetch --tags --force --quiet"):
			sawFetch = true
		case line == "-C "+filepath.Join(st.dir, coreRepo)+" ls-tree -r --name-only v0.3.0 -- migrations":
			sawList = true
		}
		if strings.Contains(line, "checkout") {
			t.Errorf("the pre-flight moved the checkout: %v", c.Args)
		}
	}
	if !sawFetch || !sawList {
		t.Errorf("the target's migrations were not read from the tag's tree: %v", st.runner.calls)
	}
}

func TestHighestMigrationIsDeployShsArithmetic(t *testing.T) {
	// Leading zeros are base-10, not octal; the highest wins numerically and not
	// lexically; down migrations and nested paths are not counted.
	got, ok := highestMigration(strings.Join([]string{
		"migrations/000009_early.up.sql",
		"migrations/000104_later.up.sql",
		"migrations/000104_later.down.sql",
		"migrations/000110_last.up.sql",
		"migrations/archive/000999_old.up.sql",
		"migrations/README.md",
	}, "\n"))
	if !ok || got != 110 {
		t.Errorf("highestMigration = %d (%v), want 110", got, ok)
	}
	if _, ok := highestMigration("migrations/README.md\n"); ok {
		t.Error("a directory with no migrations reported a version")
	}
}

// ---------------------------------------------------------------------------
// Arming.

func TestUpdateArmsRollbackForTheNextRelease(t *testing.T) {
	st := newUpdateStage(t)
	if err := st.run("--yes"); err != nil {
		t.Fatalf("update = %v", err)
	}
	contains(t, st.out(), "rollback: ARMED", "core=v0.2.0 user=v0.2.0 search=v0.2.0")
}

// The schema-compat policy covers ONE release: release N's schema keeps release
// N-1's code running, and says nothing about N-2. Skipping a release therefore
// disarms the tag flip, and says so before the operator confirms.
func TestUpdateDisarmsRollbackAcrossTwoReleases(t *testing.T) {
	st := newUpdateStage(t)
	st.github.releases[coreRepo] = published("v0.1.0", "v0.2.0", "v0.3.0", "v0.4.0")
	st.github.releases[userRepo] = published("v0.1.0", "v0.2.0", "v0.3.0", "v0.4.0")
	st.github.releases[searchRepo] = published("v0.1.0", "v0.2.0", "v0.3.0", "v0.4.0")
	if err := st.run("--yes"); err != nil {
		t.Fatalf("update = %v", err)
	}
	contains(t, st.out(), "rollback: NOT ARMED", "2 releases ahead", "restore.sh")
}

func TestUpdateDisarmsRollbackOnRequest(t *testing.T) {
	st := newUpdateStage(t)
	if err := st.run("--yes", "--no-rollback"); err != nil {
		t.Fatalf("update = %v", err)
	}
	contains(t, st.out(), "rollback: NOT ARMED", "--no-rollback")
}

// A tag that is not in the release list at all — deleted, or never a release —
// makes "how far back would a flip go" unanswerable, and unanswerable disarms.
//
// The deployment stays pinned at v0.2.0 and it is the RELEASE LIST that no longer
// carries it, which is the deleted-release case. Pinning something older would
// disarm for the floor instead (rollback.sh refuses a core tag below v0.2.0), and
// this test would then be asserting the other reason's text.
func TestUpdateDisarmsWhenTheRunningTagIsNotARelease(t *testing.T) {
	st := newUpdateStage(t)
	st.github.releases[coreRepo] = published("v0.1.0", "v0.2.5", "v0.3.0")
	if err := st.run("--yes"); err != nil {
		t.Fatalf("update = %v", err)
	}
	contains(t, st.out(), "rollback: NOT ARMED", "v0.2.0", "published releases")
}

// ---------------------------------------------------------------------------
// Confirmation.

func TestUpdateRefusesToConfirmWithAPipe(t *testing.T) {
	st := newUpdateStage(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = r.Close() }()
	// A pipe with a "y" in it is still refused: the point is that the descriptor
	// is not a terminal, not that it happens to be empty.
	if _, err := w.WriteString("y\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = w.Close()

	var out, errBuf bytes.Buffer
	err = run(streams{in: r, out: &out, err: &errBuf}, []string{"update", "-C", st.dir})
	if err == nil {
		t.Fatal("an update was confirmed by a pipe")
	}
	contains(t, err.Error(), "--yes", "terminal")
	st.wantCalls()

	// --yes is the answer the message asks for, and it deploys.
	if err := st.run("--yes"); err != nil {
		t.Fatalf("--yes = %v", err)
	}
	st.wantCalls("deploy/deploy.sh")
}

func TestUpdateStopsWhenTheAnswerIsNo(t *testing.T) {
	st := newUpdateStage(t)
	st.h.stdin = "n\n"
	err := st.run()
	if err == nil {
		t.Fatal("declining the update exited 0 — a script chaining on it would read that as done")
	}
	if !errors.Is(err, errReported) {
		t.Errorf("declining printed a second error line: %v", err)
	}
	contains(t, st.out(), "Nothing was changed.")
	st.wantCalls()
	if !strings.Contains(st.envFile(), "VIDRA_CORE_TAG=v0.2.0") {
		t.Error("a declined update rewrote the env file")
	}
}

func TestUpdateAcceptsAYes(t *testing.T) {
	st := newUpdateStage(t)
	st.h.stdin = "y\n"
	if err := st.run(); err != nil {
		t.Fatalf("update = %v", err)
	}
	st.wantCalls("deploy/deploy.sh")
}

// ---------------------------------------------------------------------------
// Doing it.

func TestUpdateSnapshotsBumpsAndDeploys(t *testing.T) {
	st := newUpdateStage(t)
	if err := st.run("--yes"); err != nil {
		t.Fatalf("update = %v", err)
	}
	// 1. the previous env file is in the history, whole and unreadable by anyone
	//    else.
	dir := filepath.Join(st.dir, "backups", "env-history")
	names := historyNames(t, dir)
	if len(names) != 1 || !strings.HasPrefix(names[0], "production.env.") {
		t.Fatalf("history holds %v", names)
	}
	snapshot, err := os.ReadFile(filepath.Join(dir, names[0]))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !strings.Contains(string(snapshot), "VIDRA_CORE_TAG=v0.2.0") {
		t.Errorf("the snapshot is not the PREVIOUS env file:\n%s", snapshot)
	}
	// 2. the env file carries the new tags and nothing else moved.
	got := st.envFile()
	contains(t, got, "VIDRA_CORE_TAG=v0.3.0", "VIDRA_USER_TAG=v0.3.0", "VIDRA_SEARCH_TAG=v0.3.0",
		"# The released image tags. Bump these, then deploy.", "JWT_SECRET=notasecret")
	// 3. deploy.sh ran, with NO arguments: what it deploys is what the file pins.
	st.wantCalls("deploy/deploy.sh")
	spec := st.runner.calls[len(st.runner.calls)-1]
	if spec.Path != "bash" || len(spec.Args) != 1 {
		t.Errorf("deploy.sh was given arguments it does not take: %v", spec.Args)
	}
	if envFile, _ := spec.envValue("ENV_FILE"); envFile != "env/production.env" {
		t.Errorf("ENV_FILE = %q", envFile)
	}
	if spec.Dir != st.dir {
		t.Errorf("deploy ran in %q, want the deployment root %q", spec.Dir, st.dir)
	}
	contains(t, st.out(), "snapshot:", "pinned: core=v0.3.0", "v0.3.0 is deployed")
}

// The env file the operator named is the one that is snapshotted, bumped and
// exported — including under an exported ENV_FILE, which beats the flag default.
func TestUpdateFollowsTheEnvFileInUse(t *testing.T) {
	st := newUpdateStage(t)
	staging := filepath.Join(st.dir, "env", "staging.env")
	write(t, staging, st.envFile())
	st.runner.environ = []string{"ENV_FILE=env/staging.env"}

	if err := st.run("--yes"); err != nil {
		t.Fatalf("update = %v", err)
	}
	names := historyNames(t, filepath.Join(st.dir, "backups", "env-history"))
	if len(names) != 1 || !strings.HasPrefix(names[0], "staging.env.") {
		t.Errorf("the history is %v, want a staging.env snapshot", names)
	}
	b, err := os.ReadFile(staging)
	if err != nil {
		t.Fatalf("read staging: %v", err)
	}
	if !strings.Contains(string(b), "VIDRA_CORE_TAG=v0.3.0") {
		t.Error("staging.env was not bumped")
	}
	if strings.Contains(st.envFile(), "v0.3.0") {
		t.Error("production.env was bumped while the run was about staging")
	}
}

// Compose resolves ${VIDRA_CORE_TAG} from the SHELL before it falls back to
// --env-file, so an exported tag beats the file this command rewrites. Without
// this refusal every line the command printed would describe something other
// than what deployed.
func TestUpdateRefusesWhenATagIsExported(t *testing.T) {
	st := newUpdateStage(t)
	st.runner.environ = []string{"VIDRA_USER_TAG=v0.1.0"}
	err := st.run("--yes")
	if err == nil {
		t.Fatal("an exported tag was allowed to shadow the file being rewritten")
	}
	contains(t, err.Error(), "VIDRA_USER_TAG", "unset")
	st.wantCalls()
}

// ---------------------------------------------------------------------------
// The failure path.

// failingDeploy makes deploy.sh exit non-zero, as it does when its health probes
// never come up, and lets rollback.sh answer however the test wants.
func failingDeploy(code int, rollback error) func(execSpec, streams) error {
	return func(spec execSpec, _ streams) error {
		if strings.HasSuffix(spec.script(), "deploy.sh") {
			return newExitError(code)
		}
		return rollback
	}
}

func TestUpdateFlipsTheTagsBackWhenTheDeployFails(t *testing.T) {
	st := newUpdateStage(t)
	st.runner.onPassthrough = failingDeploy(1, nil)

	err := st.run("--yes")
	if err == nil {
		t.Fatal("a failed deploy reported success")
	}
	// The rollback is per-component and carries the tags that were running, not a
	// bare tag: an earlier partial rollback can leave the three apart, and
	// `rollback.sh <tag>` would set all three to one value.
	st.wantCalls("deploy/deploy.sh", "deploy/rollback.sh --core v0.2.0 --user v0.2.0 --search v0.2.0")
	contains(t, st.h.err.String(),
		"THE DEPLOY OF v0.3.0 FAILED",
		"THE UPDATE TO v0.3.0 FAILED AND WAS ROLLED BACK",
		"./deploy/restore.sh",
		"backups/env-history/production.env.")
	// Non-zero even though the rollback worked perfectly: the release that was
	// asked for is not the one running.
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != 1 {
		t.Errorf("exit = %v, want the deploy's own non-zero code", err)
	}
}

func TestUpdateIsLoudWhenTheRollbackAlsoFails(t *testing.T) {
	st := newUpdateStage(t)
	st.runner.onPassthrough = failingDeploy(3, newExitError(1))
	// A dump deploy.sh took before it started, so the restore line is a command
	// to paste rather than a shape to fill in.
	if err := os.MkdirAll(filepath.Join(st.dir, "backups"), 0o700); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	write(t, filepath.Join(st.dir, "backups", "pre-deploy-2026-08-20T101500.dump.gz"), "x")
	write(t, filepath.Join(st.dir, "backups", "pre-deploy-2026-08-19T101500.dump.gz"), "x")

	err := st.run("--yes")
	if err == nil {
		t.Fatal("a double failure reported success")
	}
	contains(t, st.h.err.String(),
		"THE ROLLBACK ALSO FAILED",
		"./deploy/rollback.sh v0.2.0",
		"./deploy/restore.sh backups/pre-deploy-2026-08-20T101500.dump.gz")
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != 3 {
		t.Errorf("exit = %v, want deploy.sh's own code 3", err)
	}
}

// A disarmed update leaves deploy.sh's own failure text standing and adds one
// line: why nothing was flipped back, and where the previous env file is.
func TestUpdateLeavesADisarmedFailureAlone(t *testing.T) {
	st := newUpdateStage(t)
	st.runner.onPassthrough = failingDeploy(1, nil)

	err := st.run("--yes", "--no-rollback")
	if err == nil {
		t.Fatal("a failed deploy reported success")
	}
	st.wantCalls("deploy/deploy.sh")
	contains(t, st.h.err.String(), "Automatic rollback was NOT armed", "--no-rollback", "backups/env-history/")
	if strings.Contains(st.h.err.String(), "ROLLED BACK") {
		t.Errorf("a disarmed run claimed a rollback:\n%s", st.h.err.String())
	}
	// The tags stay where the update put them, because that is where the deploy
	// left the stack.
	contains(t, st.envFile(), "VIDRA_CORE_TAG=v0.3.0")
}

// Arming promises a recovery; a deployment with no rollback.sh cannot deliver
// one, and finding that out during an incident is too late.
func TestUpdateDisarmsWithoutARollbackScript(t *testing.T) {
	st := newUpdateStage(t)
	if err := os.Remove(filepath.Join(st.dir, "deploy", "rollback.sh")); err != nil {
		t.Fatalf("rm rollback.sh: %v", err)
	}
	if err := st.run("--yes"); err != nil {
		t.Fatalf("update = %v", err)
	}
	contains(t, st.out(), "rollback: NOT ARMED", "deploy/rollback.sh is not in this deployment")
}

// ---------------------------------------------------------------------------
// Flags and usage.

func TestUpdateRefusesAFlagItDoesNotKnow(t *testing.T) {
	st := newUpdateStage(t)
	// Unlike the wrapper commands, update forwards nothing to a script — so an
	// unknown flag is a mistake, not an argument that belongs to somebody else.
	// `--dry-run` silently performing the update is the failure this prevents.
	if err := st.run("--dry-run"); err == nil {
		t.Fatal("--dry-run was accepted")
	}
	contains(t, st.h.err.String(), "--dry-run", "--check")
	st.wantCalls()
}

func TestUpdateHelpNamesTheContract(t *testing.T) {
	st := newUpdateStage(t)
	if err := st.run("-h"); err != nil {
		t.Fatalf("-h = %v", err)
	}
	contains(t, st.out(), "--check", "--tag", "--yes", "--no-rollback",
		"backups/env-history", "ONE release", "/schemaz")
	if len(st.runner.calls) != 0 {
		t.Errorf("-h ran something: %v", st.runner.calls)
	}
}

func TestUpdateIsInTheCommandTable(t *testing.T) {
	var out bytes.Buffer
	usage(&out)
	contains(t, out.String(), "update")
}
