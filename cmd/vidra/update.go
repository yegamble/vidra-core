package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// runUpdate is `vidra update`: find the newest release, pin it, deploy it, and
// put the previous tags back if the deploy's health probes fail.
//
// IT IS NOT A SIXTH WRAPPER. deploy, rollback, backup, restore and release hand
// their argv to a script and get out of the way (passthrough.go). This one has
// work of its own that no script does, and each piece of it is the answer to
// something an operator otherwise does by hand and gets wrong:
//
//   - DISCOVERY. "Which version am I on and what is current" is currently a
//     browser tab. Worse, it is three browser tabs, because a release spans
//     vidra-core, vidra-user and vidra-search and deploy/release.sh cuts all
//     three with one tag — a tag that exists in only two of them is a broken
//     release, and pinning it produces a `docker compose pull` failure halfway
//     through a deploy instead of a refusal before it.
//
//   - THE SCHEMA FLOOR. `vidra deploy` will happily ship an image whose embedded
//     migrations are OLDER than the database's ledger. Nothing downstream
//     catches it: `migrate up` on an image that is behind is a no-op, the stack
//     comes up, and the fault surfaces later as code reading columns that were
//     added after it was built. /schemaz reports the ledger version as a NUMBER
//     precisely so this comparison can be made, and this is the command that
//     makes it.
//
//   - THE TAG-FLIP ROLLBACK. deploy.sh's failure text tells the operator to run
//     rollback.sh. That is a correct instruction and a bad moment to be reading
//     one: the site is down, and the previous tags are in a file that has just
//     been overwritten. So they are held in memory here and flipped back
//     automatically — but ONLY when the target is one release ahead of what is
//     running, because a tag flip does not touch the database and the one-release
//     schema-compat policy (enforced by scripts/migrate-lint.sh in both
//     migration-owning repos) is exactly the guarantee that makes flipping back
//     one release safe. It says nothing about flipping back three.
//
//   - THE HISTORY. rollback.sh keeps one .bak. Two rollbacks and it holds the
//     middle of the incident. See envhistory.go.
//
// What it still does NOT do, deliberately: it re-implements no gate. The
// pre-deploy dump, MIN_EMBEDDED_MIGRATE_TAG, the Caddyfile and DNS preflights,
// the migration steps and every probe belong to deploy/deploy.sh, which is run
// through Passthrough with the operator's terminal attached so they watch it
// happen. This command chooses a tag, writes it down, and cleans up after a
// failure.
func runUpdate(s streams, args []string) error {
	f, uf, err := parseUpdateFlags(args)
	if err != nil {
		fmt.Fprintf(s.err, "vidra: %s\n\n", err)
		updateUsage(s.err)
		return errReported
	}
	if f.help {
		updateUsage(s.out)
		return nil
	}
	dep, deployPath, err := resolve("update", f, "deploy.sh")
	if err != nil {
		return err
	}
	values, err := dep.values()
	if err != nil {
		return fmt.Errorf("update: %s — it holds the tags this command reads and rewrites", err)
	}
	processEnv := environMap(theRunner.Environ())

	current, err := currentTags(processEnv, values, dep.envFile)
	if err != nil {
		return err
	}
	owner := imageOwner(processEnv, values)

	ctx := context.Background()
	plan, err := discover(ctx, processEnv, owner, current, uf.tag)
	if err != nil {
		return err
	}

	if uf.check {
		renderUpdateCheck(s.out, dep, plan)
		return nil
	}
	if plan.uptodate {
		fmt.Fprintf(s.out, "vidra update — %s (%s)\n\nAlready on %s, which is the newest release of %s/%s. Nothing to do.\n",
			dep.root, dep.envFile, plan.target, owner, coreRepo)
		return nil
	}

	// A target older than what is running is a ROLLBACK, and rollback.sh is the
	// command that knows how to do one (it re-probes afterwards, and its own
	// header carries the restore-first sequence for a flip across an incompatible
	// schema change). Doing it here under the name "update" would also skip the
	// pre-deploy dump question entirely.
	if plan.downgrade {
		return fmt.Errorf("update: %s is OLDER than the %s this deployment is running. Going backwards is `vidra rollback %s`, which flips the tags and re-probes without pretending it is an update — and if the newer release migrated the database, deploy/README.md's restore-first sequence is the one to follow instead",
			plan.target, plan.oldest, plan.target)
	}
	if len(plan.missing) > 0 {
		return fmt.Errorf("update: %s is released in %s/%s but NOT in %s. deploy/release.sh cuts all three components with one tag, so this release is only half cut — its missing image would fail the deploy's `pull` step after the pre-deploy dump had already been taken. Publish the missing release (or pass --tag with an older one) and re-run",
			plan.target, owner, coreRepo, ownedList(owner, plan.missing))
	}

	// The schema floor, and the one refusal in this command that is about the
	// DATABASE rather than about tags.
	gate := schemaGate(ctx, dep, processEnv, values, plan.target)
	if gate.refuse != "" {
		return fmt.Errorf("update: %s", gate.refuse)
	}

	armed, why := armRollback(dep, plan, uf.noRollback)
	renderUpdatePlan(s.out, dep, plan, gate, armed, why)

	if !uf.yes {
		ok, err := confirmUpdate(s)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(s.out, "Nothing was changed.")
			return errReported
		}
	}

	// From here on the deployment is being modified, and every step says what it
	// did before the next one starts — an update interrupted by a dropped ssh
	// session has to be reconstructable from what reached the terminal.
	snapshot, snapErr := snapshotEnvFile(dep.root, dep.envPath, time.Now())
	if snapshot == "" {
		return fmt.Errorf("update: %v", snapErr)
	}
	fmt.Fprintf(s.out, "\nsnapshot: %s\n", snapshot)
	if snapErr != nil {
		// The snapshot landed; only the prune failed. Worth a line, not a refusal.
		fmt.Fprintf(s.err, "note: %v\n", snapErr)
	}
	if err := bumpTagsInEnvFile(dep.envPath, plan.bump()); err != nil {
		return fmt.Errorf("update: %v", err)
	}
	fmt.Fprintf(s.out, "pinned: core=%s user=%s search=%s in %s\n\n", plan.target, plan.target, plan.target, dep.envFile)

	// deploy.sh takes NO arguments: what it deploys is what the env file pins,
	// which is what the two lines above just wrote. Passthrough rather than
	// Capture because it runs for minutes, prints its six steps as it goes, and
	// its output IS the operator's view of the deploy.
	deployErr := theRunner.Passthrough(dep.bash(deployPath), s)
	if deployErr == nil {
		fmt.Fprintf(s.out, "\nvidra update: %s is deployed. The previous env file is %s.\n", plan.target, snapshot)
		return nil
	}
	return recoverFromFailedDeploy(s, dep, plan, armed, why, snapshot, deployErr)
}

