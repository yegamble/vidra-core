package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The env file that the tests below snapshot and rewrite. It is deliberately
// shaped like the real one: a header, a paragraph above the values, a
// commented-out key and a secret, so "the rewrite preserved everything else" is
// a claim about something.
const historyEnv = `# ------------------------------------------------------------------------------
# Vidra deployment
# ------------------------------------------------------------------------------

# The released image tags. Bump these, then deploy.
VIDRA_CORE_TAG=v0.2.0
VIDRA_USER_TAG=v0.2.0
VIDRA_SEARCH_TAG=v0.2.0

# DESTRUCTIVE TO ROTATE: every session is invalidated.
JWT_SECRET=notasecret
#DATABASE_URL=postgres://managed.example.org/vidra
HTTP_PORT=8080
`

func TestSnapshotWritesThePreviousEnvFileAt0600(t *testing.T) {
	dir := fakeDeployment(t, historyEnv)
	envPath := filepath.Join(dir, "env", "production.env")
	at := time.Date(2026, 8, 20, 13, 45, 0, 0, time.UTC)

	path, err := snapshotEnvFile(dir, envPath, envHistoryKeepDefault, at)
	if err != nil {
		t.Fatalf("snapshotEnvFile: %v", err)
	}
	// THE PATH CONVENTION. It is shared with the deployment's bash scripts, which
	// snapshot into the same directory under the same name — an operator reading
	// `ls backups/env-history/` has to see one history, not two interleaved ones.
	want := filepath.Join(dir, "backups", "env-history", "production.env.20260820T134500Z")
	if path != want {
		t.Errorf("snapshot at %s, want %s", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(got) != historyEnv {
		t.Errorf("the snapshot is not the env file:\n%s", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// The file holds JWT_SECRET and the database password. A history directory
	// that widened them would be a credential leak introduced by a safety feature.
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("snapshot mode %o, want 600", mode)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("history directory mode %o, want 700", mode)
	}
}

// A directory deploy/lib.sh's env_snapshot created first, under a wide umask.
// MkdirAll would leave it exactly as it found it — its mode argument only
// applies to directories it creates — so the snapshot's own 0600 would sit inside
// a world-readable directory for the life of the host. lib.sh chmods 700 on every
// call for this reason; so does this.
func TestSnapshotTightensADirectoryItDidNotCreate(t *testing.T) {
	dir := fakeDeployment(t, historyEnv)
	history := filepath.Join(dir, "backups", "env-history")
	if err := os.MkdirAll(history, 0o755); err != nil {
		t.Fatalf("mkdir history: %v", err)
	}
	// Explicitly, because MkdirAll's mode is masked by the process umask and the
	// point of this test is a directory that really is 0755.
	if err := os.Chmod(history, 0o755); err != nil {
		t.Fatalf("chmod history: %v", err)
	}
	if _, err := snapshotEnvFile(dir, filepath.Join(dir, "env", "production.env"),
		envHistoryKeepDefault, time.Date(2026, 8, 20, 13, 45, 0, 0, time.UTC)); err != nil {
		t.Fatalf("snapshotEnvFile: %v", err)
	}
	info, err := os.Stat(history)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("history directory left at %o, want 700 — a pre-existing loose directory was not tightened", mode)
	}
}

// The retention count is half of a cross-language contract: deploy/lib.sh prunes
// the same directory with ${VIDRA_ENV_HISTORY_KEEP:-10}, so a Go side hardcoded
// at ten deletes the generations an operator raised the shell side to keep.
func TestEnvHistoryKeepFollowsTheSharedVariable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		processEnv map[string]string
		values     map[string]string
		want       int
		warn       string
	}{
		{name: "nobody set it", want: envHistoryKeepDefault},
		{name: "the env file sets it", values: map[string]string{envHistoryKeepVar: "30"}, want: 30},
		{name: "a quoted value in the env file", values: map[string]string{envHistoryKeepVar: `"3"`}, want: 3},
		{
			// envGet's precedence, which is lib.sh's: a real environment variable
			// beats the file, because that is how the shell half would read it too.
			name:       "the process environment beats the file",
			processEnv: map[string]string{envHistoryKeepVar: "2"},
			values:     map[string]string{envHistoryKeepVar: "30"},
			want:       2,
		},
		{name: "a typo", values: map[string]string{envHistoryKeepVar: "ten"}, want: envHistoryKeepDefault, warn: "ten"},
		{name: "zero", values: map[string]string{envHistoryKeepVar: "0"}, want: envHistoryKeepDefault, warn: "0"},
		{name: "negative", values: map[string]string{envHistoryKeepVar: "-3"}, want: envHistoryKeepDefault, warn: "-3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, warn := envHistoryKeepValue(tc.processEnv, tc.values)
			if got != tc.want {
				t.Errorf("keep = %d, want %d", got, tc.want)
			}
			switch {
			case tc.warn == "" && warn != "":
				t.Errorf("a usable value warned: %s", warn)
			case tc.warn != "":
				// The warning names the value, not just the variable: an operator who
				// has to go and find the typo needs to know what it looks like.
				contains(t, warn, envHistoryKeepVar, tc.warn, "10")
			}
		})
	}
}

