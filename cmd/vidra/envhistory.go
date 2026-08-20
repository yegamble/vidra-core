package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/setup"
)

// The env file's HISTORY, and the tag rewrite that makes one worth keeping.
//
// THE GAP THIS CLOSES. deploy/rollback.sh keeps exactly one `.bak` of the env
// file, written just before it rewrites the tags. That is enough state for one
// rollback and no more: the second rollback overwrites the .bak with the tags
// the FIRST rollback had already installed, so after two flips the file that was
// supposed to record "what were we running before all this started" records the
// middle of the incident instead. There is then no way to answer "what was
// running on Tuesday" from the box, at exactly the moment somebody needs to.
//
// So every tag bump this CLI performs snapshots the whole env file into
// backups/env-history/ under a UTC timestamp first. Ten generations by default,
// pruned by name, which is chronological by construction.
//
// The directory, the name convention, the mode and the RETENTION COUNT are all
// SHARED with the deployment's bash scripts, which snapshot into the same place.
// Do not vary any of them: an operator reading `ls backups/env-history/` must see
// one history, not two interleaved ones with different spellings — and the two
// halves prune each other's snapshots, so a count that differs means whichever
// ran last silently enforces its own.

const (
	// envHistoryDirName is relative to the deployment root, and lives under
	// backups/ deliberately — that directory is already the one an operator knows
	// holds the things this deployment can be restored from, it is already
	// gitignored, and deploy.sh's own pre-deploy dumps are its neighbours.
	envHistoryDirName = "backups/env-history"
	// envHistoryStamp is UTC %Y%m%dT%H%M%SZ. Sortable as a string, unambiguous
	// across the timezone the droplet is in versus the one the operator is in,
	// and the same shape deploy.sh's pre-deploy dumps use.
	envHistoryStamp = "20060102T150405Z"
	// envHistoryKeepDefault is how many generations survive a prune when nobody
	// has said otherwise. Ten is roughly a year of a small instance's updates and
	// a fortnight of a busy one's, and the files are a few kilobytes each — and it
	// is ten rather than eight because that is deploy/lib.sh's default, which
	// prunes the same directory.
	envHistoryKeepDefault = 10
	// envHistoryKeepVar is the operator knob, spelled as deploy/lib.sh spells it.
	envHistoryKeepVar = "VIDRA_ENV_HISTORY_KEEP"
)

// envHistoryKeepValue resolves how many generations to keep, and returns the
// warning to print when the answer had to be defaulted.
//
// THE COUNT IS THE OTHER HALF OF THE SHARED-DIRECTORY CONTRACT. deploy/lib.sh's
// env_snapshot prunes the SAME directory with `${VIDRA_ENV_HISTORY_KEEP:-10}`,
// and the two halves alternate over the life of a deployment: a `vidra update`
// here, a `./deploy/rollback.sh` there. A Go side hardcoded at ten, against a
// shell side an operator raised to thirty, does not keep thirty generations — it
// keeps ten, because the next update deletes the twenty the shell preserved,
// silently, on the one host where somebody cared enough to change the setting.
//
// The lookup is envGet's — process environment, then the env file, then the
// default — which is a SUPERSET of lib.sh, where the value is read off the
// process environment alone. An operator who writes the knob into
// env/production.env is honoured here and not there, so the shell would prune to
// ten what this kept; naming the resolved count on the plan screen is what makes
// that visible, and lib.sh is the copy to widen if it ever bites. The asymmetry
// is in the safe direction: the shell prunes MORE, never less, so the directory
// cannot grow without bound.
//
// Garbage defaults rather than refusing, and says so. lib.sh, handed a value that
// is not a number, dies inside its own `$((keep + 1))` and takes the deploy with
// it; that is a bad model for this half, whose snapshot — the thing that protects
// the operator — is already on disk by the time the count is used. A typo in a
// retention knob is not a reason to abandon an update in the middle.
func envHistoryKeepValue(processEnv, values map[string]string) (int, string) {
	raw := envGet(processEnv, values, envHistoryKeepVar, "")
	if raw == "" {
		return envHistoryKeepDefault, ""
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return envHistoryKeepDefault, fmt.Sprintf(
			"%s=%s is not a positive whole number of generations, so the env-file history is being kept at %d. deploy/lib.sh, which prunes the same directory, fails outright on that value — fix it before the next ./deploy/deploy.sh",
			envHistoryKeepVar, raw, envHistoryKeepDefault)
	}
	return n, ""
}