// ---------------------------------------------------------------------------
// Flags.

// updateFlags are `vidra update`'s own.
type updateFlags struct {
	check      bool
	tag        string
	yes        bool
	noRollback bool
}

// parseUpdateFlags reads the WHOLE command line, and unlike parseWrapperFlags it
// refuses what it does not recognise.
//
// The difference is not an inconsistency, it is the difference between the two
// kinds of command. A wrapper stops at the first unknown token because
// everything after it belongs to a script (`vidra restore -- --yes dump.gz`);
// update runs deploy.sh with no arguments at all, so there is nobody for a
// stray token to belong to, and forwarding one would mean `vidra update --dry-run`
// silently performing the update. Order does not matter here either, which is
// the other thing operators expect from a command whose flags are all its own.
func parseUpdateFlags(args []string) (wrapperFlags, updateFlags, error) {
	f := wrapperFlags{repo: ".", envFile: defaultEnvFile}
	var uf updateFlags
	for i := 0; i < len(args); i++ {
		name, value, hasValue := splitFlag(args[i])
		// The three flags that take a value read the next argument when it was not
		// spelled --flag=value. Resolved before the switch so the "needs a value"
		// refusal is one sentence in one place.
		switch name {
		case "-C", "--repo", "-repo", "--env", "-env", "--tag", "-tag":
			if !hasValue {
				if i+1 >= len(args) {
					return f, uf, fmt.Errorf("update: %s needs a value", name)
				}
				i++
				value = args[i]
			}
		}
		switch name {
		case "-C", "--repo", "-repo":
			f.repo = value
		case "--env", "-env":
			f.envFile, f.explicit = value, true
		case "--tag", "-tag":
			uf.tag = value
		case "--check", "-check":
			uf.check = true
		case "--yes", "-yes", "-y":
			uf.yes = true
		case "--no-rollback", "-no-rollback":
			uf.noRollback = true
		case "-h", "--help", "-help":
			f.help = true
		default:
			return f, uf, fmt.Errorf("update: %s is not one of this command's flags (--check, --tag, --yes, --no-rollback, -C/--repo, --env)", args[i])
		}
	}
	if f.repo == "" {
		return f, uf, fmt.Errorf("update: -C/--repo needs a directory")
	}
	if f.envFile == "" {
		return f, uf, fmt.Errorf("update: --env needs a file")
	}
	if uf.tag != "" {
		if _, ok := parseReleaseTag(uf.tag); !ok {
			return f, uf, fmt.Errorf("update: --tag %q is not a release tag — releases are spelled vMAJOR.MINOR.PATCH (v0.2.0), with no suffix", uf.tag)
		}
	}
	return f, uf, nil
}

// ---------------------------------------------------------------------------
// What is running, and who publishes the images.

// updateComponent is one of the three things a release moves: the env-file key
// that pins it and the repository it is released from.
type updateComponent struct {
	name string
	key  string
	repo string
}