// An operator on --env env/staging.env gets staging.env.<stamp>, so two
// deployments out of one checkout keep two histories that do not pretend to be
// each other.
func TestSnapshotUsesTheEnvFilesOwnName(t *testing.T) {
	dir := fakeDeployment(t, historyEnv)
	staging := filepath.Join(dir, "env", "staging.env")
	write(t, staging, historyEnv)
	path, err := snapshotEnvFile(dir, staging, envHistoryKeepDefault, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatalf("snapshotEnvFile: %v", err)
	}
	if base := filepath.Base(path); base != "staging.env.20260102T030405Z" {
		t.Errorf("snapshot named %q, want staging.env.20260102T030405Z", base)
	}
}

// THE WHOLE POINT OF THE DIRECTORY. rollback.sh keeps one .bak, so after two
// rollbacks it holds the middle of the incident. Ten generations survive here,
// and the newest ten are the ones that do.
func TestSnapshotPrunesToTenGenerations(t *testing.T) {
	dir := fakeDeployment(t, historyEnv)
	envPath := filepath.Join(dir, "env", "production.env")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 15 {
		if _, err := snapshotEnvFile(dir, envPath, envHistoryKeepDefault, base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("snapshot %d: %v", i, err)
		}
	}
	names := historyNames(t, filepath.Join(dir, "backups", "env-history"))
	if len(names) != envHistoryKeepDefault {
		t.Fatalf("%d snapshots kept, want %d: %v", len(names), envHistoryKeepDefault, names)
	}
	// The names sort chronologically, so the survivors are hours 5..14.
	if names[0] != "production.env.20260101T050000Z" || names[len(names)-1] != "production.env.20260101T140000Z" {
		t.Errorf("the wrong ten survived: %v", names)
	}
}

// Two deployments out of one checkout: production's churn must not evict
// staging's history. The names only sort chronologically WITHIN one basename
// group anyway, so pruning the directory as a whole would be wrong as well as
// destructive.
func TestPruneLeavesTheOtherEnvFilesHistoryAlone(t *testing.T) {
	dir := t.TempDir()
	for i := range 12 {
		write(t, filepath.Join(dir, fmt.Sprintf("production.env.202601%02dT000000Z", i+1)), "x")
	}
	for i := range 3 {
		write(t, filepath.Join(dir, fmt.Sprintf("staging.env.202601%02dT000000Z", i+1)), "x")
	}
	// A stray file nobody asked this code to manage. It deletes things, so the
	// rule for what it will delete is as narrow as it can be made.
	write(t, filepath.Join(dir, "README"), "these are env file snapshots")

	if err := pruneEnvHistory(dir, "production.env", envHistoryKeepDefault); err != nil {
		t.Fatalf("pruneEnvHistory: %v", err)
	}
	names := historyNames(t, dir)
	var production, staging int
	for _, n := range names {
		switch {
		case strings.HasPrefix(n, "production.env."):
			production++
		case strings.HasPrefix(n, "staging.env."):
			staging++
		}
	}
	if production != envHistoryKeepDefault {
		t.Errorf("%d production snapshots left, want %d", production, envHistoryKeepDefault)
	}
	if staging != 3 {
		t.Errorf("%d staging snapshots left, want all 3 — the other deployment's history was pruned", staging)
	}
	if _, err := os.Stat(filepath.Join(dir, "README")); err != nil {
		t.Errorf("a file that is not a snapshot was deleted: %v", err)
	}
}

func historyNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// The env file is the artifact an operator reads to understand their own
// deployment. A tag bump that reformatted it, dropped a comment or reordered a
// key would be taking that away one update at a time.
func TestBumpTagsRewritesOnlyTheTags(t *testing.T) {
	dir := fakeDeployment(t, historyEnv)
	envPath := filepath.Join(dir, "env", "production.env")
	if err := bumpTagsInEnvFile(envPath, map[string]string{
		"VIDRA_CORE_TAG":   "v0.3.0",
		"VIDRA_USER_TAG":   "v0.3.0",
		"VIDRA_SEARCH_TAG": "v0.3.0",
	}); err != nil {
		t.Fatalf("bumpTagsInEnvFile: %v", err)
	}
	got, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := strings.NewReplacer(
		"VIDRA_CORE_TAG=v0.2.0", "VIDRA_CORE_TAG=v0.3.0",
		"VIDRA_USER_TAG=v0.2.0", "VIDRA_USER_TAG=v0.3.0",
		"VIDRA_SEARCH_TAG=v0.2.0", "VIDRA_SEARCH_TAG=v0.3.0",
	).Replace(historyEnv)
	if string(got) != want {
		t.Errorf("the rewrite changed more than the three tags:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if info, err := os.Stat(envPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("env file mode after the rewrite: %v (%v)", info.Mode().Perm(), err)
	}
}

// A file with no VIDRA_CORE_TAG line is one whose tag comes from somewhere else.
// Appending it here would look exactly like a successful bump while changing
// nothing about what deploys.
func TestBumpTagsRefusesAKeyThatIsNotThere(t *testing.T) {
	dir := fakeDeployment(t, "HTTP_PORT=8080\n")
	err := bumpTagsInEnvFile(filepath.Join(dir, "env", "production.env"),
		map[string]string{"VIDRA_CORE_TAG": "v0.3.0"})
	if err == nil {
		t.Fatal("a tag was appended to a file that does not assign it")
	}
	for _, want := range []string{"VIDRA_CORE_TAG", "production.env.example"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}
}