// snapshotEnvFile copies the deployment's env file into the history directory
// and prunes the older generations. It returns the path it wrote, which is
// worth printing: it is the thing the operator needs if the update goes wrong
// and the tags have to be put back by hand.
//
// The modes are the env file's own, not the defaults: 0700 on the directory and
// 0600 on the snapshot. The file being copied holds JWT_SECRET,
// POSTGRES_PASSWORD, REDIS_PASSWORD and the federation KEK, and a history
// directory that widened them would be a credential leak introduced by a safety
// feature.
func snapshotEnvFile(root, envPath string, keep int, now time.Time) (string, error) {
	content, err := os.ReadFile(envPath)
	if err != nil {
		return "", fmt.Errorf("%s could not be read, so no history snapshot was taken", envPath)
	}
	dir := filepath.Join(root, filepath.FromSlash(envHistoryDirName))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("%s could not be created for the env-file history: %v", dir, err)
	}
	// TIGHTENED EVERY TIME, and not only when this call created it. MkdirAll
	// applies its mode to directories it CREATES, and on any deployment that has
	// been rolled back once the directory already exists — created by
	// deploy/lib.sh's env_snapshot, under whatever umask that shell ran with. That
	// is why lib.sh follows its own `mkdir -p` with an unconditional `chmod 700`,
	// and why this does too: the directory is about to receive a copy of the file
	// holding JWT_SECRET, POSTGRES_PASSWORD and the federation KEK, and a mode
	// nobody re-checks is a mode that stays wrong for the life of the host.
	//
	// A chmod that fails is fatal here, as it is there (`set -e`), and it is fatal
	// BEFORE the copy exists: a snapshot this command could not protect is one it
	// should not have written.
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("%s could not be tightened to 0700 for the env-file history, and it is about to hold a copy of the deployment's secrets: %v", dir, err)
	}
	base := filepath.Base(envPath)
	// The BASENAME of the env file this run uses, not a hardcoded
	// "production.env": an operator on --env env/staging.env gets
	// staging.env.<stamp>, and the two deployments' histories sit side by side
	// without either pretending to be the other.
	path := filepath.Join(dir, base+"."+now.UTC().Format(envHistoryStamp))
	// setup.WriteFile is the same atomic temp+rename+fsync at 0600 that writes the
	// env file itself. A snapshot that survived a crash as zero bytes would be
	// worse than no snapshot: it looks like a record.
	if err := setup.WriteFile(path, content); err != nil {
		return "", fmt.Errorf("the env-file history snapshot could not be written to %s: %v", path, err)
	}
	if err := pruneEnvHistory(dir, base, keep); err != nil {
		// Non-fatal, and deliberately so: the snapshot — the thing that protects
		// the operator — is already on disk, and refusing the update because a
		// year-old copy of it could not be deleted would be the tail wagging the
		// dog.
		return path, err
	}
	return path, nil
}

// pruneEnvHistory keeps the newest `keep` snapshots OF ONE ENV FILE and removes
// the rest.
//
// It prunes per basename rather than per directory, which matters on the one
// host that runs two deployments out of one checkout: production's churn must
// not evict staging's history, and the names only sort chronologically within a
// single basename group anyway.
func pruneEnvHistory(dir, base string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("the env-file history in %s could not be listed, so nothing was pruned: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// `<base>.<stamp>` and nothing else. A stray file an operator dropped in
		// here is left alone: this code deletes things, and the narrowest possible
		// rule for what it will delete is the whole safety of that.
		stamp, ok := strings.CutPrefix(e.Name(), base+".")
		if !ok || len(stamp) != len(envHistoryStamp) {
			continue
		}
		if _, err := time.Parse(envHistoryStamp, stamp); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) <= keep {
		return nil
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var failed []string
	for _, name := range names[keep:] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			failed = append(failed, name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d old env-file snapshot(s) in %s could not be removed (%s)", len(failed), dir, strings.Join(failed, ", "))
	}
	return nil
}

// bumpTagsInEnvFile rewrites the VIDRA_*_TAG assignments in place.
//
// It goes through the setup package's parse/render/write rather than sed or a
// line rewrite of its own, and that is the whole reason this function exists
// instead of a shell one-liner. env/production.env is a SELF-DOCUMENTING
// artifact: every value sits under the paragraph explaining what it does, what
// it costs to rotate, and which of them are destructive to change. Render
// carries every comment, blank line and unrelated assignment through byte for
// byte and rewrites only the values it is given, and WriteFile lands the result
// with the same atomic temp+rename+fsync the file was created with — so an
// update interrupted at the wrong instant leaves the previous env file, never
// half of one.
//
// A key that is not already an ACTIVE assignment is refused rather than
// appended. An env file with no VIDRA_CORE_TAG line is one whose tag comes from
// somewhere else — the shell, a systemd unit — and appending a line here would
// not change what deploys while looking exactly as though it had.
func bumpTagsInEnvFile(path string, tags map[string]string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s does not exist, so there are no tags to bump", path)
		}
		return fmt.Errorf("%s could not be read", path)
	}
	f, err := setup.ParseEnvFile(b)
	if err != nil {
		return errors.New(strings.TrimPrefix(err.Error(), "setup: "))
	}
	values := f.Values()
	for key, tag := range tags {
		if !f.Has(key) {
			return fmt.Errorf("%s is not assigned in %s, so this update has nothing to bump. Add the three VIDRA_*_TAG lines (env/production.env.example carries them) and re-run — appending them here would look like a bump while the tag that actually deploys came from somewhere else", key, filepath.Base(path))
		}
		values[key] = tag
	}
	return setup.WriteFile(path, f.Render(values, nil, nil))
}