var updateComponents = []updateComponent{
	{name: "core", key: "VIDRA_CORE_TAG", repo: coreRepo},
	{name: "user", key: "VIDRA_USER_TAG", repo: userRepo},
	{name: "search", key: "VIDRA_SEARCH_TAG", repo: searchRepo},
}

// currentTags reads the three pinned tags OUT OF THE ENV FILE, and refuses when
// the process environment holds one too.
//
// The refusal is not pedantry. Compose resolves ${VIDRA_CORE_TAG} from the
// SHELL environment before it falls back to --env-file, so an exported tag beats
// the file — and this command's whole effect is to write the file. Without this
// check `vidra update` would report a successful bump, run a deploy, and ship
// the exported tag, with every line it printed describing something else.
func currentTags(processEnv, values map[string]string, envFile string) (map[string]string, error) {
	out := make(map[string]string, len(updateComponents))
	for _, c := range updateComponents {
		if v := strings.TrimSpace(processEnv[c.key]); v != "" {
			return nil, fmt.Errorf("update: %s=%s is exported in this environment, and compose reads the environment BEFORE %s — so rewriting the file would change nothing about what deploys. Unset it (`unset %s`) and re-run, or deploy by hand with the tag you want",
				c.key, v, envFile, c.key)
		}
		tag := unquote(strings.TrimSpace(values[c.key]))
		if tag == "" {
			return nil, fmt.Errorf("update: %s is not set in %s, so there is nothing to update FROM. `vidra setup` writes the three tag lines; env/production.env.example carries them", c.key, envFile)
		}
		out[c.key] = tag
	}
	return out, nil
}

// imageOwner is the GitHub account the releases are read from.
//
// VIDRA_IMAGE_OWNER first, then GITHUB_OWNER — which is the spelling
// deploy/release.sh uses — then this project's own account. Both spellings are
// honoured rather than one being declared canonical, because a fork that set
// only the release script's variable would otherwise have `vidra update` quietly
// offering it upstream's releases, which is the one wrong answer that looks
// completely normal.
func imageOwner(processEnv, values map[string]string) string {
	if v := envGet(processEnv, values, "VIDRA_IMAGE_OWNER", ""); v != "" {
		return v
	}
	if v := envGet(processEnv, values, "GITHUB_OWNER", ""); v != "" {
		return v
	}
	return "yegamble"
}

// ---------------------------------------------------------------------------
// Discovery.

// updatePlan is what an update WOULD do: where it is, where it is going, and
// what the releases in between are.
type updatePlan struct {
	owner string
	// current is key -> tag, as the env file pins them.
	current map[string]string
	// oldest is the oldest of the three current tags, and the one every
	// comparison uses. Three tags that disagree is not a normal state, but it is a
	// reachable one — a `rollback.sh --user vX` leaves the other two alone — and
	// the safe reading of "how far back would a flip go" is the FURTHEST one.
	oldest string
	// tags is every eligible release, oldest first.
	tags   []string
	latest string
	target string
	// missing names the sibling repositories that have no release for target.
	missing []string
	// steps is how many releases target is ahead of oldest: 1 for the next one,
	// 2 when one release is being skipped. Zero when it could not be computed —
	// see distanceKnown.
	steps         int
	distanceKnown bool
	uptodate      bool
	downgrade     bool
}

// bump is the plan as the env-file rewrite it implies.
func (p updatePlan) bump() map[string]string {
	out := make(map[string]string, len(updateComponents))
	for _, c := range updateComponents {
		out[c.key] = p.target
	}
	return out
}

// discover reads vidra-core's releases, picks the target, and checks the other
// two repositories carry it.
//
// vidra-core is the repository asked FIRST and the one whose list defines what
// exists. It is not arbitrary: release.sh cuts core first, the schema floor
// below is computed from core's migrations, and a release that exists anywhere
// exists there.
func discover(ctx context.Context, processEnv map[string]string, owner string, current map[string]string, wantTag string) (updatePlan, error) {
	p := updatePlan{owner: owner, current: current}
	releases, err := listReleases(ctx, processEnv, owner, coreRepo)
	if err != nil {
		return p, fmt.Errorf("update: %v", err)
	}
	p.tags = releaseTags(releases)
	if len(p.tags) == 0 {
		return p, fmt.Errorf("update: %s/%s has no published vMAJOR.MINOR.PATCH release, so there is nothing to update to. Drafts and prereleases are skipped deliberately — a draft has no image behind it, and a prerelease is a tag somebody chose on purpose", owner, coreRepo)
	}
	p.latest = p.tags[len(p.tags)-1]
	p.target = p.latest
	if wantTag != "" {
		if !containsTag(p.tags, wantTag) {
			return p, fmt.Errorf("update: --tag %s is not a published release of %s/%s. The releases it offers are %s (newest last); drafts and prereleases are not among them",
				wantTag, owner, coreRepo, strings.Join(lastFew(p.tags, 8), ", "))
		}
		p.target = wantTag
	}

	p.oldest = oldestTag(current)
	p.uptodate = true
	for _, c := range updateComponents {
		if current[c.key] != p.target {
			p.uptodate = false
		}
	}
	if p.uptodate {
		// Nothing else is worth two more requests to GitHub: the release being
		// asked about is the one already running.
		return p, nil
	}

	targetVersion, _ := parseReleaseTag(p.target)
	if oldestVersion, ok := parseReleaseTag(p.oldest); ok && targetVersion.less(oldestVersion) {
		p.downgrade = true
		return p, nil
	}
	p.steps, p.distanceKnown = releaseSteps(p.tags, p.oldest, p.target)

	for _, c := range updateComponents {
		if c.repo == coreRepo {
			continue
		}
		ok, err := hasRelease(ctx, processEnv, owner, c.repo, p.target)
		if err != nil {
			return p, fmt.Errorf("update: %v", err)
		}
		if !ok {
			p.missing = append(p.missing, c.repo)
		}
	}
	return p, nil
}

// oldestTag is the oldest of the three pinned tags, by version. A tag that does
// not parse (a branch name, a digest, something hand-edited) sorts as the oldest
// thing there is, because the honest answer to "how far back does this go" for a
// tag nobody can order is "further than you think".
func oldestTag(current map[string]string) string {
	oldest, oldestV := "", semver{}
	unparsed := ""
	for _, c := range updateComponents {
		tag := current[c.key]
		v, ok := parseReleaseTag(tag)
		if !ok {
			if unparsed == "" {
				unparsed = tag
			}
			continue
		}
		if oldest == "" || v.less(oldestV) {
			oldest, oldestV = tag, v
		}
	}
	if unparsed != "" {
		return unparsed
	}
	return oldest
}

// releaseSteps is how many releases apart two tags are: 1 when target is the
// very next release after current, 2 when one release is being skipped, and so
// on.
//
// It returns false when the question cannot be answered — a current tag that is
// not in the release list at all, because it was deleted, was a prerelease, or
// was never a release. "Not provably one step" then disarms the automatic
// rollback rather than guessing, which is the whole reason this returns two
// values instead of a number and a zero.
func releaseSteps(tags []string, current, target string) (int, bool) {
	ci, ti := indexOfTag(tags, current), indexOfTag(tags, target)
	if ci < 0 || ti < 0 {
		return 0, false
	}
	return ti - ci, true
}

func indexOfTag(tags []string, tag string) int {
	for i, t := range tags {
		if t == tag {
			return i
		}
	}
	return -1
}

func containsTag(tags []string, tag string) bool { return indexOfTag(tags, tag) >= 0 }

// lastFew is the tail of a list, for an error message that must name the
// options without printing four years of them.
func lastFew(tags []string, n int) []string {
	if len(tags) <= n {
		return tags
	}
	return append([]string{"…"}, tags[len(tags)-n:]...)
}

// ownedList renders repository names as owner/name, for a message that has to be
// unambiguous about WHICH account is missing the release.
func ownedList(owner string, repos []string) string {
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		out = append(out, owner+"/"+r)
	}
	return strings.Join(out, " and ")
}

// ---------------------------------------------------------------------------
// The schema floor.

// schemaCheck is the pre-flight's finding: either a refusal, or a note to print
// and carry on with.
type schemaCheck struct {
	// refuse is non-empty when the update must not happen. It is the whole
	// operator-facing sentence.
	refuse string
	// notes are the things that could not be checked. They are printed, and they
	// do not stop anything.
	notes []string
	// dbVersion / targetVersion are printed in the plan when both are known.
	dbVersion     int64
	targetVersion int64
	known         bool
}

// updateGitTimeout covers a `git fetch --tags` over the network from a droplet.
const updateGitTimeout = 60 * time.Second

// schemaGate refuses an update whose image is BEHIND the database.
//
// The comparison is between two numbers that already exist for other reasons:
// the running api's /schemaz reports the schema_migrations ledger version, and
// the target tag's migrations/ directory names the highest migration compiled
// into that release's binary. If the second is smaller than the first, the image
// about to be deployed was built before migrations the database has already
// applied — its `migrate up` is a silent no-op, the stack comes up green, and
// the failure surfaces later as code reading columns that do not exist in the
// build that is running. There is no gate downstream that catches it: deploy.sh
// compares the ledger against the CHECKOUT it just moved to the same tag, so it
// compares the new image against itself and agrees.
//
// EVERY OTHER OUTCOME IS A WARNING. A /schemaz that 404s (an image older than
// that endpoint), an api that is not running, a missing vidra-core checkout, a
// git that cannot reach GitHub — none of those is evidence that the update is
// wrong, and a command that refused whenever it could not prove an update SAFE
// would refuse on every host where the stack is currently down, which is a large
// share of the hosts anybody updates.
func schemaGate(ctx context.Context, dep deployment, processEnv, values map[string]string, target string) schemaCheck {
	var out schemaCheck
	port := envGet(processEnv, values, "HTTP_PORT", "8080")
	dbVersion, dbKnown, dbNote := runningSchemaVersion(ctx, port)
	if dbNote != "" {
		out.notes = append(out.notes, dbNote)
	}
	targetVersion, tgtKnown, tgtNote := targetSchemaVersion(ctx, dep, target)
	if tgtNote != "" {
		out.notes = append(out.notes, tgtNote)
	}
	if !dbKnown || !tgtKnown {
		return out
	}
	out.dbVersion, out.targetVersion, out.known = dbVersion, targetVersion, true
	if targetVersion < dbVersion {
		out.refuse = fmt.Sprintf("%s carries migrations up to %d, but this deployment's database is already at %d. That image was built BEFORE migrations the database has applied: its own `migrate up` would do nothing, the stack would come up healthy, and the api would then be code that predates columns it is reading. This is the one thing `vidra deploy` cannot notice on its own — it checks the ledger against the checkout it just moved to the SAME tag, so it compares the image against itself. Deploy a release at or above %d, or restore the pre-deploy dump from before the schema moved (deploy/README.md has that sequence)",
			target, targetVersion, dbVersion, dbVersion)
	}
	return out
}

// runningSchemaVersion reads the live database's migration version out of the
// api's /schemaz, which is host-local and unauthenticated for exactly this kind
// of tooling. status.go parses the same document; the shape is shared, the
// judgement is not.
func runningSchemaVersion(ctx context.Context, port string) (int64, bool, string) {
	client := &http.Client{Timeout: statusHTTPTimeout}
	status, body, err := get(ctx, client, loopback(port)+"/schemaz")
	if err != nil {
		return 0, false, "the api is not answering on " + loopback(port) + ", so the database's schema version could not be read (" + dialSummary(err) + ") — the update is not gated on it"
	}
	if status == http.StatusNotFound {
		return 0, false, "this api image predates /schemaz, so the database's schema version could not be read from it — the update is not gated on it. `vidra doctor` reads the ledger out of the database directly"
	}
	if status != http.StatusOK {
		return 0, false, fmt.Sprintf("/schemaz answered HTTP %d instead of the schema document, so the database's version could not be read — the update is not gated on it", status)
	}
	var parsed schemaBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, false, "/schemaz answered with something that is not the expected document, so the database's version could not be read — the update is not gated on it"
	}
	switch {
	case parsed.Schema.Error != "":
		return 0, false, "the api could not read its own migration ledger (" + truncate(firstLine(parsed.Schema.Error), 120) + "), so the database's version could not be compared with the target's"
	case !parsed.Schema.Applied:
		return 0, false, "no migration has ever run against this database, so there is no floor for the target's migrations to be below"
	}
	return parsed.Schema.Version, true, ""
}

// targetSchemaVersion is the highest migration number compiled into the target
// release, read WITHOUT checking anything out.
//
// `git ls-tree` on a tag lists that commit's tree; nothing in the working
// directory moves. This matters more than it sounds: the checkout under the
// deployment root is what deploy.sh moves to the tag it is about to ship, and a
// pre-flight that moved it first would leave a rejected update with the wrong
// commit checked out.
//
// The arithmetic is deploy.sh's, deliberately (deploy.sh:471-473): the basename
// of every migrations/*.up.sql, the field before the first underscore, numerically
// the largest, parsed base-10 so 000104 is 104 and not an octal error. Two
// different answers to "which migration is this release at" would be a second
// opinion nobody asked for.
func targetSchemaVersion(ctx context.Context, dep deployment, target string) (int64, bool, string) {
	repo := filepath.Join(dep.root, coreRepo)
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return 0, false, "there is no " + coreRepo + " checkout under " + dep.root + ", so the target release's migration version could not be read — the update is not gated on it"
	}
	gctx, cancel := context.WithTimeout(ctx, updateGitTimeout)
	defer cancel()
	// --force for the same reason deploy.sh uses it: git refuses to move a tag the
	// host already has, so a tag re-pointed upstream would otherwise be read at
	// the stale object forever.
	fetch, err := theRunner.Capture(gctx, dep.git(repo, "fetch", "--tags", "--force", "--quiet"))
	if err != nil || fetch.ExitCode != 0 {
		return 0, false, "`git fetch --tags` failed in " + repo + ", so the target release's migration version could not be read — the update is not gated on it"
	}
	list, err := theRunner.Capture(gctx, dep.git(repo, "ls-tree", "-r", "--name-only", target, "--", "migrations"))
	if err != nil || list.ExitCode != 0 {
		return 0, false, "`git ls-tree " + target + "` failed in " + repo + " (is that tag fetched?), so the target release's migration version could not be read — the update is not gated on it"
	}
	version, ok := highestMigration(list.Stdout)
	if !ok {
		return 0, false, "no migrations/*.up.sql files are listed at " + target + ", so its migration version could not be read — the update is not gated on it"
	}
	return version, true, ""
}

// highestMigration is deploy.sh's expected_version, over a `git ls-tree` listing
// instead of an `ls`.
func highestMigration(listing string) (int64, bool) {
	var highest int64
	found := false
	for _, line := range strings.Split(listing, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		// Only migrations/<file>.up.sql, one level deep — the same set deploy.sh's
		// `migrations/*.up.sql` glob matches, so a future subdirectory cannot make
		// the two disagree.
		rest, ok := strings.CutPrefix(name, "migrations/")
		if !ok || strings.Contains(rest, "/") || !strings.HasSuffix(rest, ".up.sql") {
			continue
		}
		prefix, _, ok := strings.Cut(rest, "_")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			continue
		}
		if !found || n > highest {
			highest, found = n, true
		}
	}
	return highest, found
}

// git builds the spec for a git command inside one of the deployment's component
// checkouts. Path is "git" and not "bash": this is the one thing `vidra update`
// runs that is not a script of the deployment's.
func (d deployment) git(repo string, args ...string) execSpec {
	return execSpec{
		Path: "git",
		Args: append([]string{"-C", repo}, args...),
		Dir:  d.root,
		Env:  d.env,
	}
}

// ---------------------------------------------------------------------------
// Arming the rollback.

// armRollback decides whether a failed deploy will be flipped back
// automatically, and returns the reason when it will not.
//
// ONE RELEASE, AND THE REASON IS THE POLICY. A tag flip does not touch the
// database, so after it the previous release's code is running against the new
// release's schema. That is safe because forward migrations are additive —
// scripts/migrate-lint.sh refuses destructive DDL in *.up.sql, in vidra-core and
// vidra-search alike, and schema-compat.yml runs the previous release's suite
// against the new schema. What that policy promises is exactly ONE release of
// compatibility: release N's schema keeps release N-1's code running. It
// promises nothing about N-2, so an update that skips a release cannot be undone
// by flipping tags back, and a command that did it anyway would be quietly
// betting the instance on a guarantee nobody made.
//
// A disarmed update is not a refused one. It runs, deploy.sh's own gates and its
// pre-deploy dump are unchanged, and the operator has been told up front that
// the way back is ./deploy/restore.sh rather than a tag flip.
func armRollback(dep deployment, p updatePlan, noRollback bool) (bool, string) {
	switch {
	case noRollback:
		return false, "--no-rollback: a failed deploy will be left exactly as deploy.sh leaves it"
	case !dep.hasRollbackScript():
		return false, "deploy/rollback.sh is not in this deployment, so there is nothing to flip the tags back with"
	case !p.distanceKnown:
		return false, fmt.Sprintf("%s is not one of %s/%s's published releases, so how far back a flip would go cannot be computed — and the one-release schema-compat policy is the only thing that would make it safe", p.oldest, p.owner, coreRepo)
	case p.steps > 1:
		return false, fmt.Sprintf("%s is %d releases ahead of %s. The schema-compat policy covers ONE release — release N's schema keeps release N-1's code running, and says nothing about N-2 — so flipping the tags back across %d of them is not something this command will do unattended. deploy.sh takes its pre-deploy dump either way; ./deploy/restore.sh is the way back", p.target, p.steps, p.oldest, p.steps)
	}
	return true, ""
}

// hasRollbackScript reports whether the deployment has the script the automatic
// flip-back would use. resolve() already proved deploy.sh is there; rollback.sh
// is checked separately because arming a recovery that cannot run would mean
// promising one on the plan screen and printing an exec failure during an
// incident.
func (d deployment) hasRollbackScript() bool {
	_, err := os.Stat(filepath.Join(d.root, "deploy", "rollback.sh"))
	return err == nil
}

// ---------------------------------------------------------------------------
// The failure path.

// recoverFromFailedDeploy is what happens after deploy.sh exits non-zero.
//
// deploy.sh has already printed why it stopped, and it is specific and correct;
// nothing here repeats it. What it cannot say is what THIS command knows: which
// tags were running ten minutes ago, and that they are still deployable. So the
// banner is short and the flip is immediate.
//
// It exits non-zero in EVERY path, including the one where the rollback works
// perfectly. The requested update did not happen; a shell, a cron entry or an
// installer that read 0 from a run that ended on the previous release would draw
// exactly the wrong conclusion. The code passed up is deploy.sh's own, for the
// same exit-code fidelity the wrapper commands promise.
func recoverFromFailedDeploy(s streams, dep deployment, p updatePlan, armed bool, why, snapshot string, deployErr error) error {
	if !armed {
		fmt.Fprintf(s.err, "\n[update] The tags in %s are still %s. Automatic rollback was NOT armed for this update: %s.\n",
			dep.envFile, p.target, why)
		fmt.Fprintf(s.err, "[update] The env file as it was before this run: %s\n", snapshot)
		return deployErr
	}
	fmt.Fprintf(s.err, `
[update] THE DEPLOY OF %s FAILED. Rolling the tags back to what was running.
[update] This flips tags only — it does not touch the database, which is safe
[update] here because %s is one release behind %s and forward migrations are
[update] additive by policy. Handing over to deploy/rollback.sh:
`, p.target, p.oldest, p.target)

	rollbackPath := filepath.Join(dep.root, "deploy", "rollback.sh")
	// Per-component and never the bare-tag form, because the three tags are not
	// necessarily equal — an earlier partial rollback leaves them apart, and
	// `rollback.sh <tag>` would set all three to one value and quietly "fix" a
	// difference this command has no business deciding about.
	//
	// No confirmation flag: rollback.sh has no prompt. It is the incident command,
	// and it was written to run without a human in the loop.
	rollbackErr := theRunner.Passthrough(dep.bash(rollbackPath,
		"--core", p.current["VIDRA_CORE_TAG"],
		"--user", p.current["VIDRA_USER_TAG"],
		"--search", p.current["VIDRA_SEARCH_TAG"]), s)
	dump := newestPreDeployDump(dep.root)
	if rollbackErr == nil {
		fmt.Fprintf(s.err, `
[update] THE UPDATE TO %s FAILED AND WAS ROLLED BACK.
[update] core=%s user=%s search=%s are running again and passed rollback.sh's
[update] /readyz and frontend probes. The database was NOT rolled back: if %s
[update] migrated it, this instance is old code on a newer schema, which the
[update] one-release compatibility policy covers and nothing beyond it does.
[update] The deeper way back is the dump deploy.sh took before it started:
[update]     ./deploy/restore.sh %s
[update] The env file as it was before this run: %s
`, p.target, p.current["VIDRA_CORE_TAG"], p.current["VIDRA_USER_TAG"], p.current["VIDRA_SEARCH_TAG"], p.target, dump, snapshot)
		return deployErr
	}
	fmt.Fprintf(s.err, `
[update] THE ROLLBACK ALSO FAILED. THIS INSTANCE NEEDS A HUMAN.
[update] The deploy of %s failed and the flip back to core=%s user=%s search=%s
[update] did not come up either. Both of them printed why, above.
[update] Roll the application back by hand:
[update]     ./deploy/rollback.sh %s
[update] If the failure is a schema change, restore the pre-deploy dump first:
[update]     ./deploy/restore.sh %s
[update] The env file as it was before this run: %s
`, p.target, p.current["VIDRA_CORE_TAG"], p.current["VIDRA_USER_TAG"], p.current["VIDRA_SEARCH_TAG"], p.oldest, dump, snapshot)
	return deployErr
}

// newestPreDeployDump names the dump deploy.sh took at the start of the run that
// just failed, so the restore line is a command to paste rather than a shape to
// fill in. The generic placeholder is the fallback when backups/ cannot be read
// — it is still the right instruction.
func newestPreDeployDump(root string) string {
	dir := filepath.Join(root, "backups")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "backups/pre-deploy-<timestamp>.dump.gz"
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "pre-deploy-") && strings.HasSuffix(e.Name(), ".dump.gz") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "backups/pre-deploy-<timestamp>.dump.gz"
	}
	// The names carry a UTC stamp, so reverse lexical order is
	// reverse-chronological — the same property deploy.sh's prune relies on.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return filepath.Join("backups", names[0])
}

// ---------------------------------------------------------------------------
// Confirmation.

// confirmUpdate asks, and refuses to ask a pipe.
//
// The one-sided terminal test is setup.go's, and for the same reason: only a
// stdin that is positively NOT a terminal is refused. Anything whose file-ness
// is not knowable — a test's reader, an in-process pipe — is asked, because "not
// provably a terminal" must never become "refuse to run".
func confirmUpdate(s streams) (bool, error) {
	if f, ok := s.in.(*os.File); ok && !isTerminal(f) {
		return false, fmt.Errorf("update: stdin is not a terminal, so there is nobody to confirm this with — pass --yes to update unattended (a cron entry, an installer), or run it from a real terminal")
	}
	fmt.Fprint(s.out, "\nUpdate this deployment? [y/N]: ")
	line, err := bufio.NewReader(s.in).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false, fmt.Errorf("update: stdin ended without an answer — pass --yes to update unattended")
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// ---------------------------------------------------------------------------
// Rendering.

func renderUpdateCheck(w io.Writer, dep deployment, p updatePlan) {
	fmt.Fprintf(w, "vidra update --check — %s (%s)\n\n", dep.root, dep.envFile)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "  component\tcurrent\tlatest\n")
	for _, c := range updateComponents {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", c.name, p.current[c.key], p.latest)
	}
	_ = tw.Flush()
	fmt.Fprintln(w)
	switch {
	case p.uptodate:
		fmt.Fprintf(w, "Up to date: %s is the newest release of %s/%s.\n", p.target, p.owner, coreRepo)
	case p.downgrade:
		fmt.Fprintf(w, "%s is OLDER than the %s this deployment runs. Going backwards is `vidra rollback %s`, not an update.\n", p.target, p.oldest, p.target)
	default:
		fmt.Fprintf(w, "An update is available: %s → %s.\n", p.oldest, p.target)
		if p.target != p.latest {
			fmt.Fprintf(w, "(--tag %s was asked for; the newest release is %s.)\n", p.target, p.latest)
		}
		if p.distanceKnown && p.steps > 1 {
			fmt.Fprintf(w, "That is %d releases ahead, so `vidra update` would run with its automatic tag-flip rollback DISARMED.\n", p.steps)
		}
		if len(p.missing) > 0 {
			fmt.Fprintf(w, "But %s is released in %s/%s only — %s %s no release for it, so `vidra update` will refuse this tag until they do.\n",
				p.target, p.owner, coreRepo, ownedList(p.owner, p.missing), plural(len(p.missing), "has", "have"))
		} else {
			fmt.Fprintln(w, "Run `vidra update` to take it. Nothing has been changed by this check.")
		}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// renderUpdatePlan is the screen an operator confirms against. Everything on it
// is something they would otherwise have to go and look up while deciding.
func renderUpdatePlan(w io.Writer, dep deployment, p updatePlan, gate schemaCheck, armed bool, why string) {
	fmt.Fprintf(w, "vidra update — %s (%s)\n\n", dep.root, dep.envFile)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "  component\tcurrent\t\ttarget\n")
	for _, c := range updateComponents {
		fmt.Fprintf(tw, "  %s\t%s\t→\t%s\n", c.name, p.current[c.key], p.target)
	}
	_ = tw.Flush()
	fmt.Fprintln(w)
	if gate.known {
		fmt.Fprintf(w, "schema:   database at %d, %s carries migrations up to %d\n", gate.dbVersion, p.target, gate.targetVersion)
	}
	for _, note := range gate.notes {
		fmt.Fprintf(w, "note:     %s\n", note)
	}
	if armed {
		fmt.Fprintf(w, "rollback: ARMED — a failed health probe flips the tags back to core=%s user=%s search=%s automatically\n",
			p.current["VIDRA_CORE_TAG"], p.current["VIDRA_USER_TAG"], p.current["VIDRA_SEARCH_TAG"])
	} else {
		fmt.Fprintf(w, "rollback: NOT ARMED — %s\n", why)
	}
	fmt.Fprintf(w, "history:  %s is copied to %s/ before it is rewritten\n", dep.envFile, envHistoryDirName)
	fmt.Fprintln(w, "then:     ./deploy/deploy.sh runs with its own gates — the pre-deploy dump, the migrations, the probes")
}

func updateUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: vidra update [-C <deployment directory>] [--env <env file>]
                    [--check] [--tag vX.Y.Z] [--yes] [--no-rollback]

Move this deployment to the newest release: find it, pin it in the env file, and
run ./deploy/deploy.sh. If the deploy's health probes fail, put the previous tags
back.

It reads the releases of vidra-core, vidra-user and vidra-search from GitHub over
plain HTTPS — no `+"`gh`"+`, no credentials. A release has to exist in all three (one
tag cuts all three) or this refuses to pin it. GITHUB_TOKEN, if you have one
exported, is sent as a bearer token and only buys a bigger rate-limit budget.

BEFORE it changes anything it refuses an update whose image is BEHIND the
database — the target's embedded migration number against the running api's
/schemaz — because that is the one mistake `+"`vidra deploy`"+` cannot notice. If the
api is down, or its image predates /schemaz, that is a note and not a refusal.

The automatic rollback is a TAG FLIP through deploy/rollback.sh and it is armed
only when the target is ONE release ahead of what is running. That is the exact
span of the schema-compatibility policy: release N's schema keeps release N-1's
code running. Skipping a release disarms it, out loud, before you confirm — the
way back from there is deploy.sh's pre-deploy dump and ./deploy/restore.sh.

The env file is copied to %s/<name>.<UTC timestamp> before it is
rewritten, and ten generations are kept. deploy/rollback.sh keeps one .bak, which
after two rollbacks holds the middle of the incident rather than the beginning.

flags:
  -C, --repo <dir>   the deployment directory (default ".")
      --env <file>   the deployment's env file, also exported as ENV_FILE
                     (default %s)
      --check        print current vs latest and exit. Changes nothing, deploys
                     nothing, and exits 0 whether or not an update is waiting
      --tag vX.Y.Z   update to this release instead of the newest one. It still
                     has to be a published release of all three repositories
      --yes          do not ask for confirmation. Required when stdin is not a
                     terminal
      --no-rollback  never flip the tags back automatically; leave a failed
                     deploy exactly as deploy.sh leaves it
  -h, --help         this text

It exits non-zero if the update did not happen — including when the rollback
afterwards worked perfectly, because the release you asked for is not the one
running.
`, envHistoryDirName, defaultEnvFile)
}
